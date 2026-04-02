package cli

import (
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui"
)

// GitOpsImpl implements webui.GitOps using the cli package git functions.
type GitOpsImpl struct{}

// NewGitOps creates a new GitOps implementation.
func NewGitOps() *GitOpsImpl {
	return &GitOpsImpl{}
}

// resolveWorkspaceConfigName translates a workspace ID (which may be a UUID or a
// config name) into the workspace config name that Resolver.SetWorkspace expects.
// Returns empty string if the ID is empty or not found.
func resolveWorkspaceConfigName(cfg *LoomConfig, wsID string) string {
	if cfg == nil || wsID == "" {
		return ""
	}
	// Direct name match (pre-T2 compatibility: MultiPool keyed by name)
	if _, ok := cfg.Workspaces[wsID]; ok {
		return wsID
	}
	// UUID match (post-T2: MultiPool keyed by UUID)
	name, _, found := WorkspaceByID(cfg, wsID)
	if found {
		return name
	}
	return ""
}

// scopeResolverToWorkspace sets the resolver's active workspace based on a
// workspace ID from the HTTP context. If workspaceID is empty or the resolver
// is not in workspace mode, this is a no-op (preserving default workspace behavior).
func scopeResolverToWorkspace(resolver *Resolver, workspaceID string) error {
	if workspaceID == "" || resolver.Mode() != ModeWorkspace {
		return nil
	}
	wsName := resolveWorkspaceConfigName(resolver.config, workspaceID)
	if wsName == "" {
		return fmt.Errorf("workspace %q not found in config", workspaceID)
	}
	return resolver.SetWorkspace(wsName)
}

func (g *GitOpsImpl) ResolveAgentWorktree(workspaceID, name string) (*webui.AgentWorktree, error) {
	resolver, err := NewResolver()
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
			aw := &webui.AgentWorktree{
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

func (g *GitOpsImpl) Push(worktreePath, sourceBranch, targetBranch, remote string) (*webui.GitPushResult, error) {
	result, err := PushBranchInRepoResult(worktreePath, sourceBranch, targetBranch, remote)
	if err != nil {
		return nil, err
	}
	return &webui.GitPushResult{
		Success:         result.Success,
		Message:         result.Message,
		AlreadyUpToDate: result.AlreadyUpToDate,
		ConflictedFiles: result.ConflictedFiles,
	}, nil
}

func (g *GitOpsImpl) Pull(worktreePath, currentBranch, sourceBranch, remote string) (*webui.GitPullResult, error) {
	result, err := PullRepoWorktreeResult(worktreePath, currentBranch, sourceBranch, remote)
	if err != nil {
		return nil, err
	}
	return &webui.GitPullResult{
		Success:         result.Success,
		Message:         result.Message,
		AlreadyUpToDate: result.AlreadyUpToDate,
		ConflictedFiles: result.ConflictedFiles,
	}, nil
}

func (g *GitOpsImpl) CreatePR(worktreePath, sourceBranch, targetBranch, remote string) (*webui.GitPRResult, error) {
	result, err := CreatePRResult(worktreePath, sourceBranch, targetBranch, remote)
	if err != nil {
		return nil, err
	}
	return &webui.GitPRResult{
		URL:           result.URL,
		Created:       result.Created,
		AlreadyExists: result.AlreadyExists,
		NoCommits:     result.NoCommits,
	}, nil
}

func (g *GitOpsImpl) Reset(worktreePath, worktreeName, targetBranch string, force, push bool) (*webui.GitResetResult, error) {
	result, err := ResetWorktreeResult(worktreePath, worktreeName, targetBranch, force, push)
	if err != nil {
		var lockedErr *LockedError
		if isLockedError(err, &lockedErr) {
			return nil, &webui.GitResetLockedError{
				AgentName: lockedErr.AgentName,
				PID:       lockedErr.PID,
				Duration:  lockedErr.Duration.Round(time.Second).String(),
				TaskID:    lockedErr.TaskID,
			}
		}
		return nil, err
	}
	return &webui.GitResetResult{
		Success:        result.Success,
		Message:        result.Message,
		PreviousBranch: result.PreviousBranch,
		Pushed:         result.Pushed,
	}, nil
}

func (g *GitOpsImpl) Status(worktreePath, targetBranch string) (*webui.GitStatusResult, error) {
	result, err := GetGitStatusSummary(worktreePath, targetBranch)
	if err != nil {
		return nil, err
	}
	return &webui.GitStatusResult{
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
	return GetCurrentBranch(worktreePath)
}

func (g *GitOpsImpl) CheckGhInstalled() error {
	return checkGhInstalled(defaultDeps)
}

func (g *GitOpsImpl) SetRepoDefaultBranch(workspaceID, repoName, branch string) error {
	resolver, err := NewResolver()
	if err != nil {
		return err
	}
	if err := scopeResolverToWorkspace(resolver, workspaceID); err != nil {
		return err
	}
	return resolver.SetRepoDefaultBranch(repoName, branch)
}

func (g *GitOpsImpl) ListAgentWorktrees(workspaceID string) ([]webui.AgentWorktree, error) {
	resolver, err := NewResolver()
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

	result := make([]webui.AgentWorktree, 0, len(worktrees))
	for _, wt := range worktrees {
		aw := webui.AgentWorktree{
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

func (g *GitOpsImpl) DiffStat(worktreePath, fromRef string) webui.DiffStatResult {
	stats := ComputeDiffStats(worktreePath, fromRef)
	return webui.DiffStatResult{
		FilesChanged: stats.FilesChanged,
		LinesAdded:   stats.LinesAdded,
		LinesRemoved: stats.LinesRemoved,
	}
}

func (g *GitOpsImpl) ResolveMergeBase(worktreePath, branch string) (string, error) {
	return ResolveMergeBase(worktreePath, branch)
}

func (g *GitOpsImpl) DiffCommits(worktreePath, mergeBase string, limit int) ([]webui.DiffCommitResult, error) {
	return DiffCommits(worktreePath, mergeBase, limit)
}

func (g *GitOpsImpl) DiffFiles(worktreePath, from, to string) ([]webui.DiffFileResult, error) {
	return DiffFiles(worktreePath, from, to)
}

func (g *GitOpsImpl) DiffFilePatch(worktreePath, from, to, path string) (*webui.DiffFilePatchResult, error) {
	return DiffFilePatch(worktreePath, from, to, path)
}

// isLockedError checks if err is a LockedError and extracts it.
func isLockedError(err error, target **LockedError) bool {
	le, ok := err.(*LockedError)
	if ok {
		*target = le
		return true
	}
	return false
}
