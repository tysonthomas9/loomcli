package workspace

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
)

// ResolvedTarget holds the result of workspace-aware argument resolution.
type ResolvedTarget struct {
	WorkDir   string // directory where Claude should run
	AgentName string // agent name for locks and prompts
	Repo      string // repo name (empty when running at workspace root)
}

// absTargetAgentName returns the agent name for an absolute-path target. It
// prefers LOOM_AGENT_NAME (set by the sandbox bootstrap, where the target is the
// clone path /sandbox/repo and filepath.Base would collapse every agent to
// "repo"), falling back to the path's base name for ordinary local runs.
func absTargetAgentName(path string) string {
	if v := os.Getenv("LOOM_AGENT_NAME"); v != "" {
		return v
	}
	return filepath.Base(path)
}

// ResolveAgentTarget resolves a CLI argument (workspace name, repo name, or
// worktree name) into the working directory and agent name. Claude runs from
// the workspace root so loom data commands resolve the active workspace.
// When repo is non-empty, the agent gets its
// own git worktree under <workspace>/worktrees/<repo>/<name>/.
func ResolveAgentTarget(name, repo string) (ResolvedTarget, error) {
	if repo == "" && name != "" && filepath.IsAbs(name) {
		if _, err := os.Stat(name); err != nil {
			return ResolvedTarget{}, fmt.Errorf("path does not exist: %s", name)
		}
		return ResolvedTarget{
			WorkDir:   name,
			AgentName: absTargetAgentName(name),
		}, nil
	}

	resolver, err := cli.NewResolver()
	if err != nil {
		if repo == "" && name == "" {
			cwd, cwdErr := os.Getwd()
			if cwdErr != nil {
				return ResolvedTarget{}, fmt.Errorf("get current directory: %w", cwdErr)
			}
			return ResolvedTarget{
				WorkDir:   cwd,
				AgentName: filepath.Base(cwd),
			}, nil
		}
		return ResolvedTarget{}, err
	}
	return resolveWorkspaceTarget(resolver, name, repo)
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
			AgentName: absTargetAgentName(name),
		}, nil
	}

	wsConfig, ok := resolver.Config.Workspaces[resolver.Workspace]
	if !ok || wsConfig.Path == "" {
		return ResolvedTarget{}, fmt.Errorf("workspace %q has no local path configured; use an absolute path such as /sandbox/repo", resolver.Workspace)
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

// ensureRepoWorktree creates a git worktree at targetPath from repoPath if it
// doesn't already exist. The worktree is created on branch branchName.
func ensureRepoWorktree(repoPath, targetPath, branchName string) error {
	return localworkspace.EnsureGitWorktree(repoPath, targetPath, branchName)
}

// EnsureRepoWorktree creates a git worktree at targetPath from repoPath if it
// does not already exist. It is exported for agent-definition commands that
// need to make a stored agent runnable by the local daemon.
func EnsureRepoWorktree(repoPath, targetPath, branchName string) error {
	return ensureRepoWorktree(repoPath, targetPath, branchName)
}
