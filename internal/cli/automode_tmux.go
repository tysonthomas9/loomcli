package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// IsTmuxAvailable checks if tmux is installed and available
func IsTmuxAvailable() bool {
	return exec.Command("tmux", "-V").Run() == nil
}

// RunAutoModeTmux runs auto mode with tmux session management and live streaming
func RunAutoModeTmux(opts AutoModeOptions, shutdown chan struct{}) {
	// Canonicalize worktree path to handle symlinks and relative paths
	// This ensures consistent paths between parent and daemon processes
	if absPath, err := filepath.Abs(opts.WorktreePath); err == nil {
		if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
			opts.WorktreePath = resolved
		} else {
			opts.WorktreePath = absPath
		}
	}

	// Include PID to prevent session name collisions
	sessionName := fmt.Sprintf("loom-%s-%s-%d", opts.AgentType, opts.AgentName, os.Getpid())

	// Setup log file — store outside worktree to avoid polluting git status
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("[auto] Warning: could not get home directory: %v\n", err)
		homeDir = os.TempDir()
	}
	logDir := filepath.Join(homeDir, ".loom", "logs")
	agentLogDir := filepath.Join(logDir, "agents")
	if err := os.MkdirAll(agentLogDir, 0700); err != nil {
		fmt.Printf("[auto] Warning: could not create log directory: %v\n", err)
	}
	logFile := filepath.Join(agentLogDir, fmt.Sprintf("%s.log", opts.AgentName))

	// Choose task checker based on agent type.
	// Note: CustomPromptGen is not used here because tmux mode delegates prompt
	// generation to the daemon subprocess via the loom command.
	var hasAvailableTasks func() (bool, error)
	if opts.CustomTaskCheck != nil {
		hasAvailableTasks = opts.CustomTaskCheck
	} else if opts.AgentType == "plan" {
		hasAvailableTasks = func() (bool, error) { return HasAvailablePlanningTasks(opts.ParentID) }
	} else {
		hasAvailableTasks = func() (bool, error) { return HasAvailableImplementationTasks(opts.ParentID) }
	}

	// Print header
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Printf("Running %s agent in AUTO MODE (tmux)\n", strings.ToUpper(opts.AgentType))
	fmt.Printf("Worktree: %s\n", opts.WorktreePath)
	fmt.Printf("Session: %s\n", sessionName)
	fmt.Println("")
	fmt.Println("Press ENTER to attach (Ctrl+B D to detach)")
	fmt.Println("Press Ctrl+C to stop")
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println("")

	// Disable terminal focus reporting to prevent ^[[I and ^[[O in output
	fmt.Print("\x1b[?1004l")
	defer fmt.Print("\x1b[?1004h") // Re-enable on exit

	// Start non-blocking input listener
	attachChan := make(chan struct{}, 1)
	go listenForAttachKey(attachChan, shutdown)

	taskCount := 0
	consecutiveNoProgress := 0
	idleStart := time.Now()
	for {
		select {
		case <-shutdown:
			cleanupTmuxSession(sessionName)
			printTmuxSummary(taskCount)
			return
		default:
		}

		// Check max tasks
		if opts.MaxTasks > 0 && taskCount >= opts.MaxTasks {
			fmt.Printf("[auto] Reached max tasks (%d)\n", opts.MaxTasks)
			cleanupTmuxSession(sessionName)
			printTmuxSummary(taskCount)
			return
		}

		// Check for available tasks before spawning session
		available, err := hasAvailableTasks()
		if err != nil {
			fmt.Printf("[auto] Error checking tasks: %v\n", err)
			time.Sleep(5 * time.Second)
			continue
		}
		if !available {
			// Check idle timeout
			if opts.IdleTimeout > 0 && time.Since(idleStart) >= time.Duration(opts.IdleTimeout)*time.Minute {
				fmt.Printf("[auto] Idle timeout exceeded (%d minutes)\n", opts.IdleTimeout)
				cleanupTmuxSession(sessionName)
				printTmuxSummary(taskCount)
				return
			}
			fmt.Printf("[auto] No tasks available, waiting %ds...\n", opts.Interval)
			if interruptibleSleep(time.Duration(opts.Interval)*time.Second, shutdown) {
				cleanupTmuxSession(sessionName)
				printTmuxSummary(taskCount)
				return
			}
			continue
		}

		// Reset idle timer when tasks are available
		idleStart = time.Now()

		// Remove leftover lock from previous cycle. The daemon intentionally
		// does not delete its lock on exit so agentClaimedTask() can read it.
		// We clean up here before the next daemon acquires a fresh lock.
		lockPath := filepath.Join(opts.WorktreePath, LockFileName)
		_ = os.Remove(lockPath)

		fmt.Printf("═══════════════════════════════════════════════════════════════\n")
		fmt.Printf("[Session] Starting...\n")
		fmt.Printf("═══════════════════════════════════════════════════════════════\n")

		if err := startTmuxSession(sessionName, opts, logFile); err != nil {
			fmt.Printf("[auto] Failed to start session: %v\n", err)
			time.Sleep(5 * time.Second)
			continue
		}

		// Stream output and wait for session to exit
		streamUntilExit(sessionName, logFile, opts.WorktreePath, attachChan, shutdown)

		select {
		case <-shutdown:
			cleanupTmuxSession(sessionName)
			printTmuxSummary(taskCount)
			return
		default:
		}

		// Check if agent actually claimed a task
		if agentClaimedTask(opts.WorktreePath) {
			taskCount++
			consecutiveNoProgress = 0
			fmt.Printf("[Session #%d] Completed, cycling...\n", taskCount)
		} else {
			consecutiveNoProgress++
			fmt.Printf("[auto] Agent exited without claiming a task (%d consecutive)\n", consecutiveNoProgress)
			if consecutiveNoProgress >= 3 {
				fmt.Printf("[auto] No tasks claimed in %d consecutive sessions, exiting\n", consecutiveNoProgress)
				cleanupTmuxSession(sessionName)
				printTmuxSummary(taskCount)
				return
			}
			// Exponential backoff: 30s, 60s, 120s (capped)
			backoff := time.Duration(30<<(consecutiveNoProgress-1)) * time.Second
			if backoff > 120*time.Second {
				backoff = 120 * time.Second
			}
			fmt.Printf("[auto] Backing off for %s before retry...\n", backoff)
			if interruptibleSleep(backoff, shutdown) {
				cleanupTmuxSession(sessionName)
				printTmuxSummary(taskCount)
				return
			}
		}
		time.Sleep(2 * time.Second)
	}
}

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

	// Propagate backend selection to subprocess
	if resolved := GetBackendName(); resolved != "claude" {
		loomCmd += fmt.Sprintf(" --backend %s", shellQuote(resolved))
	}

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
			case keyChan <- buf[0]:
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
	return exec.Command("tmux", "has-session", "-t", name).Run() == nil
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
	out, err := exec.Command("tmux", "list-panes", "-t", sessionName, "-F", format).Output()
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

	// Send Ctrl+C for graceful shutdown (allows Claude to save state)
	_ = exec.Command("tmux", "send-keys", "-t", name, "C-c").Run()
	time.Sleep(100 * time.Millisecond)

	// Kill session
	_ = exec.Command("tmux", "kill-session", "-t", name).Run()
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

// printTmuxSummary prints the auto mode completion summary
func printTmuxSummary(taskCount int) {
	fmt.Println("")
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Printf("AUTO MODE COMPLETE - %d task(s) processed\n", taskCount)
	fmt.Println("═══════════════════════════════════════════════════════════════")
}

// workspaceHash returns a deterministic short hash for a workspace path.
// Used to create per-workspace subdirectories outside the git worktree.
func workspaceHash(path string) string {
	hash := sha256.Sum256([]byte(path))
	return hex.EncodeToString(hash[:8])
}
