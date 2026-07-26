package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

var (
	completeNoWork bool
	completeReason string
)

var completeCmd = &cobra.Command{
	Use:   "complete",
	Short: "Signal task completion to auto mode",
	Long: `Signal that the current task is complete.

This command is used by Claude agents to signal task completion to the
auto mode parent process. It writes a signal file to a temporary location
outside the git worktree, so it won't be affected by git clean operations.

Usage:
  loom complete                                    # Signal completion from current directory
  loom complete --no-work --reason "nothing to do" # Advisory/read-only roles: report no actionable work`,
	Run: runComplete,
}

func init() {
	completeCmd.Flags().BoolVar(&completeNoWork, "no-work", false,
		"Report that there was nothing actionable to do this cycle (advisory/read-only roles)")
	completeCmd.Flags().StringVar(&completeReason, "reason", "",
		"Reason for --no-work (defaults to a generic message)")
	cli.RegisterCommand(completeCmd)
}

func runComplete(cmd *cobra.Command, args []string) {
	// Primary: Check env var (set by loom when invoking Claude)
	worktreePath := os.Getenv("LOOM_WORKTREE_PATH")

	if worktreePath == "" {
		// Fallback: Find .loom.lock by traversing up
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting current directory: %v\n", err)
			cli.ExitWithFlush(1)
		}

		worktreePath, err = findWorktreeRoot(cwd)
		if err != nil {
			worktreePath = cwd
		}
	}

	// Canonicalize the path to match what automode uses
	if absPath, err := filepath.Abs(worktreePath); err == nil {
		if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
			worktreePath = resolved
		} else {
			worktreePath = absPath
		}
	}

	// Drop the fleet-db claim lock on the agent's current task, if any. Closes
	// the planner-leak-lock path in LOOM-1: a planner that writes only
	// --design and exits cleanly previously left the claim held even though
	// the worktree lock and process were gone. Best-effort — failures are
	// logged but do not block the completion signal below.
	releaseClaimOnComplete(worktreePath)

	if completeNoWork {
		writeNoWorkMarker(worktreePath)
	}

	// Write signal file to a safe location outside git's reach
	signalFile := getSignalFilePath(worktreePath)
	signalDir := filepath.Dir(signalFile)

	if err := cli.EnsureSignalDir(signalDir); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating signal directory: %v\n", err)
		cli.ExitWithFlush(1)
	}

	// Write the worktree path to the signal file (for debugging/verification)
	if err := os.WriteFile(signalFile, []byte(worktreePath), 0600); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing signal file: %v\n", err)
		cli.ExitWithFlush(1)
	}

	fmt.Println("Task completion signaled")
}

// writeNoWorkMarker writes the no-work marker file the supervisor reads at
// exit (see internal/cli/daemon/supervisor/nowork.go) so an advisory/read-only
// role that held no claim — or held one but found nothing actionable — can
// report "nothing to do" distinctly from a heuristic "no task claimed" NoWork
// classification. Best-effort: a failed marker write is a warning, never a
// non-zero exit — a failed marker must not turn a clean no-op into a
// crash-classified exit.
func writeNoWorkMarker(worktreePath string) {
	markerPath := os.Getenv("LOOM_NOWORK_FILE")
	if markerPath == "" {
		markerPath = filepath.Join(worktreePath, noWorkFileName)
	}

	taskID := ""
	reportedBy := os.Getenv("LOOM_AGENT_NAME")
	if info, err := cli.ReadLockFile(worktreePath); err == nil && info != nil {
		taskID = info.TaskID
		if reportedBy == "" {
			reportedBy = info.AgentName
		}
	}

	reason := completeReason
	if reason == "" {
		reason = "agent reported no work"
	}

	report := &noWorkReport{
		Reason:     reason,
		TaskID:     taskID,
		ReportedAt: time.Now(),
		ReportedBy: reportedBy,
	}
	if err := writeNoWorkFile(markerPath, report); err != nil {
		fmt.Fprintf(os.Stderr, "complete: failed to write no-work marker (continuing): %v\n", err)
	}
}

// GetSignalFilePath returns the path to the signal file for a given worktree.
// The signal file is stored in a temporary directory to avoid being deleted
// by git clean operations.
func getSignalFilePath(worktreePath string) string {
	signalDir := filepath.Join(os.TempDir(), fmt.Sprintf("loom-signals-%d", os.Getuid()))
	return filepath.Join(signalDir, cli.WorkspaceHash(worktreePath))
}

// findWorktreeRoot traverses up from startPath looking for .loom.lock
// Returns the directory containing .loom.lock, or an error if not found
func findWorktreeRoot(startPath string) (string, error) {
	dir := startPath
	for {
		lockPath := filepath.Join(dir, ".loom.lock")
		if _, err := os.Stat(lockPath); err == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root without finding .loom.lock
			return "", fmt.Errorf("no .loom.lock found")
		}
		dir = parent
	}
}
