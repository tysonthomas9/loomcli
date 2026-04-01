package webui

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

// SetOnSessionKilled sets a callback invoked after a tmux session is killed.
// Must be called once during initialization, before any sessions are created.
func (m *TerminalManager) SetOnSessionKilled(fn func(sessionName string)) {
	m.onSessionKilled = fn
}

// captureScrollbackToFile captures the scrollback buffer of a tmux session and writes
// it to ~/.loom/session-scrollback/{sessionName}.log. Returns the file path on success.
// Best-effort: failure does not prevent the kill.
func (m *TerminalManager) captureScrollbackToFile(name string) string {
	if !validSessionName.MatchString(name) {
		slog.Warn("rejecting scrollback capture for invalid session name", "session", name)
		return ""
	}
	internalName := m.tmuxName(name)
	if !m.tmuxHasSession(internalName) {
		return ""
	}

	cmd := exec.Command(m.tmuxPath, "capture-pane", "-p", "-t", internalName, "-S", "-10000") //nolint:gosec // tmuxPath from LookPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		slog.Warn("failed to capture scrollback", "session", name, "err", err)
		return ""
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		slog.Warn("failed to get home dir for scrollback capture", "err", err)
		return ""
	}

	dir := homeDir + "/.loom/session-scrollback"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Warn("failed to create scrollback dir", "err", err)
		return ""
	}

	path := dir + "/" + name + ".log"
	if err := os.WriteFile(path, out, 0o600); err != nil {
		slog.Warn("failed to write scrollback file", "path", path, "err", err)
		return ""
	}

	return path
}

// KillAllSessions closes all PTY connections and kills all prefixed tmux sessions.
// Unlike Shutdown, it does not reset the manager itself — it remains usable for new sessions.
func (m *TerminalManager) KillAllSessions() error {
	m.mu.Lock()
	// Cancel all pending deferred kills.
	for name, cancel := range m.pendingKills {
		cancel()
		delete(m.pendingKills, name)
	}
	// Collect and clear all sessions.
	sessions := make(map[string]*TerminalSession, len(m.sessions))
	for k, v := range m.sessions {
		sessions[k] = v
	}
	m.sessions = make(map[string]*TerminalSession)
	m.scrollbackBuffers = make(map[string]*ScrollbackBuffer)
	m.sessionOwners = make(map[string]string)
	m.mu.Unlock()

	// Close all PTYs.
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
		cmd := exec.Command(m.tmuxPath, "kill-session", "-t", session.Name) //nolint:gosec // tmuxPath is set at construction from LookPath
		if err := cmd.Run(); err != nil {
			slog.Warn("error killing tmux session", "session", session.Name, "err", err)
		}
	}

	return nil
}

// CaptureScrollback captures the scrollback buffer of a tmux session using `tmux capture-pane`.
// Returns up to 5000 lines of scrollback text. Returns error if the session doesn't exist.
// The session name should be the user-facing name (prefix is applied internally).
func (m *TerminalManager) CaptureScrollback(name string) (string, error) {
	if !validSessionName.MatchString(name) {
		return "", fmt.Errorf("invalid session name %q: must match [a-zA-Z0-9_-]+", name)
	}

	internalName := m.tmuxName(name)

	if !m.tmuxHasSession(internalName) {
		return "", fmt.Errorf("tmux session %q not found", name)
	}

	cmd := exec.Command(m.tmuxPath, "capture-pane", "-p", "-t", internalName, "-S", "-5000") //nolint:gosec // tmuxPath from LookPath, name validated
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane: %w: %s", err, strings.TrimSpace(string(out)))
	}

	// Trim trailing empty lines
	text := strings.TrimRight(string(out), "\n")

	return text, nil
}

// HasActiveConnections reports whether there are any active PTY connections
// to the named session (using the user-facing name, prefix applied internally).
func (m *TerminalManager) HasActiveConnections(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	internalName := m.tmuxName(name)
	for _, sess := range m.sessions {
		if sess.Name == internalName {
			return true
		}
	}
	return false
}
