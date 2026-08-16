package opsimpl

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/query/operationalview"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/git"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

// WorkspaceProjection is the exact machine-local workspace view required by
// Source Control mechanics. The composition root adapts the legacy aggregate
// store; this implementation never receives store.Store.
type WorkspaceProjection interface {
	WorkspaceData(context.Context, string) (*operationalview.Workspace, error)
	WorkspacePath(context.Context, string) string
}

// LocalSourceControlMechanics provides machine-local repository mechanics to
// the Source Control owner. It does not expose a product-facing port.
type LocalSourceControlMechanics struct {
	workspaces   WorkspaceProjection
	agentQueries agents.IdentityQueries
}

// NewLocalSourceControlMechanics creates the private local adapter mechanics.
func NewLocalSourceControlMechanics() *LocalSourceControlMechanics {
	return &LocalSourceControlMechanics{}
}

// WithWorkspaceProjection enables FleetDB-backed workspace and worktree
// resolution through a consumer-owned narrow port.
func (g *LocalSourceControlMechanics) WithWorkspaceProjection(workspaces WorkspaceProjection) *LocalSourceControlMechanics {
	g.workspaces = workspaces
	return g
}

// WithAgentQueries provides the canonical Agent identity projection used for
// store-backed worktree placement. The retired supervised-assignment store is
// deliberately not a fallback: a partially composed server must fail closed.
func (g *LocalSourceControlMechanics) WithAgentQueries(queries agents.IdentityQueries) *LocalSourceControlMechanics {
	g.agentQueries = queries
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
// workspace ID from the HTTP context. If workspaceID is empty, the caller keeps
// the resolver's existing explicit scope.
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

func (g *LocalSourceControlMechanics) ResolveAgentWorktree(workspaceID, name string) (*sourcecontrol.Worktree, error) {
	if g != nil && g.workspaces != nil {
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

// ResolveAgentWorktreeForRepo resolves one explicit agent+repo checkout under
// <ws>/worktrees/<repo>/<agent>.
func (g *LocalSourceControlMechanics) ResolveAgentWorktreeForRepo(workspaceID, name, repoName string) (*sourcecontrol.Worktree, error) {
	repoName = strings.TrimSpace(repoName)
	if repoName == "" {
		return g.ResolveAgentWorktree(workspaceID, name)
	}
	if g != nil && g.workspaces != nil {
		ws, err := g.loadStoreWorkspace(context.Background(), workspaceID, name)
		if err != nil {
			return nil, err
		}
		agent, err := g.loadRuntimeIdentity(context.Background(), workspaceID, name)
		if err != nil {
			return nil, err
		}
		repo, err := selectAgentRepoByName(ws.Repos, agent.AgentID, agent.Repos, agent.RepoGroups, repoName)
		if err != nil {
			return nil, err
		}
		return resolveAgentWorktreeFromWSForRepo(ws, name, repo)
	}

	resolver, err := cli.NewResolver()
	if err != nil {
		return nil, fmt.Errorf("creating resolver: %v", err)
	}
	if err := scopeResolverToWorkspace(resolver, workspaceID); err != nil {
		return nil, err
	}
	ws, ok := resolver.Config.Workspaces[resolver.Workspace]
	if !ok {
		return nil, fmt.Errorf("workspace %q not found in config", resolver.Workspace)
	}
	root, err := validateWorkspaceRoot(workspaceID, ws.Path)
	if err != nil {
		return nil, err
	}
	repos, _ := configWorkspaceRepos(ws, root)
	repo, ok := findWorkspaceRepo(repos, repoName)
	if !ok {
		return nil, fmt.Errorf("%w: repo %q is not known in workspace %q", sourcecontrol.ErrAgentRepoNotAllowed, repoName, workspaceID)
	}
	return resolveAgentWorktreeFromWSForRepo(&operationalview.Workspace{Path: root}, name, repo)
}

// loadStoreWorkspace loads the workspace topology for store-backed agent
// resolution, applying the shared nil-guard and not-found mapping.
func (g *LocalSourceControlMechanics) loadStoreWorkspace(ctx context.Context, workspaceID, name string) (*operationalview.Workspace, error) {
	if g == nil || g.workspaces == nil || workspaceID == "" || name == "" {
		return nil, persistence.ErrNotFound
	}
	ws, err := g.workspaces.WorkspaceData(ctx, workspaceID)
	if err != nil {
		if errors.Is(err, persistence.ErrNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("load fleet-db workspace %q: %w", workspaceID, err)
	}
	return ws, nil
}

func (g *LocalSourceControlMechanics) loadRuntimeIdentity(ctx context.Context, workspaceID, name string) (*agents.RuntimeIdentity, error) {
	if g == nil || g.agentQueries == nil {
		return nil, fmt.Errorf("canonical agent identity queries are not composed: %w", persistence.ErrNotFound)
	}
	record, err := g.agentQueries.GetAgent(ctx, workspaceID, name)
	if err != nil {
		return nil, fmt.Errorf("agent %q in workspace %q: %w", name, workspaceID, err)
	}
	identity, err := agents.ResolveRuntimeIdentity(record)
	if err != nil {
		return nil, fmt.Errorf("project runtime identity for agent %q in workspace %q: %w", name, workspaceID, err)
	}
	return identity, nil
}

// newWorkspaceWorktree builds an AgentWorktree pointing at a workspace-local
// path (an agent worktree or a lead's primary repo). DefaultBranch falls back
// to "main" when the repo declares none.
func newWorkspaceWorktree(name, path, branch string, repo operationalview.Repository) *sourcecontrol.Worktree {
	db := repo.DefaultBranch
	if db == "" {
		db = "main"
	}
	return &sourcecontrol.Worktree{
		Name:          name,
		Path:          path,
		Branch:        branch,
		DefaultBranch: db,
		Remote:        repo.Remote,
		RepoName:      repo.Name,
		IsWorkspace:   true,
	}
}

func (g *LocalSourceControlMechanics) resolveAgentWorktreeFromStore(ctx context.Context, workspaceID, name string) (*sourcecontrol.Worktree, error) {
	ws, err := g.loadStoreWorkspace(ctx, workspaceID, name)
	if err != nil {
		return nil, err
	}
	agent, err := g.loadRuntimeIdentity(ctx, workspaceID, name)
	if err != nil {
		return nil, err
	}
	return resolveAgentWorktreeFromWS(ws, agent)
}

// resolveAgentWorktreeFromWS builds the agent's own worktree (under
// <ws>/worktrees) from an already-loaded workspace.
func resolveAgentWorktreeFromWS(ws *operationalview.Workspace, agent *agents.RuntimeIdentity) (*sourcecontrol.Worktree, error) {
	repo, err := selectAgentRepo(ws.Repos, agent.AgentID, agent.Repos, agent.RepoGroups)
	if err != nil {
		return nil, err
	}
	return resolveAgentWorktreeFromWSForRepo(ws, agent.AgentID, repo)
}

func resolveAgentWorktreeFromWSForRepo(ws *operationalview.Workspace, name string, repo operationalview.Repository) (*sourcecontrol.Worktree, error) {
	wtPath := filepath.Join(ws.Path, "worktrees", repo.Name, name)
	if _, err := os.Stat(filepath.Join(wtPath, ".git")); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: agent %q worktree for repo %q is not checked out on this machine at %s", sourcecontrol.ErrAgentWorktreeNotFound, name, repo.Name, wtPath)
		}
		return nil, fmt.Errorf("inspect agent %q worktree: %w", name, err)
	}
	branch, err := cli.GetCurrentBranch(wtPath)
	if err != nil {
		branch = "unknown"
	}
	return newWorkspaceWorktree(name, wtPath, branch, repo), nil
}

// ResolveWorkspaceRoot resolves the workspace root folder (ws.Path) as a browse
// root for Source Control. Store-backed deployments read the per-machine path from the local
// state cache (with a one-shot self-heal); the non-store path falls back to the
// local config. It deliberately avoids loading the full workspace topology —
// only the folder path is needed, and this runs on every browser list/read.
func (g *LocalSourceControlMechanics) ResolveWorkspaceRoot(workspaceID string) (string, error) {
	if workspaceID == "" {
		return "", fmt.Errorf("workspace id is required")
	}

	if g != nil && g.workspaces != nil {
		path := g.workspaces.WorkspacePath(context.Background(), workspaceID)
		return validateWorkspaceRoot(workspaceID, path)
	}

	// Non-store (config) path: resolve the workspace folder from local config.
	resolver, err := cli.NewResolver()
	if err != nil {
		return "", fmt.Errorf("creating resolver: %v", err)
	}
	wsName := resolveWorkspaceConfigName(resolver.Config, workspaceID)
	if wsName == "" {
		return "", fmt.Errorf("workspace %q not found in config", workspaceID)
	}
	return validateWorkspaceRoot(workspaceID, resolver.Config.Workspaces[wsName].Path)
}

// ResolveWorkspaceData returns workspace topology for file-scope target
// validation. Store-backed deployments use the fleet-db projection; the legacy
// config path exposes repo topology from local config.
func (g *LocalSourceControlMechanics) ResolveWorkspaceData(workspaceID string) (*operationalview.Workspace, error) {
	if workspaceID == "" {
		return nil, fmt.Errorf("workspace id is required")
	}

	if g != nil && g.workspaces != nil {
		return g.workspaces.WorkspaceData(context.Background(), workspaceID)
	}

	return resolveConfigWorkspaceData(workspaceID)
}

func resolveConfigWorkspaceData(workspaceID string) (*operationalview.Workspace, error) {
	resolver, err := cli.NewResolver()
	if err != nil {
		return nil, fmt.Errorf("creating resolver: %v", err)
	}
	wsName := resolveWorkspaceConfigName(resolver.Config, workspaceID)
	if wsName == "" {
		return nil, fmt.Errorf("workspace %q not found in config", workspaceID)
	}
	ws := resolver.Config.Workspaces[wsName]
	root, err := validateWorkspaceRoot(workspaceID, ws.Path)
	if err != nil {
		return nil, err
	}

	repos, groups := configWorkspaceRepos(ws, root)
	return &operationalview.Workspace{
		ID:     ws.ID,
		Name:   wsName,
		Path:   root,
		Repos:  repos,
		Groups: groups,
	}, nil
}

func configWorkspaceRepos(ws config.WorkspaceConfig, root string) ([]operationalview.Repository, []string) {
	repos := make([]operationalview.Repository, 0, len(ws.Repos))
	groupSet := make(map[string]bool)
	for _, r := range ws.Repos {
		db := r.DefaultBranch
		if db == "" {
			db = "main"
		}
		remote := r.Remote
		if remote == "" {
			remote = "origin"
		}
		repoPath := r.ResolveAbsPath(root)
		if r.Path == "" {
			repoPath = filepath.Join(root, r.Name)
		}
		repos = append(repos, operationalview.Repository{
			Name:          r.Name,
			Path:          repoPath,
			DefaultBranch: db,
			Remote:        remote,
			SourceRepoID:  r.SourceRepoID,
			Groups:        r.Groups,
		})
		for _, group := range r.Groups {
			groupSet[group] = true
		}
	}
	groups := make([]string, 0, len(groupSet))
	for group := range groupSet {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	return repos, groups
}

// validateWorkspaceRoot checks that path is a real directory on this machine so
// the viewer surfaces a clear "not checked out" error instead of a read failure.
func validateWorkspaceRoot(workspaceID, path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("workspace %q has no local path on this machine", workspaceID)
	}
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("workspace %q is not checked out on this machine at %s", workspaceID, path)
		}
		return "", fmt.Errorf("inspect workspace %q root: %w", workspaceID, err)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("workspace %q root is not a directory: %s", workspaceID, path)
	}
	return path, nil
}

