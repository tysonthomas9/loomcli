package workspacemgr

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	storepkg "github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/workspaceerrors"
)

// BuildStoreBackedCreateWorkspace returns a create function for fleet-db store
// mode. Existing-dir ("empty") creation writes workspace/repo metadata to the
// store as the source of truth and records only local checkout paths in
// ~/.loom/state.json.
func BuildStoreBackedCreateWorkspace(s storepkg.Store) service.WorkspaceCreateFn {
	if s == nil {
		return nil
	}
	return func(ctx context.Context, req service.WorkspaceCreateRequest) (service.WorkspaceCreateResult, error) {
		if req.Type == "clone" {
			return createStoreBackedCloneWorkspace(ctx, s, req)
		}
		return createStoreBackedEmptyWorkspace(ctx, s, req)
	}
}

// BuildStoreBackedAddRepos returns a repo attachment function for fleet-db
// store mode. It creates git worktrees, then registers those repos in the
// store and local state cache as one rollback-aware operation.
func BuildStoreBackedAddRepos(s storepkg.Store) service.WorkspaceAddReposFn {
	if s == nil {
		return nil
	}
	return func(ctx context.Context, req service.WorkspaceAddReposRequest) (service.WorkspaceCreateResult, error) {
		return addReposToStoreBackedWorkspace(ctx, s, req)
	}
}

//nolint:cyclop,funlen,gocognit // Orchestrates filesystem, git, and store rollback steps for one workflow.
func createStoreBackedEmptyWorkspace(ctx context.Context, s storepkg.Store, req service.WorkspaceCreateRequest) (service.WorkspaceCreateResult, error) {
	if req.Type != "empty" {
		return service.WorkspaceCreateResult{}, fmt.Errorf("unsupported workspace type: %s", req.Type)
	}
	if existing, err := s.Workspaces().GetByName(ctx, req.Name); err == nil && existing != nil {
		return service.WorkspaceCreateResult{}, workspaceerrors.New(workspaceerrors.AlreadyExists, fmt.Sprintf("workspace %q already exists", req.Name), nil)
	} else if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return service.WorkspaceCreateResult{}, fmt.Errorf("check workspace name: %w", err)
	}

	wsPlan, err := resolveWorkspaceDirForCreate(req.Path, req.Name)
	if err != nil {
		return service.WorkspaceCreateResult{}, err
	}
	wsDir := wsPlan.path
	var resolved []resolvedRepo
	if len(req.Repos) > 0 {
		resolved, err = resolveRepoPaths(req.Repos)
		if err != nil {
			return service.WorkspaceCreateResult{}, err
		}
	}

	branch := req.Branch
	if branch == "" {
		branch = req.Name
	}
	key := service.WorkspaceKeyFromName(req.Name)
	if _, err := s.Workspaces().Get(ctx, key); err == nil {
		return service.WorkspaceCreateResult{}, workspaceerrors.New(workspaceerrors.AlreadyExists, fmt.Sprintf("workspace %q already exists", req.Name), nil)
	} else if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return service.WorkspaceCreateResult{}, fmt.Errorf("check workspace key: %w", err)
	}

	if err := os.MkdirAll(wsDir, 0755); err != nil {
		return service.WorkspaceCreateResult{}, fmt.Errorf("cannot create workspace directory: %w", err)
	}
	var created []createdWorktree
	var repos []config.RepoConfig
	if len(resolved) > 0 {
		created, repos, err = addWorktrees(ctx, resolved, wsDir, branch)
		if err != nil {
			cleanupWorktrees(wsPlan, created)
			return service.WorkspaceCreateResult{}, err
		}
	}

	if _, err := s.Workspaces().Create(ctx, storepkg.WorkspaceCreate{
		Key:           key,
		Name:          req.Name,
		DefaultBranch: branch,
	}); err != nil {
		cleanupWorktrees(wsPlan, created)
		if errors.Is(err, domain.ErrAlreadyExists) {
			return service.WorkspaceCreateResult{}, workspaceerrors.New(workspaceerrors.AlreadyExists, fmt.Sprintf("workspace %q already exists", req.Name), err)
		}
		return service.WorkspaceCreateResult{}, fmt.Errorf("create workspace in store: %w", err)
	}
	rollbackStore := func() {
		deleteLocalWorkspaceState(key)
		if err := s.Workspaces().Delete(context.Background(), key); err != nil && !errors.Is(err, domain.ErrNotFound) {
			slog.Warn("failed to rollback store workspace create", "workspace", key, "err", err)
		}
	}
	if err := seedBuiltInRoles(ctx, s, key); err != nil {
		rollbackStore()
		cleanupWorktrees(wsPlan, created)
		return service.WorkspaceCreateResult{}, fmt.Errorf("seed built-in roles: %w", err)
	}

	for _, r := range repos {
		remoteName := r.Remote
		if remoteName == "" {
			remoteName = "origin"
		}
		if _, err := s.Repos().Create(ctx, storepkg.RepoCreate{
			WorkspaceKey:  key,
			Name:          r.Name,
			RemoteURL:     gitRemoteURL(r.Path, remoteName),
			Remote:        remoteName,
			DefaultBranch: branch,
			SourceRepoID:  r.SourceRepoID,
		}); err != nil {
			rollbackStore()
			cleanupWorktrees(wsPlan, created)
			return service.WorkspaceCreateResult{}, fmt.Errorf("create repo %q in store: %w", r.Name, err)
		}
	}

	if err := saveLocalWorkspaceState(key, wsDir, repos, true); err != nil {
		rollbackStore()
		cleanupWorktrees(wsPlan, created)
		return service.WorkspaceCreateResult{}, err
	}
	if err := updateStoreWorkspaceState(ctx, s, key, domain.WorkspaceStateReady); err != nil {
		rollbackStore()
		cleanupWorktrees(wsPlan, created)
		return service.WorkspaceCreateResult{}, fmt.Errorf("mark workspace ready: %w", err)
	}

	return service.WorkspaceCreateResult{WorkspaceID: key, WorkspacePath: wsDir}, nil
}

