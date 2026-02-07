package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
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
	CustomPromptGen func(string, *WorkspaceConfig) string // Custom prompt generator (overrides AgentType selection)
	CustomTaskCheck func() (bool, error)                   // Custom task availability check (overrides AgentType selection)
}

// AutoModeState tracks the current state of auto mode execution
type AutoModeState struct {
	TasksCompleted        int
	ConsecutiveErrors     int
	ConsecutiveNoProgress int // sessions that completed without claiming a task
	LastTaskTime          time.Time
	IdleStartTime         time.Time
	ShouldExit            bool
	ExitReason            string
}

// useFixedPolling allows reverting to fixed 200ms polling via environment variable
var useFixedPolling = os.Getenv("LOOM_FIXED_POLLING") != ""

// adaptivePoller implements exponential backoff for polling intervals
type adaptivePoller struct {
	minInterval     time.Duration
	maxInterval     time.Duration
	currentInterval time.Duration
	backoffFactor   float64
}

// newAdaptivePoller creates a poller with sensible defaults
func newAdaptivePoller() *adaptivePoller {
	return &adaptivePoller{
		minInterval:     100 * time.Millisecond,  // Fast when active
		maxInterval:     1000 * time.Millisecond, // Slow when idle
		currentInterval: 200 * time.Millisecond,  // Start at legacy value
		backoffFactor:   1.5,
	}
}

// tick returns a channel that fires after the current interval
func (p *adaptivePoller) tick() <-chan time.Time {
	return time.After(p.currentInterval)
}

// hadActivity resets to fast polling
func (p *adaptivePoller) hadActivity() {
	p.currentInterval = p.minInterval
}

