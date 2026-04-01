package webui

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"

	"github.com/creack/pty"
)

// tmuxHasSession checks whether a tmux session with the given name exists.
func (m *TerminalManager) tmuxHasSession(name string) bool {
	cmd := exec.Command(m.tmuxPath, "has-session", "-t", name) //nolint:gosec // tmuxPath from LookPath, name validated by caller
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
	cmd := exec.Command(m.tmuxPath, args...) //nolint:gosec // tmuxPath from LookPath, args are controlled
	if err := cmd.Run(); err != nil {
		return err
	}

	// Enable mouse mode and set scrollback history limit.
	for _, opt := range [][2]string{
		{"mouse", "on"},
		{"history-limit", fmt.Sprintf("%d", m.scrollbackMaxLines)},
	} {
		c := exec.Command(m.tmuxPath, "set-option", "-t", name, opt[0], opt[1]) //nolint:gosec // tmuxPath from LookPath, opt values are string literals
		if err := c.Run(); err != nil {
			slog.Warn("failed to set tmux option", "option", opt[0], "session", name, "err", err)
		}
	}
	return nil
}

// tmuxAttach spawns a tmux attach-session process with a PTY.
func (m *TerminalManager) tmuxAttach(name string) (*exec.Cmd, *os.File, error) {
	cmd := exec.Command(m.tmuxPath, "attach-session", "-t", name) //nolint:gosec // tmuxPath from LookPath, name validated by caller
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
