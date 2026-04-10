package terminal

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"
)

// TerminalSession represents an active tmux attach process with its PTY.
type TerminalSession struct {
	ConnID  string   // unique connection ID (e.g., "talk-to-lead:1")
	Name    string   // tmux session name (e.g., "talk-to-lead")
	Command string   // command running in the session
	PTY     *os.File // PTY master fd from creack/pty
	cmd     *exec.Cmd
	killCh  chan struct{} // closed by KillSessionByName to signal WS handlers
	mu      sync.Mutex
	closed  bool
}

// KillCh returns a channel closed when the session is killed by KillSessionByName.
func (s *TerminalSession) KillCh() <-chan struct{} { return s.killCh }

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
		_ = s.cmd.Wait()
	}
	return firstErr
}

// HasSession reports whether the named tmux session exists using a raw
// (non-prefixed) session name. It is intended for callers that already hold
// the tmux-internal name (e.g., realtime.SessionMonitor adapters).
func (m *TerminalManager) HasSession(name string) bool { return m.tmuxHasSession(name) }

// tmuxCmd creates an exec.Cmd for a tmux subcommand with the cached environment.
func (m *TerminalManager) tmuxCmd(args ...string) *exec.Cmd {
	cmd := exec.Command(m.tmuxPath, args...) //nolint:gosec // tmuxPath from LookPath, args are controlled
	cmd.Env = m.tmuxEnv
	return cmd
}

// tmuxHasSession checks whether a tmux session with the given name exists.
func (m *TerminalManager) tmuxHasSession(name string) bool {
	return m.tmuxCmd("has-session", "-t", name).Run() == nil
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
	if err := m.tmuxCmd(args...).Run(); err != nil {
		return err
	}

	// Enable mouse mode and set scrollback history limit.
	for _, opt := range [][2]string{
		{"mouse", "on"},
		{"history-limit", fmt.Sprintf("%d", m.scrollbackMaxLines)},
	} {
		if err := m.tmuxCmd("set-option", "-t", name, opt[0], opt[1]).Run(); err != nil {
			slog.Warn("failed to set tmux option", "option", opt[0], "session", name, "err", err)
		}
	}
	return nil
}

// tmuxAttach spawns a tmux attach-session process with a PTY.
func (m *TerminalManager) tmuxAttach(name string) (*exec.Cmd, *os.File, error) {
	cmd := m.tmuxCmd("attach-session", "-t", name)
	// Always ensure TERM is a capable value. tmux 3.6+ exits immediately
	// when TERM is unset, "dumb", or unrecognized.
	cmd.Env = append(cmd.Env, "TERM=xterm-256color")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, nil, fmt.Errorf("pty.Start tmux attach: %w", err)
	}
	return cmd, ptmx, nil
}
