package opsimpl

import (
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/git"
	"github.com/tysonthomas9/loomcli/internal/ops"
)

// GitOpsImpl implements ops.GitOps using the cli package git functions.
type GitOpsImpl struct{}

// NewGitOps creates a new GitOps implementation.
func NewGitOps() *GitOpsImpl {
	return &GitOpsImpl{}
}

// resolveWorkspaceConfigName translates a workspace ID (which may be a UUID or a
// config name) into the workspace config name that cli.Resolver.SetWorkspace expects.
// Returns empty string if the ID is empty or not found.
func resolveWorkspaceConfigName(cfg *config.LoomConfig, wsID string) string {
	if cfg == nil || wsID == "" {
		return ""
	}
	// Direct name match (pre-T2 compatibility: MultiPool keyed by name)
	if _, ok := cfg.Workspaces[wsID]; ok {
		return wsID
	}
	// UUID match (post-T2: MultiPool keyed by UUID)
	name, _, found := config.WorkspaceByID(cfg, wsID)
	if found {
		return name
	}
	return ""
}

// scopeResolverToWorkspace sets the resolver's active workspace based on a
// workspace ID from the HTTP context. If workspaceID is empty or the resolver
// is not in workspace mode, this is a no-op (preserving default workspace behavior).
func scopeResolverToWorkspace(resolver *cli.Resolver, workspaceID string) error {
	if workspaceID == "" || resolver.Mode != cli.ModeWorkspace {
		return nil
	}
	wsName := resolveWorkspaceConfigName(resolver.Config, workspaceID)
	if wsName == "" {
		return fmt.Errorf("workspace %q not found in config", workspaceID)
	}
	return resolver.SetWorkspace(wsName)
}

func (g *GitOpsImpl) ResolveAgentWorktree(workspaceID, name string) (*ops.AgentWorktree, error) {
	resolver, err := cli.NewResolver()
	if err != nil {
		return nil, fmt.Errorf("creating resolver: %v", err)
	}

	if err := scopeResolverToWorkspace(resolver, workspaceID); err != nil {
		return nil, err
	}

	worktrees, err := resolver.DiscoverWorktrees()
	if err != nil {
		return nil, fmt.Errorf("discovering worktrees: %v", err)
	}

	for _, wt := range worktrees {
		if wt.Name == name {
			aw := &ops.AgentWorktree{
				Name:          wt.Name,
				Path:          wt.Path,
				Branch:        wt.Branch,
				DefaultBranch: "main",
			}
			if wt.Repo != nil {
				if wt.Repo.DefaultBranch != "" {
					aw.DefaultBranch = wt.Repo.DefaultBranch
				}
				aw.Remote = wt.Repo.Remote
				aw.RepoName = wt.Repo.Name
				aw.IsWorkspace = true
			}
			return aw, nil
		}
	}

	return nil, fmt.Errorf("worktree %q not found", name)
}

func (g *GitOpsImpl) Push(worktreePath, sourceBranch, targetBranch, remote string) (*ops.GitPushResult, error) {
	result, err := git.PushBranchInRepoResult(worktreePath, sourceBranch, targetBranch, remote)
	if err != nil {
		return nil, err
	}
	return &ops.GitPushResult{
		Success:         result.Success,
		Message:         result.Message,
		AlreadyUpToDate: result.AlreadyUpToDate,
		ConflictedFiles: result.ConflictedFiles,
	}, nil
}

func (g *GitOpsImpl) Pull(worktreePath, currentBranch, sourceBranch, remote string) (*ops.GitPullResult, error) {
	result, err := git.PullRepoWorktreeResult(worktreePath, currentBranch, sourceBranch, remote)
	if err != nil {
		return nil, err
	}
	return &ops.GitPullResult{
		Success:         result.Success,
		Message:         result.Message,
		AlreadyUpToDate: result.AlreadyUpToDate,
		ConflictedFiles: result.ConflictedFiles,
	}, nil
}

func (g *GitOpsImpl) CreatePR(worktreePath, sourceBranch, targetBranch, remote string) (*ops.GitPRResult, error) {
	result, err := git.CreatePRResult(worktreePath, sourceBranch, targetBranch, remote)
	if err != nil {
		return nil, err
	}
	return &ops.GitPRResult{
		URL:           result.URL,
		Created:       result.Created,
		AlreadyExists: result.AlreadyExists,
		NoCommits:     result.NoCommits,
	}, nil
}

