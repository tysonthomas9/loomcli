package terminal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
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

// pendingKill wraps a cancel function with a unique identity token. The
// identity is the pointer to the pendingKill struct itself, which is used by
// the ScheduleKill goroutine to detect whether a later ScheduleKill has
// replaced its entry. Comparing CancelFunc values directly (or via fmt.Sprintf
// "%p") does not work because Go prints the underlying code PC rather than
// the closure heap address, so two distinct WithCancel closures compare equal.
type pendingKill struct {
	cancel context.CancelFunc
}

// TerminalManager manages tmux session lifecycles.
// Multiple WebSocket connections can attach to the same tmux session simultaneously,
// each tracked by a unique connection ID.
type TerminalManager struct {
	sessions           map[string]*TerminalSession  // keyed by connection ID
	pendingKills       map[string]*pendingKill      // deferred session kills, guarded by mu; pointer identity distinguishes a replaced entry from the original
	killingSet         map[string]time.Time         // sessions being killed (user-initiated), tombstone with timestamp; keyed by qualified internal tmux name
	scrollbackBuffers  map[string]*ScrollbackBuffer // keyed by qualified internal tmux session name
	sessionOwners      map[string]string            // user-facing session name -> workspace ID, guarded by mu
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
	onSessionKilled    func(wsID, sessionName, scrollbackPath string) // set once at init, read-only after; scrollbackPath is "" if capture failed
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
	// Clamp capacity so per-command `append(cmd.Env, ...)` (e.g. TERM in
	// tmuxAttach) allocates a new backing array instead of racing on this
	// shared slice's spare capacity.
	env = env[:len(env):len(env)]
	return &TerminalManager{
		sessions:           make(map[string]*TerminalSession),
		pendingKills:       make(map[string]*pendingKill),
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

// tmuxName returns the internal tmux session name qualified by the server
// instance prefix and the workspace short ID. Both the server prefix and the
// workspace short ID are embedded so two workspaces with the same user-facing
// session name never collide in tmux, and two loom instances on the same host
// stay isolated from each other.
//
// Format: "<serverPrefix>-<wsShort>-<name>" (or "<wsShort>-<name>" if the
// server prefix is empty — currently only possible in tests).
//
// Callers must have validated wsID is non-empty; an empty wsID silently maps
// to the literal "default" via workspace.ShortWorkspaceID, but public methods
// reject empty wsID before reaching this helper so the fallback is defensive.
func (m *TerminalManager) tmuxName(wsID, name string) string {
	wsShort := workspace.ShortWorkspaceID(wsID)
	if m.sessionPrefix == "" {
		return wsShort + "-" + name
	}
	return m.sessionPrefix + "-" + wsShort + "-" + name
}

// workspacePrefix returns the prefix that all tmux session names for a given
// workspace share, including the trailing dash. Used by KillWorkspaceSessions
// and ListActiveSessionsForWorkspace to filter tmux's list-sessions output.
// Equivalent to tmuxName(wsID, "") — tmuxName always appends "-<name>" so
// passing an empty name leaves the trailing dash that callers rely on.
func (m *TerminalManager) workspacePrefix(wsID string) string {
	return m.tmuxName(wsID, "")
}

// SessionExists reports whether the named tmux session already exists in the
// given workspace. The server prefix and workspace short ID are applied
// automatically. Returns false if wsID is empty.
func (m *TerminalManager) SessionExists(wsID, name string) bool {
	if wsID == "" {
		return false
	}
	return m.tmuxHasSession(m.tmuxName(wsID, name))
}

// checkAttachPreconditionsLocked verifies a session is not being killed
// (tombstone, 30s TTL) and that the concurrent session limit is not exceeded.
// killKey must be the already-qualified internal tmux session name.
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
// Sessions are identified by the shared server-instance prefix. The returned
// names are user-facing (workspace short prefix stripped), so two workspaces
// running the same session name produce duplicate entries — callers that care
// about workspace scoping should use ListActiveSessionsForWorkspace instead.
func (m *TerminalManager) ListActiveSessions() ([]service.TerminalSessionInfo, error) {
	m.mu.RLock()
	sessionPrefix := m.sessionPrefix
	m.mu.RUnlock()

	allSessions, err := m.listTmuxSessions()
	if err != nil {
		return nil, err
	}

	prefix := sessionPrefix + "-"
	var result []service.TerminalSessionInfo

	for _, s := range allSessions {
		var remainder string
		if sessionPrefix == "" {
			remainder = s.name
		} else if strings.HasPrefix(s.name, prefix) {
			remainder = strings.TrimPrefix(s.name, prefix)
		} else {
			continue
		}

		// remainder is "<wsShort>-<userName>"; strip the first segment.
		idx := strings.Index(remainder, "-")
		if idx < 0 || idx == len(remainder)-1 {
			continue
		}
		name := remainder[idx+1:]

		result = append(result, service.TerminalSessionInfo{
			Name:    name,
			Label:   name,
			Created: s.created,
		})
	}

	return result, nil
}

// ListActiveSessionsForWorkspace returns active sessions filtered by the
// server-instance and workspace prefixes. Only sessions that belong to wsID
// (based on their qualified tmux name) are returned, so two workspaces running
// the same user-facing session name stay isolated. Returns an empty slice
// when wsID is empty.
func (m *TerminalManager) ListActiveSessionsForWorkspace(wsID string) ([]service.TerminalSessionInfo, error) {
	if wsID == "" {
		return nil, nil
	}
	allSessions, err := m.listTmuxSessions()
	if err != nil {
		return nil, err
	}

	prefix := m.workspacePrefix(wsID)
	hasTalkToLead := false
	var result []service.TerminalSessionInfo

	for _, s := range allSessions {
		if !strings.HasPrefix(s.name, prefix) {
			continue
		}
		name := strings.TrimPrefix(s.name, prefix)
		if name == "" {
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

// Attach creates a new PTY connection to a tmux session in the given workspace.
// If the tmux session doesn't exist, it creates one with the given command.
// If command is empty, uses the manager's default command.
// Multiple connections can attach to the same tmux session simultaneously.
// Re-attaching cancels any pending deferred kill for this session.
//
// wsID must be non-empty; the caller is expected to derive it from the
// request's workspace context.
func (m *TerminalManager) Attach(wsID, name, command string, cols, rows uint16) (*TerminalSession, error) {
	if wsID == "" {
		return nil, fmt.Errorf("wsID must not be empty")
	}
	if !validSessionName.MatchString(name) {
		return nil, fmt.Errorf("invalid session name %q: must match [a-zA-Z0-9_-]+", name)
	}
	cols, rows = m.normalizeDimensions(cols, rows)
	if command == "" {
		command = m.defaultCommand
	}

	// Apply prefix scheme to get the qualified tmux session name. All in-memory
	// state (pendingKills, killingSet, scrollbackBuffers) is keyed by this
	// qualified name so two workspaces with the same user-facing session name
	// never collide.
	internalName := m.tmuxName(wsID, name)
	connID := m.nextConnID(internalName)

	m.mu.Lock()
	defer m.mu.Unlock()

	// Cancel any pending deferred kill and check preconditions inside the
	// same critical section. Doing the cancel before the lock left a window
	// where a scheduled-kill goroutine's timer could fire after the cancel
	// but before the preconditions check, marking the session as killing
	// even though we were trying to reconnect to it.
	if entry, ok := m.pendingKills[internalName]; ok {
		entry.cancel()
		delete(m.pendingKills, internalName)
	}

	if err := m.checkAttachPreconditionsLocked(internalName); err != nil {
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