//nolint:cyclop,funlen // Coordinates local git worktrees, fleet-db repo records, and local state rollback.
func addReposToStoreBackedWorkspace(ctx context.Context, s storepkg.Store, req service.WorkspaceAddReposRequest) (service.WorkspaceCreateResult, error) {
	key, ws, err := resolveWorkspaceForAddRepos(ctx, s, req.WorkspaceID)
	if err != nil {
		return service.WorkspaceCreateResult{}, err
	}
	wsDir, err := prepareWorkspaceDir(key)
	if err != nil {
		return service.WorkspaceCreateResult{}, err
	}

	resolved, err := resolveRequestRepos(req.Repos)
	if err != nil {
		return service.WorkspaceCreateResult{}, err
	}
	seen, err := dedupAddReposAgainstExisting(ctx, s, key, resolved)
	if err != nil {
		return service.WorkspaceCreateResult{}, err
	}

	branch := pickAddReposBranch(req.Branch, ws, key)

	created, repos, err := materializeAddReposWorktrees(ctx, resolved, wsDir, branch)
	if err != nil {
		return service.WorkspaceCreateResult{}, err
	}
	clonedRepos, err := materializeAddReposClones(ctx, req.CloneURLs, wsDir, seen, created)
	if err != nil {
		return service.WorkspaceCreateResult{}, err
	}
	repos = append(repos, clonedRepos...)

	if err := persistAddReposRecords(ctx, s, key, wsDir, branch, repos, created, clonedRepos); err != nil {
		return service.WorkspaceCreateResult{}, err
	}
	return service.WorkspaceCreateResult{WorkspaceID: key, WorkspacePath: wsDir}, nil
}

// resolveWorkspaceForAddRepos looks up the workspace by ID, then falls back
// to lookup-by-name so callers can pass either. Returns the canonical key
// (which may differ from the input when matched by name).
func resolveWorkspaceForAddRepos(ctx context.Context, s storepkg.Store, workspaceID string) (string, *domain.Workspace, error) {
	key := strings.TrimSpace(workspaceID)
	if key == "" {
		return "", nil, workspaceerrors.New(workspaceerrors.PathNotFound, "workspace ID is required", nil)
	}
	ws, err := s.Workspaces().Get(ctx, key)
	if err == nil {
		return key, ws, nil
	}
	byName, byNameErr := s.Workspaces().GetByName(ctx, key)
	if byNameErr != nil {
		return "", nil, fmt.Errorf("load workspace %q: %w", workspaceID, err)
	}
	return byName.Key, byName, nil
}