func (g *GitOpsImpl) Reset(worktreePath, worktreeName, targetBranch string, force, push bool) (*ops.GitResetResult, error) {
	result, err := git.ResetWorktreeResult(worktreePath, worktreeName, targetBranch, force, push)
	if err != nil {
		var lockedErr *git.LockedError
		if isLockedError(err, &lockedErr) {
			return nil, &ops.GitResetLockedError{
				AgentName: lockedErr.AgentName,
				PID:       lockedErr.PID,
				Duration:  lockedErr.Duration.Round(time.Second).String(),
				TaskID:    lockedErr.TaskID,
			}
		}
		return nil, err
	}
	return &ops.GitResetResult{
		Success:        result.Success,
		Message:        result.Message,
		PreviousBranch: result.PreviousBranch,
		Pushed:         result.Pushed,
	}, nil
}

func (g *GitOpsImpl) Status(worktreePath, targetBranch string) (*ops.GitStatusResult, error) {
	result, err := git.GetGitStatusSummary(worktreePath, targetBranch)
	if err != nil {
		return nil, err
	}
	return &ops.GitStatusResult{
		Branch:          result.Branch,
		TargetBranch:    result.TargetBranch,
		IsClean:         result.IsClean,
		Ahead:           result.Ahead,
		Behind:          result.Behind,
		ChangedFiles:    result.ChangedFiles,
		ConflictedFiles: result.ConflictedFiles,
		HasConflicts:    result.HasConflicts,
		StashCount:      result.StashCount,
	}, nil
}

func (g *GitOpsImpl) GetCurrentBranch(worktreePath string) (string, error) {
	return cli.GetCurrentBranch(worktreePath)
}

func (g *GitOpsImpl) CheckGhInstalled() error {
	return git.CheckGhInstalled()
}

func (g *GitOpsImpl) SetRepoDefaultBranch(workspaceID, repoName, branch string) error {
	resolver, err := cli.NewResolver()
	if err != nil {
		return err
	}
	if err := scopeResolverToWorkspace(resolver, workspaceID); err != nil {
		return err
	}
	return resolver.SetRepoDefaultBranch(repoName, branch)
}

func (g *GitOpsImpl) ListAgentWorktrees(workspaceID string) ([]ops.AgentWorktree, error) {
	resolver, err := cli.NewResolver()
	if err != nil {
		return nil, fmt.Errorf("creating resolver: %v", err)
	}

	if err := scopeResolverToWorkspace(resolver, workspaceID); err != nil {
		return nil, err
	}

	worktrees, err := resolver.DiscoverWorktrees()
	if err != nil {
		return nil, fmt.Errorf("discovering worktrees: %v", err)
	}

	result := make([]ops.AgentWorktree, 0, len(worktrees))
	for _, wt := range worktrees {
		aw := ops.AgentWorktree{
			Name:          wt.Name,
			Path:          wt.Path,
			Branch:        wt.Branch,
			DefaultBranch: "main",
		}
		if wt.Repo != nil {
			if wt.Repo.DefaultBranch != "" {
				aw.DefaultBranch = wt.Repo.DefaultBranch
			}
			aw.Remote = wt.Repo.Remote
			aw.RepoName = wt.Repo.Name
			aw.IsWorkspace = true
		}
		result = append(result, aw)
	}
	return result, nil
}

func (g *GitOpsImpl) DiffStat(worktreePath, fromRef string) ops.DiffStatResult {
	stats := git.ComputeDiffStats(worktreePath, fromRef)
	return ops.DiffStatResult{
		FilesChanged: stats.FilesChanged,
		LinesAdded:   stats.LinesAdded,
		LinesRemoved: stats.LinesRemoved,
	}
}

func (g *GitOpsImpl) ResolveMergeBase(worktreePath, branch string) (string, error) {
	return git.ResolveMergeBase(worktreePath, branch)
}

func (g *GitOpsImpl) DiffCommits(worktreePath, mergeBase string, limit int) ([]ops.DiffCommitResult, error) {
	return git.DiffCommits(worktreePath, mergeBase, limit)
}

func (g *GitOpsImpl) DiffFiles(worktreePath, from, to string) ([]ops.DiffFileResult, error) {
	return git.DiffFiles(worktreePath, from, to)
}

func (g *GitOpsImpl) DiffFilePatch(worktreePath, from, to, path string) (*ops.DiffFilePatchResult, error) {
	return git.DiffFilePatch(worktreePath, from, to, path)
}

// isLockedError checks if err is a git.LockedError and extracts it.
func isLockedError(err error, target **git.LockedError) bool {
	le, ok := err.(*git.LockedError)
	if ok {
		*target = le
		return true
	}
	return false
}