func selectAgentRepo(repos []operationalview.Repository, agentName string, agentRepos, agentRepoGroups []string) (operationalview.Repository, error) {
	if len(repos) == 0 {
		return operationalview.Repository{}, fmt.Errorf("workspace has no repos for agent %q", agentName)
	}
	allowed := make(map[string]bool)
	for _, name := range agentRepos {
		allowed[name] = true
	}
	for _, group := range agentRepoGroups {
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
	return operationalview.Repository{}, fmt.Errorf("agent %q repo affinity does not match any workspace repo", agentName)
}

func selectAgentRepoByName(repos []operationalview.Repository, agentName string, agentRepos, agentRepoGroups []string, repoName string) (operationalview.Repository, error) {
	repo, ok := findWorkspaceRepo(repos, repoName)
	if !ok {
		return operationalview.Repository{}, fmt.Errorf("%w: repo %q is not known in workspace", sourcecontrol.ErrAgentRepoNotAllowed, repoName)
	}
	if !agentRepoAllowed(repos, agentRepos, agentRepoGroups, repo.Name) {
		return operationalview.Repository{}, fmt.Errorf("%w: repo %q is not allowed for agent %q", sourcecontrol.ErrAgentRepoNotAllowed, repo.Name, agentName)
	}
	return repo, nil
}

func findWorkspaceRepo(repos []operationalview.Repository, repoName string) (operationalview.Repository, bool) {
	for _, repo := range repos {
		if repo.Name == repoName {
			return repo, true
		}
	}
	return operationalview.Repository{}, false
}

func agentRepoAllowed(repos []operationalview.Repository, agentRepos, agentRepoGroups []string, repoName string) bool {
	if len(agentRepos) == 0 && len(agentRepoGroups) == 0 {
		_, ok := findWorkspaceRepo(repos, repoName)
		return ok
	}
	for _, name := range agentRepos {
		if name == repoName {
			return true
		}
	}
	for _, group := range agentRepoGroups {
		for _, repo := range repos {
			if repo.Name != repoName {
				continue
			}
			for _, repoGroup := range repo.Groups {
				if repoGroup == group {
					return true
				}
			}
		}
	}
	return false
}

func (g *LocalSourceControlMechanics) Push(worktreePath, sourceBranch, targetBranch, remote string) (*sourcecontrol.PushResult, error) {
	result, err := git.PushBranchInRepoResult(worktreePath, sourceBranch, targetBranch, remote)
	if err != nil {
		return nil, err
	}
	return &sourcecontrol.PushResult{
		Success:         result.Success,
		Message:         result.Message,
		AlreadyUpToDate: result.AlreadyUpToDate,
		ConflictedFiles: result.ConflictedFiles,
	}, nil
}

func (g *LocalSourceControlMechanics) Pull(worktreePath, currentBranch, sourceBranch, remote string) (*sourcecontrol.PullResult, error) {
	result, err := git.PullRepoWorktreeResult(worktreePath, currentBranch, sourceBranch, remote)
	if err != nil {
		return nil, err
	}
	return &sourcecontrol.PullResult{
		Success:         result.Success,
		Message:         result.Message,
		AlreadyUpToDate: result.AlreadyUpToDate,
		ConflictedFiles: result.ConflictedFiles,
	}, nil
}

func (g *LocalSourceControlMechanics) CreatePR(worktreePath, sourceBranch, targetBranch, remote string) (*sourcecontrol.PullRequestCreation, error) {
	result, err := git.CreatePRResult(worktreePath, sourceBranch, targetBranch, remote)
	if err != nil {
		return nil, err
	}
	return &sourcecontrol.PullRequestCreation{
		URL:           result.URL,
		Created:       result.Created,
		AlreadyExists: result.AlreadyExists,
		NoCommits:     result.NoCommits,
	}, nil
}

func githubRepoName(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "git@github.com:") {
		return githubRepoNameFromPath(strings.TrimPrefix(raw, "git@github.com:"))
	}
	if !strings.Contains(raw, "://") {
		return githubRepoNameFromPath(raw)
	}
	parsed, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(parsed.Hostname(), "github.com") {
		return ""
	}
	return githubRepoNameFromPath(parsed.Path)
}

