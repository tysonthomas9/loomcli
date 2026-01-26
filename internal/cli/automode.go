package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
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
// Returns a channel that receives true when shutdown is requested
func SetupSignalHandler() chan bool {
	shutdown := make(chan bool, 1)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		shutdown <- true
	}()

	return shutdown
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
func RunAutoModeLoop(opts AutoModeOptions, shutdown chan bool) {
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
			time.Sleep(time.Duration(opts.Interval) * time.Second)
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
			time.Sleep(time.Duration(opts.Interval) * time.Second)
			continue
		}

		// Reset idle timer when we find tasks
		state.IdleStartTime = time.Now()

		// Invoke Claude to work on one task
		fmt.Println("")
		fmt.Printf("[auto] === Starting task %d ===\n", state.TasksCompleted+1)
		fmt.Println("")

		prompt := generatePrompt(opts.AgentName)
		err = InvokeClaudeNonInteractive(opts.WorktreePath, prompt, shutdown)

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
			time.Sleep(time.Duration(opts.Interval) * time.Second)
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
		time.Sleep(2 * time.Second)
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
