package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// AutoModeOptions holds configuration for auto mode
type AutoModeOptions struct {
	Interval    int  // Polling interval in seconds when no tasks available
	MaxTasks    int  // Maximum tasks to process before exiting (0 = unlimited)
	IdleTimeout int  // Exit after N minutes with no available tasks (0 = no timeout)
	AgentType   string // "plan" or "task"
	AgentName   string
	WorktreePath string
}

// AutoModeState tracks the current state of auto mode execution
type AutoModeState struct {
	TasksCompleted    int
	ConsecutiveErrors int
	LastTaskTime      time.Time
	IdleStartTime     time.Time
	ShouldExit        bool
	ExitReason        string
}

// SetupSignalHandler sets up graceful shutdown on SIGINT/SIGTERM
// Returns a channel that will be CLOSED when shutdown is requested
// (closed channel pattern allows multiple goroutines to detect shutdown)
func SetupSignalHandler() chan struct{} {
	shutdown := make(chan struct{})
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		fmt.Println("\n[auto] Shutdown signal received, stopping gracefully...")
		close(shutdown) // Closing unblocks ALL receivers
	}()

	return shutdown
}

// interruptibleSleep waits for the specified duration or until shutdown is signaled
// Returns true if interrupted by shutdown, false if duration elapsed
func interruptibleSleep(d time.Duration, shutdown <-chan struct{}) bool {
	select {
	case <-time.After(d):
		return false
	case <-shutdown:
		return true
	}
}

// HasAvailablePlanningTasks checks if there are tasks that need planning
// (ready tasks without a design, excluding [Need Review] and epics)
func HasAvailablePlanningTasks() (bool, error) {
	result := execCommand(".", "bd", "ready", "--json")
	if result.Err != nil {
		return false, fmt.Errorf("failed to check ready tasks: %w", result.Err)
	}

	var issues []BdIssue
	if err := json.Unmarshal([]byte(result.Stdout), &issues); err != nil {
		return false, fmt.Errorf("failed to parse task list: %w", err)
	}

	for _, issue := range issues {
		// Skip [Need Review] tasks - they're waiting for human review
		if strings.Contains(issue.Title, "[Need Review]") {
			continue
		}
		// Skip in_progress tasks - another agent is working on them
		if issue.Status == "in_progress" {
			continue
		}
		// Skip epics - agents shouldn't work on epics directly
		if issue.IssueType == "epic" {
			continue
		}
		// Task needs planning if it has no design
		if issue.Design == "" {
			return true, nil
		}
	}

	return false, nil
}

// HasAvailableImplementationTasks checks if there are tasks ready for implementation
// (ready tasks WITH an approved design, excluding [Need Review] and epics)
func HasAvailableImplementationTasks() (bool, error) {
	result := execCommand(".", "bd", "ready", "--json")
	if result.Err != nil {
		return false, fmt.Errorf("failed to check ready tasks: %w", result.Err)
	}

	var issues []BdIssue
	if err := json.Unmarshal([]byte(result.Stdout), &issues); err != nil {
		return false, fmt.Errorf("failed to parse task list: %w", err)
	}

	for _, issue := range issues {
		// Skip [Need Review] tasks - they're waiting for human review
		if strings.Contains(issue.Title, "[Need Review]") {
			continue
		}
		// Skip in_progress tasks - another agent is working on them
		if issue.Status == "in_progress" {
			continue
		}
		// Skip epics - agents shouldn't work on epics directly
		if issue.IssueType == "epic" {
			continue
		}
		// Task is ready for implementation if it HAS a design
		if issue.Design != "" {
			return true, nil
		}
	}

	return false, nil
}