func githubRepoNameFromPath(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return ""
	}
	return strings.TrimSpace(parts[0]) + "/" + strings.TrimSuffix(strings.TrimSpace(parts[1]), ".git")
}

func (g *LocalSourceControlMechanics) listWorkspaceRepos(workspaceID string) ([]operationalview.Repository, error) {
	if g != nil && g.workspaces != nil {
		ws, err := g.workspaces.WorkspaceData(context.Background(), workspaceID)
		if err != nil {
			return nil, fmt.Errorf("load fleet-db workspace %q: %w", workspaceID, err)
		}
		return ws.Repos, nil
	}

	resolver, err := cli.NewResolver()
	if err != nil {
		return nil, fmt.Errorf("creating resolver: %v", err)
	}
	if err := scopeResolverToWorkspace(resolver, workspaceID); err != nil {
		return nil, err
	}

	worktrees, err := resolver.DiscoverWorktrees()
	if err != nil {
		return nil, fmt.Errorf("discovering repos: %v", err)
	}
	return workspaceReposFromWorktrees(worktrees), nil
}

// workspaceReposFromWorktrees maps discovered worktrees to workspace repos,
// deduping by path and sorting by name.
func workspaceReposFromWorktrees(worktrees []cli.WorktreeInfo) []operationalview.Repository {
	byPath := make(map[string]operationalview.Repository)
	for _, wt := range worktrees {
		if wt.Path == "" {
			continue
		}
		if _, ok := byPath[wt.Path]; ok {
			continue
		}
		repo := operationalview.Repository{
			Name:          wt.Name,
			Path:          wt.Path,
			CurrentBranch: wt.Branch,
			DefaultBranch: "main",
		}
		if wt.Repo != nil {
			if wt.Repo.Name != "" {
				repo.Name = wt.Repo.Name
			}
			if wt.Repo.DefaultBranch != "" {
				repo.DefaultBranch = wt.Repo.DefaultBranch
			}
			repo.Remote = wt.Repo.Remote
		}
		byPath[wt.Path] = repo
	}

	repos := make([]operationalview.Repository, 0, len(byPath))
	for _, repo := range byPath {
		repos = append(repos, repo)
	}
	sort.Slice(repos, func(i, j int) bool {
		return repos[i].Name < repos[j].Name
	})
	return repos
}