// prepareWorkspaceDir resolves the workspace's on-disk path, validates it,
// and ensures the directory exists.
func prepareWorkspaceDir(key string) (string, error) {
	wsDir, err := localWorkspacePath(key)
	if err != nil {
		return "", err
	}
	if err := validateWorkspacePath(wsDir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(wsDir, 0755); err != nil {
		return "", fmt.Errorf("cannot create workspace directory: %w", err)
	}
	return wsDir, nil
}

func resolveRequestRepos(reqRepos []string) ([]resolvedRepo, error) {
	if len(reqRepos) == 0 {
		return nil, nil
	}
	return resolveRepoPaths(reqRepos)
}

// dedupAddReposAgainstExisting builds the set of repo names already present
// in the workspace, then verifies the requested repos don't collide. Returns
// the merged seen-set so downstream clone steps can extend it.
func dedupAddReposAgainstExisting(ctx context.Context, s storepkg.Store, key string, resolved []resolvedRepo) (map[string]bool, error) {
	existing, err := s.Repos().List(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("list workspace repos: %w", err)
	}
	seen := make(map[string]bool, len(existing)+len(resolved))
	for _, r := range existing {
		seen[r.Name] = true
	}
	for _, r := range resolved {
		if seen[r.name] {
			return nil, workspaceerrors.New(workspaceerrors.AlreadyExists, fmt.Sprintf("repo %q already exists in workspace %q", r.name, key), nil)
		}
		seen[r.name] = true
	}
	return seen, nil
}

// pickAddReposBranch resolves the target branch using the precedence
// request → workspace default → workspace name → workspace key. Same fallbacks
// the inline code used; lifted out so the outer function isn't paying the
// cognitive cost of three sequential ifs.
func pickAddReposBranch(reqBranch string, ws *domain.Workspace, key string) string {
	if reqBranch != "" {
		return reqBranch
	}
	if ws.DefaultBranch != "" {
		return ws.DefaultBranch
	}
	if ws.Name != "" {
		return ws.Name
	}
	return key
}

// materializeAddReposWorktrees attaches a worktree for each resolved repo,
// rolling back partially-attached worktrees on failure.
func materializeAddReposWorktrees(ctx context.Context, resolved []resolvedRepo, wsDir, branch string) ([]createdWorktree, []config.RepoConfig, error) {
	if len(resolved) == 0 {
		return nil, nil, nil
	}
	created, repos, err := addWorktrees(ctx, resolved, wsDir, branch)
	if err != nil {
		cleanupAttachedWorktrees(created)
		return nil, nil, err
	}
	return created, repos, nil
}

// materializeAddReposClones clones any --clone-url repos under the workspace
// directory, rolling back previously-attached worktrees on failure.
func materializeAddReposClones(ctx context.Context, cloneURLs []string, wsDir string, seen map[string]bool, created []createdWorktree) ([]config.RepoConfig, error) {
	if len(cloneURLs) == 0 {
		return nil, nil
	}
	cloned, err := cloneReposWithSeen(ctx, cloneURLs, wsDir, seen)
	if err != nil {
		cleanupAttachedWorktrees(created)
		return nil, err
	}
	return cloned, nil
}

// persistAddReposRecords writes the fleet-db repo records for each new
// repo and saves the local-state file. On any failure it rolls back the
// store records, attached worktrees, and clone directories so the caller
// is left with the pre-call state.
func persistAddReposRecords(ctx context.Context, s storepkg.Store, key, wsDir, branch string, repos []config.RepoConfig, created []createdWorktree, clonedRepos []config.RepoConfig) error {
	var storeRepos []string
	rollback := func() {
		for _, name := range storeRepos {
			if err := s.Repos().Delete(context.Background(), key, name); err != nil && !errors.Is(err, domain.ErrNotFound) {
				slog.Warn("failed to rollback store repo create", "workspace", key, "repo", name, "err", err)
			}
		}
		cleanupAttachedWorktrees(created)
		cleanupClonedRepos(clonedRepos)
	}

	for _, r := range repos {
		if err := createStoreRepo(ctx, s, key, branch, r); err != nil {
			rollback()
			return err
		}
		storeRepos = append(storeRepos, r.Name)
	}
	if err := saveLocalWorkspaceState(key, wsDir, repos, true); err != nil {
		rollback()
		return err
	}
	return nil
}

func createStoreRepo(ctx context.Context, s storepkg.Store, key, branch string, r config.RepoConfig) error {
	remoteName := r.Remote
	if remoteName == "" {
		remoteName = "origin"
	}
	if _, err := s.Repos().Create(ctx, storepkg.RepoCreate{
		WorkspaceKey:  key,
		Name:          r.Name,
		RemoteURL:     gitRemoteURL(r.Path, remoteName),
		Remote:        remoteName,
		DefaultBranch: branch,
		SourceRepoID:  r.SourceRepoID,
	}); err != nil {
		return fmt.Errorf("create repo %q in store: %w", r.Name, err)
	}
	return nil
}

func localWorkspacePath(key string) (string, error) {
	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		return "", fmt.Errorf("load local workspace state: %w", err)
	}
	if sc != nil {
		if local, ok := sc.Workspaces[key]; ok && strings.TrimSpace(local.Path) != "" {
			return local.Path, nil
		}
	}
	return "", workspaceerrors.New(workspaceerrors.PathNotFound, fmt.Sprintf("workspace %q has no local path; open it on this machine before adding repos", key), nil)
}

