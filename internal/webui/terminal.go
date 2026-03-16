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

// TerminalManager manages tmux session lifecycles.
// Multiple WebSocket connections can attach to the same tmux session simultaneously,
// each tracked by a unique connection ID.
type TerminalManager struct {
	sessions           map[string]*TerminalSession   // keyed by connection ID
	pendingKills       map[string]context.CancelFunc // deferred session kills, guarded by mu
	scrollbackBuffers  map[string]*ScrollbackBuffer  // keyed by tmux session name (internal)
	mu                 sync.RWMutex
	tmuxPath           string
	sessionPrefix      string // prepended to tmux session names for isolation between server instances
	defaultCommand     string // command to run in all terminal sessions
	defaultCols        uint16
	defaultRows        uint16
	maxSessions        int // maximum concurrent connections (immutable after construction)
	scrollbackMaxLines int // max lines per scrollback buffer (default: defaultScrollbackMaxLines)
	connCounter        atomic.Uint64
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
	return &TerminalManager{
		sessions:           make(map[string]*TerminalSession),
		pendingKills:       make(map[string]context.CancelFunc),
		scrollbackBuffers:  make(map[string]*ScrollbackBuffer),
		tmuxPath:           tmuxPath,
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

// tmuxHasSession checks whether a tmux session with the given name exists.
func (m *TerminalManager) tmuxHasSession(name string) bool {
	cmd := exec.Command(m.tmuxPath, "has-session", "-t", name)
	return cmd.Run() == nil
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
	cmd := exec.Command(m.tmuxPath, args...)
	if err := cmd.Run(); err != nil {
		return err
	}

	// Enable mouse mode and set scrollback history limit.
	for _, opt := range [][2]string{
		{"mouse", "on"},
		{"history-limit", fmt.Sprintf("%d", m.scrollbackMaxLines)},
	} {
		c := exec.Command(m.tmuxPath, "set-option", "-t", name, opt[0], opt[1])
		if err := c.Run(); err != nil {
			log.Printf("Warning: failed to set %s for session %q: %v", opt[0], name, err)
		}
	}
	return nil
}

// tmuxAttach spawns a tmux attach-session process with a PTY.
func (m *TerminalManager) tmuxAttach(name string) (*exec.Cmd, *os.File, error) {
	cmd := exec.Command(m.tmuxPath, "attach-session", "-t", name)
	// Ensure TERM is set to a capable terminal type so tmux can operate.
	// With TERM unset or set to "dumb", tmux 3.6+ exits immediately with status 1.
	if term := os.Getenv("TERM"); term == "" || term == "dumb" {
		cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	}
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

	// Enforce concurrent session limit.
	if m.maxSessions > 0 && len(m.sessions) >= m.maxSessions {
		return nil, ErrMaxSessionsReached
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

	if m.maxSessions > 0 && len(m.sessions) >= m.maxSessions {
		return nil, ErrMaxSessionsReached
	}

	if !m.tmuxHasSession(tmuxSessionName) {
		return nil, fmt.Errorf("tmux session %q not found", tmuxSessionName)
	}

	// Mirror Talk-to-Lead behavior so wheel/input interactions are consistent.
	mouseCmd := exec.Command(m.tmuxPath, "set-option", "-t", tmuxSessionName, "mouse", "on")
	if err := mouseCmd.Run(); err != nil {
		log.Printf("Warning: failed to enable mouse mode for session %q: %v", tmuxSessionName, err)
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
	}
	m.sessions[connID] = session
	return session, nil
}

type tmuxSessionMeta struct {
	name    string
	created int64
}

func (m *TerminalManager) listTmuxSessions() ([]tmuxSessionMeta, error) {
	cmd := exec.Command(m.tmuxPath, "list-sessions", "-F", "#{session_name}\t#{session_created}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.ToLower(string(out))
		// No tmux server/sessions is a normal state for archive fallback.
		if strings.Contains(msg, "failed to connect to server") || strings.Contains(msg, "no server running") {
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
// naming convention for an agent: loom-<role>-<agent>-<pid>.
func (m *TerminalManager) FindLatestAgentSession(agentName string) (string, bool, error) {
	if !validSessionName.MatchString(agentName) {
		return "", false, fmt.Errorf("invalid agent name %q", agentName)
	}

	sessions, err := m.listTmuxSessions()
	if err != nil {
		return "", false, err
	}
	pattern := regexp.MustCompile(fmt.Sprintf(`^loom-[a-zA-Z0-9_-]+-%s-[0-9]+$`, regexp.QuoteMeta(agentName)))

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
	cmd := exec.Command(m.tmuxPath, "resize-window", "-t", session.Name, "-x", fmt.Sprintf("%d", cols), "-y", fmt.Sprintf("%d", rows))
	if err := cmd.Run(); err != nil {
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

// Shutdown kills all tmux sessions and cleans up all PTYs.
func (m *TerminalManager) Shutdown() error {
	m.mu.Lock()
	// Cancel all pending deferred kills.
	for name, cancel := range m.pendingKills {
		cancel()
		delete(m.pendingKills, name)
	}
	sessions := make(map[string]*TerminalSession, len(m.sessions))
	for k, v := range m.sessions {
		sessions[k] = v
	}
	m.sessions = make(map[string]*TerminalSession)
	m.scrollbackBuffers = make(map[string]*ScrollbackBuffer)
	m.mu.Unlock()

	// Close all PTYs first.
	for connID, session := range sessions {
		if err := session.Close(); err != nil {
			log.Printf("Warning: error closing connection %q: %v", connID, err)
		}
	}

	// Kill tmux sessions (deduplicate by session name).
	killed := make(map[string]bool)
	for _, session := range sessions {
		if killed[session.Name] {
			continue
		}
		killed[session.Name] = true
		cmd := exec.Command(m.tmuxPath, "kill-session", "-t", session.Name)
		if err := cmd.Run(); err != nil {
			log.Printf("Warning: error killing tmux session %q: %v", session.Name, err)
		}
	}

	return nil
}

// SetDefaultCommand updates the command used for new terminal sessions.
func (m *TerminalManager) SetDefaultCommand(cmd string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.defaultCommand = cmd
}

// DefaultCommand returns the current default command for new sessions.
func (m *TerminalManager) DefaultCommand() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.defaultCommand
}

// KillSessionByName closes all connections to the named session and kills the tmux session.
// Returns nil if the session doesn't exist.
func (m *TerminalManager) KillSessionByName(name string) error {
	internalName := m.tmuxName(name)

	// Collect connections matching this session name under write lock.
	m.mu.Lock()
	var toClose []*TerminalSession
	for connID, session := range m.sessions {
		if session.Name == internalName {
			toClose = append(toClose, session)
			delete(m.sessions, connID)
		}
	}
	m.mu.Unlock()

	// Close collected sessions outside the lock.
	for _, session := range toClose {
		if err := session.Close(); err != nil {
			log.Printf("Warning: error closing connection for session %q: %v", internalName, err)
		}
	}

	// Kill the tmux session. Ignore errors (session may not exist).
	cmd := exec.Command(m.tmuxPath, "kill-session", "-t", internalName)
	_ = cmd.Run()

	// Clean up the scrollback buffer for this session.
	m.mu.Lock()
	delete(m.scrollbackBuffers, internalName)
	m.mu.Unlock()

	return nil
}

// SendKeys sends text to a tmux session via `tmux send-keys`.
// The session name should be the user-facing name (prefix is applied internally).
// The text is sent as literal keys without a trailing Enter.
func (m *TerminalManager) SendKeys(sessionName string, text string) error {
	if !validSessionName.MatchString(sessionName) {
		return fmt.Errorf("invalid session name %q: must match [a-zA-Z0-9_-]+", sessionName)
	}

	internalName := m.tmuxName(sessionName)

	if !m.tmuxHasSession(internalName) {
		return fmt.Errorf("tmux session %q not found", sessionName)
	}

	cmd := exec.Command(m.tmuxPath, "send-keys", "-t", internalName, "-l", text)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux send-keys failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// SessionCount returns the number of active terminal connections.
func (m *TerminalManager) SessionCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}

// MaxSessions returns the maximum allowed concurrent connections.
func (m *TerminalManager) MaxSessions() int {
	return m.maxSessions
}

// ScheduleKill schedules a deferred kill for the named session after the given delay.
// If a pending kill already exists for this session, it is replaced.
// The kill is cancelled if the session is re-attached before the delay expires.
func (m *TerminalManager) ScheduleKill(name string, delay time.Duration) {
	m.mu.Lock()
	// Cancel any existing pending kill for this session.
	if cancel, ok := m.pendingKills[name]; ok {
		cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.pendingKills[name] = cancel
	myCancel := cancel // capture for identity check in goroutine
	m.mu.Unlock()

	go func() {
		select {
		case <-time.After(delay):
			m.mu.Lock()
			// Only delete if this goroutine's cancel is still the current one.
			// A replacement ScheduleKill may have installed a new cancel.
			if current, ok := m.pendingKills[name]; ok && sameFunc(current, myCancel) {
				delete(m.pendingKills, name)
			}
			m.mu.Unlock()
			// Only kill if no active connections remain.
			if !m.HasActiveConnections(name) {
				_ = m.KillSessionByName(name)
			}
		case <-ctx.Done():
			// Cancelled — session was re-attached or server is shutting down.
		}
	}()
}

// sameFunc compares two function values by pointer identity.
func sameFunc(a, b context.CancelFunc) bool {
	return fmt.Sprintf("%p", a) == fmt.Sprintf("%p", b)
}

// CancelPendingKill cancels a pending deferred kill for the named session.
// Returns true if a pending kill was found and cancelled.
func (m *TerminalManager) CancelPendingKill(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cancel, ok := m.pendingKills[name]; ok {
		cancel()
		delete(m.pendingKills, name)
		return true
	}
	return false
}
