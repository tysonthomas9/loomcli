package automode

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

// listenForAttachKey listens for Enter key in a separate goroutine
// Uses blocking reads but the goroutine exits when process terminates
func listenForAttachKey(attachChan chan struct{}, shutdown chan struct{}) {
	// Use a channel to signal when a key is read
	keyChan := make(chan byte, 1)

	// Start a goroutine for blocking reads
	go func() {
		buf := make([]byte, 1)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil || n == 0 {
				return
			}
			// Filter escape sequences (e.g., focus events ^[[I ^[[O)
			if buf[0] == '\x1b' {
				// Drain the rest of the escape sequence
				_, _ = os.Stdin.Read(make([]byte, 2))
				continue
			}
			select {
			case keyChan <- buf[0]: //nolint:gosec // buf[0] is safe: n > 0 is checked above
			default:
			}
		}
	}()

	// Main loop: check for keys or shutdown
	for {
		select {
		case <-shutdown:
			return
		case key := <-keyChan:
			if key == '\n' || key == '\r' {
				select {
				case attachChan <- struct{}{}:
				default:
				}
			}
		}
	}
}

// tmuxSessionExists checks if a tmux session with the given name exists
func tmuxSessionExists(name string) bool {
	return exec.Command("tmux", "has-session", "-t", name).Run() == nil //nolint:gosec // constant tmux subcommands
}

// PaneState holds detailed information about a tmux pane
type PaneState struct {
	Dead       bool
	ExitStatus int
	ExitSignal string
	PID        int
}

// getPaneState returns detailed state information about the session's pane
func getPaneState(sessionName string) (*PaneState, error) {
	format := "#{pane_dead}|#{pane_dead_status}|#{pane_dead_signal}|#{pane_pid}"
	out, err := exec.Command("tmux", "list-panes", "-t", sessionName, "-F", format).Output() //nolint:gosec // constant tmux subcommands + validated session name
	if err != nil {
		return nil, fmt.Errorf("failed to query pane state: %w", err)
	}

	parts := strings.Split(strings.TrimSpace(string(out)), "|")
	if len(parts) != 4 {
		return nil, fmt.Errorf("unexpected format: %s", string(out))
	}

	state := &PaneState{
		Dead: parts[0] == "1",
	}

	if state.Dead {
		state.ExitStatus, _ = strconv.Atoi(parts[1])
		state.ExitSignal = parts[2]
	}

	state.PID, _ = strconv.Atoi(parts[3])
	return state, nil
}

// tmuxPaneDead checks if the pane's command has exited (but session may still exist)
// This happens when tmux keeps the session alive after the command exits (remain-on-exit)
func tmuxPaneDead(sessionName string) bool {
	state, err := getPaneState(sessionName)
	if err != nil {
		return true // Assume dead if we can't check
	}
	return state.Dead
}

// cleanupTmuxSession gracefully stops and kills a tmux session
// Sends Ctrl+C first to allow Claude to handle interrupt gracefully,
// then kills the session after a brief grace period.
// Errors are ignored since session may already be dead.
func cleanupTmuxSession(name string) {
	// Skip if session already gone (saves 100ms)
	if !tmuxSessionExists(name) {
		return
	}

	// Send Ctrl+C for graceful shutdown (allows Claude to save state).
	// Grace period is generous (3s) to cover slow tmux event-loop processing
	// under CPU contention — the full path (send-keys → SIGINT delivery →
	// shell trap → pane exit) is bounded by tmux + shell scheduling, not by
	// our polling, and a too-tight deadline races us to kill-session before
	// the foreground process has a chance to handle the signal.
	_ = exec.Command("tmux", "send-keys", "-t", name, "C-c").Run() //nolint:gosec // constant tmux subcommands
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !tmuxSessionExists(name) || tmuxPaneDead(name) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Kill session
	_ = exec.Command("tmux", "kill-session", "-t", name).Run() //nolint:gosec // constant tmux subcommands
}

// shellQuote safely quotes a string for use in shell commands
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// getTerminalSize returns the current terminal width and height
func getTerminalSize() (width, height int, err error) {
	cmd := exec.Command("stty", "size")
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, err
	}
	_, err = fmt.Sscanf(strings.TrimSpace(string(out)), "%d %d", &height, &width)
	return width, height, err
}

// filterFocusEscapes removes terminal focus event escape sequences
// These are sent by some terminals when focus is gained/lost:
// - ESC [ I = focus gained
// - ESC [ O = focus lost
func filterFocusEscapes(data []byte) []byte {
	result := bytes.ReplaceAll(data, []byte("\x1b[I"), nil)
	result = bytes.ReplaceAll(result, []byte("\x1b[O"), nil)
	return result
}

// workspaceHash delegates to cli.WorkspaceHash.
func workspaceHash(path string) string {
	return cli.WorkspaceHash(path)
}
