package webui

import (
	"fmt"
	"log"
	"os"
	"strings"
)

// SetOnSessionKilled sets a callback invoked after a tmux session is killed.
// Must be called once during initialization, before any sessions are created.
func (m *TerminalManager) SetOnSessionKilled(fn func(sessionName string)) {
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
		log.Printf("Warning: rejecting scrollback capture for invalid session name %q", userName)
		return ""
	}
	if !m.tmuxHasSession(internalName) {
		return ""
	}

	cmd := m.tmuxCmd("capture-pane", "-p", "-t", internalName, "-S", "-10000")
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Warning: failed to capture scrollback for session %q: %v", userName, err)
		return ""
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Printf("Warning: failed to get home dir for scrollback capture: %v", err)
		return ""
	}

	dir := homeDir + "/.loom/session-scrollback"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("Warning: failed to create scrollback dir: %v", err)
		return ""
	}

	// Use the internal (qualified) name as the file name component to prevent
	// two workspaces with the same user-facing session name from clobbering
	// each other's scrollback files.
	path := dir + "/" + internalName + ".log"
	if err := os.WriteFile(path, out, 0o600); err != nil {
		log.Printf("Warning: failed to write scrollback file %s: %v", path, err)
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

	// Collect the internal names we need to kill. Using a set because the
	// same internal name can't appear twice in list-sessions, but this
	// guards against any future list-sessions quirks.
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

	// Kill each one via the shared helper. killByInternal also handles the
	// tombstone marking, PTY close, scrollback capture, tmux kill-session,
	// scrollback buffer cleanup, and onSessionKilled callback.
	for internalName := range toKill {
		userName := strings.TrimPrefix(internalName, prefix)
		if err := m.killByInternal(userName, internalName); err != nil {
			log.Printf("Warning: error killing session %q: %v", internalName, err)
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

	cmd := m.tmuxCmd("capture-pane", "-p", "-t", internalName, "-S", "-5000")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane: %w: %s", err, strings.TrimSpace(string(out)))
	}

	// Trim trailing empty lines
	text := strings.TrimRight(string(out), "\n")

	return text, nil
}

// HasActiveConnections reports whether there are any active PTY connections
// to the named session in the given workspace (using the user-facing name;
// prefix and workspace short ID are applied internally).
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
