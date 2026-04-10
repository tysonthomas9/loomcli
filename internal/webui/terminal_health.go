package webui

import (
	"fmt"
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
	cmd := m.tmuxCmd("capture-pane", "-t", internalName, "-p", "-S", fmt.Sprintf("-%d", lineCount))
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

// paneDead checks pane_dead using the raw internal tmux session name (no prefix applied).
func (m *TerminalManager) paneDead(internalName string) bool {
	out, err := m.tmuxCmd("list-panes", "-t", internalName, "-F", "#{pane_dead}").CombinedOutput()
	if err != nil {
		return true // If we can't check, assume dead
	}
	return strings.TrimSpace(string(out)) == "1"
}

// capturePaneRaw captures pane output using the raw internal tmux session name (no prefix applied).
func (m *TerminalManager) capturePaneRaw(internalName string, lineCount int) string {
	if lineCount <= 0 {
		lineCount = 50
	}
	out, err := m.tmuxCmd("capture-pane", "-t", internalName, "-p", "-S", fmt.Sprintf("-%d", lineCount)).CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(out), "\n")
}
