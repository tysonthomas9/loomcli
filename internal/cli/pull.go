package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var pullAll bool
var pullWorkspace string

var pullCmd = &cobra.Command{
	Use:               "pull [worktree] [branch]",
	Short:             "Pull branch into worktree",
	GroupID:           "git",
	ValidArgsFunction: worktreeThenBranchCompletion,
	Long: `Pull latest changes from a branch into worktree(s).

Merges the source branch (e.g., main) INTO the worktree branch, updating
the worktree with the latest changes. If conflicts occur, Claude
is launched to resolve them.

Arguments:
  worktree    Worktree name (e.g., falcon)
  branch      Source branch to pull from (default: main or per-repo default)

Flags:
  -a, --all          Pull into all worktrees
  -W, --workspace    Workspace to operate on (workspace mode only)

Examples:
  loom pull falcon                        # Pull main (or per-repo default) into falcon
  loom pull falcon main                   # Pull main into falcon explicitly
  loom pull --all                         # Pull into all worktrees from their defaults
  loom pull --all main                    # Pull main into all worktrees
  loom pull -W myworkspace falcon         # Pull in specific workspace`,
	Args: func(cmd *cobra.Command, args []string) error {
		if IsWorkspaceMode() {
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
		}
		// Legacy mode
		if pullAll {
			if len(args) != 1 {
				return fmt.Errorf("--all flag requires exactly 1 argument (source branch)")
			}
			return nil
		}
		if len(args) != 2 {
			return fmt.Errorf("requires exactly 2 arguments: <worktree> <branch>")
		}
		return nil
	},
	Run: runPull,
}

func init() {
	pullCmd.Flags().BoolVarP(&pullAll, "all", "a", false, "Pull into all worktrees")
	pullCmd.Flags().StringVarP(&pullWorkspace, "workspace", "W", "", "Workspace to operate on")
	rootCmd.AddCommand(pullCmd)
}

func runPull(cmd *cobra.Command, args []string) {
	resolver := GetDeps(cmd).ResolverOrDefault()

	if IsWorkspaceMode() {
		if pullAll && pullWorkspace != "" {
			fmt.Fprintln(os.Stderr, "Error: --all and --workspace are mutually exclusive")
			os.Exit(1)
		}

		sourceBranch := ""
		worktreeName := ""

		if pullAll {
			if len(args) == 1 {
				sourceBranch = args[0]
			}
			pullAllWorkspaces(resolver, sourceBranch)
		} else {
			worktreeName = args[0]
			if len(args) == 2 {
				sourceBranch = args[1]
			}

			wsName := pullWorkspace
			if wsName != "" {
				if err := resolver.SetWorkspace(wsName); err != nil {
					available := resolver.WorkspaceNames()
					fmt.Fprintf(os.Stderr, "Error: workspace %q not found. Available: %v\n", wsName, available)
					os.Exit(1)
				}
			}

			pullWorkspaceRepo(resolver, worktreeName, sourceBranch)
		}
		return
	}

	// Legacy mode
	if pullAll {
		pullAllWorktrees(resolver, args[0])
	} else {
		pullWorktree(args[0], args[1])
	}
}

func pullAllWorkspaces(resolver *Resolver, sourceBranch string) {
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

		pullWorkspaceWorktrees(worktrees, sourceBranch)
		fmt.Println("")
	}

	fmt.Println("=========================================")
	fmt.Println("All workspaces pulled!")
	fmt.Println("=========================================")
}

