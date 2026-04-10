package terminal

import (
	"bufio"
	"context"
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
	"time"

	"github.com/creack/pty"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
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
	sessionOwners      map[string]string             // user-facing session name -> workspace ID, guarded by mu
	mu                 sync.RWMutex
	tmuxPath           string
	tmuxEnv            []string // cached environment for tmux subprocesses (TMUX_TMPDIR pinned)
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
		sessionOwners:      make(map[string]string),
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

// SessionExists reports whether the named tmux session already exists.
// The session prefix is applied automatically.
func (m *TerminalManager) SessionExists(name string) bool {
	return m.tmuxHasSession(m.tmuxName(name))
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

// SetSessionOwner records which workspace owns a session.
// Uses first-write-wins: if ownership is already recorded, subsequent calls are no-ops.
// name is the user-facing session name (not the tmux-internal prefixed name).
func (m *TerminalManager) SetSessionOwner(name, workspaceID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.sessionOwners[name]; !exists {
		m.sessionOwners[name] = workspaceID
	}
}

// SessionOwner returns the workspace ID that owns the given session.
// Returns empty string and false if no owner is recorded.
func (m *TerminalManager) SessionOwner(name string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ws, ok := m.sessionOwners[name]
	return ws, ok
}

// ListActiveSessions returns sessions owned by this server instance.
func (m *TerminalManager) ListActiveSessions() ([]service.TerminalSessionInfo, error) {
	m.mu.RLock()
	sessionPrefix := m.sessionPrefix
	m.mu.RUnlock()

	allSessions, err := m.listTmuxSessions()
	if err != nil {
		return nil, err
	}

	prefix := sessionPrefix + "-"
	hasTalkToLead := false
	var result []service.TerminalSessionInfo

	for _, s := range allSessions {
		var name string
		if sessionPrefix == "" {
			name = s.name
		} else if strings.HasPrefix(s.name, prefix) {
			name = strings.TrimPrefix(s.name, prefix)
		} else {
			continue
		}

		result = append(result, service.TerminalSessionInfo{
			Name:    name,
			Label:   name,
			Created: s.created,
		})
		if name == "talk-to-lead" {
			hasTalkToLead = true
		}
	}

	if !hasTalkToLead {
		result = append([]service.TerminalSessionInfo{{
			Name:    "talk-to-lead",
			Label:   "talk-to-lead",
			Created: 0,
		}}, result...)
	}

	return result, nil
}

// ListActiveSessionsForWorkspace returns active sessions filtered by workspace ownership.
// Sessions owned by the specified workspace are included.
// Sessions with no recorded owner are also included (backward compatibility).
// Sessions owned by a different workspace are excluded.
func (m *TerminalManager) ListActiveSessionsForWorkspace(workspaceID string) ([]service.TerminalSessionInfo, error) {
	all, err := m.ListActiveSessions()
	if err != nil {
		return nil, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []service.TerminalSessionInfo
	for _, s := range all {
		owner, hasOwner := m.sessionOwners[s.Name]
		if !hasOwner || owner == workspaceID {
			result = append(result, s)
		}
	}
	return result, nil
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
	cols, rows = m.normalizeDimensions(cols, rows)
	if command == "" {
		command = m.defaultCommand
	}

	// Cancel any pending deferred kill for this session.
	m.CancelPendingKill(name)

	internalName := m.tmuxName(name)
	connID := m.nextConnID(internalName)

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkAttachPreconditionsLocked(name); err != nil {
		return nil, err
	}

	if !m.tmuxHasSession(internalName) {
		if err := m.tmuxNewSession(internalName, command, cols, rows); err != nil {
			return nil, fmt.Errorf("tmux new-session: %w", err)
		}
	}

	return m.attachPTY(connID, internalName, command, cols, rows)
}

// normalizeDimensions returns cols/rows with defaults applied for zero values.
func (m *TerminalManager) normalizeDimensions(cols, rows uint16) (uint16, uint16) {
	if cols == 0 {
		cols = m.defaultCols
	}
	if rows == 0 {
		rows = m.defaultRows
	}
	return cols, rows
}

// nextConnID generates a unique connection ID for the given internal session name.
func (m *TerminalManager) nextConnID(internalName string) string {
	connNum := m.connCounter.Add(1)
	return fmt.Sprintf("%s:%d", internalName, connNum)
}

// attachPTY creates a PTY attachment to an existing tmux session and registers it.
// Must be called with m.mu held.
func (m *TerminalManager) attachPTY(connID, internalName, command string, cols, rows uint16) (*TerminalSession, error) {
	cmd, ptmx, err := m.tmuxAttach(internalName)
	if err != nil {
		return nil, err
	}

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
	cols, rows = m.normalizeDimensions(cols, rows)

	connID := m.nextConnID(tmuxSessionName)

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
		slog.Warn("failed to enable mouse mode for session", "session", tmuxSessionName, "err", err, "output", strings.TrimSpace(string(out)))
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
