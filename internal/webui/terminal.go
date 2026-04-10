package webui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/creack/pty"

	"github.com/tysonthomas9/loomcli/internal/workspace"
)

// ErrTmuxNotFound is returned when tmux binary is not in PATH.
var ErrTmuxNotFound = errors.New("tmux binary not found in PATH")

// lookPathTmux is the function used to locate the tmux binary.
// It is a variable so tests can override it.
var lookPathTmux = exec.LookPath

// ErrMaxSessionsReached is returned when the maximum number of concurrent
// terminal sessions has been reached.
var ErrMaxSessionsReached = errors.New("maximum terminal sessions reached")

// validSessionName matches alphanumeric characters, hyphens, and underscores.
var validSessionName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// TerminalSession represents a single active PTY connection to a tmux session.
type TerminalSession struct {
	ConnID  string   // unique connection ID (e.g., "talk-to-lead:1")
	Name    string   // tmux session name (e.g., "talk-to-lead")
	Command string   // command running in the session
	PTY     *os.File // PTY master fd from creack/pty
	cmd     *exec.Cmd
	killCh  chan struct{} // closed by KillSessionByName to signal WS handlers
	mu      sync.Mutex
	closed  bool
}

// Close closes the PTY and waits for the tmux attach process to exit.
// It is safe to call multiple times.
func (s *TerminalSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true

	var firstErr error
	if s.PTY != nil {
		if err := s.PTY.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.cmd != nil && s.cmd.Process != nil {
		// Wait for process to exit after PTY close.
		// Ignore error — process may have already exited.
		_ = s.cmd.Wait()
	}
	return firstErr
}

// defaultMaxTerminalSessions is the maximum number of concurrent terminal
// connections when no explicit limit is configured.
const defaultMaxTerminalSessions = 20

// ErrSessionBeingKilled is returned by Attach when a session is in the killing set.
var ErrSessionBeingKilled = errors.New("session is being killed")

// TerminalManager manages tmux session lifecycles.
// Multiple WebSocket connections can attach to the same tmux session simultaneously,
// each tracked by a unique connection ID.
type TerminalManager struct {
	sessions           map[string]*TerminalSession   // keyed by connection ID
	pendingKills       map[string]context.CancelFunc // deferred session kills, guarded by mu
	killingSet         map[string]time.Time          // sessions being killed (user-initiated), tombstone with timestamp
	scrollbackBuffers  map[string]*ScrollbackBuffer  // keyed by tmux session name (internal)
	mu                 sync.RWMutex
	tmuxPath           string
	tmuxEnv            []string // cached environment for tmux subprocesses
	sessionPrefix      string   // prepended to tmux session names for isolation between server instances
	defaultCommand     string   // command to run in all terminal sessions
	defaultCols        uint16
	defaultRows        uint16
	maxSessions        int // maximum concurrent connections (immutable after construction)
	scrollbackMaxLines int // max lines per scrollback buffer (default: defaultScrollbackMaxLines)
	connCounter        atomic.Uint64
	onSessionKilled    func(sessionName string) // set once at init, read-only after
}

// NewTerminalManager creates a manager. Returns ErrTmuxNotFound if tmux is not installed.
// The defaultCommand parameter specifies what command to run in all terminal sessions.
// The sessionPrefix is prepended to tmux session names (e.g., port number) to isolate
// sessions when multiple server instances share the same tmux server.
// maxSessions limits concurrent connections; 0 means use defaultMaxTerminalSessions.
func NewTerminalManager(defaultCommand, sessionPrefix string, maxSessions int) (*TerminalManager, error) {
	tmuxPath, err := lookPathTmux("tmux")
	if err != nil {
		return nil, ErrTmuxNotFound
	}
	if maxSessions <= 0 {
		maxSessions = defaultMaxTerminalSessions
	}
	// Cache environment for tmux subprocesses, adding TMUX_TMPDIR if absent
	// so all tmux processes use the same socket path.
	env := os.Environ()
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
	return &TerminalManager{
		sessions:           make(map[string]*TerminalSession),
		pendingKills:       make(map[string]context.CancelFunc),
		killingSet:         make(map[string]time.Time),
		scrollbackBuffers:  make(map[string]*ScrollbackBuffer),
		tmuxPath:           tmuxPath,
		tmuxEnv:            env,
		sessionPrefix:      sessionPrefix,
		defaultCommand:     defaultCommand,
		defaultCols:        80,
		defaultRows:        24,
		maxSessions:        maxSessions,
		scrollbackMaxLines: defaultScrollbackMaxLines,
	}, nil
}

// tmuxName returns the internal tmux session name with the prefix applied.
func (m *TerminalManager) tmuxName(name string) string {
	if m.sessionPrefix == "" {
		return name
	}
	return m.sessionPrefix + "-" + name
}

// checkAttachPreconditionsLocked verifies a session is not being killed
// (tombstone, 30s TTL) and that the concurrent session limit is not exceeded.
// Caller must hold m.mu.
func (m *TerminalManager) checkAttachPreconditionsLocked(killKey string) error {
	if killTime, beingKilled := m.killingSet[killKey]; beingKilled {
		if time.Since(killTime) < 30*time.Second {
			return ErrSessionBeingKilled
		}
		delete(m.killingSet, killKey)
	}
	if m.maxSessions > 0 && len(m.sessions) >= m.maxSessions {
		return ErrMaxSessionsReached
	}
	return nil
}

// SessionExists reports whether the named tmux session already exists.
// The session prefix is applied automatically.
func (m *TerminalManager) SessionExists(name string) bool {
	return m.tmuxHasSession(m.tmuxName(name))
}

// tmuxCmd creates an exec.Cmd for a tmux subcommand with the cached environment.
func (m *TerminalManager) tmuxCmd(args ...string) *exec.Cmd {
	cmd := exec.Command(m.tmuxPath, args...)
	cmd.Env = m.tmuxEnv
	return cmd
}

// tmuxHasSession checks whether a tmux session with the given name exists.
func (m *TerminalManager) tmuxHasSession(name string) bool {
	return m.tmuxCmd("has-session", "-t", name).Run() == nil
}

// tmuxNewSession creates a new detached tmux session with the given name, size, and command.
// Enables mouse mode so wheel events are forwarded to the application inside tmux.
func (m *TerminalManager) tmuxNewSession(name, command string, cols, rows uint16) error {
	args := []string{
		"new-session", "-d",
		"-s", name,
		"-x", fmt.Sprintf("%d", cols),
		"-y", fmt.Sprintf("%d", rows),
	}
	if command != "" {
		args = append(args, command)
	}
	if err := m.tmuxCmd(args...).Run(); err != nil {
		return err
	}
	m.applySessionOptions(name)
	return nil
}

// applySessionOptions enables mouse mode and sets the scrollback history limit
// on an existing tmux session. Failures are logged but not returned — these are
// best-effort ergonomic tweaks, not correctness requirements.
func (m *TerminalManager) applySessionOptions(name string) {
	for _, opt := range [][2]string{
		{"mouse", "on"},
		{"history-limit", fmt.Sprintf("%d", m.scrollbackMaxLines)},
	} {
		optCmd := m.tmuxCmd("set-option", "-t", name, opt[0], opt[1])
		if out, err := optCmd.CombinedOutput(); err != nil {
			log.Printf("Warning: failed to set %s for session %q: %v: %s", opt[0], name, err, strings.TrimSpace(string(out)))
		}
	}
}

// tmuxAttach spawns a tmux attach-session process with a PTY.
func (m *TerminalManager) tmuxAttach(name string) (*exec.Cmd, *os.File, error) {
	cmd := m.tmuxCmd("attach-session", "-t", name)
	// Always set TERM to a capable terminal type for the subprocess.
	// tmux 3.6+ exits immediately with "open terminal failed" when TERM is
	// unset, "dumb", "unknown", or any other value it doesn't recognize.
	cmd.Env = append(cmd.Env, "TERM=xterm-256color")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, nil, fmt.Errorf("pty.Start tmux attach: %w", err)
	}
	return cmd, ptmx, nil
}