func (g *LocalSourceControlMechanics) Reset(worktreePath, worktreeName, targetBranch string, force, push bool) (*sourcecontrol.ResetResult, error) {
	result, err := git.ResetWorktreeResult(worktreePath, worktreeName, targetBranch, force, push)
	if err != nil {
		var lockedErr *git.LockedError
		if isLockedError(err, &lockedErr) {
			return nil, &sourcecontrol.ResetLockedError{
				AgentID: lockedErr.AgentName,
				PID:     lockedErr.PID,
				Age:     lockedErr.Duration.Round(time.Second).String(),
				TaskID:  lockedErr.TaskID,
			}
		}
		return nil, err
	}
	return &sourcecontrol.ResetResult{
		Success:        result.Success,
		Message:        result.Message,
		PreviousBranch: result.PreviousBranch,
		Pushed:         result.Pushed,
	}, nil
}

func (g *LocalSourceControlMechanics) Status(worktreePath, targetBranch string) (*sourcecontrol.AgentStatusResult, error) {
	result, err := git.GetGitStatusSummary(worktreePath, targetBranch)
	if err != nil {
		return nil, err
	}
	return &sourcecontrol.AgentStatusResult{
		Branch:          result.Branch,
		TargetBranch:    result.TargetBranch,
		Clean:           result.IsClean,
		Ahead:           result.Ahead,
		Behind:          result.Behind,
		ChangedFiles:    result.ChangedFiles,
		ConflictedFiles: result.ConflictedFiles,
		HasConflicts:    result.HasConflicts,
		StashCount:      result.StashCount,
	}, nil
}