func pullWorkspaceRepo(resolver *Resolver, worktreeName, sourceBranch string) {
	// Resolve the specific worktree path
	worktreePath, err := resolver.ResolveWorktreePath(worktreeName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving worktree: %v\n", err)
		os.Exit(1)
	}

	// Find the matching WorktreeInfo to get Repo config
	worktrees, err := resolver.DiscoverWorktrees()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error discovering repos: %v\n", err)
		os.Exit(1)
	}

	var matched *WorktreeInfo
	for i, wt := range worktrees {
		if wt.Path == worktreePath || wt.Name == worktreeName {
			matched = &worktrees[i]
			break
		}
	}

	if matched == nil || matched.Repo == nil {
		// Fall back to basic pull without repo config
		source := sourceBranch
		if source == "" {
			source = "main"
		}
		pullWorktree(worktreeName, source)
		return
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

	err = pullRepoWorktree(matched.Path, matched.Branch, source, remote)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func pullWorkspaceWorktrees(worktrees []WorktreeInfo, sourceBranch string) {
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

		err := pullRepoWorktree(wt.Path, wt.Branch, source, remote)
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

func pullRepoWorktree(repoPath, currentBranch, sourceBranch, remote string) error {
	r := resolveRemote(remote)

	fmt.Println("=========================================")
	fmt.Printf("Pull: %s <- %s (repo: %s, remote: %s)\n", currentBranch, sourceBranch, repoPath, r)
	fmt.Println("=========================================")

	// Fetch latest
	if err := GitFetchRemote(repoPath, remote); err != nil {
		return fmt.Errorf("fetching: %v", err)
	}

	// Attempt merge
	mergeMsg := fmt.Sprintf("Pull from %s\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", sourceBranch)
	if err := GitMergeRemote(repoPath, remote, sourceBranch, mergeMsg); err != nil {
		// Check for conflicts
		conflicts, conflictErr := GetConflictedFiles(repoPath)
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
		if err := InvokeAgentForConflicts(repoPath, sourceBranch, currentBranch, conflicts); err != nil {
			return fmt.Errorf("resolving conflicts: %v", err)
		}
		return nil
	}

	fmt.Println("✓ Pull completed successfully (no conflicts)")

	// Push
	if err := GitPushRemote(repoPath, remote, currentBranch); err != nil {
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

func pullAllWorktrees(resolver *Resolver, sourceBranch string) {
	fmt.Println("=========================================")
	fmt.Printf("Pulling all worktrees <- %s\n", sourceBranch)
	fmt.Println("=========================================")
	fmt.Println("")

	worktrees, err := resolver.DiscoverWorktrees()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error discovering worktrees: %v\n", err)
		os.Exit(1)
	}

	if len(worktrees) == 0 {
		fmt.Println("No worktrees found.")
		return
	}

	// Pull into each worktree
	for _, wt := range worktrees {
		pullWorktree(wt.Name, sourceBranch)
		fmt.Println("")
	}

	fmt.Println("=========================================")
	fmt.Println("All worktrees pulled!")
	fmt.Println("=========================================")
}

func pullWorktree(worktreeName, sourceBranch string) {
	worktreePath, err := ResolveWorktreePath(worktreeName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return
	}

	fmt.Println("=========================================")
	fmt.Printf("Pulling: %s <- %s\n", worktreeName, sourceBranch)
	fmt.Println("=========================================")

	// Get current branch
	currentBranch, err := GetCurrentBranch(worktreePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting current branch: %v\n", err)
		return
	}

	// Fetch latest
	if err := GitFetch(worktreePath); err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching: %v\n", err)
		return
	}

	// Attempt merge
	mergeMsg := fmt.Sprintf("Pull from %s\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", sourceBranch)
	if err := GitMergeOrigin(worktreePath, sourceBranch, mergeMsg); err != nil {
		// Check for conflicts
		conflicts, conflictErr := GetConflictedFiles(worktreePath)
		if conflictErr != nil || len(conflicts) == 0 {
			fmt.Fprintf(os.Stderr, "Pull failed: %v\n", err)
			return
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
		if err := InvokeAgentForConflicts(worktreePath, sourceBranch, currentBranch, conflicts); err != nil {
			fmt.Fprintf(os.Stderr, "Error resolving conflicts: %v\n", err)
			return
		}
		return
	}

	fmt.Println("✓ Pull completed successfully (no conflicts)")

	// Push
	if err := GitPush(worktreePath, currentBranch); err != nil {
		fmt.Fprintf(os.Stderr, "Error pushing: %v\n", err)
		return
	}

	fmt.Printf("✓ Pushed to origin/%s\n", currentBranch)
}
