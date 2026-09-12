package git

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

var pullAll bool
var pullWorkspace string
var pullNoPush bool

var pullCmd = &cobra.Command{
	Use:               "pull [worktree] [branch]",
	Short:             "Pull branch into worktree",
	GroupID:           "git",
	ValidArgsFunction: cli.WorktreeThenBranchCompletion,
	Long: `Pull latest changes from a branch into worktree(s).

Merges the source branch (e.g., main) INTO the worktree branch, updating
the worktree with the latest changes. If conflicts occur, Claude
is launched to resolve them.

After a clean merge the worktree branch is pushed to its remote, so the
remote copy of the branch stays current. Pass --no-push to merge without
publishing anything.

Arguments:
  worktree    Worktree name (e.g., falcon)
  branch      Source branch to pull from (default: main or per-repo default)

Flags:
  -a, --all          Pull into all worktrees
  -W, --workspace    Workspace to operate on
      --no-push      Do not push the merge result back to the remote

Examples:
  loom pull falcon                        # Pull main (or per-repo default) into falcon
  loom pull falcon main                   # Pull main into falcon explicitly
  loom pull --all                         # Pull into all worktrees from their defaults
  loom pull --all main                    # Pull main into all worktrees
  loom pull --all --no-push               # Merge in latest without publishing
  loom pull -W myworkspace falcon         # Pull in specific workspace`,
	Args: func(cmd *cobra.Command, args []string) error {
		if pullAll {
			if len(args) > 1 {
				return fmt.Errorf("--all flag accepts at most 1 argument (source branch)")
			}
			return nil
		}
		if len(args) < 1 || len(args) > 2 {
			return fmt.Errorf("requires 1-2 arguments: <worktree> [branch]")
		}
		return nil
	},
	RunE: runPull,
}

func init() {
	pullCmd.Flags().BoolVarP(&pullAll, "all", "a", false, "Pull into all worktrees")
	pullCmd.Flags().StringVarP(&pullWorkspace, "workspace", "W", "", "Workspace to operate on")
	pullCmd.Flags().BoolVar(&pullNoPush, "no-push", false, "Do not push the merge result back to the remote")
	cli.RegisterCommand(pullCmd)
}

func runPull(cmd *cobra.Command, args []string) error {
	deps := cli.GetDeps(cmd)
	all, _ := cmd.Flags().GetBool("all")
	ws, _ := cmd.Flags().GetString("workspace")
	noPush, _ := cmd.Flags().GetBool("no-push")

	if all && ws != "" {
		fmt.Fprintln(os.Stderr, "Error: --all and --workspace are mutually exclusive")
		os.Exit(1)
	}

	sourceBranch := ""
	worktreeName := ""

	if all {
		if len(args) == 1 {
			sourceBranch = args[0]
		}
		pullAllWorkspaces(deps, sourceBranch, !noPush)
		return nil
	}

	worktreeName = args[0]
	if len(args) == 2 {
		sourceBranch = args[1]
	}

	resolver, err := cli.NewResolver()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating resolver: %v\n", err)
		os.Exit(1)
	}

	if ws != "" {
		if err := resolver.SetWorkspace(ws); err != nil {
			available := resolver.WorkspaceNames()
			fmt.Fprintf(os.Stderr, "Error: workspace %q not found. Available: %v\n", ws, available)
			os.Exit(1)
		}
	}

	pullWorkspaceRepo(deps, resolver, worktreeName, sourceBranch, !noPush)
	return nil
}

func pullAllWorkspaces(deps *cli.Deps, sourceBranch string, pushAfterPull bool) {
	resolver, err := cli.NewResolver()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating resolver: %v\n", err)
		os.Exit(1)
	}

	wsNames := resolver.WorkspaceNames()
	if len(wsNames) == 0 {
		fmt.Println("No workspaces found.")
		return
	}

	fmt.Println("=========================================")
	fmt.Printf("Pulling all workspaces <- %s\n", sourceBranchDisplay(sourceBranch))
	fmt.Println("=========================================")
	fmt.Println("")

	for _, wsName := range wsNames {
		fmt.Printf("--- Workspace: %s ---\n", wsName)
		if err := resolver.SetWorkspace(wsName); err != nil {
			fmt.Fprintf(os.Stderr, "Error setting workspace %s: %v\n", wsName, err)
			continue
		}

		worktrees, err := resolver.DiscoverWorktrees()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error discovering repos in workspace %s: %v\n", wsName, err)
			continue
		}

		if len(worktrees) == 0 {
			fmt.Printf("No repos found in workspace %s\n", wsName)
			continue
		}

		pullWorkspaceWorktrees(deps, worktrees, sourceBranch, pushAfterPull)
		fmt.Println("")
	}

	fmt.Println("=========================================")
	fmt.Println("All workspaces pulled!")
	fmt.Println("=========================================")
}