func gitRemoteURL(repoPath, remote string) string {
	if remote == "" {
		remote = "origin"
	}
	out, err := cli.RunGitCommand(repoPath, "remote", "get-url", remote)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

//nolint:cyclop,funlen // Orchestrates clone lifecycle state, filesystem cleanup, and store writes.
func createStoreBackedCloneWorkspace(ctx context.Context, s storepkg.Store, req service.WorkspaceCreateRequest) (service.WorkspaceCreateResult, error) {
	cloneURLs := req.CloneURLs
	if len(cloneURLs) == 0 {
		return service.WorkspaceCreateResult{}, workspaceerrors.New(workspaceerrors.PathNotFound, "no clone URLs specified", nil)
	}
	if existing, err := s.Workspaces().GetByName(ctx, req.Name); err == nil && existing != nil {
		return service.WorkspaceCreateResult{}, workspaceerrors.New(workspaceerrors.AlreadyExists, fmt.Sprintf("workspace %q already exists", req.Name), nil)
	} else if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return service.WorkspaceCreateResult{}, fmt.Errorf("check workspace name: %w", err)
	}

	wsPlan, err := resolveWorkspaceDirForCreate(req.Path, req.Name)
	if err != nil {
		return service.WorkspaceCreateResult{}, err
	}
	wsDir := wsPlan.path
	branch := req.Branch
	if branch == "" {
		branch = "main"
	}
	key := service.WorkspaceKeyFromName(req.Name)
	if _, err := s.Workspaces().Get(ctx, key); err == nil {
		return service.WorkspaceCreateResult{}, workspaceerrors.New(workspaceerrors.AlreadyExists, fmt.Sprintf("workspace %q already exists", req.Name), nil)
	} else if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return service.WorkspaceCreateResult{}, fmt.Errorf("check workspace key: %w", err)
	}

	if _, err := s.Workspaces().Create(ctx, storepkg.WorkspaceCreate{
		Key:           key,
		Name:          req.Name,
		DefaultBranch: branch,
	}); err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			return service.WorkspaceCreateResult{}, workspaceerrors.New(workspaceerrors.AlreadyExists, fmt.Sprintf("workspace %q already exists", req.Name), err)
		}
		return service.WorkspaceCreateResult{}, fmt.Errorf("create workspace in store: %w", err)
	}
	rollbackStore := func() {
		deleteLocalWorkspaceState(key)
		if err := s.Workspaces().Delete(context.Background(), key); err != nil && !errors.Is(err, domain.ErrNotFound) {
			slog.Warn("failed to rollback store clone workspace create", "workspace", key, "err", err)
		}
	}
	if err := seedBuiltInRoles(ctx, s, key); err != nil {
		rollbackStore()
		return service.WorkspaceCreateResult{}, fmt.Errorf("seed built-in roles: %w", err)
	}
	_ = updateStoreWorkspaceState(ctx, s, key, domain.WorkspaceStateCreating)

	if err := os.MkdirAll(wsDir, 0755); err != nil {
		rollbackStore()
		return service.WorkspaceCreateResult{}, fmt.Errorf("cannot create workspace directory: %w", err)
	}

	if err := updateStoreWorkspaceState(ctx, s, key, domain.WorkspaceStateCloning); err != nil {
		cleanupWorkspaceRoot(wsPlan)
		rollbackStore()
		return service.WorkspaceCreateResult{}, fmt.Errorf("mark workspace cloning: %w", err)
	}
	repos, err := cloneRepos(ctx, cloneURLs, wsDir)
	if err != nil {
		cleanupWorkspaceRoot(wsPlan)
		rollbackStore()
		return service.WorkspaceCreateResult{}, err
	}

	if err := updateStoreWorkspaceState(ctx, s, key, domain.WorkspaceStateInitializing); err != nil {
		cleanupCloneWorkspace(wsPlan, repos)
		rollbackStore()
		return service.WorkspaceCreateResult{}, fmt.Errorf("mark workspace initializing: %w", err)
	}
	for _, r := range repos {
		remoteURL := gitRemoteURL(r.Path, "origin")
		if _, err := s.Repos().Create(ctx, storepkg.RepoCreate{
			WorkspaceKey:  key,
			Name:          r.Name,
			RemoteURL:     remoteURL,
			DefaultBranch: branch,
			SourceRepoID:  r.SourceRepoID,
		}); err != nil {
			cleanupCloneWorkspace(wsPlan, repos)
			rollbackStore()
			return service.WorkspaceCreateResult{}, fmt.Errorf("create repo %q in store: %w", r.Name, err)
		}
	}
	if err := saveLocalWorkspaceState(key, wsDir, repos, true); err != nil {
		cleanupCloneWorkspace(wsPlan, repos)
		rollbackStore()
		return service.WorkspaceCreateResult{}, err
	}
	if err := updateStoreWorkspaceState(ctx, s, key, domain.WorkspaceStateReady); err != nil {
		cleanupCloneWorkspace(wsPlan, repos)
		rollbackStore()
		return service.WorkspaceCreateResult{}, fmt.Errorf("mark workspace ready: %w", err)
	}

	return service.WorkspaceCreateResult{WorkspaceID: key, WorkspacePath: wsDir}, nil
}

