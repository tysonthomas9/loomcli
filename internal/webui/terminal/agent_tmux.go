// agent_tmux.go holds the minimal tmux-backed manager that survived the tmux
// removal. Its only job is to let the web UI attach to long-lived tmux
// sessions that the CLI auto-mode (loom task --auto / loom plan --auto)
// creates for Claude agents — and to clean those sessions up when a
// workspace is deleted. The main web terminal path uses PTYManager; this
// file exists solely because auto-mode agents still live inside tmux.
//
// Naming convention for auto-mode tmux sessions (see internal/cli/automode_tmux.go):
//
//	loom-<wsShort>-<role>-<agent>-<pid>
//
// FindLatestAgentSession matches that pattern; KillWorkspaceSessions sweeps
// every session with a matching wsShort prefix.
package terminal

import (
	"bufio"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/creack/pty"

	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
)

// ErrTmuxNotFound is returned by NewAgentTmuxManager when the tmux binary is
// not in $PATH. Callers should treat the agent-terminal feature as disabled
// and fall back to archive-log streaming.
var ErrTmuxNotFound = errors.New("tmux binary not found in PATH")

// ErrMaxSessionsReached is returned when the concurrent-attach limit is hit.
var ErrMaxSessionsReached = errors.New("maximum terminal sessions reached")

// lookPathTmux is exported (as a variable) so tests can override it.
var lookPathTmux = exec.LookPath

// validTmuxName is the alphanumeric/dash/underscore check for tmux session
// names accepted by AttachExistingRaw. FindLatestAgentSession's match
// pattern is stricter still.
var validTmuxName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// AgentTmuxManager attaches browser PTYs to already-running tmux sessions
// owned by the CLI auto-mode. It never creates tmux sessions — only attaches
// to existing ones — and tracks the attached PTYs to enforce a concurrency
// cap and clean them up on shutdown.
type AgentTmuxManager struct {
	tmuxPath string
	env      []string

	max     int
	counter atomic.Uint64

	mu    sync.RWMutex
	conns map[string]*AgentTmuxConn
}

// AgentTmuxConn is one browser-side PTY attached to a running tmux session.
type AgentTmuxConn struct {
	ConnID      string
	SessionName string // raw tmux session name (no prefix rewriting)
	PTY         *os.File

	cmd    *exec.Cmd
	killCh chan struct{}

	mu     sync.Mutex
	closed bool
}

// KillCh returns a channel the ws handler can select on to learn when the
// session was killed out from under it.
func (c *AgentTmuxConn) KillCh() <-chan struct{} { return c.killCh }

// Close tears down the PTY master and reaps the tmux attach child. Idempotent.
func (c *AgentTmuxConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true

	var firstErr error
	if c.PTY != nil {
		if err := c.PTY.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Wait()
	}
	return firstErr
}

// NewAgentTmuxManager returns a manager, or (nil, ErrTmuxNotFound) if the
// tmux binary isn't available. maxSessions <= 0 falls back to the package
// default.
func NewAgentTmuxManager(maxSessions int) (*AgentTmuxManager, error) {
	tmuxPath, err := lookPathTmux("tmux")
	if err != nil {
		return nil, ErrTmuxNotFound
	}
	if maxSessions <= 0 {
		maxSessions = defaultPTYMaxSessions
	}
	// Ensure child tmux processes share the same socket path as the
	// auto-mode CLI that created the sessions we're attaching to.
	env := platformruntime.CurrentSubprocessEnv(platformruntime.SubprocessEnvInteractionChild)
	hasTmpDir := false
	for _, e := range env {
		if strings.HasPrefix(e, "TMUX_TMPDIR=") {
			hasTmpDir = true
			break
		}
	}
	if !hasTmpDir {
		env = append(env, "TMUX_TMPDIR=/tmp")
	}
	return &AgentTmuxManager{
		tmuxPath: tmuxPath,
		env:      env,
		max:      maxSessions,
		conns:    make(map[string]*AgentTmuxConn),
	}, nil
}

// tmuxCmd builds an exec.Cmd with the manager's cached environment.
func (m *AgentTmuxManager) tmuxCmd(args ...string) *exec.Cmd {
	cmd := exec.Command(m.tmuxPath, args...) //nolint:gosec // tmuxPath from LookPath, caller validates args
	cmd.Env = m.env
	return cmd
}

// HasSession reports whether the named tmux session exists. Used by the WS
// handler's crash detection when the PTY closes unexpectedly.
func (m *AgentTmuxManager) HasSession(name string) bool {
	return m.tmuxCmd("has-session", "-t", name).Run() == nil
}

// PaneDead reports whether the tmux pane's process has exited, even while
// the session itself is still up.
func (m *AgentTmuxManager) PaneDead(name string) bool {
	out, err := m.tmuxCmd("list-panes", "-t", name, "-F", "#{pane_dead}").CombinedOutput()
	if err != nil {
		return true
	}
	return strings.TrimSpace(string(out)) == "1"
}

