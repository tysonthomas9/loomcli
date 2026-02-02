package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
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

This is the recommended way to keep worktrees in sync with the main branch.

Flags:
  --push-only        Only push (skip pulling)
  --pull-only        Only pull (skip pushing)
  -W, --workspace    Workspace to operate on (workspace mode only)

Examples:
  loom sync                      # Full sync: push all ready + pull all
  loom sync --push-only          # Only push completed work
  loom sync --pull-only          # Only pull latest (same as pull --all)
  loom sync -W myworkspace       # Sync specific workspace`,
	Args: cobra.NoArgs,
	Run:  runFullSync,
}

func init() {
	syncCmd.Flags().BoolVar(&syncPushOnly, "push-only", false, "Only push (skip pulling)")
	syncCmd.Flags().BoolVar(&syncPullOnly, "pull-only", false, "Only pull (skip pushing)")
	syncCmd.Flags().StringVarP(&syncWorkspaceFlag, "workspace", "W", "", "Workspace to operate on")
	rootCmd.AddCommand(syncCmd)
}

func runFullSync(cmd *cobra.Command, args []string) {
	if syncPushOnly && syncPullOnly {
		fmt.Fprintln(os.Stderr, "Error: --push-only and --pull-only are mutually exclusive")
		os.Exit(1)
	}

	if IsWorkspaceMode() {
		runWorkspaceSync()
		return
	}

	runLegacySync()
}

func runWorkspaceSync() {
	resolver, err := NewResolver()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating resolver: %v\n", err)
		os.Exit(1)
	}

	// If workspace flag specified, operate on just that workspace
	if syncWorkspaceFlag != "" {
		if err := resolver.SetWorkspace(syncWorkspaceFlag); err != nil {
			available := resolver.WorkspaceNames()
			fmt.Fprintf(os.Stderr, "Error: workspace %q not found. Available: %v\n", syncWorkspaceFlag, available)
			os.Exit(1)
		}
		syncSingleWorkspace(resolver)
		return
	}

	// Sync all workspaces
	wsNames := resolver.WorkspaceNames()
	if len(wsNames) == 0 {
		fmt.Println("No workspaces found.")
		return
	}

	fmt.Println("=========================================")
	fmt.Println("Full Sync: All Workspaces")
	fmt.Println("=========================================")
	fmt.Println("")

	for _, wsName := range wsNames {
		fmt.Printf("=== Workspace: %s ===\n", wsName)
		if err := resolver.SetWorkspace(wsName); err != nil {
			fmt.Fprintf(os.Stderr, "Error setting workspace %s: %v\n", wsName, err)
			continue
		}
		syncSingleWorkspace(resolver)
		fmt.Println("")
	}

	fmt.Println("=========================================")
	fmt.Println("Full sync complete!")
	fmt.Println("=========================================")
}

func syncSingleWorkspace(resolver *Resolver) {
	worktrees, err := resolver.DiscoverWorktrees()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error discovering repos: %v\n", err)
		return
	}

	if len(worktrees) == 0 {
		fmt.Printf("No repos found in workspace %s\n", resolver.WorkspaceName())
		return
	}

	// Phase 1: Push (unless pull-only)
	if !syncPullOnly {
		fmt.Println("")
		fmt.Println("--- Phase 1: Push ---")
		pushWorkspaceWorktrees(worktrees, "", "")
	}

	// Phase 2: Pull (unless push-only)
	if !syncPushOnly {
		fmt.Println("")
		fmt.Println("--- Phase 2: Pull ---")
		pullWorkspaceWorktrees(worktrees, "")
	}
}

func runLegacySync() {
	fmt.Println("=========================================")
	fmt.Println("Full Sync: All Worktrees")
	fmt.Println("=========================================")
	fmt.Println("")

	worktrees, err := DiscoverWorktrees()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error discovering worktrees: %v\n", err)
		os.Exit(1)
	}

	if len(worktrees) == 0 {
		fmt.Println("No worktrees found.")
		return
	}

	// Get the default branch
	defaultBranch := GetDefaultBranch()

	// Phase 1: Push all worktrees (unless pull-only)
	if !syncPullOnly {
		fmt.Println("--- Phase 1: Push ---")
		fmt.Printf("Pushing all worktrees -> %s\n", defaultBranch)
		fmt.Println("")

		for _, wt := range worktrees {
			pushBranch(wt.Branch, defaultBranch)
			fmt.Println("")
		}
	}

	// Phase 2: Pull into all worktrees (unless push-only)
	if !syncPushOnly {
		fmt.Println("--- Phase 2: Pull ---")
		fmt.Printf("Pulling %s -> all worktrees\n", defaultBranch)
		fmt.Println("")

		for _, wt := range worktrees {
			pullWorktree(wt.Name, defaultBranch)
			fmt.Println("")
		}
	}

	fmt.Println("=========================================")
	fmt.Println("Full sync complete!")
	fmt.Println("=========================================")
}