// RunAutoModeLoop runs the auto mode loop for either plan or task agents
func RunAutoModeLoop(opts AutoModeOptions, shutdown chan struct{}) {
	state := &AutoModeState{
		LastTaskTime:  time.Now(),
		IdleStartTime: time.Now(),
	}

	// Choose the appropriate task checker based on agent type
	var hasAvailableTasks func() (bool, error)
	var generatePrompt func(string) string
	if opts.AgentType == "plan" {
		hasAvailableTasks = HasAvailablePlanningTasks
		generatePrompt = GeneratePlanningPrompt
	} else {
		hasAvailableTasks = HasAvailableImplementationTasks
		generatePrompt = GenerateTaskPrompt
	}

	fmt.Println("=========================================")
	fmt.Printf("Running %s agent in AUTO MODE\n", strings.ToUpper(opts.AgentType))
	fmt.Printf("Worktree: %s\n", opts.WorktreePath)
	fmt.Printf("Agent name: %s\n", opts.AgentName)
	fmt.Printf("Interval: %ds | Max tasks: %s | Idle timeout: %s\n",
		opts.Interval,
		formatLimit(opts.MaxTasks),
		formatTimeout(opts.IdleTimeout))
	fmt.Println("Press Ctrl+C to stop gracefully")
	fmt.Println("=========================================")
	fmt.Println("")

	for {
		// Set idle state at loop start
		if err := UpdateLockState(opts.WorktreePath, StateIdle); err != nil {
			fmt.Printf("[auto] Warning: failed to update state: %v\n", err)
		}

		// Check for shutdown signal (non-blocking)
		select {
		case <-shutdown:
			state.ShouldExit = true
			state.ExitReason = "shutdown signal received"
		default:
		}

		if state.ShouldExit {
			break
		}

		// Check max tasks limit
		if opts.MaxTasks > 0 && state.TasksCompleted >= opts.MaxTasks {
			state.ShouldExit = true
			state.ExitReason = fmt.Sprintf("reached max tasks limit (%d)", opts.MaxTasks)
			break
		}

		// Check for available tasks
		available, err := hasAvailableTasks()
		if err != nil {
			fmt.Printf("[auto] Error checking tasks: %v\n", err)
			// Continue and retry after interval
			if interruptibleSleep(time.Duration(opts.Interval)*time.Second, shutdown) {
				state.ShouldExit = true
				state.ExitReason = "shutdown signal received"
				break
			}
			continue
		}

		if !available {
			// No tasks available - check idle timeout
			if opts.IdleTimeout > 0 {
				idleDuration := time.Since(state.IdleStartTime)
				if idleDuration >= time.Duration(opts.IdleTimeout)*time.Minute {
					state.ShouldExit = true
					state.ExitReason = fmt.Sprintf("idle timeout exceeded (%d minutes)", opts.IdleTimeout)
					break
				}
			}

			fmt.Printf("[auto] No tasks available, waiting %ds...\n", opts.Interval)
			if interruptibleSleep(time.Duration(opts.Interval)*time.Second, shutdown) {
				state.ShouldExit = true
				state.ExitReason = "shutdown signal received"
				break
			}
			continue
		}

		// Reset idle timer when we find tasks
		state.IdleStartTime = time.Now()

		// Set active state before invoking Claude
		if err := UpdateLockState(opts.WorktreePath, StateActive); err != nil {
			fmt.Printf("[auto] Warning: failed to update state: %v\n", err)
		}

		// Invoke Claude to work on one task
		fmt.Println("")
		fmt.Printf("[auto] === Starting task %d ===\n", state.TasksCompleted+1)
		fmt.Println("")

		prompt := generatePrompt(opts.AgentName)
		err = InvokeClaudeNonInteractive(opts.WorktreePath, prompt, shutdown)

		// Return to idle state after Claude finishes
		if updateErr := UpdateLockState(opts.WorktreePath, StateIdle); updateErr != nil {
			fmt.Printf("[auto] Warning: failed to update state: %v\n", updateErr)
		}

		if err != nil {
			fmt.Printf("[auto] Claude exited with error: %v\n", err)
			state.ConsecutiveErrors++

			// Stop after 3 consecutive errors
			if state.ConsecutiveErrors >= 3 {
				state.ShouldExit = true
				state.ExitReason = "too many consecutive errors"
				break
			}

			// Backoff before retry
			fmt.Printf("[auto] Waiting %ds before retry...\n", opts.Interval)
			if interruptibleSleep(time.Duration(opts.Interval)*time.Second, shutdown) {
				state.ShouldExit = true
				state.ExitReason = "shutdown signal received"
				break
			}
			continue // Don't increment TasksCompleted
		}

		// Success - reset error counter and count the task
		state.ConsecutiveErrors = 0
		state.TasksCompleted++
		state.LastTaskTime = time.Now()

		fmt.Println("")
		fmt.Printf("[auto] Task completed. Total: %d\n", state.TasksCompleted)
		fmt.Println("")

		// Brief pause before checking for next task
		if interruptibleSleep(2*time.Second, shutdown) {
			state.ShouldExit = true
			state.ExitReason = "shutdown signal received"
			break
		}
	}

	// Print summary
	fmt.Println("")
	fmt.Println("=========================================")
	fmt.Println("AUTO MODE SUMMARY")
	fmt.Println("=========================================")
	fmt.Printf("Exit reason: %s\n", state.ExitReason)
	fmt.Printf("Tasks completed: %d\n", state.TasksCompleted)
	if state.ConsecutiveErrors > 0 {
		fmt.Printf("Errors at exit: %d consecutive\n", state.ConsecutiveErrors)
	}
	fmt.Println("=========================================")
}

// formatLimit formats the max tasks limit for display
func formatLimit(limit int) string {
	if limit <= 0 {
		return "unlimited"
	}
	return fmt.Sprintf("%d", limit)
}