// Attach creates a new PTY connection to a tmux session.
// If the tmux session doesn't exist, it creates one with the given command.
// If command is empty, uses the manager's default command.
// Multiple connections can attach to the same tmux session simultaneously.
// Re-attaching cancels any pending deferred kill for this session.
func (m *TerminalManager) Attach(name, command string, cols, rows uint16) (*TerminalSession, error) {
	if !validSessionName.MatchString(name) {
		return nil, fmt.Errorf("invalid session name %q: must match [a-zA-Z0-9_-]+", name)
	}
	if cols == 0 {
		cols = m.defaultCols
	}
	if rows == 0 {
		rows = m.defaultRows
	}
	if command == "" {
		command = m.defaultCommand
	}

	// Cancel any pending deferred kill for this session.
	m.CancelPendingKill(name)

	// Apply prefix to get the actual tmux session name.
	internalName := m.tmuxName(name)

	// Generate unique connection ID using internal name for debuggability.
	connNum := m.connCounter.Add(1)
	connID := fmt.Sprintf("%s:%d", internalName, connNum)

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkAttachPreconditionsLocked(name); err != nil {
		return nil, err
	}

	// Create tmux session if it doesn't exist.
	if !m.tmuxHasSession(internalName) {
		if err := m.tmuxNewSession(internalName, command, cols, rows); err != nil {
			return nil, fmt.Errorf("tmux new-session: %w", err)
		}
	}

	// Attach with a fresh PTY.
	cmd, ptmx, err := m.tmuxAttach(internalName)
	if err != nil {
		return nil, err
	}

	// Set initial size.
	if err := pty.Setsize(ptmx, &pty.Winsize{Cols: cols, Rows: rows}); err != nil {
		ptmx.Close()
		_ = cmd.Wait()
		return nil, fmt.Errorf("pty.Setsize: %w", err)
	}

	session := &TerminalSession{
		ConnID:  connID,
		Name:    internalName,
		Command: command,
		PTY:     ptmx,
		cmd:     cmd,
		killCh:  make(chan struct{}),
	}
	m.sessions[connID] = session
	return session, nil
}

