package webui

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

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
	m.sessionOwners = make(map[string]string)
	m.mu.Unlock()

	// Close all PTYs first.
	for connID, session := range sessions {
		if err := session.Close(); err != nil {
			slog.Warn("error closing connection", "conn_id", connID, "err", err)
		}
	}

	// Kill tmux sessions (deduplicate by session name).
	killed := make(map[string]bool)
	for _, session := range sessions {
		if killed[session.Name] {
			continue
		}
		killed[session.Name] = true
		cmd := exec.Command(m.tmuxPath, "kill-session", "-t", session.Name) //nolint:gosec // tmuxPath validated at init; session.Name is internal
		if err := cmd.Run(); err != nil {
			slog.Warn("error killing tmux session", "session", session.Name, "err", err)
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
			slog.Warn("error closing connection for session", "session", internalName, "err", err)
		}
	}

	// Capture scrollback to file before killing tmux session (best-effort).
	m.captureScrollbackToFile(name)

	// Kill the tmux session. Ignore errors (session may not exist).
	cmd := exec.Command(m.tmuxPath, "kill-session", "-t", internalName) //nolint:gosec // tmuxPath validated at init; internalName is prefixed internal name
	_ = cmd.Run()

	// Clean up the scrollback buffer and ownership entry for this session.
	m.mu.Lock()
	delete(m.scrollbackBuffers, internalName)
	delete(m.sessionOwners, name)
	m.mu.Unlock()

	// Invoke the session-killed callback (for session history recording).
	if m.onSessionKilled != nil {
		m.onSessionKilled(name)
	}

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

	cmd := exec.Command(m.tmuxPath, "send-keys", "-t", internalName, "-l", text) //nolint:gosec // tmuxPath validated at init; name regex-validated
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
