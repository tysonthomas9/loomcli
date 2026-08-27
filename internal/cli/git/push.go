package git

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

var pushRepo string
var pushRemote string

var pushCmd = &cobra.Command{
	Use:               "push <worktree>",
	Short:             "Publish a worktree feature branch",
	GroupID:           "git",
	ValidArgsFunction: cli.BranchCompletion,
	Long: `Push exactly one worktree's current feature branch to the same named
branch on its Git remote. This command never checks out, merges, stashes, or
force-pushes. Use ` + "`loom pr`" + ` to open a pull request and ` + "`loom merge`" + `
to merge it through GitHub.`,
	Args: cobra.ExactArgs(1),
	RunE: runPush,
}

func init() {
	pushCmd.Flags().StringVar(&pushRepo, "repo", "", "Repository name (required when the worktree is ambiguous)")
	pushCmd.Flags().StringVar(&pushRemote, "remote", "", "Git remote (required when the repository has multiple remotes)")
	cli.RegisterCommand(pushCmd)
}

func runPush(cmd *cobra.Command, args []string) error {
	deps := cli.GetDeps(cmd)
	resolver, err := cli.NewResolver()
	if err != nil {
		return fmt.Errorf("creating resolver: %w", err)
	}
	worktrees, err := resolver.DiscoverWorktrees()
	if err != nil {
		return fmt.Errorf("discovering worktrees: %w", err)
	}
	wt, err := resolveFeatureWorktree(worktrees, args[0], pushRepo)
	if err != nil {
		return err
	}
	if wt.Repo == nil {
		return errors.New("feature publication requires a repository-backed worktree")
	}
	branch := strings.TrimSpace(wt.Branch)
	if branch == "" {
		return errors.New("cannot publish a detached worktree")
	}
	if branch == cli.DefaultBranchForWorktree(wt) {
		return fmt.Errorf("refusing to publish default branch %q", branch)
	}
	remote, err := resolveFeatureRemote(deps, wt)
	if err != nil {
		return err
	}
	if remote == "" {
		return errors.New("no Git remote configured; specify --remote")
	}
	if err := gitPushRemote(deps, wt.Path, remote, branch); err != nil {
		return fmt.Errorf("publishing %s to %s: %w", branch, remote, err)
	}
	fmt.Printf("Published %s to %s/%s\n", branch, remote, branch)
	return nil
}

func resolveFeatureWorktree(worktrees []cli.WorktreeInfo, name, repo string) (cli.WorktreeInfo, error) {
	var matches []cli.WorktreeInfo
	for _, wt := range worktrees {
		if wt.Name == name && (repo == "" || (wt.Repo != nil && wt.Repo.Name == repo)) {
			matches = append(matches, wt)
		}
	}
	if len(matches) == 0 {
		return cli.WorktreeInfo{}, fmt.Errorf("worktree %q not found", name)
	}
	if len(matches) > 1 {
		return cli.WorktreeInfo{}, fmt.Errorf("worktree %q is ambiguous; specify --repo", name)
	}
	return matches[0], nil
}

func resolveFeatureRemote(deps *cli.Deps, wt cli.WorktreeInfo) (string, error) {
	remote := strings.TrimSpace(pushRemote)
	if remote == "" && wt.Repo != nil {
		remote = strings.TrimSpace(wt.Repo.Remote)
	}
	if remote != "" {
		return remote, nil
	}
	out, err := cli.RunGit(deps, wt.Path, "remote")
	if err != nil {
		return "", fmt.Errorf("resolving remotes: %w", err)
	}
	for _, candidate := range strings.Fields(out) {
		if remote != "" {
			return "", errors.New("repository has multiple remotes; specify --remote")
		}
		remote = candidate
	}
	return remote, nil
}
