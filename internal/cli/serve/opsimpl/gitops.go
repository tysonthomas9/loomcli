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

	"golang.org/x/sync/errgroup"

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

// ResolveAgentWorktreeForRepo resolves one explicit agent+repo checkout under
// <ws>/worktrees/<repo>/<agent>.
func (g *GitOpsImpl) ResolveAgentWorktreeForRepo(workspaceID, name, repoName string) (*ops.AgentWorktree, error) {
	repoName = strings.TrimSpace(repoName)
	if repoName == "" {
		return g.ResolveAgentWorktree(workspaceID, name)
	}
	if g != nil && g.store != nil {
		ws, err := g.loadStoreWorkspace(context.Background(), workspaceID, name)
		if err != nil {
			return nil, err
		}
		agent, err := findWorkspaceAgent(ws, workspaceID, name)
		if err != nil {
			return nil, err
		}
		repo, err := selectAgentRepoByName(ws.Repos, *agent, repoName)
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
		return nil, fmt.Errorf("%w: repo %q is not known in workspace %q", ops.ErrAgentRepoNotAllowed, repoName, workspaceID)
	}
	return resolveAgentWorktreeFromWSForRepo(&ops.WorkspaceData{Path: root}, name, repo)
}

// loadStoreWorkspace loads the workspace topology for store-backed agent
// resolution, applying the shared nil-guard and not-found mapping.
func (g *GitOpsImpl) loadStoreWorkspace(ctx context.Context, workspaceID, name string) (*ops.WorkspaceData, error) {
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
	return ws, nil
}

// findWorkspaceAgent returns the named agent from an already-loaded workspace.
func findWorkspaceAgent(ws *ops.WorkspaceData, workspaceID, name string) (*ops.WorkspaceAgentInfo, error) {
	for i := range ws.Agents {
		if ws.Agents[i].Name == name {
			return &ws.Agents[i], nil
		}
	}
	return nil, fmt.Errorf("agent %q in workspace %q: %w", name, workspaceID, domain.ErrNotFound)
}

// newWorkspaceWorktree builds an AgentWorktree pointing at a workspace-local
// path (an agent worktree or a lead's primary repo). DefaultBranch falls back
// to "main" when the repo declares none.
func newWorkspaceWorktree(name, path, branch string, repo ops.WorkspaceRepo) *ops.AgentWorktree {
	db := repo.DefaultBranch
	if db == "" {
		db = "main"
	}
	return &ops.AgentWorktree{
		Name:          name,
		Path:          path,
		Branch:        branch,
		DefaultBranch: db,
		Remote:        repo.Remote,
		RepoName:      repo.Name,
		IsWorkspace:   true,
	}
}

func (g *GitOpsImpl) resolveAgentWorktreeFromStore(ctx context.Context, workspaceID, name string) (*ops.AgentWorktree, error) {
	ws, err := g.loadStoreWorkspace(ctx, workspaceID, name)
	if err != nil {
		return nil, err
	}
	return resolveAgentWorktreeFromWS(ws, workspaceID, name)
}

// resolveAgentWorktreeFromWS builds the agent's own worktree (under
// <ws>/worktrees) from an already-loaded workspace.
func resolveAgentWorktreeFromWS(ws *ops.WorkspaceData, workspaceID, name string) (*ops.AgentWorktree, error) {
	agent, err := findWorkspaceAgent(ws, workspaceID, name)
	if err != nil {
		return nil, err
	}
	repo, err := selectAgentRepo(ws.Repos, *agent)
	if err != nil {
		return nil, err
	}
	return resolveAgentWorktreeFromWSForRepo(ws, name, repo)
}