// CapturePane returns up to lineCount lines from the tmux session's pane,
// for capturing the last-seen output when a backend crashes.
func (m *AgentTmuxManager) CapturePane(name string, lineCount int) string {
	if lineCount <= 0 {
		lineCount = 50
	}
	out, err := m.tmuxCmd("capture-pane", "-t", name, "-p", "-S", fmt.Sprintf("-%d", lineCount)).CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(out), "\n")
}

// SessionCount returns the number of in-progress browser attaches.
func (m *AgentTmuxManager) SessionCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.conns)
}

// MaxSessions returns the configured concurrent-attach cap.
func (m *AgentTmuxManager) MaxSessions() int { return m.max }

// listTmuxSessions returns every tmux session visible on the configured
// socket. Returns (nil, nil) when no tmux server is running — that's a
// normal state, not an error.
func (m *AgentTmuxManager) listTmuxSessions() ([]tmuxSessionMeta, error) {
	cmd := m.tmuxCmd("list-sessions", "-F", "#{session_name}\t#{session_created}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.ToLower(string(out))
		if strings.Contains(msg, "failed to connect to server") ||
			strings.Contains(msg, "no server running") ||
			strings.Contains(msg, "error connecting to") {
			return nil, nil
		}
		return nil, fmt.Errorf("tmux list-sessions failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var sessions []tmuxSessionMeta
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), "\t", 2)
		if len(parts) != 2 {
			continue
		}
		created, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		if err != nil {
			continue
		}
		name := strings.TrimSpace(parts[0])
		if name == "" {
			continue
		}
		sessions = append(sessions, tmuxSessionMeta{name: name, created: created})
	}
	return sessions, scanner.Err()
}

// tmuxSessionMeta is the raw (name, creation-time) pair listTmuxSessions returns.
type tmuxSessionMeta struct {
	name    string
	created int64
}

// FindLatestAgentSession returns the newest tmux session matching the
// auto-mode naming convention for agentName in workspace wsID. Returns
// (_, false, nil) when no session matches — the ws handler then falls back
// to archive logs.
func (m *AgentTmuxManager) FindLatestAgentSession(wsID, agentName string) (string, bool, error) {
	if !validTmuxName.MatchString(agentName) {
		return "", false, fmt.Errorf("invalid agent name %q", agentName)
	}
	if wsID == "" {
		return "", false, nil
	}
	sessions, err := m.listTmuxSessions()
	if err != nil {
		return "", false, err
	}
	wsPrefix := platformruntime.ShortWorkspaceID(wsID)
	pattern := regexp.MustCompile(fmt.Sprintf(
		`^loom-%s-[a-zA-Z0-9_-]+-%s-[0-9]+$`,
		regexp.QuoteMeta(wsPrefix),
		regexp.QuoteMeta(agentName),
	))
	var bestName string
	var bestCreated int64
	found := false
	for _, s := range sessions {
		if !pattern.MatchString(s.name) {
			continue
		}
		if !found || s.created > bestCreated || (s.created == bestCreated && s.name > bestName) {
			bestName = s.name
			bestCreated = s.created
			found = true
		}
	}
	return bestName, found, nil
}

// AttachExistingRaw spawns a tmux attach-session child process bound to a new
// PTY and tracks it under a synthetic connID.
//
//nolint:funlen // tmux attach flow is linear setup; splitting hurts readability
func (m *AgentTmuxManager) AttachExistingRaw(sessionName string, cols, rows uint16) (*AgentTmuxConn, error) {
	if !validTmuxName.MatchString(sessionName) {
		return nil, fmt.Errorf("invalid session name %q", sessionName)
	}
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}

	// HasSession shells out to tmux — check before the lock + connID allocation
	// so rejected attach attempts don't inflate the monotonic counter and a
	// concurrent Shutdown isn't blocked on the tmux exec.
	if !m.HasSession(sessionName) {
		return nil, fmt.Errorf("tmux session %q not found", sessionName)
	}

	m.mu.Lock()
	if len(m.conns) >= m.max {
		m.mu.Unlock()
		return nil, ErrMaxSessionsReached
	}
	connID := fmt.Sprintf("agent-%s-%d", sessionName, m.counter.Add(1))
	m.mu.Unlock()

	// Mouse-mode keeps wheel/input behavior consistent with how auto-mode
	// would see its own sessions. Best-effort — log on failure.
	if out, err := m.tmuxCmd("set-option", "-t", sessionName, "mouse", "on").CombinedOutput(); err != nil {
		slog.Warn("failed to enable mouse mode for tmux session",
			"session", sessionName, "err", err, "output", strings.TrimSpace(string(out)))
	}

	cmd := m.tmuxCmd("attach-session", "-t", sessionName)
	cmd.Env = append(cmd.Env, termEnv)

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("pty.Start tmux attach: %w", err)
	}
	if err := pty.Setsize(ptmx, &pty.Winsize{Cols: cols, Rows: rows}); err != nil {
		ptmx.Close()
		_ = cmd.Wait()
		return nil, fmt.Errorf("pty.Setsize: %w", err)
	}

	conn := &AgentTmuxConn{
		ConnID:      connID,
		SessionName: sessionName,
		PTY:         ptmx,
		cmd:         cmd,
		killCh:      make(chan struct{}),
	}

	m.mu.Lock()
	m.conns[connID] = conn
	m.mu.Unlock()

	return conn, nil
}

