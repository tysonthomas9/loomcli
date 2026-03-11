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

func (g *GitOpsImpl) ResolveAgentWorktree(name string) (*webui.AgentWorktree, error) {
	resolver, err := NewResolver()
	if err != nil {
		return nil, fmt.Errorf("creating resolver: %v", err)
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

func (g *GitOpsImpl) Reset(worktreePath, worktreeName, targetBranch string, force bool) (*webui.GitResetResult, error) {
	result, err := ResetWorktreeResult(worktreePath, worktreeName, targetBranch, force)
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
	return checkGhInstalled()
}

func (g *GitOpsImpl) SetRepoDefaultBranch(repoName, branch string) error {
	resolver, err := NewResolver()
	if err != nil {
		return err
	}
	return resolver.SetRepoDefaultBranch(repoName, branch)
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
