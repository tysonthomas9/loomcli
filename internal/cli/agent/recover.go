package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
)

// strPtr returns a pointer to s. Used for UpdateOpts.Assignee where
// nil means "don't change" and &"" means "clear".
func strPtr(s string) *string { return &s }

var (
	recoverNoAnalyze bool
	recoverForce     bool
)

var recoverCmd = &cobra.Command{
	Use:               "recover <worktree>",
	Short:             "Recover from agent error state",
	GroupID:           "agents",
	ValidArgsFunction: cli.WorktreeCompletion,
	Long: `Recover a worktree from error state by clearing stale locks
and handling orphaned tasks intelligently.

This command will:
  1. Check if the agent process is still running
  2. If not running, clear the stale lock file
  3. Analyze the orphaned task using Claude to determine if it was completed
  4. Close completed tasks, or reset incomplete tasks to open status
  5. Clean up untracked files left by the crashed agent (with confirmation)

In workspace mode, recovery clears the shared workspace-level lock,
searches git logs across all repos for task completion evidence, and
cleans untracked files in all workspace repos.

Use this when 'loom monitor' shows an agent in error state.

Flags:
  --force        Kill running agent and clean untracked files without prompting
  --no-analyze   Skip Claude analysis, always reset task to open status

Examples:
  loom recover falcon              # Recover with task analysis (default)
  loom recover ember --no-analyze  # Skip analysis, always reset to open
  loom recover falcon --force      # Kill running agent and clean files without prompting
  loom recover myworkspace         # Recover workspace-level agent`,
	Args: cobra.ExactArgs(1),
	Run:  runRecover,
}

func init() {
	recoverCmd.Flags().BoolVar(&recoverNoAnalyze, "no-analyze", false,
		"Skip Claude analysis, always reset task to open status")
	recoverCmd.Flags().BoolVar(&recoverForce, "force", false,
		"Skip all confirmation prompts (kill process, clean files)")
	cli.RegisterCommand(recoverCmd)
}

func runRecover(cmd *cobra.Command, args []string) {
	deps := cli.GetDeps(cmd)
	worktreeName := args[0]

	worktreePath, err := cli.ResolveWorktreePath(worktreeName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("=========================================")
	fmt.Printf("Recovering agent: %s\n", worktreeName)
	fmt.Println("=========================================")
	fmt.Println("")

	lockInfo, isRunning, err := cli.CheckLock(worktreePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error checking lock: %v\n", err)
		os.Exit(1)
	}

	if lockInfo == nil {
		fmt.Println("No lock file found - checking for orphaned tasks...")
		resetOrphanedAgentTasks(cmd.Context(), deps, worktreePath, worktreeName, "", !recoverNoAnalyze)
		fmt.Println("Agent is ready for new work.")
		return
	}

	if isRunning {
		if !handleRunningAgent(lockInfo.PID) {
			return
		}
	}

	clearStaleLock(worktreePath, lockInfo.PID)

	if lockInfo.TaskID != "" {
		handleOrphanedTask(cmd.Context(), deps, worktreePath, lockInfo.TaskID, !recoverNoAnalyze)
	}

	resetOrphanedAgentTasks(cmd.Context(), deps, worktreePath, lockInfo.AgentName, lockInfo.TaskID, !recoverNoAnalyze)
	cleanUntrackedFiles(worktreePath, recoverForce)

	fmt.Println("")
	fmt.Println("=========================================")
	fmt.Printf("✓ Agent '%s' recovered and ready for work\n", worktreeName)
	fmt.Println("=========================================")
}

// releaseFleetIssueLock releases the fleet-db lock held by agentName. A missing
// lock is an idempotent success. Callers still verify task ownership immediately
// before any destructive status or assignee reset.
func releaseFleetIssueLock(parent context.Context, deps *cli.Deps, agentName, taskID string) error {
	if deps == nil || deps.IssueBackend == nil || agentName == "" || taskID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	if err := deps.IssueBackend.ReleaseIssueLock(ctx, taskID, agentName); err != nil {
		if backend.IsKind(err, backend.KindNotFound) {
			return nil
		}
		if errors.Is(err, context.DeadlineExceeded) {
			fmt.Printf("[recover] WARN: timed out releasing fleet-db lock for %s (actor=%s)\n",
				taskID, agentName)
			return err
		}
		fmt.Printf("[recover] WARN: failed to release fleet-db lock for %s (actor=%s): %v\n",
			taskID, agentName, err)
		return err
	}
	fmt.Printf("[recover] released fleet-db lock issue=%s actor=%s\n", taskID, agentName)
	return nil
}

// handleRunningAgent prompts to kill a running agent process. Returns true if
// the process was killed (or force mode is on), false if the user aborted.
func handleRunningAgent(pid int) bool {
	fmt.Printf("Agent process (PID %d) is still running.\n", pid)

	shouldKill := recoverForce
	if !shouldKill {
		shouldKill = confirmKill(pid)
	}
	if !shouldKill {
		fmt.Println("Aborted. Agent process left running.")
		return false
	}

	if err := killProcess(pid); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to kill process: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Killed agent process (PID %d)\n", pid)
	return true
}

// clearStaleLock removes the lock file for a no-longer-running process.
func clearStaleLock(worktreePath string, pid int) {
	fmt.Printf("Clearing stale lock (PID %d no longer running)...\n", pid)
	if err := forceReleaseLock(worktreePath); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to clear lock: %v\n", err)
	} else {
		fmt.Println("✓ Lock cleared")
	}
}