// hadNoActivity increases the polling interval (exponential backoff)
func (p *adaptivePoller) hadNoActivity() {
	newInterval := time.Duration(float64(p.currentInterval) * p.backoffFactor)
	if newInterval > p.maxInterval {
		newInterval = p.maxInterval
	}
	p.currentInterval = newInterval
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

// hasOpenBlockers returns true if any dependency is a blocking relationship.
// A blocking dependency indicates the task cannot start until the blocker is resolved.
// The presence of a "blocks" dependency means the blocker hasn't been resolved yet
// (resolved blockers have their dependencies removed from the database).
func hasOpenBlockers(deps []Dependency) bool {
	for _, dep := range deps {
		if dep.Type == "blocks" {
			return true
		}
	}
	return false
}

// GetAvailablePlanningTasks returns tasks that need planning
// (ready tasks without a design OR with needs-revision label, excluding epics)
func GetAvailablePlanningTasks() ([]BdIssue, error) {
	result := execCommand(GetBeadsDir(), "bd", "ready", "--json", "--limit", "100")
	if result.Err != nil {
		return nil, fmt.Errorf("failed to check ready tasks: %w", result.Err)
	}

	var issues []BdIssue
	if err := json.Unmarshal([]byte(result.Stdout), &issues); err != nil {
		return nil, fmt.Errorf("failed to parse task list: %w", err)
	}

	var candidates []BdIssue
	for _, issue := range issues {
		// Only consider open tasks - skip in_progress, review, blocked, etc.
		if issue.Status != "open" {
			continue
		}
		// Skip epics - agents shouldn't work on epics directly
		if issue.IssueType == "epic" {
			continue
		}
		// Safety net: skip tasks with open blocking dependencies
		if hasOpenBlockers(issue.Dependencies) {
			continue
		}
		// Task needs planning if:
		// 1. No design (new task), OR
		// 2. Has 'needs-revision' label (revision task)
		hasRevisionLabel := slices.Contains(issue.Labels, "needs-revision")
		if issue.Design == "" || hasRevisionLabel {
			candidates = append(candidates, issue)
		}
	}

	return candidates, nil
}

// HasAvailablePlanningTasks checks if there are tasks that need planning
// (ready tasks without a design OR with needs-revision label, excluding epics)
func HasAvailablePlanningTasks() (bool, error) {
	tasks, err := GetAvailablePlanningTasks()
	if err != nil {
		return false, err
	}
	return len(tasks) > 0, nil
}

// GetAvailableImplementationTasks returns tasks ready for implementation
// (ready tasks WITH an approved design, excluding tasks with needs-revision label and epics)
func GetAvailableImplementationTasks() ([]BdIssue, error) {
	result := execCommand(GetBeadsDir(), "bd", "ready", "--json", "--limit", "100")
	if result.Err != nil {
		return nil, fmt.Errorf("failed to check ready tasks: %w", result.Err)
	}

	var issues []BdIssue
	if err := json.Unmarshal([]byte(result.Stdout), &issues); err != nil {
		return nil, fmt.Errorf("failed to parse task list: %w", err)
	}

	var candidates []BdIssue
	for _, issue := range issues {
		// Only consider open tasks - skip in_progress, review, blocked, etc.
		if issue.Status != "open" {
			continue
		}
		// Skip epics - agents shouldn't work on epics directly
		if issue.IssueType == "epic" {
			continue
		}
		// Safety net: skip tasks with open blocking dependencies
		if hasOpenBlockers(issue.Dependencies) {
			continue
		}
		// Task ready for implementation if it HAS a design AND no revision label
		hasRevisionLabel := slices.Contains(issue.Labels, "needs-revision")
		if issue.Design != "" && !hasRevisionLabel {
			candidates = append(candidates, issue)
		}
	}

	return candidates, nil
}

// HasAvailableImplementationTasks checks if there are tasks ready for implementation
// (ready tasks WITH an approved design, excluding tasks with needs-revision label and epics)
func HasAvailableImplementationTasks() (bool, error) {
	tasks, err := GetAvailableImplementationTasks()
	if err != nil {
		return false, err
	}
	return len(tasks) > 0, nil
}

// GetAnyAvailableTasks returns any ready tasks regardless of design status.
// Used by custom roles with task_filter=any.
func GetAnyAvailableTasks() ([]BdIssue, error) {
	result := execCommand(GetBeadsDir(), "bd", "ready", "--json", "--limit", "100")
	if result.Err != nil {
		return nil, fmt.Errorf("failed to check ready tasks: %w", result.Err)
	}

	var issues []BdIssue
	if err := json.Unmarshal([]byte(result.Stdout), &issues); err != nil {
		return nil, fmt.Errorf("failed to parse task list: %w", err)
	}

	var candidates []BdIssue
	for _, issue := range issues {
		// Only consider open tasks - skip in_progress, review, blocked, etc.
		if issue.Status != "open" {
			continue
		}
		if issue.IssueType == "epic" {
			continue
		}
		// Safety net: skip tasks with open blocking dependencies
		if hasOpenBlockers(issue.Dependencies) {
			continue
		}
		candidates = append(candidates, issue)
	}

	return candidates, nil
}

// HasAnyAvailableTasks checks if there are any ready tasks regardless of design status.
// Used by custom roles with task_filter=any.
func HasAnyAvailableTasks() (bool, error) {
	tasks, err := GetAnyAvailableTasks()
	if err != nil {
		return false, err
	}
	return len(tasks) > 0, nil
}

// agentClaimedTask checks the lock file to determine if the agent claimed a task
// during its session. Returns true if TaskID is non-empty.
func agentClaimedTask(worktreePath string) bool {
	info, err := ReadLockFile(worktreePath)
	if err != nil {
		// Can't read lock file — daemon never ran or failed before writing lock.
		// No task was claimed (no progress).
		return false
	}
	return info.TaskID != ""
}

// RunAutoModeLoop runs the auto mode loop for either plan or task agents
func RunAutoModeLoop(opts AutoModeOptions, shutdown chan struct{}) {
	state := &AutoModeState{
		LastTaskTime:  time.Now(),
		IdleStartTime: time.Now(),
	}

	// Choose the appropriate task checker based on agent type
	var hasAvailableTasks func() (bool, error)
	var generatePrompt func(string, *WorkspaceConfig) string
	if opts.CustomPromptGen != nil && opts.CustomTaskCheck != nil {
		hasAvailableTasks = opts.CustomTaskCheck
		generatePrompt = opts.CustomPromptGen
	} else if opts.AgentType == "plan" {
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

		// Clear TaskID before new session so we can detect if the agent claims one
		if clearErr := ClearLockTaskID(opts.WorktreePath); clearErr != nil {
			fmt.Printf("[auto] Warning: failed to clear task ID: %v\n", clearErr)
		}

		// Invoke agent to work on one task
		fmt.Println("")
		fmt.Printf("[auto] === Starting task %d ===\n", state.TasksCompleted+1)
		fmt.Println("")

		workspace, _ := ResolveActiveWorkspace()
		prompt := generatePrompt(opts.AgentName, workspace)
		err = InvokeAgentNonInteractive(opts.WorktreePath, prompt, opts.AgentName, shutdown)

		// Return to idle state after agent finishes
		if updateErr := UpdateLockState(opts.WorktreePath, StateIdle); updateErr != nil {
			fmt.Printf("[auto] Warning: failed to update state: %v\n", updateErr)
		}

		if err != nil {
			fmt.Printf("[auto] Agent exited with error: %v\n", err)
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

		// Success - reset error counter, check if real work happened
		state.ConsecutiveErrors = 0

		if agentClaimedTask(opts.WorktreePath) {
			state.TasksCompleted++
			state.ConsecutiveNoProgress = 0
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
		} else {
			state.ConsecutiveNoProgress++
			fmt.Println("")
			fmt.Printf("[auto] Agent exited without claiming a task (%d consecutive)\n", state.ConsecutiveNoProgress)

			if state.ConsecutiveNoProgress >= 3 {
				state.ShouldExit = true
				state.ExitReason = fmt.Sprintf("no tasks claimed in %d consecutive sessions", state.ConsecutiveNoProgress)
				break
			}

			// Exponential backoff: 30s, 60s, 120s (capped)
			backoff := time.Duration(30<<(state.ConsecutiveNoProgress-1)) * time.Second
			if backoff > 120*time.Second {
				backoff = 120 * time.Second
			}
			fmt.Printf("[auto] Backing off for %s before retry...\n", backoff)
			fmt.Println("")
			if interruptibleSleep(backoff, shutdown) {
				state.ShouldExit = true
				state.ExitReason = "shutdown signal received"
				break
			}
			continue
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
	if state.ConsecutiveNoProgress > 0 {
		fmt.Printf("No-progress sessions at exit: %d consecutive\n", state.ConsecutiveNoProgress)
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
	if err := os.MkdirAll(agentLogDir, 0755); err != nil {
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
		hasAvailableTasks = HasAvailablePlanningTasks
	} else {
		hasAvailableTasks = HasAvailableImplementationTasks
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
	// TERM=dumb disables alternate screen buffer, enabling output streaming via capture-pane
	loomCmd := fmt.Sprintf("TERM=dumb loom %s %s --daemon-mode", opts.AgentType, opts.WorktreePath)

	// Propagate backend selection to subprocess
	if resolved := GetBackendName(); resolved != "claude" {
		loomCmd += fmt.Sprintf(" --backend %s", resolved)
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

	// Setup logging via loom-router for intelligent log routing
	// loom-router writes to agent log always, and task log when a task is claimed
	// logFile is ~/.loom/logs/agents/{agentName}.log, so logDir is two levels up
	logDir := filepath.Dir(filepath.Dir(logFile))
	lockPath := filepath.Join(ResolveLockDir(opts.WorktreePath), LockFileName)
	routerCmd := fmt.Sprintf("loom-router --agent %s --base-dir %s --lock-path %s",
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
			} else {
				// No new output - back off
				if poller != nil {
					poller.hadNoActivity()
				}
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
