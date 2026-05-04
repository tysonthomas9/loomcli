package opsimpl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/git"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

// GitOpsImpl implements ops.GitOps using the cli package git functions.
type GitOpsImpl struct {
	store store.Store
}

// NewGitOps creates a new GitOps implementation.
func NewGitOps() *GitOpsImpl {
	return &GitOpsImpl{}
}

// WithStore enables FleetDB-backed workspace/agent worktree resolution.
func (g *GitOpsImpl) WithStore(s store.Store) *GitOpsImpl {
	g.store = s
	return g
}

// resolveWorkspaceConfigName translates a workspace ID (which may be a UUID or a
// config name) into the workspace config name that cli.Resolver.SetWorkspace expects.
// Returns empty string if the ID is empty or not found.
func resolveWorkspaceConfigName(cfg *config.LoomConfig, wsID string) string {
	if cfg == nil || wsID == "" {
		return ""
	}
	// Direct name match.
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
// workspace ID from the HTTP context. If workspaceID is empty, this preserves
// the default workspace behavior.
func scopeResolverToWorkspace(resolver *cli.Resolver, workspaceID string) error {
	if workspaceID == "" {
		return nil
	}
	if resolver.Mode != cli.ModeWorkspace {
		return fmt.Errorf("workspace %q requested but resolver is not workspace-scoped", workspaceID)
	}
	wsName := resolveWorkspaceConfigName(resolver.Config, workspaceID)
	if wsName == "" {
		return fmt.Errorf("workspace %q not found in config", workspaceID)
	}
	return resolver.SetWorkspace(wsName)
}

func (g *GitOpsImpl) ResolveAgentWorktree(workspaceID, name string) (*ops.AgentWorktree, error) {
	if g != nil && g.store != nil {
		return g.resolveAgentWorktreeFromStore(context.Background(), workspaceID, name)
	}

	resolver, err := cli.NewResolver()
	if err != nil {
		return nil, fmt.Errorf("creating resolver: %v", err)
	}

	if err := scopeResolverToWorkspace(resolver, workspaceID); err != nil {
		return nil, err
	}

	// Direct path lookup: O(repos) with 1 git subprocess instead of O(agents).
	wt, err := resolver.ResolveAgentByName(name)
	if err != nil {
		return nil, err
	}

	aw := toAgentWorktree(wt)
	return &aw, nil
}

func (g *GitOpsImpl) resolveAgentWorktreeFromStore(ctx context.Context, workspaceID, name string) (*ops.AgentWorktree, error) {
	if g == nil || g.store == nil || workspaceID == "" || name == "" {
		return nil, domain.ErrNotFound
	}
	ws, err := storeadapter.BuildWorkspaceDataForKey(ctx, g.store, workspaceID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("load fleet-db workspace %q: %w", workspaceID, err)
	}
	var agent *ops.WorkspaceAgentInfo
	for i := range ws.Agents {
		if ws.Agents[i].Name == name {
			agent = &ws.Agents[i]
			break
		}
	}
	if agent == nil {
		return nil, fmt.Errorf("agent %q in workspace %q: %w", name, workspaceID, domain.ErrNotFound)
	}
	repo, err := selectAgentRepo(ws.Repos, *agent)
	if err != nil {
		return nil, err
	}
	wtPath := filepath.Join(ws.Path, "worktrees", repo.Name, name)
	if _, err := os.Stat(filepath.Join(wtPath, ".git")); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("agent %q worktree for repo %q is not checked out on this machine at %s", name, repo.Name, wtPath)
		}
		return nil, fmt.Errorf("inspect agent %q worktree: %w", name, err)
	}
	branch, err := cli.GetCurrentBranch(wtPath)
	if err != nil {
		return nil, fmt.Errorf("get current branch for agent %q: %w", name, err)
	}
	db := repo.DefaultBranch
	if db == "" {
		db = "main"
	}
	return &ops.AgentWorktree{
		Name:          name,
		Path:          wtPath,
		Branch:        branch,
		DefaultBranch: db,
		Remote:        repo.Remote,
		RepoName:      repo.Name,
		IsWorkspace:   true,
	}, nil
}

func selectAgentRepo(repos []ops.WorkspaceRepo, agent ops.WorkspaceAgentInfo) (ops.WorkspaceRepo, error) {
	if len(repos) == 0 {
		return ops.WorkspaceRepo{}, fmt.Errorf("workspace has no repos for agent %q", agent.Name)
	}
	allowed := make(map[string]bool)
	for _, name := range agent.Repos {
		allowed[name] = true
	}
	for _, group := range agent.RepoGroups {
		for _, repo := range repos {
			for _, repoGroup := range repo.Groups {
				if repoGroup == group {
					allowed[repo.Name] = true
					break
				}
			}
		}
	}
	if len(allowed) == 0 {
		return repos[0], nil
	}
	for _, repo := range repos {
		if allowed[repo.Name] {
			return repo, nil
		}
	}
	return ops.WorkspaceRepo{}, fmt.Errorf("agent %q repo affinity does not match any workspace repo", agent.Name)
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
	if g != nil && g.store != nil {
		return g.listAgentWorktreesFromStore(context.Background(), workspaceID)
	}

	resolver, err := cli.NewResolver()
	if err != nil {
		return nil, fmt.Errorf("creating resolver: %v", err)
	}

	if err := scopeResolverToWorkspace(resolver, workspaceID); err != nil {
		return nil, err
	}

	worktrees, err := resolver.DiscoverAgentWorktrees()
	if err != nil {
		return nil, fmt.Errorf("discovering agent worktrees: %v", err)
	}

	result := make([]ops.AgentWorktree, 0, len(worktrees))
	for _, wt := range worktrees {
		result = append(result, toAgentWorktree(wt))
	}
	return result, nil
}

func (g *GitOpsImpl) listAgentWorktreesFromStore(ctx context.Context, workspaceID string) ([]ops.AgentWorktree, error) {
	ws, err := storeadapter.BuildWorkspaceDataForKey(ctx, g.store, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("load fleet-db workspace %q: %w", workspaceID, err)
	}
	result := make([]ops.AgentWorktree, 0, len(ws.Agents))
	for _, agent := range ws.Agents {
		wt, err := g.resolveAgentWorktreeFromStore(ctx, workspaceID, agent.Name)
		if err != nil {
			return nil, err
		}
		result = append(result, *wt)
	}
	return result, nil
}

// toAgentWorktree converts a cli.WorktreeInfo to an ops.AgentWorktree.
func toAgentWorktree(wt cli.WorktreeInfo) ops.AgentWorktree {
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
	return aw
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
