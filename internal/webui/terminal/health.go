package terminal

import (
	"fmt"
	"os/exec"
	"strings"
)

// SessionAlive checks whether the named tmux session still exists.
// Returns true if the session is running, false if it has exited.
func (m *TerminalManager) SessionAlive(name string) bool {
	return m.tmuxHasSession(m.tmuxName(name))
}

// CapturePane captures the last lineCount lines of terminal output from the named tmux session.
// Uses `tmux capture-pane` to grab output, which is useful for extracting error messages
// when a backend process crashes.
func (m *TerminalManager) CapturePane(name string, lineCount int) (string, error) {
	internalName := m.tmuxName(name)
	if lineCount <= 0 {
		lineCount = 50
	}
	cmd := exec.Command(m.tmuxPath, "capture-pane", "-t", internalName, "-p", "-S", fmt.Sprintf("-%d", lineCount)) //nolint:gosec // tmuxPath from LookPath, name validated
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// PaneDead checks whether the pane's process has exited, even if the tmux session is still up.
// The name is a user-facing session name; the prefix is applied internally.
func (m *TerminalManager) PaneDead(name string) bool {
	return m.paneDead(m.tmuxName(name))
}

// PaneDeadRaw checks pane_dead using the raw internal tmux session name (no prefix applied).
// It is intended for callers that already hold the tmux-internal name.
func (m *TerminalManager) PaneDeadRaw(internalName string) bool { return m.paneDead(internalName) }

// paneDead checks pane_dead using the raw internal tmux session name (no prefix applied).
func (m *TerminalManager) paneDead(internalName string) bool {
	cmd := exec.Command(m.tmuxPath, "list-panes", "-t", internalName, "-F", "#{pane_dead}") //nolint:gosec // tmuxPath from LookPath, name validated
	out, err := cmd.CombinedOutput()
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
	cmd := exec.Command(m.tmuxPath, "capture-pane", "-t", internalName, "-p", "-S", fmt.Sprintf("-%d", lineCount)) //nolint:gosec // tmuxPath from LookPath, name validated
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(out), "\n")
}
