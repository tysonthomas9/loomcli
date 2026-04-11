package webui

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Shutdown kills all tmux sessions and cleans up all PTYs.
func (m *TerminalManager) Shutdown() error {
	m.mu.Lock()
	// Cancel all pending deferred kills.
	for name, entry := range m.pendingKills {
		entry.cancel()
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
		if err := m.tmuxCmd("kill-session", "-t", session.Name).Run(); err != nil {
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

// MarkKilling adds a session to the killing tombstone set, preventing Attach
// from recreating it for 30 seconds. Tombstones are keyed by qualified tmux
// name so two workspaces with the same user-facing session name don't share
// each other's tombstones.
func (m *TerminalManager) MarkKilling(wsID, name string) {
	if wsID == "" {
		return
	}
	internalName := m.tmuxName(wsID, name)
	m.markKillingByInternal(internalName)
}

// markKillingByInternal is the internal helper that records a tombstone keyed
// by the already-qualified internal tmux session name. Used by killByInternal
// and KillWorkspaceSessions where the internal name is already in hand.
func (m *TerminalManager) markKillingByInternal(internalName string) {
	m.mu.Lock()
	m.killingSet[internalName] = time.Now()
	m.mu.Unlock()
}

// SessionIsBeingKilled reports whether a session is in the killing tombstone
// set for the given workspace.
func (m *TerminalManager) SessionIsBeingKilled(wsID, name string) bool {
	if wsID == "" {
		return false
	}
	internalName := m.tmuxName(wsID, name)
	m.mu.RLock()
	defer m.mu.RUnlock()
	killTime, ok := m.killingSet[internalName]
	return ok && time.Since(killTime) < 30*time.Second
}

// KillSession closes all connections to the named session in the given
// workspace and kills the tmux session. Returns nil if the session doesn't
// exist. wsID must be non-empty.
func (m *TerminalManager) KillSession(wsID, name string) error {
	if wsID == "" {
		return fmt.Errorf("wsID must not be empty")
	}
	return m.killByInternal(name, m.tmuxName(wsID, name))
}

// killByInternal is the implementation shared by KillSession,
// KillWorkspaceSessions, and ScheduleKill's deferred goroutine. It takes the
// user-facing name (for the onSessionKilled callback so session history is
// keyed by what the UI shows) and the already-computed internal tmux session
// name (used as the in-memory map key and as the tmux -t target).
func (m *TerminalManager) killByInternal(userName, internalName string) error {
	// Mark as killing so Attach rejects reconnect attempts.
	m.markKillingByInternal(internalName)

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

	// Signal kill to WS handlers, then close PTYs.
	for _, session := range toClose {
		close(session.killCh)
		if err := session.Close(); err != nil {
			slog.Warn("error closing connection for session", "session", internalName, "err", err)
		}
	}

	// Capture scrollback to file before killing tmux session (best-effort).
	m.captureScrollbackToFileByInternal(userName, internalName)

	// Kill the tmux session. Ignore errors (session may not exist).
	_ = m.tmuxCmd("kill-session", "-t", internalName).Run()

	// Clean up the scrollback buffer for this session.
	m.mu.Lock()
	delete(m.scrollbackBuffers, internalName)
	m.mu.Unlock()

	// Invoke the session-killed callback (for session history recording).
	// The callback receives the user-facing session name so session-history
	// records are keyed the same way the UI refers to them.
	if m.onSessionKilled != nil {
		m.onSessionKilled(userName)
	}

	return nil
}

// SendKeys sends text to a tmux session via `tmux send-keys`.
// The session name should be the user-facing name (prefix and workspace are
// applied internally). The text is sent as literal keys without a trailing
// Enter. wsID must be non-empty.
func (m *TerminalManager) SendKeys(wsID, sessionName, text string) error {
	if wsID == "" {
		return fmt.Errorf("wsID must not be empty")
	}
	if !validSessionName.MatchString(sessionName) {
		return fmt.Errorf("invalid session name %q: must match [a-zA-Z0-9_-]+", sessionName)
	}

	internalName := m.tmuxName(wsID, sessionName)

	if !m.tmuxHasSession(internalName) {
		return fmt.Errorf("tmux session %q not found", sessionName)
	}

	cmd := m.tmuxCmd("send-keys", "-t", internalName, "-l", text)
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

// ScheduleKill schedules a deferred kill for the named session after the given
// delay. If a pending kill already exists for this session, it is replaced.
// The kill is cancelled if the session is re-attached before the delay expires.
// wsID must be non-empty.
//
// The whole "fire the delayed kill" decision runs under m.mu so that a racing
// Attach cannot squeeze in between "no active connections" and "mark killing /
// kill tmux session". Without this, an Attach could create the session right
// after this goroutine's check but before the kill ran, leaving the Attach
// caller with a TerminalSession whose tmux process is about to be destroyed.
func (m *TerminalManager) ScheduleKill(wsID, name string, delay time.Duration) {
	if wsID == "" {
		return
	}
	internalName := m.tmuxName(wsID, name)

	m.mu.Lock()
	// Cancel any existing pending kill for this session.
	if existing, ok := m.pendingKills[internalName]; ok {
		existing.cancel()
	}
	// gosec G118: cancel is stored in the pendingKill struct and invoked by
	// CancelPendingKill, cancelPendingKillByInternal, Attach's precondition
	// block, Shutdown, and by a replacement ScheduleKill (see the "existing"
	// branch above). The linter can't trace the flow through the struct field,
	// so the directive documents the invariant instead of silencing it.
	ctx, cancel := context.WithCancel(context.Background()) //nolint:gosec // G118: see comment above
	myEntry := &pendingKill{cancel: cancel}
	m.pendingKills[internalName] = myEntry
	m.mu.Unlock()

	go func() {
		select {
		case <-time.After(delay):
			// Under m.mu: confirm we're still the scheduled kill, check for
			// active connections, and if this goroutine should fire, tombstone
			// the session before releasing the lock. Holding the lock across
			// the decision prevents a racing Attach from creating a session
			// between our check and our kill.
			m.mu.Lock()
			current, stillPending := m.pendingKills[internalName]
			isCurrent := stillPending && current == myEntry
			if isCurrent {
				delete(m.pendingKills, internalName)
			}
			hasActive := false
			if isCurrent {
				for _, sess := range m.sessions {
					if sess.Name == internalName {
						hasActive = true
						break
					}
				}
			}
			shouldKill := isCurrent && !hasActive
			if shouldKill {
				// Mark the tombstone while the lock is held so any racing
				// Attach that arrives during killByInternal's subprocess
				// calls (which happen outside the lock) sees the tombstone.
				m.killingSet[internalName] = time.Now()
			}
			m.mu.Unlock()
			if shouldKill {
				_ = m.killByInternal(name, internalName)
			}
		case <-ctx.Done():
			// Cancelled — session was re-attached or server is shutting down.
		}
	}()
}

// CancelPendingKill cancels a pending deferred kill for the named session in
// the given workspace. Returns true if a pending kill was found and cancelled.
func (m *TerminalManager) CancelPendingKill(wsID, name string) bool {
	if wsID == "" {
		return false
	}
	return m.cancelPendingKillByInternal(m.tmuxName(wsID, name))
}

// cancelPendingKillByInternal cancels a pending deferred kill keyed by the
// already-qualified internal tmux session name. Used by Attach where the
// internal name is already in hand, and by the public CancelPendingKill.
func (m *TerminalManager) cancelPendingKillByInternal(internalName string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if entry, ok := m.pendingKills[internalName]; ok {
		entry.cancel()
		delete(m.pendingKills, internalName)
		return true
	}
	return false
}