func updateStoreWorkspaceState(ctx context.Context, s storepkg.Store, key string, state domain.WorkspaceState) error {
	msg := ""
	_, err := s.Workspaces().Update(ctx, key, storepkg.WorkspaceUpdate{
		State:        &state,
		ErrorMessage: &msg,
	})
	return err
}

// seedBuiltInRoles creates the domain.BuiltinRoleNames set — the two lists
// must stay in step, since delete guards consult the domain list.
func seedBuiltInRoles(ctx context.Context, s storepkg.Store, key string) error {
	roles := []storepkg.RoleCreate{
		{
			WorkspaceKey: key,
			Name:         "plan",
			Description:  "Planning agent",
			TaskFilter:   "needs_plan",
			ReadOnly:     true,
		},
		{
			WorkspaceKey: key,
			Name:         "task",
			Description:  "Task implementation agent",
			TaskFilter:   "has_design",
		},
		{
			WorkspaceKey: key,
			Name:         "lead",
			Description:  "Lead/orchestrator terminal",
		},
	}
	for _, role := range roles {
		if _, err := s.Roles().Create(ctx, role); err != nil {
			return fmt.Errorf("create role %q: %w", role.Name, err)
		}
	}
	return nil
}

func saveLocalWorkspaceState(key, wsDir string, repos []config.RepoConfig, makeActive bool) error {
	if key == "" {
		return errors.New("save local workspace state: key must not be empty")
	}
	if err := bootstrap.MutateWorkspaceLocalState(key, func(local *bootstrap.WorkspaceLocalState) error {
		local.Path = wsDir
		if local.Repos == nil {
			local.Repos = make(map[string]string, len(repos))
		}
		for _, r := range repos {
			local.Repos[r.Name] = r.Path
		}
		if local.Agents == nil {
			local.Agents = make(map[string]bootstrap.AgentLocalState)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("save local workspace state: %w", err)
	}
	if makeActive {
		return bootstrap.SetActiveWorkspaceKey(key)
	}
	return nil
}

func deleteLocalWorkspaceState(key string) {
	if key == "" {
		return
	}
	if err := bootstrap.WithStateLock(func() error {
		sc, err := bootstrap.LoadStateCache()
		if err != nil {
			return fmt.Errorf("load local workspace state: %w", err)
		}
		delete(sc.Workspaces, key)
		if sc.LastWorkspace == key {
			sc.LastWorkspace = ""
		}
		return bootstrap.SaveStateCache(sc)
	}); err != nil {
		slog.Warn("failed to rollback local workspace state", "workspace", key, "err", err)
	}
}