func (g *LocalSourceControlMechanics) GitStatusPorcelain(ctx context.Context, worktreePath string) (sourcecontrol.GitFileStatusResult, error) {
	status, err := git.NewGitInspector().Status(ctx, worktreePath)
	return sourcecontrol.GitFileStatusResult{Entries: status.Entries, Partial: status.Partial, LimitHit: status.LimitHit}, err
}

func (g *LocalSourceControlMechanics) GitShowFileAtRev(ctx context.Context, worktreePath, rev, path string, maxBytes int64) (*sourcecontrol.GitFileContentAtRev, error) {
	result, err := git.NewGitInspector().Show(ctx, worktreePath, rev, path, maxBytes)
	if err != nil {
		return nil, err
	}
	return &sourcecontrol.GitFileContentAtRev{
		Content:   result.Content,
		Size:      result.Size,
		Truncated: result.Truncated,
	}, nil
}

func (g *LocalSourceControlMechanics) GitDiffFile(ctx context.Context, worktreePath, path, from, to string) (sourcecontrol.GitBoundedTextResult, error) {
	result, err := git.NewGitInspector().Diff(ctx, worktreePath, path, from, to)
	return sourcecontrol.GitBoundedTextResult{Output: string(result.Output), Partial: result.Partial, LimitHit: result.LimitHit}, err
}

