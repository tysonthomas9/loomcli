package git

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

var pullAll bool
var pullWorkspace string

var pullCmd = &cobra.Command{
	Use:               "pull [worktree] [branch]",
	Short:             "Pull branch into worktree",
	GroupID:           "git",
	ValidArgsFunction: cli.WorktreeThenBranchCompletion,
	Long: `Pull latest changes from a branch into worktree(s).

Merges the source branch (e.g., main) INTO the worktree branch, updating
the worktree with the latest changes. If conflicts occur, Claude
is launched to resolve them.

Arguments:
  worktree    Worktree name (e.g., falcon)
  branch      Source branch to pull from (default: main or per-repo default)

Flags:
  -a, --all          Pull into all worktrees
  -W, --workspace    Workspace to operate on

Examples:
  loom pull falcon                        # Pull main (or per-repo default) into falcon
  loom pull falcon main                   # Pull main into falcon explicitly
  loom pull --all                         # Pull into all worktrees from their defaults
  loom pull --all main                    # Pull main into all worktrees
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
	cli.RegisterCommand(pullCmd)
}

func runPull(cmd *cobra.Command, args []string) error {
	deps := cli.GetDeps(cmd)
	all, _ := cmd.Flags().GetBool("all")
	ws, _ := cmd.Flags().GetString("workspace")

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
		return pullAllWorkspaces(deps, sourceBranch)
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

	pullWorkspaceRepo(deps, resolver, worktreeName, sourceBranch)
	return nil
}

// pullAllWorkspaces returns a non-nil error when any workspace ended with a
// repo that is not in sync. The per-repo lines already say so; the error is
// what carries that into the exit code, so a script cannot read "All
// workspaces pulled!" over a repo that is still commits behind.
func pullAllWorkspaces(deps *cli.Deps, sourceBranch string) error {
	resolver, err := cli.NewResolver()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating resolver: %v\n", err)
		os.Exit(1)
	}

	wsNames := resolver.WorkspaceNames()
	if len(wsNames) == 0 {
		fmt.Println("No workspaces found.")
		return nil
	}

	fmt.Println("=========================================")
	fmt.Printf("Pulling all workspaces <- %s\n", sourceBranchDisplay(sourceBranch))
	fmt.Println("=========================================")
	fmt.Println("")

	notInSync := 0
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

		notInSync += summaryFailures(pullWorkspaceWorktrees(deps, worktrees, sourceBranch))
		fmt.Println("")
	}

	fmt.Println("=========================================")
	if notInSync > 0 {
		fmt.Fprintf(os.Stderr, "%d repo(s) are not in sync after the pull\n", notInSync)
		fmt.Println("=========================================")
		return fmt.Errorf("%d repo(s) are not in sync after the pull", notInSync)
	}
	fmt.Println("All workspaces pulled!")
	fmt.Println("=========================================")
	return nil
}

func pullWorkspaceRepo(deps *cli.Deps, resolver *cli.Resolver, worktreeName, sourceBranch string) {
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

	if _, err := pullRepoWorktree(deps, matched.Path, matched.Branch, source, remote); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func pullWorkspaceWorktrees(deps *cli.Deps, worktrees []cli.WorktreeInfo, sourceBranch string) []pullOutcome {
	return pullWorkspaceWorktreesWithCoverage(deps, worktrees, sourceBranch, nil)
}

// pullWorkspaceWorktreesWithCoverage pulls every repo checkout and renders the
// summary from what git reported afterwards. notCovered names worktrees the run
// never visited, so the summary cannot imply a coverage it does not have.
func pullWorkspaceWorktreesWithCoverage(deps *cli.Deps, worktrees []cli.WorktreeInfo, sourceBranch string, notCovered []string) []pullOutcome {
	var outcomes []pullOutcome

	for _, wt := range worktrees {
		if wt.Repo == nil {
			// A worktree the command declined to touch must still be visible:
			// silently continuing here is how a repo vanished from the summary.
			outcomes = append(outcomes, pullOutcome{
				Name:   wt.Name,
				Path:   wt.Path,
				State:  syncStateSkipped,
				Detail: "no repo metadata in workspace config",
			})
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

		outcome, _ := pullRepoWorktree(deps, wt.Path, wt.Branch, source, remote)
		outcome.Name = wt.Name
		outcomes = append(outcomes, outcome)
		fmt.Println("")
	}

	printPullSummary(outcomes, notCovered)
	return outcomes
}

// pullRepoWorktree pulls sourceBranch into the worktree at repoPath and returns
// what actually happened there.
//
// Contract: the returned outcome always reflects a state read back from git
// after the last mutation. A nil error alone is not evidence the worktree is in
// sync — only outcome.InSync() is. The error is non-nil whenever a step failed
// AND whenever verification found the worktree still behind or mid-merge, so
// the single-worktree callers that exit on error stay honest too.
func pullRepoWorktree(deps *cli.Deps, repoPath, currentBranch, sourceBranch, remote string) (pullOutcome, error) {
	r := resolveRemote(remote)

	o := pullOutcome{
		Name:   filepath.Base(repoPath),
		Path:   repoPath,
		Branch: currentBranch,
		Source: sourceBranch,
		Remote: remote,
	}

	fmt.Println("=========================================")
	fmt.Printf("Pull: %s <- %s (repo: %s, remote: %s)\n", currentBranch, sourceBranch, repoPath, r)
	fmt.Println("=========================================")

	// Read HEAD before the merge so the summary can say what moved. A failure
	// here is not fatal: the verdict comes from the behind-count, not from this.
	if head, err := gitRevParseDeps(deps, repoPath, "HEAD"); err == nil {
		o.HeadBefore = head
	}

	// Fetch latest
	if err := gitFetchRemote(deps, repoPath, remote); err != nil {
		err = fmt.Errorf("fetching: %v", err)
		return o.failed(err), err
	}

	// Attempt merge
	mergeMsg := fmt.Sprintf("Pull from %s\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", sourceBranch)
	if err := gitMergeRemote(deps, repoPath, remote, sourceBranch, mergeMsg); err != nil {
		return resolveMergeConflicts(deps, o, currentBranch, err)
	}

	// Push
	if err := gitPushRemote(deps, repoPath, remote, currentBranch); err != nil {
		err = fmt.Errorf("pushing: %v", err)
		return o.failed(err), err
	}

	fmt.Printf("✓ Pushed to %s/%s\n", r, currentBranch)
	return finishPull(deps, o)
}

// resolveMergeConflicts handles a merge that did not complete: it launches the
// conflict agent when there are conflicts to resolve, then measures the
// worktree. Launching the agent is not evidence the merge finished, so this
// path verifies like every other one — returning nil here is what produced the
// reported false ✓.
func resolveMergeConflicts(deps *cli.Deps, o pullOutcome, currentBranch string, mergeErr error) (pullOutcome, error) {
	conflicts, conflictErr := getConflictedFilesDeps(deps, o.Path)
	if conflictErr != nil || len(conflicts) == 0 {
		err := fmt.Errorf("merge failed: %v", mergeErr)
		return o.failed(err), err
	}

	fmt.Println("")
	fmt.Println("⚠ Merge conflicts detected. Launching AI agent to resolve...")
	fmt.Println("")
	fmt.Println("Conflicted files:")
	for _, f := range conflicts {
		fmt.Printf("  - %s\n", f)
	}
	fmt.Println("")

	if err := invokeAgentForConflictsDeps(deps, o.Path, o.Source, currentBranch, conflicts); err != nil {
		err = fmt.Errorf("resolving conflicts: %v", err)
		return o.failed(err), err
	}
	return finishPull(deps, o)
}

// finishPull verifies the worktree and prints the measured per-repo line. The
// running commentary must not claim more than the summary does.
func finishPull(deps *cli.Deps, o pullOutcome) (pullOutcome, error) {
	verifyPulled(deps, &o)
	fmt.Printf("%s %s\n", o.marker(), o.summaryDetail())

	if o.State == syncStateBehind || o.State == syncStateUnresolved {
		return o, fmt.Errorf("%s: %s", o.Name, o.summaryDetail())
	}
	return o, nil
}

func sourceBranchDisplay(source string) string {
	if source == "" {
		return "(per-repo default)"
	}
	return source
}
