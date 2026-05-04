package agent

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

var completeCmd = &cobra.Command{
	Use:   "complete",
	Short: "Signal task completion to auto mode",
	Long: `Signal that the current task is complete.

This command is used by Claude agents to signal task completion to the
auto mode parent process. It writes a signal file to a temporary location
outside the git worktree, so it won't be affected by git clean operations.

Usage:
  loom complete    # Signal completion from current directory`,
	Run: runComplete,
}

func init() {
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
			os.Exit(1)
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

	// Write signal file to a safe location outside git's reach
	signalFile := getSignalFilePath(worktreePath)
	signalDir := filepath.Dir(signalFile)

	if err := cli.EnsureSignalDir(signalDir); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating signal directory: %v\n", err)
		os.Exit(1)
	}

	// Write the worktree path to the signal file (for debugging/verification)
	if err := os.WriteFile(signalFile, []byte(worktreePath), 0600); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing signal file: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Task completion signaled")
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