func pullWorkspaceRepo(deps *cli.Deps, resolver *cli.Resolver, worktreeName, sourceBranch string, pushAfterPull bool) {
	// Resolve the specific worktree path
	worktreePath, err := resolver.ResolveWorktreePath(worktreeName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving worktree: %v\n", err)
		os.Exit(1)
	}

	// Find the matching cli.WorktreeInfo to get Repo config
	worktrees, err := resolver.DiscoverWorktrees()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error discovering repos: %v\n", err)
		os.Exit(1)
	}

	var matched *cli.WorktreeInfo
	for i, wt := range worktrees {
		if wt.Path == worktreePath || wt.Name == worktreeName {
			matched = &worktrees[i]
			break
		}
	}

	if matched == nil || matched.Repo == nil {
		fmt.Fprintf(os.Stderr, "Error: repo %q is missing FleetDB repo metadata in workspace %q\n", worktreeName, resolver.WorkspaceName())
		os.Exit(1)
	}

	source := sourceBranch
	if source == "" {
		source = matched.Repo.DefaultBranch
		if source == "" {
			source = "main"
		}
	}

	remote := matched.Repo.Remote

	fmt.Println("=========================================")
	fmt.Printf("Pulling workspace %q repo %q <- %s\n", resolver.WorkspaceName(), worktreeName, source)
	fmt.Println("=========================================")
	fmt.Println("")

	err = pullRepoWorktree(deps, matched.Path, matched.Branch, source, remote, pushAfterPull)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func pullWorkspaceWorktrees(deps *cli.Deps, worktrees []cli.WorktreeInfo, sourceBranch string, pushAfterPull bool) {
	type result struct {
		repo    string
		success bool
		err     string
	}
	var results []result

	for _, wt := range worktrees {
		if wt.Repo == nil {
			continue
		}

		source := sourceBranch
		if source == "" {
			source = wt.Repo.DefaultBranch
			if source == "" {
				source = "main"
			}
		}

		remote := wt.Repo.Remote

		err := pullRepoWorktree(deps, wt.Path, wt.Branch, source, remote, pushAfterPull)
		if err != nil {
			results = append(results, result{repo: wt.Name, success: false, err: err.Error()})
		} else {
			results = append(results, result{repo: wt.Name, success: true})
		}
		fmt.Println("")
	}

	// Print summary
	fmt.Println("--- Summary ---")
	for _, r := range results {
		if r.success {
			fmt.Printf("  ✓ %s\n", r.repo)
		} else {
			fmt.Printf("  ✗ %s: %s\n", r.repo, r.err)
		}
	}
}

// pullRepoWorktree merges sourceBranch into currentBranch and, when
// pushAfterPull is true, publishes the merge result by pushing currentBranch to
// the remote.
//
// With pushAfterPull=false the function performs NO remote-writing operation:
// it fetches and merges only. That is what `loom sync --pull-only` needs — the
// flag promises that nothing is published, and this push is the one that used
// to break that promise by publishing every worktree's current branch.
func pullRepoWorktree(deps *cli.Deps, repoPath, currentBranch, sourceBranch, remote string, pushAfterPull bool) error {
	r := resolveRemote(remote)

	fmt.Println("=========================================")
	fmt.Printf("Pull: %s <- %s (repo: %s, remote: %s)\n", currentBranch, sourceBranch, repoPath, r)
	fmt.Println("=========================================")

	// Fetch latest
	if err := gitFetchRemote(deps, repoPath, remote); err != nil {
		return fmt.Errorf("fetching: %v", err)
	}

	// Attempt merge
	mergeMsg := fmt.Sprintf("Pull from %s\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", sourceBranch)
	if err := gitMergeRemote(deps, repoPath, remote, sourceBranch, mergeMsg); err != nil {
		// Check for conflicts
		conflicts, conflictErr := getConflictedFilesDeps(deps, repoPath)
		if conflictErr != nil || len(conflicts) == 0 {
			return fmt.Errorf("merge failed: %v", err)
		}

		fmt.Println("")
		fmt.Println("⚠ Merge conflicts detected. Launching AI agent to resolve...")
		fmt.Println("")
		fmt.Println("Conflicted files:")
		for _, f := range conflicts {
			fmt.Printf("  - %s\n", f)
		}
		fmt.Println("")

		// Launch Claude for conflict resolution
		if err := invokeAgentForConflictsDeps(deps, repoPath, sourceBranch, currentBranch, conflicts); err != nil {
			return fmt.Errorf("resolving conflicts: %v", err)
		}
		return nil
	}

	fmt.Println("✓ Pull completed successfully (no conflicts)")

	if !pushAfterPull {
		return nil
	}

	// Push
	if err := gitPushRemote(deps, repoPath, remote, currentBranch); err != nil {
		return fmt.Errorf("pushing: %v", err)
	}

	fmt.Printf("✓ Pushed to %s/%s\n", r, currentBranch)
	return nil
}

func sourceBranchDisplay(source string) string {
	if source == "" {
		return "(per-repo default)"
	}
	return source
}
