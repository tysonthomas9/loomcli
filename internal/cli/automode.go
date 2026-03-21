package cli

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// resolveWebUIURL returns the local webui server URL for session notifications.
// Uses LOOM_WEBUI_URL env if set, otherwise defaults to http://127.0.0.1:8080.
func resolveWebUIURL() string {
	if url := os.Getenv("LOOM_WEBUI_URL"); url != "" {
		return url
	}
	return "http://127.0.0.1:8080"
}

// AutoModeOptions holds configuration for auto mode
type AutoModeOptions struct {
	Interval        int    // Polling interval in seconds when no tasks available
	MaxTasks        int    // Maximum tasks to process before exiting (0 = unlimited)
	IdleTimeout     int    // Exit after N minutes with no available tasks (0 = no timeout)
	AgentType       string // "plan" or "task"
	AgentName       string
	WorktreePath    string
	ParentID        string                                // Epic ID to scope task discovery to (empty = all tasks)
	CustomPromptGen func(string, *WorkspaceConfig) string // Custom prompt generator (overrides AgentType selection)
	CustomTaskCheck func() (bool, error)                  // Custom task availability check (overrides AgentType selection)
	BackoffBase     time.Duration                         // Base backoff duration for no-progress retries (default 30s)
	EventBus        events.Emitter                        // Event emission for observability (nil = no events)

	// Interface abstractions for remote agent support.
	// When nil, local filesystem implementations are used (existing behavior).
	LockBridge   LockBridge
	EventEmitter EventEmitter
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

// SetupSignalHandler sets up graceful shutdown on SIGINT/SIGTERM
// Returns a channel that will be CLOSED when shutdown is requested
// (closed channel pattern allows multiple goroutines to detect shutdown)
func SetupSignalHandler() chan struct{} {
	shutdown := make(chan struct{})
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	go func() {
		sig := <-sigChan
		signal.Stop(sigChan) // Stop delivering signals to this channel
		log.Printf("[auto] Shutdown signal received: %v (PID=%d, PGID=%d)", sig, os.Getpid(), syscall.Getpgrp())
		fmt.Printf("\n[auto] Shutdown signal received (%v), stopping gracefully...\n", sig)
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

// agentClaimedTask checks the lock file to determine if the agent claimed a task
// during its session. Returns true if TaskID is non-empty.
// When bridge is non-nil, reads via the bridge; otherwise uses the local filesystem.
func agentClaimedTask(worktreePath, agentName string, bridge LockBridge) bool {
	var info *LockInfo
	var err error
	if bridge != nil {
		info, err = bridge.ReadLock(agentName)
	} else {
		info, err = ReadLockFile(worktreePath)
	}
	if err != nil {
		// Can't read lock file — daemon never ran or failed before writing lock.
		// No task was claimed (no progress).
		return false
	}
	return info.TaskID != ""
}

// RunAutoModeLoop runs the auto mode loop for either plan or task agents
func RunAutoModeLoop(opts AutoModeOptions, shutdown chan struct{}) {
	if opts.BackoffBase == 0 {
		opts.BackoffBase = 30 * time.Second
	}
	if opts.EventBus == nil {
		opts.EventBus = events.NopBus{}
	}

	state := &AutoModeState{
		LastTaskTime:  time.Now(),
		IdleStartTime: time.Now(),
	}

	// Choose the appropriate task checker based on agent type
	var hasAvailableTasks func() (bool, error)
	var generatePrompt func(string, *WorkspaceConfig) string

	// Task check: custom overrides default
	if opts.CustomTaskCheck != nil {
		hasAvailableTasks = opts.CustomTaskCheck
	} else if opts.AgentType == "plan" {
		hasAvailableTasks = func() (bool, error) { return HasAvailablePlanningTasks(opts.ParentID, os.Getenv("LOOM_AGENT_REPO")) }
	} else {
		hasAvailableTasks = func() (bool, error) {
			return HasAvailableImplementationTasks(opts.ParentID, os.Getenv("LOOM_AGENT_REPO"))
		}
	}

	// Prompt gen: custom overrides default
	if opts.CustomPromptGen != nil {
		generatePrompt = opts.CustomPromptGen
	} else if opts.AgentType == "plan" {
		generatePrompt = func(name string, ws *WorkspaceConfig) string {
			return GeneratePlanningPrompt(name, ws, opts.ParentID)
		}
	} else {
		generatePrompt = func(name string, ws *WorkspaceConfig) string {
			return GenerateTaskPrompt(name, ws, opts.ParentID, GetBackendName())
		}
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

	// Initialize usage store (non-fatal if it fails)
	usageStore, usageErr := usage.NewStore(GetBeadsDir())
	if usageErr != nil {
		fmt.Printf("[auto] Warning: usage tracking disabled: %v\n", usageErr)
	}

	// Initialize session store (non-fatal if it fails)
	sessStore, sessErr := sessions.NewStore(GetBeadsDir())
	if sessErr != nil {
		log.Printf("[auto] Warning: session store unavailable: %v", sessErr)
	}

	// Helper closures for lock operations that use bridge when available.
	updateState := func(state string) error {
		if opts.LockBridge != nil {
			return opts.LockBridge.UpdateState(opts.AgentName, state)
		}
		return UpdateLockState(opts.WorktreePath, state)
	}
	clearTaskID := func() error {
		if opts.LockBridge != nil {
			return opts.LockBridge.ClearTaskID(opts.AgentName)
		}
		return ClearLockTaskID(opts.WorktreePath)
	}
	readLock := func() (*LockInfo, error) {
		if opts.LockBridge != nil {
			return opts.LockBridge.ReadLock(opts.AgentName)
		}
		return ReadLockFile(opts.WorktreePath)
	}

	for {
		// Set idle state at loop start
		if err := updateState(StateIdle); err != nil {
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
		if err := updateState(StateActive); err != nil {
			fmt.Printf("[auto] Warning: failed to update state: %v\n", err)
		}

		// Clear TaskID before new session so we can detect if the agent claims one
		if clearErr := clearTaskID(); clearErr != nil {
			fmt.Printf("[auto] Warning: failed to clear task ID: %v\n", clearErr)
		}

		// Invoke agent to work on one task
		fmt.Println("")
		fmt.Printf("[auto] === Starting task %d ===\n", state.TasksCompleted+1)
		fmt.Println("")

		// Capture HEAD ref before agent run (for diff stats on completion)
		beforeRef := captureHEADRef(opts.WorktreePath)

		workspace, _ := ResolveActiveWorkspace()
		prompt := generatePrompt(opts.AgentName, workspace)

		// Create session record before invocation
		var sess *sessions.Session
		if sessStore != nil {
			sess, _ = sessStore.CreateSession(sessions.CreateOptions{
				AgentName:  opts.AgentName,
				Backend:    ResolveBackendName(),
				EpicID:     opts.ParentID,
				Prompt:     prompt,
				AttemptNum: state.TasksCompleted + 1,
			})
			if sess != nil {
				SetActiveSessionEnv(GetBeadsDir(), sess.SessionID())
				go sessions.NotifyWebUI(context.Background(), resolveWebUIURL(), "", sess.SessionID(), sessions.StatusRunning)
			}
		}

		// Set up usage collector before invocation
		backendName := GetBackendName()
		collector := usage.NewCollector(backendName, opts.AgentName)
		startedAt := time.Now()

		err = InvokeAgentNonInteractive(opts.WorktreePath, prompt, opts.AgentName, shutdown, collector)

		endedAt := time.Now()
		recordSessionUsage(usageStore, collector, opts.WorktreePath, opts.AgentName, opts.ParentID, startedAt, endedAt, err, opts.LockBridge)

		// Finalize session record
		if sess != nil {
			exitCode := 0
			if err != nil {
				exitCode = 1
				var exitErr *exec.ExitError
				if errors.As(err, &exitErr) {
					exitCode = exitErr.ExitCode()
				}
			}
			taskID := ""
			if info, lockErr := ReadLockFile(opts.WorktreePath); lockErr == nil {
				taskID = info.TaskID
			}
			diffStats := ComputeDiffStats(opts.WorktreePath, beforeRef)
			_ = sess.Finalize(sessions.FinalizeOptions{
				TaskID:   taskID,
				ExitCode: exitCode,
				DiffStats: sessions.DiffStats{
					FilesChanged: diffStats.FilesChanged,
					LinesAdded:   diffStats.LinesAdded,
					LinesRemoved: diffStats.LinesRemoved,
				},
			})
			ClearActiveSessionEnv()
			go sessions.NotifyWebUI(context.Background(), resolveWebUIURL(), taskID, sess.SessionID(), sess.Meta.Status)
		}

		// Return to idle state after agent finishes
		if updateErr := updateState(StateIdle); updateErr != nil {
			fmt.Printf("[auto] Warning: failed to update state: %v\n", updateErr)
		}

		if err != nil {
			fmt.Printf("[auto] Agent exited with error: %v\n", err)
			state.ConsecutiveErrors++

			// Emit task_failed event (try to include TaskID from lock file)
			failedTaskID := ""
			if info, readErr := readLock(); readErr == nil && info != nil {
				failedTaskID = info.TaskID
			}
			if evt, evtErr := events.NewEvent(events.TaskFailed, opts.AgentName, "", "", events.TaskFailedData{TaskID: failedTaskID, Error: err.Error()}); evtErr == nil {
				if emitErr := opts.EventBus.Emit(evt); emitErr != nil {
					log.Printf("[auto] Failed to emit task_failed event: %v", emitErr)
				}
			}

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

		if agentClaimedTask(opts.WorktreePath, opts.AgentName, opts.LockBridge) {
			state.TasksCompleted++
			state.ConsecutiveNoProgress = 0
			state.LastTaskTime = time.Now()

			// Emit task_completed event with diff stats
			taskID := ""
			if info, readErr := readLock(); readErr == nil && info != nil {
				taskID = info.TaskID
			}
			diffStats := ComputeDiffStats(opts.WorktreePath, beforeRef)
			duration := events.Duration{Duration: endedAt.Sub(startedAt)}
			if evt, evtErr := events.NewEvent(events.TaskCompleted, opts.AgentName, "", "", events.TaskCompletedData{
				TaskID:       taskID,
				Duration:     duration,
				FilesChanged: diffStats.FilesChanged,
				LinesAdded:   diffStats.LinesAdded,
				LinesRemoved: diffStats.LinesRemoved,
			}); evtErr == nil {
				if emitErr := opts.EventBus.Emit(evt); emitErr != nil {
					log.Printf("[auto] Failed to emit task_completed event: %v", emitErr)
				}
			}

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

			// Exponential backoff: base, 2*base, 4*base (capped at 4*base)
			backoff := opts.BackoffBase << (state.ConsecutiveNoProgress - 1)
			if cap := 4 * opts.BackoffBase; backoff > cap {
				backoff = cap
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

// recordSessionUsage finalizes the usage collector and appends the record to the store.
// Failures are logged but do not interrupt the auto mode loop.
// When bridge is non-nil, reads the lock via the bridge; otherwise uses the local filesystem.
func recordSessionUsage(store *usage.Store, collector *usage.Collector, worktreePath, agentName, parentID string, startedAt, endedAt time.Time, invokeErr error, bridge LockBridge) {
	if store == nil || collector == nil {
		return
	}

	// Derive exit code from error
	exitCode := 0
	if invokeErr != nil {
		exitCode = 1
		var exitErr *exec.ExitError
		if errors.As(invokeErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}

	// Read task/epic context from lock file
	var taskID, epicID string
	if bridge != nil {
		if info, err := bridge.ReadLock(agentName); err == nil && info != nil {
			taskID = info.TaskID
		}
	} else {
		if info, err := ReadLockFile(worktreePath); err == nil && info != nil {
			taskID = info.TaskID
		}
	}
	epicID = parentID

	record := collector.Finalize(taskID, epicID, startedAt, endedAt, exitCode)
	if err := store.Append(record); err != nil {
		log.Printf("[auto] Warning: failed to record usage: %v", err)
	}
}

// captureHEADRef returns the current HEAD ref for the worktree.
// Returns empty string on error (ComputeDiffStats handles this gracefully).
func captureHEADRef(worktreePath string) string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