// RecoverWorktree provides a non-interactive recovery path for daemon use:
// force-release locks, kill processes, reset orphaned tasks, clean files.
// On clean exit (code 0) trusts agent's task status; on non-zero resets tasks.
func RecoverWorktree(ctx context.Context, worktreePath, agentName string, exitCode int) error {
	deps := &cli.Deps{}
	*deps = *cli.GetDeps(nil)
	deps.IssueBackend = cli.DefaultIssueBackend()

	// 1. Check lock status
	lockInfo, isRunning, err := cli.CheckLock(worktreePath)
	if err != nil {
		return fmt.Errorf("failed to check lock: %w", err)
	}

	if lockInfo != nil {
		// 2. If lock exists and process is running, kill it
		if isRunning {
			if err := killProcess(lockInfo.PID); err != nil {
				return fmt.Errorf("failed to kill agent process (PID %d): %w", lockInfo.PID, err)
			}
		}

		// 3. Handle the distributed claim before removing the local recovery
		// marker. If another actor now owns the task, fleet-db returns a conflict;
		// fail closed so this stale agent cannot reset/unassign the new owner's
		// task. Keeping the local lock lets a later cold-recovery attempt retry the
		// same guarded operation instead of forgetting which task was interrupted.
		if lockInfo.TaskID != "" {
			// Release the fleet-db issue lock on every exit path.
			// Status mutations the agent already performed (review/closed/
			// open via Update or Close) do NOT release the lock server-side,
			// so without this call the lock survives until its TTL expires
			// and other agents get spurious claim conflicts.
			releaseErr := releaseFleetIssueLock(ctx, deps, agentName, lockInfo.TaskID)
			if releaseErr != nil {
				return fmt.Errorf("release task %s before recovery mutations: %w", lockInfo.TaskID, releaseErr)
			}

			if exitCode == 0 {
				// Clean exit: trust the agent updated task status correctly.
				// Do NOT reset — the agent may have set status to review/closed.
				fmt.Printf("[recover] Agent %s exited cleanly (code 0), trusting agent's task status for %s\n",
					agentName, lockInfo.TaskID)
			} else {
				fmt.Printf("[recover] Agent %s exited with code %d, resetting task %s\n",
					agentName, exitCode, lockInfo.TaskID)
				if err := resetTaskOwnedByAgent(ctx, deps, lockInfo.TaskID, agentName); err != nil {
					return fmt.Errorf("reset interrupted task %s: %w", lockInfo.TaskID, err)
				}
			}
		}

		// 4. Clear stale lock only after the guarded backend mutation finishes
		// (fatal if removal fails, unlike interactive runRecover which warns).
		if err := forceReleaseLock(worktreePath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to clear lock: %w", err)
		}
	}

	// 5. Reset any additional orphaned tasks for this agent (no analysis)
	lockTaskID := ""
	if lockInfo != nil {
		lockTaskID = lockInfo.TaskID
	}
	resetOrphanedAgentTasks(ctx, deps, worktreePath, agentName, lockTaskID, false)

	// 6. Clean untracked files (force=true, no prompting)
	cleanUntrackedFiles(worktreePath, true)

	return nil
}
