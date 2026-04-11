package terminal

import (
	"fmt"
	"strings"
)

// SessionAlive checks whether the named tmux session still exists in the
// given workspace. Returns true if the session is running, false if it has
// exited or wsID is empty.
func (m *TerminalManager) SessionAlive(wsID, name string) bool {
	if wsID == "" {
		return false
	}
	return m.tmuxHasSession(m.tmuxName(wsID, name))
}

// CapturePane captures the last lineCount lines of terminal output from the
// named tmux session in the given workspace. Uses `tmux capture-pane` to grab
// output, which is useful for extracting error messages when a backend
// process crashes. wsID must be non-empty.
func (m *TerminalManager) CapturePane(wsID, name string, lineCount int) (string, error) {
	if wsID == "" {
		return "", fmt.Errorf("wsID must not be empty")
	}
	internalName := m.tmuxName(wsID, name)
	if lineCount <= 0 {
		lineCount = 50
	}
	cmd := m.tmuxCmd("capture-pane", "-t", internalName, "-p", "-S", fmt.Sprintf("-%d", lineCount))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// PaneDead checks whether the pane's process has exited, even if the tmux
// session is still up. The name is a user-facing session name; the prefix and
// workspace short ID are applied internally.
func (m *TerminalManager) PaneDead(wsID, name string) bool {
	if wsID == "" {
		return true
	}
	return m.paneDead(m.tmuxName(wsID, name))
}

// PaneDeadRaw checks pane_dead using the raw internal tmux session name (no prefix applied).
// It is intended for callers that already hold the tmux-internal name.
func (m *TerminalManager) PaneDeadRaw(internalName string) bool { return m.paneDead(internalName) }

// paneDead checks pane_dead using the raw internal tmux session name (no prefix applied).
func (m *TerminalManager) paneDead(internalName string) bool {
	out, err := m.tmuxCmd("list-panes", "-t", internalName, "-F", "#{pane_dead}").CombinedOutput()
	if err != nil {
		return true // If we can't check, assume dead
	}
	return strings.TrimSpace(string(out)) == "1"
}

// CapturePaneRaw captures pane output using the raw internal tmux session name (no prefix applied).
func (m *TerminalManager) CapturePaneRaw(internalName string, lineCount int) string {
	if lineCount <= 0 {
		lineCount = 50
	}
	out, err := m.tmuxCmd("capture-pane", "-t", internalName, "-p", "-S", fmt.Sprintf("-%d", lineCount)).CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(out), "\n")
}
