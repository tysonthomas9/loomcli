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
	Short:   "Restack all working areas onto their recorded trunks",
	GroupID: "git",
	Long: `Sync fetches each recorded trunk and restacks its working area.

This command:
1. Finds the workspace's working areas
2. Pulls each recorded trunk and restacks unlanded layers

Sync never publishes or pushes. Use loom publish for an approved revision.

Flags:
  --push-only        Unsupported; use loom publish
  --pull-only        Alias for sync
  -W, --workspace    Workspace to operate on
  -y, --yes          Confirm every workspace in non-interactive mode

Examples:
  loom sync                      # Restack all working areas
  loom sync --pull-only          # Same as sync
  loom sync -W myworkspace       # Sync specific workspace`,
	Args: cobra.NoArgs,
	RunE: runFullSync,
}

func init() {
	syncCmd.Flags().BoolVar(&syncPushOnly, "push-only", false, "Unsupported; use loom publish")
	syncCmd.Flags().BoolVar(&syncPullOnly, "pull-only", false, "Alias for sync")
	syncCmd.Flags().StringVarP(&syncWorkspaceFlag, "workspace", "W", "", "Workspace to operate on")
	syncCmd.Flags().BoolVarP(&syncYes, "yes", "y", false, "Confirm every workspace without prompting")
	cli.RegisterCommand(syncCmd)
}

func runFullSync(cmd *cobra.Command, args []string) error {
	deps := cli.GetDeps(cmd)
	pushOnly, _ := cmd.Flags().GetBool("push-only")
	pullOnly, _ := cmd.Flags().GetBool("pull-only")
	ws, _ := cmd.Flags().GetString("workspace")
	if pushOnly {
		return fmt.Errorf("sync no longer pushes; use loom publish for an approved revision")
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
	resolver, err := allWorkspacesResolver()
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

func syncSingleWorkspace(deps *cli.Deps, resolver *cli.Resolver, pushOnly, pullOnly bool) error {
	if pushOnly {
		return fmt.Errorf("sync no longer pushes; use loom publish for an approved revision")
	}
	_ = pullOnly
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
	if err := pullWorkspaceWorktrees(deps, worktrees, ""); err != nil {
		return err
	}
	return nil
}
