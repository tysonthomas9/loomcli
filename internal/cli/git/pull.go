package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/loomgit/pull"
)

var pullLocal = pull.PullLocal
var restackLocal = pull.RestackLocal

var pullAll bool
var pullWorkspace string

var pullCmd = &cobra.Command{
	Use:               "pull [worktree] [branch]",
	Short:             "Restack working area onto its recorded trunk",
	GroupID:           "git",
	ValidArgsFunction: cli.WorktreeThenBranchCompletion,
	Long: `Pull latest changes from a branch into worktree(s).

Fetches the recorded trunk and replays each unlanded layer onto it.
Conflicts and overlapping uncommitted edits hold the swap for review.
Pull never pushes or starts an agent.

Arguments:
  worktree    Worktree name (e.g., falcon)
  branch      Optional guard; must equal the recorded trunk

Flags:
  -a, --all          Pull into all worktrees
  -W, --workspace    Workspace to operate on

Examples:
  loom pull falcon                        # Restack falcon onto its recorded trunk
  loom pull falcon main                   # Require main to be its recorded trunk
  loom pull --all                         # Restack all working areas
  loom pull --all main                    # Require main for every working area
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
	restackCmd.Flags().StringP("workspace", "W", "", "Workspace to operate on")
	cli.RegisterCommand(restackCmd)
}

var restackCmd = &cobra.Command{
	Use:     "restack <working-area> <base-sha> [change-id ...]",
	Short:   "Restack a working area's layers onto an explicit commit",
	GroupID: "git",
	Args:    cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		resolver, err := cli.NewResolver()
		if err != nil {
			return err
		}
		workspace, _ := cmd.Flags().GetString("workspace")
		if workspace != "" {
			if err := resolver.SetWorkspace(workspace); err != nil {
				return err
			}
		}
		path, err := resolver.ResolveWorktreePath(args[0])
		if err != nil {
			return err
		}
		return restackRepoWorktree(cmd.Context(), path, args[1], args[2:])
	},
}

func restackRepoWorktree(ctx context.Context, path, baseSHA string, order []string) error {
	result, err := restackLocal(ctx, path, baseSHA, order, uuid.NewString())
	if err != nil {
		if len(result.Paths) > 0 {
			return fmt.Errorf("%w: %s", err, strings.Join(result.Paths, ", "))
		}
		return err
	}
	fmt.Printf("Restacked working area at %s\n", result.HeadSHA)
	return nil
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

	var failures []error
	for _, wsName := range wsNames {
		if err := pullOneWorkspace(deps, resolver, wsName, sourceBranch); err != nil {
			failures = append(failures, err)
		}
	}

	fmt.Println("=========================================")
	if len(failures) == 0 {
		fmt.Println("All workspaces restacked.")
	} else {
		fmt.Printf("Pull failed in %d workspace(s).\n", len(failures))
	}
	fmt.Println("=========================================")
	return errors.Join(failures...)
}

func pullOneWorkspace(deps *cli.Deps, resolver *cli.Resolver, name, sourceBranch string) error {
	fmt.Printf("--- Workspace: %s ---\n", name)
	if err := resolver.SetWorkspace(name); err != nil {
		return fmt.Errorf("setting workspace %s: %w", name, err)
	}
	worktrees, err := resolver.DiscoverWorktrees()
	if err != nil {
		return fmt.Errorf("discovering repos in workspace %s: %w", name, err)
	}
	if len(worktrees) == 0 {
		fmt.Printf("No repos found in workspace %s\n", name)
		return nil
	}
	err = pullWorkspaceWorktrees(deps, worktrees, sourceBranch)
	fmt.Println("")
	return err
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
		source = "(recorded trunk)"
	}

	remote := matched.Repo.Remote

	fmt.Println("=========================================")
	fmt.Printf("Pulling workspace %q repo %q <- %s\n", resolver.WorkspaceName(), worktreeName, source)
	fmt.Println("=========================================")
	fmt.Println("")

	err = pullRepoWorktree(deps, matched.Path, matched.Branch, sourceBranch, remote)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func pullWorkspaceWorktrees(deps *cli.Deps, worktrees []cli.WorktreeInfo, sourceBranch string) error {
	type result struct {
		repo    string
		success bool
		err     string
	}
	var results []result
	var failures []error

	for _, wt := range worktrees {
		if wt.Repo == nil {
			continue
		}

		remote := wt.Repo.Remote

		err := pullRepoWorktree(deps, wt.Path, wt.Branch, sourceBranch, remote)
		if err != nil {
			results = append(results, result{repo: wt.Name, success: false, err: err.Error()})
			failures = append(failures, fmt.Errorf("%s: %w", wt.Name, err))
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
	return errors.Join(failures...)
}

func pullRepoWorktree(_ *cli.Deps, repoPath, _, sourceBranch, remote string) error {
	result, err := pullLocal(context.Background(), repoPath, remote, sourceBranch, uuid.NewString())
	if err != nil {
		if len(result.Paths) > 0 {
			return fmt.Errorf("%w: %s", err, strings.Join(result.Paths, ", "))
		}
		return err
	}
	fmt.Printf("Restacked working area at %s\n", result.HeadSHA)
	return nil
}

func sourceBranchDisplay(source string) string {
	if source == "" {
		return "(recorded trunk)"
	}
	return source
}
