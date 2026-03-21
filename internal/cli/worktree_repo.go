package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolvedTarget holds the result of workspace-aware argument resolution.
type ResolvedTarget struct {
	WorkDir   string // directory where Claude should run
	AgentName string // agent name for locks and prompts
	Repo      string // repo name (empty in legacy mode or workspace root)
}

// ResolveAgentTarget resolves a CLI argument (workspace name, repo name, or
// worktree name) into the working directory and agent name. In workspace mode,
// Claude always runs from the workspace root so bd commands use the shared
// .beads/ directory. When repo is non-empty in workspace mode, the agent gets
// its own git worktree under <workspace>/worktrees/<repo>/<name>/.
func ResolveAgentTarget(name, repo string) (ResolvedTarget, error) {
	resolver, _ := NewResolver()
	if resolver.Mode() == ModeWorkspace {
		return resolveWorkspaceTarget(resolver, name, repo)
	}

	// Legacy mode - per-repo routing not supported
	if repo != "" {
		return ResolvedTarget{}, fmt.Errorf("per-repo routing requires workspace mode (repo %q specified)", repo)
	}

	worktreePath, err := ResolveWorktreePath(name)
	if err != nil {
		return ResolvedTarget{}, err
	}
	return ResolvedTarget{
		WorkDir:   worktreePath,
		AgentName: GetWorktreeName(worktreePath),
	}, nil
}

// resolveWorkspaceTarget handles workspace-mode resolution including per-repo routing.
func resolveWorkspaceTarget(resolver *Resolver, name, repo string) (ResolvedTarget, error) {
	// Absolute paths are used as-is even in workspace mode
	if name != "" && filepath.IsAbs(name) {
		if _, err := os.Stat(name); err != nil {
			return ResolvedTarget{}, fmt.Errorf("path does not exist: %s", name)
		}
		return ResolvedTarget{
			WorkDir:   name,
			AgentName: filepath.Base(name),
		}, nil
	}

	wsConfig, ok := resolver.config.Workspaces[resolver.workspace]
	if !ok || wsConfig.Path == "" {
		return ResolvedTarget{}, fmt.Errorf("workspace %q has no path configured", resolver.workspace)
	}

	// Per-repo worktree routing: create/find worktree under repo directory
	if repo != "" {
		return resolveRepoWorktreeTarget(resolver, wsConfig, name, repo)
	}

	// Try workspace name first
	if wsPath, ok := resolver.ResolveWorkspaceByName(name); ok {
		return ResolvedTarget{
			WorkDir:   wsPath,
			AgentName: name,
		}, nil
	}
	// Validate repo name exists (but still use workspace root for Claude)
	if name != "" {
		if _, err := resolver.ResolveWorktreePath(name); err != nil {
			return ResolvedTarget{}, fmt.Errorf("'%s' is not a workspace or repo name: %w", name, err)
		}
	}
	// In workspace mode, use workspace root for Claude
	return ResolvedTarget{
		WorkDir:   wsConfig.Path,
		AgentName: resolver.WorkspaceName(),
	}, nil
}

// resolveRepoWorktreeTarget creates or finds a per-repo, per-agent worktree.
func resolveRepoWorktreeTarget(resolver *Resolver, wsConfig WorkspaceConfig, name, repo string) (ResolvedTarget, error) {
	if err := validateWorktreeName(name); err != nil {
		return ResolvedTarget{}, err
	}
	if err := validateWorktreeName(repo); err != nil {
		return ResolvedTarget{}, fmt.Errorf("invalid repo name: %w", err)
	}
	repoPath, err := resolver.resolveWorkspacePath(repo)
	if err != nil {
		return ResolvedTarget{}, fmt.Errorf("repo %q: %w", repo, err)
	}
	worktreePath := filepath.Join(wsConfig.Path, "worktrees", repo, name)
	if err := ensureRepoWorktree(repoPath, worktreePath, name); err != nil {
		return ResolvedTarget{}, fmt.Errorf("repo %q worktree: %w", repo, err)
	}
	return ResolvedTarget{
		WorkDir:   worktreePath,
		AgentName: name,
		Repo:      repo,
	}, nil
}

// GetWorktreeName extracts the worktree name from a path
func GetWorktreeName(path string) string {
	return filepath.Base(path)
}

// ensureRepoWorktree creates a git worktree at targetPath from repoPath if it
// doesn't already exist. The worktree is created on branch branchName.
func ensureRepoWorktree(repoPath, targetPath, branchName string) error {
	// Check if worktree already exists
	gitFile := filepath.Join(targetPath, ".git")
	if _, err := os.Stat(gitFile); err == nil {
		return nil // already created
	}

	// Create parent directories
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("creating worktree parent directory: %w", err)
	}

	// Try creating with new branch
	_, err := RunGitCommand(repoPath, "worktree", "add", targetPath, "-b", branchName)
	if err == nil {
		return nil
	}

	errStr := err.Error()
	if strings.Contains(errStr, "already exists") || strings.Contains(errStr, "already a worktree") {
		// Branch exists — try attaching to existing branch
		_, err = RunGitCommand(repoPath, "worktree", "add", targetPath, branchName)
		if err == nil {
			return nil
		}
		errStr = err.Error()
		if strings.Contains(errStr, "already exists") ||
			strings.Contains(errStr, "already a worktree") ||
			strings.Contains(errStr, "already checked out") {
			return fmt.Errorf("worktree or branch %q conflict: %w", branchName, err)
		}
	}

	return err
}
