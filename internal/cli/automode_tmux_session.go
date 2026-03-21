package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// startTmuxSession creates a detached tmux session running loom --daemon-mode
func startTmuxSession(sessionName string, opts AutoModeOptions, logFile string) error {
	// Kill any existing session with this name (error expected if session doesn't exist)
	_ = exec.Command("tmux", "kill-session", "-t", sessionName).Run()

	// Build the loom command to run inside tmux
	// TERM=dumb disables alternate screen buffer for Claude, enabling output streaming via capture-pane.
	// Other backends (codex, opencode) need a real TERM value to detect TTY properly.
	termPrefix := "TERM=dumb "
	if resolved := GetBackendName(); resolved != "claude" {
		termPrefix = ""
	}
	loomCmd := fmt.Sprintf("%sloom %s %s --daemon-mode", termPrefix, shellQuote(opts.AgentType), shellQuote(opts.WorktreePath))

	// Always propagate backend to subprocess so the tmux-spawned process
	// (which runs the installed binary) uses the same backend as the parent.
	loomCmd += fmt.Sprintf(" --backend %s", shellQuote(GetBackendName()))

	// Propagate parent ID filter to subprocess
	if opts.ParentID != "" {
		loomCmd += fmt.Sprintf(" --parent %s", shellQuote(opts.ParentID))
	}

	// Create detached session with current terminal dimensions
	args := []string{"new-session", "-d", "-s", sessionName}

	// Get terminal size and pass to tmux so output uses full width
	if width, height, err := getTerminalSize(); err == nil && width > 0 && height > 0 {
		args = append(args, "-x", fmt.Sprintf("%d", width), "-y", fmt.Sprintf("%d", height))
	}

	args = append(args, loomCmd)
	if err := exec.Command("tmux", args...).Run(); err != nil {
		return fmt.Errorf("tmux new-session failed: %w", err)
	}

	// Disable tmux focus-events to prevent ^[[I and ^[[O in output
	_ = exec.Command("tmux", "set", "-t", sessionName, "focus-events", "off").Run()

	// Setup logging via loom log-router for intelligent log routing
	// log-router writes to agent log always, and task log when a task is claimed
	// logFile is ~/.loom/logs/agents/{agentName}.log, so logDir is two levels up
	logDir := filepath.Dir(filepath.Dir(logFile))
	lockPath := filepath.Join(ResolveLockDir(opts.WorktreePath), LockFileName)
	routerCmd := fmt.Sprintf("loom log-router --agent %s --base-dir %s --lock-path %s --max-log-size 50",
		shellQuote(opts.AgentName),
		shellQuote(logDir),
		shellQuote(lockPath))
	if err := exec.Command("tmux", "pipe-pane", "-t", sessionName, "-o", routerCmd).Run(); err != nil { //nolint:gosec // args are shell-quoted
		fmt.Printf("[auto] Warning: logging setup failed: %v\n", err)
	}

	return nil
}