// AttachExistingRaw attaches a PTY connection to an already-running tmux session
// name without applying session prefix rewriting and without creating a new session.
func (m *TerminalManager) AttachExistingRaw(tmuxSessionName string, cols, rows uint16) (*TerminalSession, error) {
	if !validSessionName.MatchString(tmuxSessionName) {
		return nil, fmt.Errorf("invalid session name %q: must match [a-zA-Z0-9_-]+", tmuxSessionName)
	}
	if cols == 0 {
		cols = m.defaultCols
	}
	if rows == 0 {
		rows = m.defaultRows
	}

	connNum := m.connCounter.Add(1)
	connID := fmt.Sprintf("%s:%d", tmuxSessionName, connNum)

	m.mu.Lock()
	defer m.mu.Unlock()

	// AttachExistingRaw uses raw names, so the tombstone is keyed on the raw name.
	if err := m.checkAttachPreconditionsLocked(tmuxSessionName); err != nil {
		return nil, err
	}

	if !m.tmuxHasSession(tmuxSessionName) {
		return nil, fmt.Errorf("tmux session %q not found", tmuxSessionName)
	}

	// Mirror Talk-to-Lead behavior so wheel/input interactions are consistent.
	if out, err := m.tmuxCmd("set-option", "-t", tmuxSessionName, "mouse", "on").CombinedOutput(); err != nil {
		log.Printf("Warning: failed to enable mouse mode for session %q: %v: %s", tmuxSessionName, err, strings.TrimSpace(string(out)))
	}

	cmd, ptmx, err := m.tmuxAttach(tmuxSessionName)
	if err != nil {
		return nil, err
	}

	if err := pty.Setsize(ptmx, &pty.Winsize{Cols: cols, Rows: rows}); err != nil {
		ptmx.Close()
		_ = cmd.Wait()
		return nil, fmt.Errorf("pty.Setsize: %w", err)
	}

	session := &TerminalSession{
		ConnID: connID,
		Name:   tmuxSessionName,
		PTY:    ptmx,
		cmd:    cmd,
		killCh: make(chan struct{}),
	}
	m.sessions[connID] = session
	return session, nil
}

