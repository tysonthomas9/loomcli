// Package localworkspace contains machine-local workspace filesystem helpers.
package localworkspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

// Repo is the local filesystem view of a workspace repository.
type Repo struct {
	Name   string
	Path   string
	Groups []string
}

// RepoPath returns the best-known local path for a repo in a workspace.
func RepoPath(local bootstrap.WorkspaceLocalState, repoName string) string {
	if local.Repos != nil && local.Repos[repoName] != "" {
		return local.Repos[repoName]
	}
	if local.Path != "" {
		return filepath.Join(local.Path, repoName)
	}
	return ""
}

// AgentWorktreePath returns the canonical local worktree path for an agent.
func AgentWorktreePath(workspacePath, repoName, agentName string) string {
	return filepath.Join(workspacePath, "worktrees", repoName, agentName)
}

// RepoCheckoutPath returns a safe direct child path under workspacePath.
func RepoCheckoutPath(workspacePath, name string) (string, error) {
	if strings.TrimSpace(workspacePath) == "" {
		return "", fmt.Errorf("workspace path is empty")
	}
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("repo name is empty")
	}
	if filepath.IsAbs(name) || strings.Contains(name, string(filepath.Separator)) {
		return "", fmt.Errorf("repo name must not be a path: %s", name)
	}
	root, err := filepath.Abs(workspacePath)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(filepath.Join(root, name))
	if err != nil {
		return "", err
	}
	if !PathContains(root, target) || root == target {
		return "", fmt.Errorf("repo checkout path escapes workspace: %s", target)
	}
	return target, nil
}

// PathContains reports whether path is equal to or inside root.
func PathContains(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// CloneRepoTo clones cloneURL into targetPath.
func CloneRepoTo(ctx context.Context, cloneURL, targetPath string) error {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("create clone parent directory: %w", err)
	}
	cmd := exec.CommandContext(ctx, "git", "clone", cloneURL, targetPath) //nolint:gosec // URL is validated upstream and passed as argv.
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone failed for %s: %s", cloneURL, strings.TrimSpace(string(output)))
	}
	return nil
}

// EnsureGitWorktree creates a git worktree at targetPath from repoPath.
func EnsureGitWorktree(repoPath, targetPath, branchName string) error {
	if _, err := os.Stat(filepath.Join(targetPath, ".git")); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("creating worktree parent: %w", err)
	}
	if out, err := runGit(repoPath, "worktree", "add", targetPath, "-b", branchName); err == nil {
		return nil
	} else if !branchAlreadyExists(out, err) {
		return err
	}
	if _, err := runGit(repoPath, "worktree", "add", targetPath, branchName); err != nil {
		return err
	}
	return nil
}

func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func branchAlreadyExists(out string, err error) bool {
	msg := out
	if err != nil {
		msg += "\n" + err.Error()
	}
	return strings.Contains(msg, "already exists") ||
		strings.Contains(msg, "already a worktree") ||
		strings.Contains(msg, "already checked out")
}

// SelectAgentRepos applies an agent's repo affinity to local repos.
func SelectAgentRepos(repos []Repo, agent domain.Agent) ([]Repo, error) {
	if len(repos) == 0 {
		return nil, nil
	}
	if agent.CrossRepo {
		return repos, nil
	}
	allowed := make(map[string]struct{})
	for _, name := range agent.Repos {
		allowed[name] = struct{}{}
	}
	for _, group := range agent.RepoGroups {
		for _, repo := range repos {
			for _, repoGroup := range repo.Groups {
				if repoGroup == group {
					allowed[repo.Name] = struct{}{}
					break
				}
			}
		}
	}
	if len(allowed) == 0 {
		return []Repo{repos[0]}, nil
	}
	selected := make([]Repo, 0, len(allowed))
	for _, repo := range repos {
		if _, ok := allowed[repo.Name]; ok {
			selected = append(selected, repo)
		}
	}
	if len(selected) == 0 {
		names := make([]string, 0, len(repos))
		for _, repo := range repos {
			names = append(names, repo.Name)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("agent repo affinity does not match any workspace repo; available repos: %s", strings.Join(names, ", "))
	}
	return selected, nil
}

// FirstWorktreePath returns a deterministic path from a repo-name keyed map.
func FirstWorktreePath(paths map[string]string) string {
	if len(paths) == 0 {
		return ""
	}
	names := make([]string, 0, len(paths))
	for name := range paths {
		names = append(names, name)
	}
	sort.Strings(names)
	return paths[names[0]]
}

// RememberAgentWorktree stores an agent's local worktree path.
func RememberAgentWorktree(wsKey, agentName, worktreePath string) error {
	return bootstrap.MutateWorkspaceLocalState(wsKey, func(local *bootstrap.WorkspaceLocalState) error {
		if local.Agents == nil {
			local.Agents = make(map[string]bootstrap.AgentLocalState)
		}
		local.Agents[agentName] = bootstrap.AgentLocalState{Worktree: worktreePath}
		return nil
	})
}

// RememberRepoPath stores a repo's local checkout path.
func RememberRepoPath(wsKey, repoName, repoPath string) error {
	return bootstrap.MutateWorkspaceLocalState(wsKey, func(local *bootstrap.WorkspaceLocalState) error {
		if local.Repos == nil {
			local.Repos = make(map[string]string)
		}
		local.Repos[repoName] = repoPath
		return nil
	})
}
