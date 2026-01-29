package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var (
	recoverNoAnalyze bool
)

var recoverCmd = &cobra.Command{
	Use:               "recover <worktree>",
	Short:             "Recover from agent error state",
	GroupID:           "agents",
	ValidArgsFunction: worktreeCompletion,
	Long: `Recover a worktree from error state by clearing stale locks
and handling orphaned tasks intelligently.

This command will:
  1. Check if the agent process is still running
  2. If not running, clear the stale lock file
  3. Analyze the orphaned task using Claude to determine if it was completed
  4. Close completed tasks, or reset incomplete tasks to open status

Use this when 'loom monitor' shows an agent in error state.

Examples:
  loom recover falcon              # Recover with task analysis (default)
  loom recover ember --no-analyze  # Skip analysis, always reset to open`,
	Args: cobra.ExactArgs(1),
	Run:  runRecover,
}

func init() {
	recoverCmd.Flags().BoolVar(&recoverNoAnalyze, "no-analyze", false,
		"Skip Claude analysis, always reset task to open status")
	rootCmd.AddCommand(recoverCmd)
}

func runRecover(cmd *cobra.Command, args []string) {
	worktreeName := args[0]

	// 1. Resolve worktree path
	worktreePath, err := ResolveWorktreePath(worktreeName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("=========================================")
	fmt.Printf("Recovering agent: %s\n", worktreeName)
	fmt.Println("=========================================")
	fmt.Println("")

	// 2. Check lock status
	lockInfo, isRunning, err := CheckLock(worktreePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error checking lock: %v\n", err)
		os.Exit(1)
	}

	if lockInfo == nil {
		fmt.Println("No lock file found - agent is not in error state.")
		fmt.Println("Agent is ready for new work.")
		return
	}

	if isRunning {
		fmt.Printf("Agent process (PID %d) is still running.\n", lockInfo.PID)
		fmt.Println("")
		fmt.Println("Options:")
		fmt.Println("  - Wait for the agent to finish")
		fmt.Println("  - Send Ctrl+C to the agent terminal")
		fmt.Printf("  - Force kill: kill -9 %d\n", lockInfo.PID)
		return
	}

	// 3. Clear stale lock
	fmt.Printf("Clearing stale lock (PID %d no longer running)...\n", lockInfo.PID)
	if err := forceReleaseLock(worktreePath); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to clear lock: %v\n", err)
	} else {
		fmt.Println("✓ Lock cleared")
	}

	// 4. Handle orphaned task if there was one
	if lockInfo.TaskID != "" {
		handleOrphanedTask(worktreePath, lockInfo.TaskID, !recoverNoAnalyze)
	}

	fmt.Println("")
	fmt.Println("=========================================")
	fmt.Printf("✓ Agent '%s' recovered and ready for work\n", worktreeName)
	fmt.Println("=========================================")
}

// handleOrphanedTask decides whether to close or reopen an orphaned task
func handleOrphanedTask(worktreePath, taskID string, analyze bool) {
	fmt.Printf("\nHandling orphaned task: %s\n", taskID)

	if analyze {
		fmt.Println("Analyzing task completion with Claude...")
		completed, reason := analyzeTaskCompletion(worktreePath, taskID)

		if completed {
			fmt.Printf("Task appears COMPLETE: %s\n", reason)
			closeTask(worktreePath, taskID, reason)
		} else {
			fmt.Printf("Task appears INCOMPLETE: %s\n", reason)
			resetTask(worktreePath, taskID)
		}
	} else {
		fmt.Println("Skipping analysis (--no-analyze)")
		resetTask(worktreePath, taskID)
	}
}

// analyzeTaskCompletion uses Claude to determine if a task was completed
func analyzeTaskCompletion(worktreePath, taskID string) (completed bool, reason string) {
	// Get task details
	taskResult := execCommand(".", "bd", "show", taskID)
	if taskResult.Err != nil {
		return false, "Could not fetch task details"
	}

	// Get git commits mentioning this task
	gitResult := execCommand(worktreePath, "git", "log", "--oneline", "-20", "--all", "--grep", taskID)
	// gitResult.Err is ignored - empty commits is fine

	// Build prompt for Claude
	prompt := fmt.Sprintf(`You are analyzing whether a software task was completed before the agent crashed.

TASK DETAILS:
%s

GIT COMMITS MENTIONING THIS TASK (task ID: %s):
%s

Based on the commits and task description:
1. If there are commits that implement the task's requirements, it's COMPLETED
2. If there are no relevant commits, or commits only show partial work, it's INCOMPLETE

Respond with EXACTLY one line in this format:
COMPLETED: <brief reason>
or
INCOMPLETE: <brief reason>
`, taskResult.Stdout, taskID, gitResult.Stdout)

	// Run claude -p with the prompt
	claudeResult := execCommand(worktreePath, "claude", "-p", "--output-format", "text", prompt)
	if claudeResult.Err != nil {
		return false, fmt.Sprintf("Claude analysis failed: %v", claudeResult.Err)
	}

	// Parse response
	response := strings.TrimSpace(claudeResult.Stdout)
	lines := strings.Split(response, "\n")

	// Find the line with COMPLETED or INCOMPLETE
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToUpper(line), "COMPLETED:") {
			if idx := strings.Index(line, ":"); idx != -1 {
				return true, strings.TrimSpace(line[idx+1:])
			}
			return true, ""
		}
		if strings.HasPrefix(strings.ToUpper(line), "INCOMPLETE:") {
			if idx := strings.Index(line, ":"); idx != -1 {
				return false, strings.TrimSpace(line[idx+1:])
			}
			return false, ""
		}
	}

	// Default to incomplete if we can't parse
	return false, "Could not determine completion status"
}

// closeTask closes a completed task
func closeTask(worktreePath, taskID, reason string) {
	closeReason := fmt.Sprintf("Completed (verified by recovery analysis): %s", reason)
	result := execCommand(worktreePath, "bd", "close", taskID, "--reason", closeReason)
	if result.Err != nil {
		fmt.Printf("Warning: failed to close task: %v\n", result.Err)
		output := result.Stdout + result.Stderr
		if len(output) > 0 {
			fmt.Printf("Output: %s\n", output)
		}
	} else {
		fmt.Printf("✓ Task %s closed\n", taskID)
	}
}

// resetTask resets a task to open status
func resetTask(worktreePath, taskID string) {
	result := execCommand(worktreePath, "bd", "update", taskID, "--status", "open", "--assignee", "")
	if result.Err != nil {
		fmt.Printf("Warning: failed to reset task: %v\n", result.Err)
		output := result.Stdout + result.Stderr
		if len(output) > 0 {
			fmt.Printf("Output: %s\n", output)
		}
		fmt.Println("")
		fmt.Println("You may need to manually reset the task:")
		fmt.Printf("  bd update %s --status open --assignee \"\"\n", taskID)
	} else {
		fmt.Printf("✓ Task %s reset to open\n", taskID)
	}
}

// forceReleaseLock removes the lock file regardless of which process owns it
// This is used for recovery from error states where the owning process is dead
func forceReleaseLock(worktreePath string) error {
	lockPath := filepath.Join(worktreePath, LockFileName)
	return os.Remove(lockPath)
}