type tmuxSessionMeta struct {
	name    string
	created int64
}

func (m *TerminalManager) listTmuxSessions() ([]tmuxSessionMeta, error) {
	cmd := m.tmuxCmd("list-sessions", "-F", "#{session_name}\t#{session_created}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.ToLower(string(out))
		// No tmux server/sessions is a normal state for archive fallback.
		if strings.Contains(msg, "failed to connect to server") || strings.Contains(msg, "no server running") || strings.Contains(msg, "error connecting to") {
			return nil, nil
		}
		return nil, fmt.Errorf("tmux list-sessions failed: %w: %s", err, strings.TrimSpace(string(out)))
	}

	var sessions []tmuxSessionMeta
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, "\t", 2)
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
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return sessions, nil
}

// FindLatestAgentSession returns the newest tmux session matching the auto-mode
// naming convention for an agent: loom-<wsPrefix>-<role>-<agent>-<pid>.
// When workspaceID is non-empty, only sessions for that workspace are matched.
// When workspaceID is empty, returns no match (fail-closed).
func (m *TerminalManager) FindLatestAgentSession(workspaceID, agentName string) (string, bool, error) {
	if !validSessionName.MatchString(agentName) {
		return "", false, fmt.Errorf("invalid agent name %q", agentName)
	}

	sessions, err := m.listTmuxSessions()
	if err != nil {
		return "", false, err
	}

	// When workspace ID is empty, fail closed — no match rather than match-all.
	if workspaceID == "" {
		return "", false, nil
	}
	wsPrefix := workspace.ShortWorkspaceID(workspaceID)
	pattern := regexp.MustCompile(fmt.Sprintf(`^loom-%s-[a-zA-Z0-9_-]+-%s-[0-9]+$`, regexp.QuoteMeta(wsPrefix), regexp.QuoteMeta(agentName)))

	var bestName string
	var bestCreated int64
	found := false
	for _, session := range sessions {
		if !pattern.MatchString(session.name) {
			continue
		}
		if !found || session.created > bestCreated || (session.created == bestCreated && session.name > bestName) {
			bestName = session.name
			bestCreated = session.created
			found = true
		}
	}
	if !found {
		return "", false, nil
	}
	return bestName, true, nil
}

// Spawn creates a tmux session without attaching a PTY connection.
// It is idempotent: if the session already exists, it returns (false, nil).
// The returned bool indicates whether a new session was created.
func (m *TerminalManager) Spawn(name, command string, cols, rows uint16) (bool, error) {
	if !validSessionName.MatchString(name) {
		return false, fmt.Errorf("invalid session name %q: must match [a-zA-Z0-9_-]+", name)
	}
	if cols == 0 {
		cols = m.defaultCols
	}
	if rows == 0 {
		rows = m.defaultRows
	}

	internalName := m.tmuxName(name)

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.tmuxHasSession(internalName) {
		return false, nil
	}

	if err := m.tmuxNewSession(internalName, command, cols, rows); err != nil {
		return false, fmt.Errorf("tmux new-session: %w", err)
	}
	return true, nil
}

// tmuxNewSessionArgv creates a new detached tmux session that execs argv directly
// instead of passing the command through a shell. Use this when any argument may
// contain user-supplied text, to avoid shell-injection risk.
// If workDir is non-empty it is passed to tmux via -c, so the child process
// starts in that directory instead of the parent's cwd.
func (m *TerminalManager) tmuxNewSessionArgv(name string, argv []string, cols, rows uint16, workDir string) error {
	if len(argv) == 0 {
		return fmt.Errorf("tmuxNewSessionArgv: argv must not be empty")
	}
	args := []string{
		"new-session", "-d",
		"-s", name,
		"-x", fmt.Sprintf("%d", cols),
		"-y", fmt.Sprintf("%d", rows),
	}
	if workDir != "" {
		args = append(args, "-c", workDir)
	}
	args = append(args, argv...)
	if err := m.tmuxCmd(args...).Run(); err != nil {
		return err
	}
	m.applySessionOptions(name)
	return nil
}