func (g *LocalSourceControlMechanics) GitLogFile(ctx context.Context, worktreePath, path string, limit int) (sourcecontrol.GitBoundedTextResult, error) {
	result, err := git.NewGitInspector().Log(ctx, worktreePath, path, limit)
	return sourcecontrol.GitBoundedTextResult{Output: string(result.Output), Partial: result.Partial, LimitHit: result.LimitHit}, err
}

func (g *LocalSourceControlMechanics) GitBlamePorcelain(ctx context.Context, worktreePath, path string) (sourcecontrol.GitBoundedTextResult, error) {
	result, err := git.NewGitInspector().Blame(ctx, worktreePath, path)
	return sourcecontrol.GitBoundedTextResult{Output: string(result.Output), Partial: result.Partial, LimitHit: result.LimitHit}, err
}

func (g *LocalSourceControlMechanics) ResolveLoomDataDir() (string, error) {
	dir := config.GetConfigDir()
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("cannot resolve loom data directory")
	}
	return filepath.Abs(dir)
}

func (g *LocalSourceControlMechanics) GitCurrentBranch(ctx context.Context, worktreePath string) (string, error) {
	return git.NewGitInspector().CurrentBranch(ctx, worktreePath)
}

func (g *LocalSourceControlMechanics) GetCurrentBranch(worktreePath string) (string, error) {
	return cli.GetCurrentBranch(worktreePath)
}

func (g *LocalSourceControlMechanics) CheckGhInstalled() error {
	return git.CheckGhInstalled()
}

func (g *LocalSourceControlMechanics) SetRepoDefaultBranch(ctx context.Context, workspaceID, repoName, branch string) error {
	resolver, err := cli.NewResolver()
	if err != nil {
		return err
	}
	if err := scopeResolverToWorkspace(resolver, workspaceID); err != nil {
		return err
	}
	return resolver.SetRepoDefaultBranch(ctx, repoName, branch)
}

func (g *LocalSourceControlMechanics) ListAgentWorktrees(workspaceID string) ([]sourcecontrol.Worktree, error) {
	if g != nil && g.workspaces != nil {
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

	result := make([]sourcecontrol.Worktree, 0, len(worktrees))
	for _, wt := range worktrees {
		result = append(result, toAgentWorktree(wt))
	}
	return result, nil
}

func (g *LocalSourceControlMechanics) listAgentWorktreesFromStore(ctx context.Context, workspaceID string) ([]sourcecontrol.Worktree, error) {
	ws, err := g.workspaces.WorkspaceData(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("load fleet-db workspace %q: %w", workspaceID, err)
	}
	result := make([]sourcecontrol.Worktree, 0, len(ws.Agents))
	for _, agent := range ws.Agents {
		wt, err := g.resolveAgentWorktreeFromStore(ctx, workspaceID, agent.Name)
		if err != nil {
			return nil, err
		}
		result = append(result, *wt)
	}
	return result, nil
}

// toAgentWorktree converts discovered local placement into Source Control's
// private machine-local worktree coordinate.
func toAgentWorktree(wt cli.WorktreeInfo) sourcecontrol.Worktree {
	aw := sourcecontrol.Worktree{
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

func (g *LocalSourceControlMechanics) DiffStat(worktreePath, fromRef string) sourcecontrol.DiffStat {
	stats := git.ComputeDiffStats(worktreePath, fromRef)
	return sourcecontrol.DiffStat{
		FilesChanged: stats.FilesChanged,
		LinesAdded:   stats.LinesAdded,
		LinesRemoved: stats.LinesRemoved,
	}
}

func (g *LocalSourceControlMechanics) ResolveMergeBase(worktreePath, branch string) (string, error) {
	return git.ResolveMergeBase(worktreePath, branch)
}

func (g *LocalSourceControlMechanics) DiffCommits(ctx context.Context, worktreePath, mergeBase string, limit int) ([]sourcecontrol.DiffCommit, error) {
	return git.DiffCommits(ctx, worktreePath, mergeBase, limit)
}

func (g *LocalSourceControlMechanics) DiffFiles(ctx context.Context, worktreePath, from, to string) ([]sourcecontrol.DiffFile, error) {
	return git.DiffFiles(ctx, worktreePath, from, to)
}

func (g *LocalSourceControlMechanics) DiffFilePatch(ctx context.Context, worktreePath, from, to, path string) (*sourcecontrol.DiffFilePatch, error) {
	return git.DiffFilePatch(ctx, worktreePath, from, to, path)
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
