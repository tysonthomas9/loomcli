package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/workspace"
)

// IsTmuxAvailable checks if tmux is installed and available.
// It is a variable so tests can override it.
var IsTmuxAvailable = func() bool {
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

	// Resolve workspace ID for session naming and log isolation.
	wsID := workspace.ResolveWorkspaceID(opts.WorkspaceID)
	wsPrefix := workspace.ShortWorkspaceID(wsID)

	// Include workspace prefix and PID to prevent session name collisions
	sessionName := fmt.Sprintf("loom-%s-%s-%s-%d", wsPrefix, opts.AgentType, opts.AgentName, os.Getpid())

	// Setup log file — store outside worktree to avoid polluting git status
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("[auto] Warning: could not get home directory: %v\n", err)
		homeDir = os.TempDir()
	}
	// Namespace log directory by workspace ID to prevent collisions between
	// same-named agents in different workspaces.
	workspaceID := wsID
	if workspaceID == "" {
		workspaceID = "_default"
	}
	logDir := filepath.Join(homeDir, ".loom", "logs", workspaceID)
	agentLogDir := filepath.Join(logDir, "agents")
	if err := os.MkdirAll(agentLogDir, 0700); err != nil {
		fmt.Printf("[auto] Warning: could not create log directory: %v\n", err)
	}
	logFile := filepath.Join(agentLogDir, fmt.Sprintf("%s.log", opts.AgentName))

	// Choose task checker based on agent type (CustomPromptGen not used — tmux delegates to daemon).
	var hasAvailableTasks func() (bool, error)
	repoLabel := os.Getenv("LOOM_AGENT_REPO")
	if opts.CustomTaskCheck != nil {
		hasAvailableTasks = opts.CustomTaskCheck
	} else if opts.AgentType == "plan" {
		hasAvailableTasks = func() (bool, error) { return HasAvailablePlanningTasks(opts.ParentID, repoLabel) }
	} else {
		hasAvailableTasks = func() (bool, error) { return HasAvailableImplementationTasks(opts.ParentID, repoLabel) }
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
			if interruptibleSleep(5*time.Second, shutdown) {
				cleanupTmuxSession(sessionName)
				printTmuxSummary(taskCount)
				return
			}
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
			if interruptibleSleep(5*time.Second, shutdown) {
				cleanupTmuxSession(sessionName)
				printTmuxSummary(taskCount)
				return
			}
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
		if agentClaimedTask(opts.WorktreePath, opts.AgentName, opts.LockBridge) {
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

// printTmuxSummary prints the auto mode completion summary
func printTmuxSummary(taskCount int) {
	fmt.Println("")
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Printf("AUTO MODE COMPLETE - %d task(s) processed\n", taskCount)
	fmt.Println("═══════════════════════════════════════════════════════════════")
}
