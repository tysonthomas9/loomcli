package git

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

var syncPushOnly bool
var syncPullOnly bool
var syncWorkspaceFlag string
var syncYes bool

var syncCmd = &cobra.Command{
	Use:     "sync",
	Short:   "Push completed work then pull latest into all worktrees",
	GroupID: "git",
	Long: `Sync performs a full push + pull cycle for all worktrees.

This command:
1. Finds all worktrees with completed work (ready to push)
2. Pushes each to main (or per-repo default)
3. Pulls main into all worktrees

This is the recommended way to keep worktrees in sync with the main branch.

Flags:
  --push-only        Only push (skip pulling)
  --pull-only        Only pull (skip pushing)
  -W, --workspace    Workspace to operate on
  -y, --yes          Confirm every workspace in non-interactive mode

Examples:
  loom sync                      # Full sync: push all ready + pull all
  loom sync --push-only          # Only push completed work
  loom sync --pull-only          # Only pull latest (same as pull --all)
  loom sync -W myworkspace       # Sync specific workspace`,
	Args: cobra.NoArgs,
	RunE: runFullSync,
}

func init() {
	syncCmd.Flags().BoolVar(&syncPushOnly, "push-only", false, "Only push (skip pulling)")
	syncCmd.Flags().BoolVar(&syncPullOnly, "pull-only", false, "Only pull (skip pushing)")
	syncCmd.Flags().StringVarP(&syncWorkspaceFlag, "workspace", "W", "", "Workspace to operate on")
	syncCmd.Flags().BoolVarP(&syncYes, "yes", "y", false, "Confirm every workspace without prompting")
	cli.RegisterCommand(syncCmd)
}

func runFullSync(cmd *cobra.Command, args []string) error {
	deps := cli.GetDeps(cmd)
	pushOnly, _ := cmd.Flags().GetBool("push-only")
	pullOnly, _ := cmd.Flags().GetBool("pull-only")
	ws, _ := cmd.Flags().GetString("workspace")

	if pushOnly && pullOnly {
		fmt.Fprintln(os.Stderr, "Error: --push-only and --pull-only are mutually exclusive")
		os.Exit(1)
	}

	return runWorkspaceSync(deps, pushOnly, pullOnly, ws)
}

// runWorkspaceSync returns a non-nil error when any workspace failed to sync.
// The failures are printed as they happen — a multi-workspace sync should not
// abandon the remaining workspaces because one of them is broken — but they
// must still reach the exit code. Swallowing them printed "Full sync complete!"
// and exited 0 over a workspace whose repos were never discovered, which is
// indistinguishable from success to anything scripting this command.
func runWorkspaceSync(deps *cli.Deps, pushOnly, pullOnly bool, ws string) error {
	return runWorkspaceSyncWithConfirmation(deps, pushOnly, pullOnly, ws, newConfirmationSession(syncYes))
}

func runWorkspaceSyncWithConfirmation(deps *cli.Deps, pushOnly, pullOnly bool, ws string, confirmation *confirmationSession) error {
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
		return syncSingleWorkspace(deps, resolver, pushOnly, pullOnly)
	}

	// Sync all workspaces
	wsNames := resolver.WorkspaceNames()
	if len(wsNames) == 0 {
		fmt.Println("No workspaces found.")
		return nil
	}
	if err := confirmation.requireInteractive("loom sync", wsNames); err != nil {
		return err
	}

	fmt.Println("=========================================")
	fmt.Println("Full Sync: All Workspaces")
	fmt.Println("=========================================")
	fmt.Println("")

	var failed []string
	var skipped []string
	for _, wsName := range wsNames {
		declined, err := syncConfirmedWorkspace(deps, resolver, wsName, pushOnly, pullOnly, confirmation)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			failed = append(failed, wsName)
			continue
		}
		if declined {
			skipped = append(skipped, wsName)
		}
	}
	return printSyncSummary(failed, skipped)
}

func printSyncSummary(failed, skipped []string) error {
	fmt.Println("=========================================")
	if len(failed) > 0 {
		fmt.Fprintf(os.Stderr, "Sync failed for %d workspace(s): %v\n", len(failed), failed)
		fmt.Println("=========================================")
		return fmt.Errorf("sync failed for %d workspace(s): %v", len(failed), failed)
	}
	if len(skipped) > 0 {
		fmt.Printf("Skipped workspaces: %v\n", skipped)
	} else {
		fmt.Println("Full sync complete!")
	}
	fmt.Println("=========================================")
	return nil
}

func syncConfirmedWorkspace(deps *cli.Deps, resolver *cli.Resolver, name string, pushOnly, pullOnly bool, confirmation *confirmationSession) (bool, error) {
	fmt.Printf("=== Workspace: %s ===\n", name)
	if err := resolver.SetWorkspace(name); err != nil {
		return false, fmt.Errorf("setting workspace %s: %w", name, err)
	}
	worktrees, err := resolver.DiscoverWorktrees()
	if err != nil {
		return false, fmt.Errorf("discovering repos in workspace %s: %w", name, err)
	}
	if err := printUnsavedWork(deps, worktrees, ""); err != nil {
		return false, err
	}
	if !confirmation.confirm("Sync workspace " + name + "?") {
		fmt.Printf("Skipped workspace %s.\n\n", name)
		return true, nil
	}
	err = runConfirmedSync(deps, resolver, pushOnly, pullOnly)
	fmt.Println("")
	return false, err
}

var runConfirmedSync = syncSingleWorkspace

// syncSingleWorkspace returns an error only for failures that mean the sync did
// not happen. A push phase that completed with per-repo errors is reported but
// not fatal — that is the pre-existing contract of pushWorkspaceWorktrees, and
// changing it belongs to a different change than this one.
func syncSingleWorkspace(deps *cli.Deps, resolver *cli.Resolver, pushOnly, pullOnly bool) error {
	worktrees, err := resolver.DiscoverWorktrees()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error discovering repos: %v\n", err)
		return fmt.Errorf("discover repos in workspace %s: %w", resolver.WorkspaceName(), err)
	}

	if len(worktrees) == 0 {
		fmt.Printf("No repos found in workspace %s\n", resolver.WorkspaceName())
		return nil
	}

	// Phase 1: Push (unless pull-only)
	if !pullOnly {
		fmt.Println("")
		fmt.Println("--- Phase 1: Push ---")
		if err := pushWorkspaceWorktrees(deps, worktrees, "", ""); err != nil {
			fmt.Fprintf(os.Stderr, "Push phase completed with errors: %v\n", err)
		}
	}

	// Phase 2: Pull (unless push-only)
	if !pushOnly {
		fmt.Println("")
		fmt.Println("--- Phase 2: Pull ---")
		pullWorkspaceWorktrees(deps, worktrees, "")
	}
	return nil
}