func resolveAgentWorktreeFromWSForRepo(ws *ops.WorkspaceData, name string, repo ops.WorkspaceRepo) (*ops.AgentWorktree, error) {
	wtPath := filepath.Join(ws.Path, "worktrees", repo.Name, name)
	if _, err := os.Stat(filepath.Join(wtPath, ".git")); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: agent %q worktree for repo %q is not checked out on this machine at %s", ops.ErrAgentWorktreeNotFound, name, repo.Name, wtPath)
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
// root for the read-only workspace file viewer. See ops.FileOps for the
// contract. Store-backed deployments read the per-machine path from the local
// state cache (with a one-shot self-heal); the non-store path falls back to the
// local config. It deliberately avoids loading the full workspace topology —
// only the folder path is needed, and this runs on every browser list/read.
func (g *GitOpsImpl) ResolveWorkspaceRoot(workspaceID string) (string, error) {
	if workspaceID == "" {
		return "", fmt.Errorf("workspace id is required")
	}

	if g != nil && g.store != nil {
		path := storeadapter.ResolveOrHealWorkspacePath(context.Background(), g.store, workspaceID)
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
func (g *GitOpsImpl) ResolveWorkspaceData(workspaceID string) (*ops.WorkspaceData, error) {
	if workspaceID == "" {
		return nil, fmt.Errorf("workspace id is required")
	}

	if g != nil && g.store != nil {
		return storeadapter.BuildWorkspaceDataForKey(context.Background(), g.store, workspaceID)
	}

	return resolveConfigWorkspaceData(workspaceID)
}

func resolveConfigWorkspaceData(workspaceID string) (*ops.WorkspaceData, error) {
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
	return &ops.WorkspaceData{
		ID:     ws.ID,
		Name:   wsName,
		Path:   root,
		Repos:  repos,
		Groups: groups,
	}, nil
}

func configWorkspaceRepos(ws config.WorkspaceConfig, root string) ([]ops.WorkspaceRepo, []string) {
	repos := make([]ops.WorkspaceRepo, 0, len(ws.Repos))
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
		repos = append(repos, ops.WorkspaceRepo{
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

func selectAgentRepoByName(repos []ops.WorkspaceRepo, agent ops.WorkspaceAgentInfo, repoName string) (ops.WorkspaceRepo, error) {
	repo, ok := findWorkspaceRepo(repos, repoName)
	if !ok {
		return ops.WorkspaceRepo{}, fmt.Errorf("%w: repo %q is not known in workspace", ops.ErrAgentRepoNotAllowed, repoName)
	}
	if !agentRepoAllowed(repos, agent, repo.Name) {
		return ops.WorkspaceRepo{}, fmt.Errorf("%w: repo %q is not allowed for agent %q", ops.ErrAgentRepoNotAllowed, repo.Name, agent.Name)
	}
	return repo, nil
}

func findWorkspaceRepo(repos []ops.WorkspaceRepo, repoName string) (ops.WorkspaceRepo, bool) {
	for _, repo := range repos {
		if repo.Name == repoName {
			return repo, true
		}
	}
	return ops.WorkspaceRepo{}, false
}

func agentRepoAllowed(repos []ops.WorkspaceRepo, agent ops.WorkspaceAgentInfo, repoName string) bool {
	if len(agent.Repos) == 0 && len(agent.RepoGroups) == 0 {
		_, ok := findWorkspaceRepo(repos, repoName)
		return ok
	}
	for _, name := range agent.Repos {
		if name == repoName {
			return true
		}
	}
	for _, group := range agent.RepoGroups {
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

// prRepoQuery pairs a workspace repo with its gh PR listing result.
type prRepoQuery struct {
	repo ops.WorkspaceRepo
	prs  []ops.GitPullRequest
	err  error
}

func (g *GitOpsImpl) ListWorkspacePullRequests(workspaceID, state string, limit int) (*ops.GitPullRequestList, error) {
	repos, err := g.listWorkspaceRepos(workspaceID)
	if err != nil {
		return nil, err
	}

	ghState := state
	if strings.EqualFold(state, "review") {
		ghState = "open"
	}

	queries := dedupeRepoPRQueries(repos)

	var eg errgroup.Group
	eg.SetLimit(4)
	for _, q := range queries {
		eg.Go(func() error {
			q.prs, q.err = git.ListPullRequests(q.repo.Path, ghState, limit)
			return nil
		})
	}
	_ = eg.Wait()

	result := &ops.GitPullRequestList{PullRequests: []ops.GitPullRequest{}}
	all := collectRepoQueryPRs(queries, result)
	all = filterPullRequestsByState(all, state)

	sort.Slice(all, func(i, j int) bool {
		return all[i].UpdatedAt > all[j].UpdatedAt
	})
	result.PullRequests = all
	return result, nil
}

// dedupeRepoPRQueries builds one gh query per repo identity — a repo with
// several agent worktrees shares a single GitHub PR list.
func dedupeRepoPRQueries(repos []ops.WorkspaceRepo) []*prRepoQuery {
	seenRepo := make(map[string]struct{})
	queries := make([]*prRepoQuery, 0, len(repos))
	for _, repo := range repos {
		if repo.Path == "" {
			continue
		}
		key := repoPRQueryKey(repo)
		if _, ok := seenRepo[key]; ok {
			continue
		}
		seenRepo[key] = struct{}{}
		queries = append(queries, &prRepoQuery{repo: repo})
	}
	return queries
}

func repoPRQueryKey(repo ops.WorkspaceRepo) string {
	remoteURL := strings.TrimSpace(repo.RemoteURL)
	if githubName := githubRepoName(remoteURL); githubName != "" {
		return "github:" + strings.ToLower(githubName)
	}
	if remoteURL != "" {
		return "remote:" + strings.TrimSuffix(strings.TrimSuffix(remoteURL, "/"), ".git")
	}
	if name := strings.TrimSpace(repo.Name); name != "" {
		return "name:" + strings.ToLower(name)
	}
	return "path:" + filepath.Clean(repo.Path)
}

// collectRepoQueryPRs merges per-repo results into one list, deduping by PR
// URL and recording per-repo failures as warnings on result.
func collectRepoQueryPRs(queries []*prRepoQuery, result *ops.GitPullRequestList) []ops.GitPullRequest {
	seen := make(map[string]struct{})
	all := result.PullRequests
	for _, q := range queries {
		if q.err != nil {
			// A repo that gh can't list (non-GitHub remote, missing auth, …)
			// must not take down the listing for the rest of the workspace.
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s: %v", q.repo.Name, q.err))
			continue
		}
		for _, pr := range q.prs {
			if pr.URL == "" {
				continue
			}
			if _, ok := seen[pr.URL]; ok {
				continue
			}
			seen[pr.URL] = struct{}{}
			pr = applyWorkspaceRepoIdentity(pr, q.repo)
			all = append(all, pr)
		}
	}
	return all
}

func applyWorkspaceRepoIdentity(pr ops.GitPullRequest, repo ops.WorkspaceRepo) ops.GitPullRequest {
	pr.SourceRepo = strings.TrimSpace(repo.Name)
	pr.RepoName = resolveGitHubRepoName(pr, repo)
	return pr
}

func resolveGitHubRepoName(pr ops.GitPullRequest, repo ops.WorkspaceRepo) string {
	for _, candidate := range []string{pr.RepoName, repo.RemoteURL, pr.URL, repo.Name} {
		if name := githubRepoName(candidate); name != "" {
			return name
		}
	}
	return ""
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

func filterPullRequestsByState(all []ops.GitPullRequest, state string) []ops.GitPullRequest {
	switch {
	case strings.EqualFold(state, "review"):
		return git.FilterPullRequestsForReview(all)
	case strings.EqualFold(state, "open"):
		filtered := make([]ops.GitPullRequest, 0, len(all))
		for _, pr := range all {
			if strings.EqualFold(pr.State, "OPEN") && !pr.IsDraft {
				filtered = append(filtered, pr)
			}
		}
		return filtered
	case strings.EqualFold(state, "merged"):
		filtered := make([]ops.GitPullRequest, 0, len(all))
		for _, pr := range all {
			if strings.EqualFold(pr.State, "MERGED") {
				filtered = append(filtered, pr)
			}
		}
		return filtered
	default:
		return all
	}
}

func (g *GitOpsImpl) listWorkspaceRepos(workspaceID string) ([]ops.WorkspaceRepo, error) {
	if g != nil && g.store != nil {
		ws, err := storeadapter.BuildWorkspaceDataForKey(context.Background(), g.store, workspaceID)
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
func workspaceReposFromWorktrees(worktrees []cli.WorktreeInfo) []ops.WorkspaceRepo {
	byPath := make(map[string]ops.WorkspaceRepo)
	for _, wt := range worktrees {
		if wt.Path == "" {
			continue
		}
		if _, ok := byPath[wt.Path]; ok {
			continue
		}
		repo := ops.WorkspaceRepo{
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

	repos := make([]ops.WorkspaceRepo, 0, len(byPath))
	for _, repo := range byPath {
		repos = append(repos, repo)
	}
	sort.Slice(repos, func(i, j int) bool {
		return repos[i].Name < repos[j].Name
	})
	return repos
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

func (g *GitOpsImpl) GitStatusPorcelain(ctx context.Context, worktreePath string) (ops.GitFileStatusResult, error) {
	status, err := git.NewGitInspector().Status(ctx, worktreePath)
	return ops.GitFileStatusResult{Entries: status.Entries, Partial: status.Partial, LimitHit: status.LimitHit}, err
}

func (g *GitOpsImpl) GitShowFileAtRev(ctx context.Context, worktreePath, rev, path string, maxBytes int64) (*ops.GitFileContentAtRev, error) {
	result, err := git.NewGitInspector().Show(ctx, worktreePath, rev, path, maxBytes)
	if err != nil {
		return nil, err
	}
	return &ops.GitFileContentAtRev{
		Content:   result.Content,
		Size:      result.Size,
		Truncated: result.Truncated,
	}, nil
}

func (g *GitOpsImpl) GitDiffFile(ctx context.Context, worktreePath, path, from, to string) (ops.GitBoundedTextResult, error) {
	result, err := git.NewGitInspector().Diff(ctx, worktreePath, path, from, to)
	return ops.GitBoundedTextResult{Output: string(result.Output), Partial: result.Partial, LimitHit: result.LimitHit}, err
}

func (g *GitOpsImpl) GitLogFile(ctx context.Context, worktreePath, path string, limit int) (ops.GitBoundedTextResult, error) {
	result, err := git.NewGitInspector().Log(ctx, worktreePath, path, limit)
	return ops.GitBoundedTextResult{Output: string(result.Output), Partial: result.Partial, LimitHit: result.LimitHit}, err
}

func (g *GitOpsImpl) GitBlamePorcelain(ctx context.Context, worktreePath, path string) (ops.GitBoundedTextResult, error) {
	result, err := git.NewGitInspector().Blame(ctx, worktreePath, path)
	return ops.GitBoundedTextResult{Output: string(result.Output), Partial: result.Partial, LimitHit: result.LimitHit}, err
}

func (g *GitOpsImpl) ResolveLoomDataDir() (string, error) {
	dir := config.GetConfigDir()
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("cannot resolve loom data directory")
	}
	return filepath.Abs(dir)
}

func (g *GitOpsImpl) GitCurrentBranch(ctx context.Context, worktreePath string) (string, error) {
	return git.NewGitInspector().CurrentBranch(ctx, worktreePath)
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

func (g *GitOpsImpl) DiffCommits(ctx context.Context, worktreePath, mergeBase string, limit int) ([]ops.DiffCommitResult, error) {
	return git.DiffCommits(ctx, worktreePath, mergeBase, limit)
}

func (g *GitOpsImpl) DiffFiles(ctx context.Context, worktreePath, from, to string) ([]ops.DiffFileResult, error) {
	return git.DiffFiles(ctx, worktreePath, from, to)
}

func (g *GitOpsImpl) DiffFilePatch(ctx context.Context, worktreePath, from, to, path string) (*ops.DiffFilePatchResult, error) {
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