// Resize updates the PTY winsize for an attached connection.
func (m *AgentTmuxManager) Resize(connID string, cols, rows uint16) error {
	m.mu.RLock()
	conn, ok := m.conns[connID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("connection %q not found", connID)
	}
	conn.mu.Lock()
	defer conn.mu.Unlock()
	if conn.closed {
		return fmt.Errorf("connection %q is closed", connID)
	}
	if err := pty.Setsize(conn.PTY, &pty.Winsize{Cols: cols, Rows: rows}); err != nil {
		return fmt.Errorf("pty.Setsize: %w", err)
	}
	// Also resize the tmux window so content reflows.
	_ = m.tmuxCmd("resize-window", "-t", conn.SessionName,
		"-x", fmt.Sprintf("%d", cols), "-y", fmt.Sprintf("%d", rows)).Run()
	return nil
}

// Detach removes connID from the map and closes the PTY + reaps the attach
// child. Does NOT kill the underlying tmux session (that's owned by the
// CLI auto-mode).
func (m *AgentTmuxManager) Detach(connID string) error {
	m.mu.Lock()
	conn, ok := m.conns[connID]
	if ok {
		delete(m.conns, connID)
	}
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("connection %q not found", connID)
	}
	return conn.Close()
}

// KillWorkspaceSessions kills every tmux session whose name begins with the
// auto-mode prefix "loom-<wsShort>-". Used when a workspace is deleted to
// stop lingering agent processes. Detached sessions and sessions with
// in-progress browser attaches are both covered.
//
//nolint:funlen // workspace teardown requires enumerating + killing multiple session classes
func (m *AgentTmuxManager) KillWorkspaceSessions(wsID string) error {
	if wsID == "" {
		return fmt.Errorf("wsID must not be empty")
	}
	wsPrefix := "loom-" + platformruntime.ShortWorkspaceID(wsID) + "-"

	sessions, err := m.listTmuxSessions()
	if err != nil {
		return fmt.Errorf("list tmux sessions: %w", err)
	}

	// Collect every session tmux currently knows about.
	toKill := make(map[string]struct{})
	for _, s := range sessions {
		if strings.HasPrefix(s.name, wsPrefix) {
			toKill[s.name] = struct{}{}
		}
	}
	// Plus any we have an attach for that list-sessions might miss mid-setup.
	m.mu.RLock()
	for _, c := range m.conns {
		if strings.HasPrefix(c.SessionName, wsPrefix) {
			toKill[c.SessionName] = struct{}{}
		}
	}
	m.mu.RUnlock()

	// Close active attaches first so ptyToWS / wsToPTY exit cleanly.
	m.mu.Lock()
	var toCloseConns []*AgentTmuxConn
	for connID, c := range m.conns {
		if _, killing := toKill[c.SessionName]; killing {
			toCloseConns = append(toCloseConns, c)
			delete(m.conns, connID)
		}
	}
	m.mu.Unlock()
	// Close attaches in parallel — each Close shells out to wait for the
	// tmux-attach child process.
	var closeWg sync.WaitGroup
	for _, c := range toCloseConns {
		closeWg.Add(1)
		go func(c *AgentTmuxConn) {
			defer closeWg.Done()
			close(c.killCh)
			if err := c.Close(); err != nil {
				slog.Warn("error closing agent tmux attach", "session", c.SessionName, "err", err)
			}
		}(c)
	}
	closeWg.Wait()

	// Kill each tmux session in parallel (each one forks a tmux client).
	var killWg sync.WaitGroup
	for name := range toKill {
		killWg.Add(1)
		go func(name string) {
			defer killWg.Done()
			if err := m.tmuxCmd("kill-session", "-t", name).Run(); err != nil {
				slog.Warn("error killing tmux session", "session", name, "err", err)
			}
		}(name)
	}
	killWg.Wait()
	return nil
}

// Shutdown closes every live attach and clears state. Does not kill the
// underlying tmux sessions (auto-mode owns those).
func (m *AgentTmuxManager) Shutdown() error {
	m.mu.Lock()
	conns := m.conns
	m.conns = make(map[string]*AgentTmuxConn)
	m.mu.Unlock()

	var firstErr error
	for _, c := range conns {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
