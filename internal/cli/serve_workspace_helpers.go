package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/workspaceerrors"
)

// createRepoWorktrees creates git worktrees for each resolved repo. Returns
// the repo configs and a cleanup function to remove worktrees on error.
func createRepoWorktrees(ctx context.Context, wsDir string, resolved []resolvedRepo, branch string) ([]RepoConfig, func(), error) {
	type wt struct{ origRepo, wtPath string }
	var created []wt
	var repos []RepoConfig
	cleanup := func() {
		for _, c := range created {
			_, _ = RunGitCommand(c.origRepo, "worktree", "remove", c.wtPath)
		}
		_ = os.RemoveAll(wsDir)
	}
	for _, repo := range resolved {
		if ctx.Err() != nil {
			return nil, cleanup, ctx.Err()
		}
		wtPath := filepath.Join(wsDir, repo.name)
		if _, err := RunGitCommand(repo.path, "worktree", "add", wtPath, "-b", branch); err != nil {
			return nil, cleanup, workspaceerrors.New(workspaceerrors.GitFailed,
				fmt.Sprintf("git worktree add failed for %s", repo.name), err)
		}
		created = append(created, wt{repo.path, wtPath})
		repos = append(repos, RepoConfig{Name: repo.name, Path: wtPath})
	}
	return repos, cleanup, nil
}

// cloneRepos clones each URL into wsDir, deduplicating names.
func cloneRepos(ctx context.Context, wsDir string, cloneURLs []string) ([]RepoConfig, error) {
	var repos []RepoConfig
	seenNames := make(map[string]bool)
	for _, cloneURL := range cloneURLs {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		repoName := repoNameFromURL(cloneURL)
		if seenNames[repoName] {
			for i := 2; ; i++ {
				candidate := fmt.Sprintf("%s-%d", repoName, i)
				if !seenNames[candidate] {
					repoName = candidate
					break
				}
			}
		}
		seenNames[repoName] = true
		clonePath := filepath.Join(wsDir, repoName)
		cmd := exec.CommandContext(ctx, "git", "clone", cloneURL, clonePath) //nolint:gosec // URL validated
		if output, err := cmd.CombinedOutput(); err != nil {
			return nil, workspaceerrors.New(workspaceerrors.GitFailed,
				fmt.Sprintf("git clone failed for %s: %s", cloneURL, strings.TrimSpace(string(output))), err)
		}
		repos = append(repos, RepoConfig{Name: repoName, Path: clonePath})
	}
	return repos, nil
}

// repoNameFromURL derives a directory name from a git clone URL.
// e.g. "https://github.com/foo/bar.git" -> "bar"
func repoNameFromURL(cloneURL string) string {
	u := strings.TrimSuffix(cloneURL, ".git")
	u = strings.TrimRight(u, "/")
	if idx := strings.LastIndex(u, "/"); idx >= 0 {
		u = u[idx+1:]
	}
	if idx := strings.LastIndex(u, ":"); idx >= 0 {
		u = u[idx+1:]
	}
	if u == "" {
		return "repo"
	}
	return u
}
