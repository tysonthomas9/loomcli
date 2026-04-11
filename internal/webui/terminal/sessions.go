package terminal

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

// SetOnSessionKilled sets a callback invoked after a tmux session is killed.
// Must be called once during initialization, before any sessions are created.
func (m *TerminalManager) SetOnSessionKilled(fn func(wsID, sessionName, scrollbackPath string)) {
	m.onSessionKilled = fn
}

// captureScrollbackToFileByInternal captures the scrollback buffer of a tmux
// session and writes it to ~/.loom/session-scrollback/<internalName>.log.
// Returns the file path on success. Best-effort: failure does not prevent
// the kill.
//
// The file is named by the fully-qualified internal tmux name (which embeds
// workspace and server-instance prefixes) so two workspaces with the same
// user-facing session name don't clobber each other's scrollback files. The
// userName parameter is kept for log message clarity — operators debugging a
// scrollback file look up the UI session name, which maps to userName.
func (m *TerminalManager) captureScrollbackToFileByInternal(userName, internalName string) string {
	if !validSessionName.MatchString(userName) {
		slog.Warn("rejecting scrollback capture for invalid session name", "session", userName)
		return ""
	}
	if !m.tmuxHasSession(internalName) {
		return ""
	}

	cmd := exec.Command(m.tmuxPath, "capture-pane", "-p", "-t", internalName, "-S", "-10000") //nolint:gosec // tmuxPath from LookPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		slog.Warn("failed to capture scrollback", "session", userName, "err", err)
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

	// Use the internal (qualified) name as the file name component to prevent
	// two workspaces with the same user-facing session name from clobbering
	// each other's scrollback files.
	path := dir + "/" + internalName + ".log"
	if err := os.WriteFile(path, out, 0o600); err != nil {
		slog.Warn("failed to write scrollback file", "path", path, "err", err)
		return ""
	}

	return path
}

// KillWorkspaceSessions closes all PTY connections and kills every tmux
// session that belongs to the given workspace. Sessions are identified by the
// shared "<serverPrefix>-<wsShort>-" prefix on their internal tmux name, so
// this catches both sessions with active PTY connections and detached
// spawned-only sessions (created via Spawn/SpawnArgv without a PTY).
//
// Unlike Shutdown, it does not reset the manager itself — it remains usable
// for new sessions in this or other workspaces. wsID must be non-empty.
func (m *TerminalManager) KillWorkspaceSessions(wsID string) error {
	if wsID == "" {
		return fmt.Errorf("wsID must not be empty")
	}

	prefix := m.workspacePrefix(wsID)

	// Enumerate tmux's view of sessions so we catch detached ones too.
	// Fall back gracefully if tmux reports no sessions.
	tmuxSessions, err := m.listTmuxSessions()
	if err != nil {
		return fmt.Errorf("list tmux sessions: %w", err)
	}

	// Collect the internal names we need to kill.
	toKill := make(map[string]struct{})
	for _, s := range tmuxSessions {
		if strings.HasPrefix(s.name, prefix) {
			toKill[s.name] = struct{}{}
		}
	}

	// Also sweep the in-memory sessions map in case a session is mid-setup
	// and doesn't yet appear in list-sessions output.
	m.mu.RLock()
	for _, sess := range m.sessions {
		if strings.HasPrefix(sess.Name, prefix) {
			toKill[sess.Name] = struct{}{}
		}
	}
	m.mu.RUnlock()

	// Kill each one via the shared helper. killByInternal handles the
	// tombstone marking, PTY close, scrollback capture, tmux kill-session,
	// scrollback buffer cleanup, and onSessionKilled callback.
	for internalName := range toKill {
		userName := strings.TrimPrefix(internalName, prefix)
		if err := m.killByInternal(wsID, userName, internalName); err != nil {
			slog.Warn("error killing session", "session", internalName, "err", err)
		}
	}

	return nil
}

// CaptureScrollback captures the scrollback buffer of a tmux session using
// `tmux capture-pane`. Returns up to 5000 lines of scrollback text. Returns
// error if the session doesn't exist. The session name should be the
// user-facing name (prefix and workspace are applied internally). wsID must
// be non-empty.
func (m *TerminalManager) CaptureScrollback(wsID, name string) (string, error) {
	if wsID == "" {
		return "", fmt.Errorf("wsID must not be empty")
	}
	if !validSessionName.MatchString(name) {
		return "", fmt.Errorf("invalid session name %q: must match [a-zA-Z0-9_-]+", name)
	}

	internalName := m.tmuxName(wsID, name)

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

// ExportSession captures the full tmux scrollback buffer for a session in the
// given workspace. Uses `tmux capture-pane -p -S -` to get the complete
// history. wsID must be non-empty.
func (m *TerminalManager) ExportSession(wsID, name string) (string, error) {
	if wsID == "" {
		return "", fmt.Errorf("wsID must not be empty")
	}
	if !validSessionName.MatchString(name) {
		return "", fmt.Errorf("invalid session name %q", name)
	}

	internalName := m.tmuxName(wsID, name)

	if !m.tmuxHasSession(internalName) {
		return "", fmt.Errorf("tmux session %q not found", name)
	}

	// Capture the full scrollback using "-S -" (start of history).
	out, err := m.runTmuxCapture(internalName)
	if err != nil {
		return "", err
	}

	return strings.TrimRight(string(out), "\n"), nil
}

// runTmuxCapture runs tmux capture-pane for the full history of a session.
func (m *TerminalManager) runTmuxCapture(internalName string) ([]byte, error) {
	cmd := exec.Command(m.tmuxPath, "capture-pane", "-p", "-t", internalName, "-S", "-") //nolint:gosec // tmuxPath from LookPath, name validated
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("tmux capture-pane: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// HasActiveConnections reports whether there are any active PTY connections
// to the named session in the given workspace (using the user-facing name;
// prefix and workspace short ID are applied internally). Returns false if
// wsID is empty.
func (m *TerminalManager) HasActiveConnections(wsID, name string) bool {
	if wsID == "" {
		return false
	}
	return m.hasActiveConnectionsByInternal(m.tmuxName(wsID, name))
}

// hasActiveConnectionsByInternal is the lock-held helper used by
// HasActiveConnections and by ScheduleKill's deferred goroutine, both of
// which already have the qualified internal name.
func (m *TerminalManager) hasActiveConnectionsByInternal(internalName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, sess := range m.sessions {
		if sess.Name == internalName {
			return true
		}
	}
	return false
}
