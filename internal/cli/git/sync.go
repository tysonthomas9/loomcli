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

var syncCmd = &cobra.Command{
	Use:     "sync",
	Short:   "Push completed work then pull latest into all worktrees",
	GroupID: "git",
	Long: `Sync performs a full push + pull cycle for all worktrees.

This command:
1. Finds all worktrees with completed work (ready to push)
2. Pushes each to main (or per-repo default)
3. Pulls main into all worktrees

Sync operates on REPO CHECKOUTS, not on agent worktrees; the summary names the
agent worktrees a run did not cover.

Each repo's result is verified after the pull by measuring the checkout against
<remote>/<default-branch>. Only a repo git reports as containing that branch is
shown as ✓; a repo still behind, or left mid-merge, is reported as a failure and
the command exits non-zero.

This is the recommended way to keep worktrees in sync with the main branch.

Flags:
  --push-only        Only push (skip pulling)
  --pull-only        Only pull (never pushes, including the post-merge push)
  -W, --workspace    Workspace to operate on

Examples:
  loom sync                      # Full sync: push all ready + pull all
  loom sync --push-only          # Only push completed work
  loom sync --pull-only          # Only pull latest; nothing is published
  loom sync -W myworkspace       # Sync specific workspace`,
	Args: cobra.NoArgs,
	RunE: runFullSync,
}

func init() {
	syncCmd.Flags().BoolVar(&syncPushOnly, "push-only", false, "Only push (skip pulling)")
	syncCmd.Flags().BoolVar(&syncPullOnly, "pull-only", false, "Only pull (never pushes, including the post-merge push)")
	syncCmd.Flags().StringVarP(&syncWorkspaceFlag, "workspace", "W", "", "Workspace to operate on")
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
		if err := syncSingleWorkspace(deps, resolver, pushOnly, pullOnly); err != nil {
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
	fmt.Println("Full sync complete!")
	fmt.Println("=========================================")
	return nil
}

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
		// --pull-only means "do not touch any remote in a writing way". The pull
		// path pushes the merge result by default; that push must be suppressed
		// too, or the flag only moves where the push happens.
		outcomes := pullWorkspaceWorktreesWithCoverage(deps, worktrees, "", agentWorktreeNames(resolver), !pullOnly)
		if n := summaryFailures(outcomes); n > 0 {
			return fmt.Errorf("pull phase left %d repo(s) not in sync in workspace %s", n, resolver.WorkspaceName())
		}
	}
	return nil
}

// agentWorktreeNames lists the agent worktrees sync does not visit, as
// <repo>/<agent>. This is a reporting nicety only: discovery errors and
// non-workspace mode yield an empty list and never fail the sync.
func agentWorktreeNames(resolver *cli.Resolver) []string {
	agents, err := resolver.DiscoverAgentWorktrees()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(agents))
	for _, a := range agents {
		if a.Repo != nil {
			names = append(names, a.Repo.Name+"/"+a.Name)
			continue
		}
		names = append(names, a.Name)
	}
	return names
}