// SpawnInDir is like Spawn but starts the tmux session in the given working
// directory via tmux -c. If workDir is empty, the session inherits the loom
// service's cwd (same as Spawn). Idempotent: if a session with this name
// already exists, returns (false, nil).
//
// Used by the terminal spawn handler to land new "+ Tab" sessions in the
// active workspace's path rather than the loom service's cwd.
func (m *TerminalManager) SpawnInDir(name, command string, cols, rows uint16, workDir string) (bool, error) {
	if !validSessionName.MatchString(name) {
		return false, fmt.Errorf("invalid session name %q: must match [a-zA-Z0-9_-]+", name)
	}
	if command == "" {
		return false, fmt.Errorf("command must not be empty")
	}
	if cols == 0 {
		cols = m.defaultCols
	}
	if rows == 0 {
		rows = m.defaultRows
	}

	internalName := m.tmuxName(name)

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.tmuxHasSession(internalName) {
		return false, nil
	}

	// Pass as single-element argv so tmux execs the command directly with the
	// cwd set via -c. For single-binary commands (claude, /bin/bash, etc.) this
	// is behaviorally identical to Spawn but with workDir support.
	if err := m.tmuxNewSessionArgv(internalName, []string{command}, cols, rows, workDir); err != nil {
		return false, fmt.Errorf("tmux new-session: %w", err)
	}
	return true, nil
}

// SpawnArgv creates a tmux session whose command is execv'd directly from argv,
// bypassing shell interpretation. Use this for commands containing user-supplied
// text (e.g. an initial agent prompt) to avoid shell-injection risks.
// If workDir is non-empty, the session starts in that directory (via tmux -c);
// otherwise it inherits the loom service's cwd. Idempotent: if the session
// already exists it returns (false, nil).
func (m *TerminalManager) SpawnArgv(name string, argv []string, cols, rows uint16, workDir string) (bool, error) {
	if !validSessionName.MatchString(name) {
		return false, fmt.Errorf("invalid session name %q: must match [a-zA-Z0-9_-]+", name)
	}
	if len(argv) == 0 {
		return false, fmt.Errorf("argv must not be empty")
	}
	if cols == 0 {
		cols = m.defaultCols
	}
	if rows == 0 {
		rows = m.defaultRows
	}

	internalName := m.tmuxName(name)

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.tmuxHasSession(internalName) {
		return false, nil
	}

	if err := m.tmuxNewSessionArgv(internalName, argv, cols, rows, workDir); err != nil {
		return false, fmt.Errorf("tmux new-session: %w", err)
	}
	return true, nil
}

// Resize changes the PTY and tmux window dimensions for a connection.
func (m *TerminalManager) Resize(connID string, cols, rows uint16) error {
	m.mu.RLock()
	session, ok := m.sessions[connID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("connection %q not found", connID)
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return fmt.Errorf("connection %q is closed", connID)
	}
	if err := pty.Setsize(session.PTY, &pty.Winsize{Cols: cols, Rows: rows}); err != nil {
		return fmt.Errorf("pty.Setsize: %w", err)
	}
	// Also tell tmux to resize the window so content reflows properly.
	if err := m.tmuxCmd("resize-window", "-t", session.Name, "-x", fmt.Sprintf("%d", cols), "-y", fmt.Sprintf("%d", rows)).Run(); err != nil {
		return fmt.Errorf("tmux resize-window: %w", err)
	}
	return nil
}

// Detach closes a specific PTY connection without killing the tmux session.
func (m *TerminalManager) Detach(connID string) error {
	m.mu.Lock()
	session, ok := m.sessions[connID]
	if ok {
		delete(m.sessions, connID)
	}
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("connection %q not found", connID)
	}
	return session.Close()
}
