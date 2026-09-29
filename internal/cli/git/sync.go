package git

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

var syncWorkspaceFlag string

var syncCmd = &cobra.Command{
	Use:     "sync",
	Short:   "Pull latest into all worktrees",
	GroupID: "git",
	Long: `Sync pulls the protected default branch into all worktrees.

This command:
1. Finds all worktrees in the workspace
2. Pulls the protected default branch into each worktree

This is the recommended way to keep worktrees in sync with the main branch.

Flags:
  -W, --workspace    Workspace to operate on

Examples:
  loom sync                      # Pull latest into all worktrees
  loom sync -W myworkspace       # Sync specific workspace`,
	Args: cobra.NoArgs,
	RunE: runFullSync,
}

func init() {
	syncCmd.Flags().StringVarP(&syncWorkspaceFlag, "workspace", "W", "", "Workspace to operate on")
	cli.RegisterCommand(syncCmd)
}

func runFullSync(cmd *cobra.Command, args []string) error {
	deps := cli.GetDeps(cmd)
	ws, _ := cmd.Flags().GetString("workspace")
	return runWorkspaceSync(deps, ws)
}

// runWorkspaceSync returns a non-nil error when any workspace failed to sync.
// The failures are printed as they happen — a multi-workspace sync should not
// abandon the remaining workspaces because one of them is broken — but they
// must still reach the exit code. Swallowing them printed "Sync complete!"
// and exited 0 over a workspace whose repos were never discovered, which is
// indistinguishable from success to anything scripting this command.
func runWorkspaceSync(deps *cli.Deps, ws string) error {
	resolver, err := cli.NewResolver()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating resolver: %v\n", err)
		os.Exit(1)
	}

	// If workspace flag specified, operate on just that workspace
	if ws != "" {
		if err := resolver.SetWorkspace(ws); err != nil {
			available := resolver.WorkspaceNames()
			fmt.Fprintf(os.Stderr, "Error: workspace %q not found. Available: %v\n", ws, available)
			os.Exit(1)
		}
		return syncSingleWorkspace(deps, resolver)
	}

	// Sync all workspaces
	wsNames := resolver.WorkspaceNames()
	if len(wsNames) == 0 {
		fmt.Println("No workspaces found.")
		return nil
	}

	fmt.Println("=========================================")
	fmt.Println("Full Sync: All Workspaces")
	fmt.Println("=========================================")
	fmt.Println("")

	var failed []string
	for _, wsName := range wsNames {
		fmt.Printf("=== Workspace: %s ===\n", wsName)
		if err := resolver.SetWorkspace(wsName); err != nil {
			fmt.Fprintf(os.Stderr, "Error setting workspace %s: %v\n", wsName, err)
			failed = append(failed, wsName)
			continue
		}
		if err := syncSingleWorkspace(deps, resolver); err != nil {
			failed = append(failed, wsName)
		}
		fmt.Println("")
	}

	fmt.Println("=========================================")
	if len(failed) > 0 {
		fmt.Fprintf(os.Stderr, "Sync failed for %d workspace(s): %v\n", len(failed), failed)
		fmt.Println("=========================================")
		return fmt.Errorf("sync failed for %d workspace(s): %v", len(failed), failed)
	}
	fmt.Println("Sync complete!")
	fmt.Println("=========================================")
	return nil
}

// syncSingleWorkspace returns an error only for failures that mean the pull did
// not happen. Individual worktree conflicts remain the responsibility of the
// existing pull workflow.
func syncSingleWorkspace(deps *cli.Deps, resolver *cli.Resolver) error {
	worktrees, err := resolver.DiscoverWorktrees()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error discovering repos: %v\n", err)
		return fmt.Errorf("discover repos in workspace %s: %w", resolver.WorkspaceName(), err)
	}

	if len(worktrees) == 0 {
		fmt.Printf("No repos found in workspace %s\n", resolver.WorkspaceName())
		return nil
	}

	fmt.Println("")
	fmt.Println("--- Pull ---")
	pullWorkspaceWorktrees(deps, worktrees, "")
	return nil
}