// formatTimeout formats the idle timeout for display
func formatTimeout(timeout int) string {
	if timeout <= 0 {
		return "none"
	}
	return fmt.Sprintf("%dm", timeout)
}

// IsTmuxAvailable checks if tmux is installed and available
func IsTmuxAvailable() bool {
	return exec.Command("tmux", "-V").Run() == nil
}

// RunAutoModeTmux runs auto mode with tmux session management and live streaming
func RunAutoModeTmux(opts AutoModeOptions, shutdown chan struct{}) {
	// Include PID to prevent session name collisions
	sessionName := fmt.Sprintf("loom-%s-%s-%d", opts.AgentType, opts.AgentName, os.Getpid())

	// Setup log file
	logDir := filepath.Join(opts.WorktreePath, ".loom", "logs")
	os.MkdirAll(logDir, 0755)
	logFile := filepath.Join(logDir, fmt.Sprintf("%s-%s.log", opts.AgentType, opts.AgentName))

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

	// Start non-blocking input listener
	attachChan := make(chan struct{}, 1)
	go listenForAttachKey(attachChan, shutdown)

	taskCount := 0
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

		taskCount++
		fmt.Printf("═══════════════════════════════════════════════════════════════\n")
		fmt.Printf("[Session #%d] Starting...\n", taskCount)
		fmt.Printf("═══════════════════════════════════════════════════════════════\n")

		if err := startTmuxSession(sessionName, opts, logFile); err != nil {
			fmt.Printf("[auto] Failed to start session: %v\n", err)
			taskCount--
			time.Sleep(5 * time.Second)
			continue
		}

		// Stream output and wait for session to exit
		streamUntilExit(sessionName, attachChan, shutdown)

		select {
		case <-shutdown:
			cleanupTmuxSession(sessionName)
			printTmuxSummary(taskCount)
			return
		default:
		}

		fmt.Printf("[Session #%d] Completed, cycling...\n", taskCount)
		time.Sleep(2 * time.Second)
	}
}

// startTmuxSession creates a detached tmux session running loom --daemon-mode
func startTmuxSession(sessionName string, opts AutoModeOptions, logFile string) error {
	// Kill any existing session with this name
	exec.Command("tmux", "kill-session", "-t", sessionName).Run()

	// Build the loom command to run inside tmux
	loomCmd := fmt.Sprintf("loom %s %s --daemon-mode", opts.AgentType, opts.WorktreePath)

	// Create detached session
	if err := exec.Command("tmux", "new-session", "-d", "-s", sessionName, loomCmd).Run(); err != nil {
		return fmt.Errorf("tmux new-session failed: %w", err)
	}

	// Setup logging (shell-quoted path for safety)
	quotedPath := shellQuote(logFile)
	exec.Command("tmux", "pipe-pane", "-t", sessionName, "-o", "cat >> "+quotedPath).Run()

	return nil
}

// streamUntilExit streams tmux output until the session exits naturally
func streamUntilExit(sessionName string, attachChan, shutdown chan struct{}) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	lastLineCount := 0

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

		case <-ticker.C:
			// PRIMARY completion check: session exited
			if !tmuxSessionExists(sessionName) {
				return
			}

			// SECONDARY completion check: command inside session exited (zombie state)
			// This happens when tmux keeps the session alive after the command exits
			if tmuxPaneDead(sessionName) {
				cleanupTmuxSession(sessionName)
				return
			}

			// Stream new output (use -S - to capture full scrollback history, not just visible pane)
			out, err := exec.Command("tmux", "capture-pane", "-t", sessionName, "-p", "-S", "-").Output()
			if err != nil {
				continue
			}

			lines := strings.Split(string(out), "\n")
			for i := lastLineCount; i < len(lines); i++ {
				line := strings.TrimRight(lines[i], " \t")
				if line != "" {
					fmt.Println(line)
				}
			}
			lastLineCount = len(lines)
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

// tmuxPaneDead checks if the pane's command has exited (but session may still exist)
// This happens when tmux keeps the session alive after the command exits (remain-on-exit)
func tmuxPaneDead(sessionName string) bool {
	out, err := exec.Command("tmux", "list-panes", "-t", sessionName, "-F", "#{pane_dead}").Output()
	if err != nil {
		return true // Assume dead if we can't check
	}
	return strings.TrimSpace(string(out)) == "1"
}

// cleanupTmuxSession kills a tmux session if it exists
func cleanupTmuxSession(name string) {
	exec.Command("tmux", "kill-session", "-t", name).Run()
}

// shellQuote safely quotes a string for use in shell commands
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// printTmuxSummary prints the auto mode completion summary
func printTmuxSummary(taskCount int) {
	fmt.Println("")
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Printf("AUTO MODE COMPLETE - %d task(s) processed\n", taskCount)
	fmt.Println("═══════════════════════════════════════════════════════════════")
}
