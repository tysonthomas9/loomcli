package opsimpl

import (
	"context"
	"errors"

	cligit "github.com/tysonthomas9/loomcli/internal/cli/git"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

// SourceControlAdapter translates machine-local repository mechanisms into
// Source Control's private placement and mechanics ports. Product callers
// receive Browse, Mutate, or Checkout; they never receive this adapter or a
// checkout path.
type SourceControlAdapter struct {
	git *LocalSourceControlMechanics
}

var _ sourcecontrol.WorkspaceLayout = (*SourceControlAdapter)(nil)
var _ sourcecontrol.CheckoutLayout = (*SourceControlAdapter)(nil)
var _ sourcecontrol.GitBrowseMechanics = (*SourceControlAdapter)(nil)
var _ sourcecontrol.BranchMechanics = (*SourceControlAdapter)(nil)
var _ sourcecontrol.ForgePublication = (*SourceControlAdapter)(nil)

func NewSourceControlAdapter(git *LocalSourceControlMechanics) *SourceControlAdapter {
	return &SourceControlAdapter{git: git}
}

func (adapter *SourceControlAdapter) ResolveAgentCheckout(
	_ context.Context,
	workspaceKey string,
	agentID string,
) (sourcecontrol.AgentCheckout, error) {
	if adapter == nil || adapter.git == nil {
		return sourcecontrol.AgentCheckout{}, sourcecontrol.ErrUnavailable
	}
	worktree, err := adapter.git.ResolveAgentWorktree(workspaceKey, agentID)
	if err != nil {
		if errors.Is(err, persistence.ErrNotFound) ||
			errors.Is(err, sourcecontrol.ErrAgentWorktreeNotFound) ||
			errors.Is(err, sourcecontrol.ErrAgentRepoNotAllowed) {
			return sourcecontrol.AgentCheckout{}, sourcecontrol.ErrNotFound
		}
		return sourcecontrol.AgentCheckout{}, err
	}
	return sourcecontrol.AgentCheckout{
		WorkspaceKey:  workspaceKey,
		AgentID:       worktree.Name,
		RepositoryRef: worktree.RepoName,
		CheckoutPath:  worktree.Path,
		Branch:        worktree.Branch,
		DefaultBranch: worktree.DefaultBranch,
		Remote:        worktree.Remote,
		IsWorkspace:   worktree.IsWorkspace,
	}, nil
}

func (adapter *SourceControlAdapter) ListAgentCheckouts(
	_ context.Context,
	workspaceKey string,
) ([]sourcecontrol.AgentCheckout, error) {
	worktrees, err := adapter.git.ListAgentWorktrees(workspaceKey)
	if err != nil {
		return nil, err
	}
	result := make([]sourcecontrol.AgentCheckout, len(worktrees))
	for index, worktree := range worktrees {
		result[index] = sourcecontrol.AgentCheckout{
			WorkspaceKey: workspaceKey, AgentID: worktree.Name,
			RepositoryRef: worktree.RepoName, CheckoutPath: worktree.Path,
			Branch: worktree.Branch, DefaultBranch: worktree.DefaultBranch,
			Remote: worktree.Remote, IsWorkspace: worktree.IsWorkspace,
		}
	}
	return result, nil
}

func (adapter *SourceControlAdapter) ListRepositoryCheckouts(
	_ context.Context,
	workspaceKey string,
) ([]sourcecontrol.RepositoryCheckoutView, error) {
	repositories, err := adapter.git.listWorkspaceRepos(workspaceKey)
	if err != nil {
		return nil, err
	}
	result := make([]sourcecontrol.RepositoryCheckoutView, 0, len(repositories))
	for _, repository := range repositories {
		if repository.Path == "" {
			continue
		}
		result = append(result, sourcecontrol.RepositoryCheckoutView{
			RepositoryRef: repository.Name,
			CheckoutPath:  repository.Path,
			ProviderName:  githubRepoName(repository.RemoteURL),
		})
	}
	return result, nil
}

func (adapter *SourceControlAdapter) SetRepositoryDefaultBranch(
	ctx context.Context,
	workspaceKey string,
	repository string,
	branch string,
) error {
	return adapter.git.SetRepoDefaultBranch(ctx, workspaceKey, repository, branch)
}

func (adapter *SourceControlAdapter) ResolveAgentWorktree(
	workspaceKey string,
	agentID string,
) (*sourcecontrol.Worktree, error) {
	checkout, err := adapter.ResolveAgentCheckout(context.Background(), workspaceKey, agentID)
	if err != nil {
		return nil, err
	}
	return sourceControlWorktree(checkout), nil
}

func (adapter *SourceControlAdapter) ResolveAgentWorktreeForRepo(
	workspaceKey string,
	agentID string,
	repository string,
) (*sourcecontrol.Worktree, error) {
	worktree, err := adapter.git.ResolveAgentWorktreeForRepo(workspaceKey, agentID, repository)
	if err != nil {
		return nil, err
	}
	return &sourcecontrol.Worktree{
		Name: worktree.Name, Path: worktree.Path, Branch: worktree.Branch,
		DefaultBranch: worktree.DefaultBranch, Remote: worktree.Remote,
		RepoName: worktree.RepoName, IsWorkspace: worktree.IsWorkspace,
	}, nil
}

func sourceControlWorktree(checkout sourcecontrol.AgentCheckout) *sourcecontrol.Worktree {
	return &sourcecontrol.Worktree{
		Name: checkout.AgentID, Path: checkout.CheckoutPath, Branch: checkout.Branch,
		DefaultBranch: checkout.DefaultBranch, Remote: checkout.Remote,
		RepoName: checkout.RepositoryRef, IsWorkspace: true,
	}
}

func (adapter *SourceControlAdapter) ResolveWorkspaceRoot(workspaceKey string) (string, error) {
	return adapter.git.ResolveWorkspaceRoot(workspaceKey)
}

func (adapter *SourceControlAdapter) ResolveWorkspaceData(
	workspaceKey string,
) (*sourcecontrol.WorkspaceTopology, error) {
	workspace, err := adapter.git.ResolveWorkspaceData(workspaceKey)
	if err != nil {
		return nil, err
	}
	result := &sourcecontrol.WorkspaceTopology{
		ID: workspace.ID, Name: workspace.Name, Path: workspace.Path,
		Groups: append([]string(nil), workspace.Groups...),
		Repos:  make([]sourcecontrol.WorkspaceRepo, len(workspace.Repos)),
		Agents: make([]sourcecontrol.WorkspaceAgent, len(workspace.Agents)),
	}
	for index, repository := range workspace.Repos {
		result.Repos[index] = sourcecontrol.WorkspaceRepo{
			Name: repository.Name, Path: repository.Path,
			DefaultBranch: repository.DefaultBranch, Remote: repository.Remote,
			RemoteURL: repository.RemoteURL, SourceRepoID: repository.SourceRepoID,
			Groups: append([]string(nil), repository.Groups...),
		}
	}
	for index, agent := range workspace.Agents {
		result.Agents[index] = sourcecontrol.WorkspaceAgent{
			Name: agent.Name, Role: agent.RoleName,
			Repos:      append([]string(nil), agent.Repos...),
			RepoGroups: append([]string(nil), agent.RepoGroups...),
		}
	}
	return result, nil
}

func (adapter *SourceControlAdapter) ResolveLoomDataDir() (string, error) {
	return adapter.git.ResolveLoomDataDir()
}

func (adapter *SourceControlAdapter) GitStatusPorcelain(
	ctx context.Context,
	path string,
) (sourcecontrol.GitFileStatusResult, error) {
	status, err := adapter.git.GitStatusPorcelain(ctx, path)
	return sourcecontrol.GitFileStatusResult{
		Entries: status.Entries, Partial: status.Partial, LimitHit: status.LimitHit,
	}, err
}

func (adapter *SourceControlAdapter) GitShowFileAtRev(
	ctx context.Context,
	path string,
	revision string,
	file string,
	maxBytes int64,
) (*sourcecontrol.GitFileContentAtRev, error) {
	result, err := adapter.git.GitShowFileAtRev(
		ctx, path, revision, file, maxBytes,
	)
	if err != nil || result == nil {
		return nil, err
	}
	return &sourcecontrol.GitFileContentAtRev{
		Content: result.Content, Size: result.Size, Truncated: result.Truncated,
	}, nil
}

func (adapter *SourceControlAdapter) GitDiffFile(
	ctx context.Context,
	path string,
	file string,
	from string,
	to string,
) (sourcecontrol.GitBoundedTextResult, error) {
	result, err := adapter.git.GitDiffFile(ctx, path, file, from, to)
	return sourcecontrol.GitBoundedTextResult{
		Output: result.Output, Partial: result.Partial, LimitHit: result.LimitHit,
	}, err
}

func (adapter *SourceControlAdapter) GitLogFile(
	ctx context.Context,
	path string,
	file string,
	limit int,
) (sourcecontrol.GitBoundedTextResult, error) {
	result, err := adapter.git.GitLogFile(ctx, path, file, limit)
	return sourcecontrol.GitBoundedTextResult{
		Output: result.Output, Partial: result.Partial, LimitHit: result.LimitHit,
	}, err
}

func (adapter *SourceControlAdapter) GitBlamePorcelain(
	ctx context.Context,
	path string,
	file string,
) (sourcecontrol.GitBoundedTextResult, error) {
	result, err := adapter.git.GitBlamePorcelain(ctx, path, file)
	return sourcecontrol.GitBoundedTextResult{
		Output: result.Output, Partial: result.Partial, LimitHit: result.LimitHit,
	}, err
}

func (adapter *SourceControlAdapter) GitCurrentBranch(
	ctx context.Context,
	path string,
) (string, error) {
	return adapter.git.GitCurrentBranch(ctx, path)
}

func (adapter *SourceControlAdapter) RepairCheckout(
	workspaceKey string,
	scope string,
	target string,
	repository string,
	force bool,
) (sourcecontrol.RepairResult, error) {
	result, err := adapter.git.RepairCheckout(workspaceKey, scope, target, repository, force)
	return sourcecontrol.RepairResult{
		Repaired: result.Repaired, Method: result.Method,
		RequiresForce: result.RequiresForce, BackupPath: result.BackupPath,
		Message: result.Message,
	}, err
}

func (adapter *SourceControlAdapter) ResolveMergeBase(
	_ context.Context,
	checkoutPath string,
	baseBranch string,
) (string, error) {
	return adapter.git.ResolveMergeBase(checkoutPath, baseBranch)
}

func (adapter *SourceControlAdapter) DiffStat(
	_ context.Context,
	checkoutPath string,
	baseBranch string,
) (sourcecontrol.DiffStat, error) {
	return adapter.git.DiffStat(checkoutPath, baseBranch), nil
}

func (adapter *SourceControlAdapter) DiffCommits(
	ctx context.Context,
	checkoutPath string,
	from string,
	limit int,
) ([]sourcecontrol.DiffCommit, error) {
	return adapter.git.DiffCommits(ctx, checkoutPath, from, limit)
}

func (adapter *SourceControlAdapter) DiffFiles(
	ctx context.Context,
	checkoutPath string,
	from string,
	to string,
) ([]sourcecontrol.DiffFile, error) {
	return adapter.git.DiffFiles(ctx, checkoutPath, from, to)
}

func (adapter *SourceControlAdapter) DiffFilePatch(
	ctx context.Context,
	checkoutPath string,
	from string,
	to string,
	path string,
) (*sourcecontrol.DiffFilePatch, error) {
	return adapter.git.DiffFilePatch(ctx, checkoutPath, from, to, path)
}

func (adapter *SourceControlAdapter) Push(
	_ context.Context,
	checkoutPath string,
	sourceBranch string,
	targetBranch string,
	remote string,
) (*sourcecontrol.PushResult, error) {
	return adapter.git.Push(checkoutPath, sourceBranch, targetBranch, remote)
}

func (adapter *SourceControlAdapter) Pull(
	_ context.Context,
	checkoutPath string,
	currentBranch string,
	sourceBranch string,
	remote string,
) (*sourcecontrol.PullResult, error) {
	return adapter.git.Pull(checkoutPath, currentBranch, sourceBranch, remote)
}

func (adapter *SourceControlAdapter) CurrentBranch(
	_ context.Context,
	checkoutPath string,
) (string, error) {
	return adapter.git.GetCurrentBranch(checkoutPath)
}

func (adapter *SourceControlAdapter) Reset(
	_ context.Context,
	checkoutPath string,
	agentID string,
	targetBranch string,
	force bool,
	push bool,
) (*sourcecontrol.ResetResult, error) {
	return adapter.git.Reset(checkoutPath, agentID, targetBranch, force, push)
}

func (adapter *SourceControlAdapter) Status(
	_ context.Context,
	checkoutPath string,
	targetBranch string,
) (*sourcecontrol.AgentStatusResult, error) {
	return adapter.git.Status(checkoutPath, targetBranch)
}

func (adapter *SourceControlAdapter) Available(context.Context) error {
	return adapter.git.CheckGhInstalled()
}

func (adapter *SourceControlAdapter) CreatePullRequest(
	_ context.Context,
	checkoutPath string,
	sourceBranch string,
	targetBranch string,
	remote string,
) (*sourcecontrol.PullRequestCreation, error) {
	return adapter.git.CreatePR(checkoutPath, sourceBranch, targetBranch, remote)
}

func (adapter *SourceControlAdapter) ListPullRequests(
	_ context.Context,
	checkoutPath string,
	state string,
	limit int,
) ([]sourcecontrol.PullRequest, error) {
	return cligit.ListPullRequests(checkoutPath, state, limit)
}