// streamUntilExit streams tmux output until the session exits naturally
// Reads from the pipe-pane log file instead of capture-pane to avoid
// visual artifacts from cursor positioning and UI redraws
func streamUntilExit(sessionName, logFile, worktreePath string, attachChan, shutdown chan struct{}) {
	// Canonicalize worktree path to handle symlinks and relative paths
	// This ensures the signal file path matches where Claude creates it
	if absPath, err := filepath.Abs(worktreePath); err == nil {
		if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
			worktreePath = resolved
		} else {
			worktreePath = absPath
		}
	}

	// Start from current file size to avoid replaying old content from previous sessions
	var lastOffset int64 = 0
	if info, err := os.Stat(logFile); err == nil {
		lastOffset = info.Size()
	}
	signalFile := GetSignalFilePath(worktreePath)

	// Use adaptive polling unless LOOM_FIXED_POLLING is set
	var poller *adaptivePoller
	var ticker *time.Ticker
	if useFixedPolling {
		ticker = time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
	} else {
		poller = newAdaptivePoller()
	}

	// getTickChan returns the appropriate tick channel based on polling mode
	getTickChan := func() <-chan time.Time {
		if useFixedPolling {
			return ticker.C
		}
		return poller.tick()
	}

	for {
		select {
		case <-shutdown:
			return

		case <-attachChan:
			fmt.Println("")
			fmt.Println("─── ATTACHED (Ctrl+B D to detach) ───")
			fmt.Println("")

			cmd := exec.Command("tmux", "attach", "-t", sessionName)
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				// Check if session is done (either gone or command exited)
				if !tmuxSessionExists(sessionName) || tmuxPaneDead(sessionName) {
					cleanupTmuxSession(sessionName)
					return
				}
				fmt.Printf("[auto] Attach error: %v\n", err)
			}

			fmt.Println("")
			fmt.Println("─── DETACHED, resuming stream ───")
			fmt.Println("")

			// Reset to fast polling after detach
			if poller != nil {
				poller.hadActivity()
			}

		case <-getTickChan():
			// HIGHEST PRIORITY: Check for explicit completion signal
			if _, err := os.Stat(signalFile); err == nil {
				// Validate signal directory ownership before trusting the signal
				if valErr := validateSignalDir(filepath.Dir(signalFile)); valErr != nil {
					fmt.Fprintf(os.Stderr, "[auto] Warning: signal directory validation failed: %v\n", valErr)
					continue // Don't remove — directory is untrusted
				}
				fmt.Println("[auto] Task completion signal received, waiting for output to settle...")
				os.Remove(signalFile) // Claim the signal

				// Wait for output silence (no new content for 10s) or max 30s
				const silenceTimeout = 10 * time.Second
				const maxWait = 30 * time.Second
				lastActivity := time.Now()
				deadline := time.Now().Add(maxWait)

				for time.Now().Before(deadline) {
					select {
					case <-shutdown:
						streamRemainingLogContent(logFile, &lastOffset)
						cleanupTmuxSession(sessionName)
						return
					default:
					}

					prevOffset := lastOffset
					streamRemainingLogContent(logFile, &lastOffset)
					if lastOffset > prevOffset {
						lastActivity = time.Now() // New output, reset silence timer
					} else if time.Since(lastActivity) >= silenceTimeout {
						break // No output for 3s, we're done
					}
					time.Sleep(200 * time.Millisecond)
				}

				cleanupTmuxSession(sessionName)
				return
			}

			// PRIMARY completion check: session exited
			if !tmuxSessionExists(sessionName) {
				// Read any remaining output from log file before returning
				streamRemainingLogContent(logFile, &lastOffset)
				return
			}

			// SECONDARY completion check: command inside session exited (zombie state)
			// This happens when tmux keeps the session alive after the command exits
			if state, err := getPaneState(sessionName); err == nil && state.Dead {
				if state.ExitStatus != 0 {
					fmt.Printf("[auto] Session exited with status %d", state.ExitStatus)
					if state.ExitSignal != "" {
						fmt.Printf(" (signal: %s)", state.ExitSignal)
					}
					fmt.Println()
				}
				// Read any remaining output before cleanup
				streamRemainingLogContent(logFile, &lastOffset)
				cleanupTmuxSession(sessionName)
				return
			}

			// Stream new output from log file (not capture-pane)
			// pipe-pane captures raw byte stream without cursor positioning artifacts
			file, err := os.Open(logFile)
			if err != nil {
				if poller != nil {
					poller.hadNoActivity()
				}
				continue
			}

			stat, err := file.Stat()
			if err != nil {
				file.Close()
				if poller != nil {
					poller.hadNoActivity()
				}
				continue
			}

			if stat.Size() > lastOffset {
				// New content available
				if _, err := file.Seek(lastOffset, 0); err == nil {
					newContent := make([]byte, stat.Size()-lastOffset)
					n, _ := file.Read(newContent)
					if n > 0 {
						filtered := filterFocusEscapes(newContent[:n])
						os.Stdout.Write(filtered)
						lastOffset += int64(n)
						if poller != nil {
							poller.hadActivity()
						}
					}
				}
			} else if stat.Size() < lastOffset {
				// Log file was truncated/rotated - reset and read from beginning
				lastOffset = 0
				if _, err := file.Seek(0, 0); err == nil {
					newContent := make([]byte, stat.Size())
					n, _ := file.Read(newContent)
					if n > 0 {
						filtered := filterFocusEscapes(newContent[:n])
						os.Stdout.Write(filtered)
						lastOffset = int64(n)
						if poller != nil {
							poller.hadActivity()
						}
					}
				}
			} else if poller != nil {
				// No new output - back off
				poller.hadNoActivity()
			}
			file.Close()
		}
	}
}

// streamRemainingLogContent reads and outputs any remaining content from the log file
func streamRemainingLogContent(logFile string, lastOffset *int64) {
	file, err := os.Open(logFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[auto] Warning: failed to read final output: %v\n", err)
		return
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[auto] Warning: failed to stat log file: %v\n", err)
		return
	}

	if stat.Size() > *lastOffset {
		if _, err := file.Seek(*lastOffset, 0); err != nil {
			fmt.Fprintf(os.Stderr, "[auto] Warning: failed to seek log file: %v\n", err)
			return
		}
		newContent := make([]byte, stat.Size()-*lastOffset)
		n, err := file.Read(newContent)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[auto] Warning: failed to read final output: %v\n", err)
		}
		if n > 0 {
			filtered := filterFocusEscapes(newContent[:n])
			os.Stdout.Write(filtered)
			*lastOffset += int64(n)
		}
	} else if stat.Size() < *lastOffset {
		// Log was truncated - read from beginning
		*lastOffset = 0
		if _, err := file.Seek(0, 0); err != nil {
			fmt.Fprintf(os.Stderr, "[auto] Warning: failed to seek log file: %v\n", err)
			return
		}
		newContent := make([]byte, stat.Size())
		n, err := file.Read(newContent)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[auto] Warning: failed to read final output: %v\n", err)
		}
		if n > 0 {
			filtered := filterFocusEscapes(newContent[:n])
			os.Stdout.Write(filtered)
			*lastOffset = int64(n)
		}
	}
}
