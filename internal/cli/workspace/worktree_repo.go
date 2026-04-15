package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
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
	resolver, _ := cli.NewResolver()
	if resolver.Mode == cli.ModeWorkspace {
		return resolveWorkspaceTarget(resolver, name, repo)
	}

	// Legacy mode - per-repo routing not supported
	if repo != "" {
		return ResolvedTarget{}, fmt.Errorf("per-repo routing requires workspace mode (repo %q specified)", repo)
	}

	worktreePath, err := cli.ResolveWorktreePath(name)
	if err != nil {
		return ResolvedTarget{}, err
	}
	return ResolvedTarget{
		WorkDir:   worktreePath,
		AgentName: GetWorktreeName(worktreePath),
	}, nil
}

// resolveWorkspaceTarget handles workspace-mode resolution including per-repo routing.
func resolveWorkspaceTarget(resolver *cli.Resolver, name, repo string) (ResolvedTarget, error) {
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

	wsConfig, ok := resolver.Config.Workspaces[resolver.Workspace]
	if !ok || wsConfig.Path == "" {
		return ResolvedTarget{}, fmt.Errorf("workspace %q has no path configured", resolver.Workspace)
	}

	// Per-repo worktree routing: create/find worktree under repo directory
	if repo != "" {
		return resolveRepoWorktreeTarget(resolver, wsConfig, name, repo)
	}

	// Try worktree/repo name first — agents run in their own worktree
	// directory for isolated lock files and working trees.
	if name != "" {
		if wtPath, err := resolver.ResolveWorktreePath(name); err == nil {
			return ResolvedTarget{
				WorkDir:   wtPath,
				AgentName: name,
			}, nil
		}
	}
	// Fall back to workspace name (e.g., switching workspace context)
	if wsPath, ok := resolver.ResolveWorkspaceByName(name); ok {
		return ResolvedTarget{
			WorkDir:   wsPath,
			AgentName: name,
		}, nil
	}
	if name != "" {
		return ResolvedTarget{}, fmt.Errorf("'%s' is not a worktree, repo, or workspace name", name)
	}
	// No name given — use workspace root
	return ResolvedTarget{
		WorkDir:   wsConfig.Path,
		AgentName: resolver.WorkspaceName(),
	}, nil
}

// resolveRepoWorktreeTarget creates or finds a per-repo, per-agent worktree.
func resolveRepoWorktreeTarget(resolver *cli.Resolver, wsConfig config.WorkspaceConfig, name, repo string) (ResolvedTarget, error) {
	if err := cli.ValidateWorktreeName(name); err != nil {
		return ResolvedTarget{}, err
	}
	if err := cli.ValidateWorktreeName(repo); err != nil {
		return ResolvedTarget{}, fmt.Errorf("invalid repo name: %w", err)
	}
	repoPath, err := resolver.ResolveWorkspacePath(repo)
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

// ensureRepoWorktreeDeps creates a git worktree at targetPath from repoPath if it
// doesn't already exist. The worktree is created on branch branchName.
func ensureRepoWorktreeDeps(deps *cli.Deps, repoPath, targetPath, branchName string) error {
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
	_, err := cli.RunGit(deps, repoPath, "worktree", "add", targetPath, "-b", branchName)
	if err == nil {
		return nil
	}

	errStr := err.Error()
	if strings.Contains(errStr, "already exists") || strings.Contains(errStr, "already a worktree") {
		// Branch exists — try attaching to existing branch
		_, err = cli.RunGit(deps, repoPath, "worktree", "add", targetPath, branchName)
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

// ensureRepoWorktree creates a git worktree at targetPath from repoPath if it
// doesn't already exist. The worktree is created on branch branchName.
func ensureRepoWorktree(repoPath, targetPath, branchName string) error {
	return ensureRepoWorktreeDeps(cli.GetDeps(nil), repoPath, targetPath, branchName)
}
