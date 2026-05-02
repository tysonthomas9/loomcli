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
// ~/.loom/state.json. Clone creation still uses the legacy flow until the clone
// parity ticket moves disk cloning to store-primary semantics.
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

	wsDir, err := resolveSecureWorkspaceDir(req.Path, req.Name)
	if err != nil {
		return service.WorkspaceCreateResult{}, err
	}
	if err := validateWorkspacePath(wsDir); err != nil {
		return service.WorkspaceCreateResult{}, err
	}
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
			cleanupWorktrees(wsDir, created)
			return service.WorkspaceCreateResult{}, err
		}
	}

	if _, err := s.Workspaces().Create(ctx, storepkg.WorkspaceCreate{
		Key:           key,
		Name:          req.Name,
		DefaultBranch: branch,
	}); err != nil {
		cleanupWorktrees(wsDir, created)
		return service.WorkspaceCreateResult{}, fmt.Errorf("create workspace in store: %w", err)
	}
	storeCreated := true
	rollbackStore := func() {
		if storeCreated {
			if err := s.Workspaces().Delete(context.Background(), key); err != nil && !errors.Is(err, domain.ErrNotFound) {
				slog.Warn("failed to rollback store workspace create", "workspace", key, "err", err)
			}
		}
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
			cleanupWorktrees(wsDir, created)
			return service.WorkspaceCreateResult{}, fmt.Errorf("create repo %q in store: %w", r.Name, err)
		}
	}

	if err := saveLocalWorkspaceState(key, wsDir, repos, true); err != nil {
		rollbackStore()
		cleanupWorktrees(wsDir, created)
		return service.WorkspaceCreateResult{}, err
	}
	if err := updateStoreWorkspaceState(ctx, s, key, domain.WorkspaceStateReady); err != nil {
		rollbackStore()
		cleanupWorktrees(wsDir, created)
		return service.WorkspaceCreateResult{}, fmt.Errorf("mark workspace ready: %w", err)
	}

	return service.WorkspaceCreateResult{WorkspaceID: key, WorkspacePath: wsDir}, nil
}

//nolint:cyclop,funlen // Coordinates local git worktrees, fleet-db repo records, and local state rollback.
func addReposToStoreBackedWorkspace(ctx context.Context, s storepkg.Store, req service.WorkspaceAddReposRequest) (service.WorkspaceCreateResult, error) {
	key := strings.TrimSpace(req.WorkspaceID)
	if key == "" {
		return service.WorkspaceCreateResult{}, workspaceerrors.New(workspaceerrors.PathNotFound, "workspace ID is required", nil)
	}
	ws, err := s.Workspaces().Get(ctx, key)
	if err != nil {
		if byName, byNameErr := s.Workspaces().GetByName(ctx, key); byNameErr == nil {
			ws = byName
			key = byName.Key
		} else {
			return service.WorkspaceCreateResult{}, fmt.Errorf("load workspace %q: %w", req.WorkspaceID, err)
		}
	}

	wsDir, err := localWorkspacePath(key)
	if err != nil {
		return service.WorkspaceCreateResult{}, err
	}
	if err := validateWorkspacePath(wsDir); err != nil {
		return service.WorkspaceCreateResult{}, err
	}
	if err := os.MkdirAll(wsDir, 0755); err != nil {
		return service.WorkspaceCreateResult{}, fmt.Errorf("cannot create workspace directory: %w", err)
	}

	resolved, err := resolveRepoPaths(req.Repos)
	if err != nil {
		return service.WorkspaceCreateResult{}, err
	}

	existing, err := s.Repos().List(ctx, key)
	if err != nil {
		return service.WorkspaceCreateResult{}, fmt.Errorf("list workspace repos: %w", err)
	}
	seen := make(map[string]bool, len(existing))
	for _, r := range existing {
		seen[r.Name] = true
	}
	for _, r := range resolved {
		if seen[r.name] {
			return service.WorkspaceCreateResult{}, workspaceerrors.New(workspaceerrors.AlreadyExists, fmt.Sprintf("repo %q already exists in workspace %q", r.name, key), nil)
		}
	}

	branch := req.Branch
	if branch == "" {
		branch = ws.DefaultBranch
	}
	if branch == "" {
		branch = ws.Name
	}
	if branch == "" {
		branch = key
	}

	created, repos, err := addWorktrees(ctx, resolved, wsDir, branch)
	if err != nil {
		cleanupAttachedWorktrees(created)
		return service.WorkspaceCreateResult{}, err
	}

	var storeRepos []string
	rollbackStoreRepos := func() {
		for _, name := range storeRepos {
			if err := s.Repos().Delete(context.Background(), key, name); err != nil && !errors.Is(err, domain.ErrNotFound) {
				slog.Warn("failed to rollback store repo create", "workspace", key, "repo", name, "err", err)
			}
		}
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
			rollbackStoreRepos()
			cleanupAttachedWorktrees(created)
			return service.WorkspaceCreateResult{}, fmt.Errorf("create repo %q in store: %w", r.Name, err)
		}
		storeRepos = append(storeRepos, r.Name)
	}

	if err := saveLocalWorkspaceState(key, wsDir, repos, true); err != nil {
		rollbackStoreRepos()
		cleanupAttachedWorktrees(created)
		return service.WorkspaceCreateResult{}, err
	}

	return service.WorkspaceCreateResult{WorkspaceID: key, WorkspacePath: wsDir}, nil
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
	if len(cloneURLs) == 0 && req.CloneURL != "" {
		cloneURLs = []string{req.CloneURL}
	}
	if len(cloneURLs) == 0 {
		return service.WorkspaceCreateResult{}, workspaceerrors.New(workspaceerrors.PathNotFound, "no clone URLs specified", nil)
	}
	if existing, err := s.Workspaces().GetByName(ctx, req.Name); err == nil && existing != nil {
		return service.WorkspaceCreateResult{}, workspaceerrors.New(workspaceerrors.AlreadyExists, fmt.Sprintf("workspace %q already exists", req.Name), nil)
	} else if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return service.WorkspaceCreateResult{}, fmt.Errorf("check workspace name: %w", err)
	}

	wsDir, err := resolveSecureWorkspaceDir(req.Path, req.Name)
	if err != nil {
		return service.WorkspaceCreateResult{}, err
	}
	if err := validateWorkspacePath(wsDir); err != nil {
		return service.WorkspaceCreateResult{}, err
	}
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
		return service.WorkspaceCreateResult{}, fmt.Errorf("create workspace in store: %w", err)
	}
	rollbackStore := func() {
		deleteLocalWorkspaceState(key)
		if err := s.Workspaces().Delete(context.Background(), key); err != nil && !errors.Is(err, domain.ErrNotFound) {
			slog.Warn("failed to rollback store clone workspace create", "workspace", key, "err", err)
		}
	}
	_ = updateStoreWorkspaceState(ctx, s, key, domain.WorkspaceStateCreating)

	if err := os.MkdirAll(wsDir, 0755); err != nil {
		rollbackStore()
		return service.WorkspaceCreateResult{}, fmt.Errorf("cannot create workspace directory: %w", err)
	}

	if err := updateStoreWorkspaceState(ctx, s, key, domain.WorkspaceStateCloning); err != nil {
		cleanupCloneDir(wsDir)
		rollbackStore()
		return service.WorkspaceCreateResult{}, fmt.Errorf("mark workspace cloning: %w", err)
	}
	repos, err := cloneRepos(ctx, cloneURLs, wsDir)
	if err != nil {
		cleanupCloneDir(wsDir)
		rollbackStore()
		return service.WorkspaceCreateResult{}, err
	}

	if err := updateStoreWorkspaceState(ctx, s, key, domain.WorkspaceStateInitializing); err != nil {
		cleanupCloneDir(wsDir)
		rollbackStore()
		return service.WorkspaceCreateResult{}, fmt.Errorf("mark workspace initializing: %w", err)
	}
	for i, r := range repos {
		remoteURL := ""
		if i < len(cloneURLs) {
			remoteURL = cloneURLs[i]
		}
		if _, err := s.Repos().Create(ctx, storepkg.RepoCreate{
			WorkspaceKey:  key,
			Name:          r.Name,
			RemoteURL:     remoteURL,
			DefaultBranch: branch,
			SourceRepoID:  r.SourceRepoID,
		}); err != nil {
			cleanupCloneDir(wsDir)
			rollbackStore()
			return service.WorkspaceCreateResult{}, fmt.Errorf("create repo %q in store: %w", r.Name, err)
		}
	}
	if err := saveLocalWorkspaceState(key, wsDir, repos, true); err != nil {
		cleanupCloneDir(wsDir)
		rollbackStore()
		return service.WorkspaceCreateResult{}, err
	}
	if err := updateStoreWorkspaceState(ctx, s, key, domain.WorkspaceStateReady); err != nil {
		cleanupCloneDir(wsDir)
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

func saveLocalWorkspaceState(key, wsDir string, repos []config.RepoConfig, makeActive bool) error {
	if key == "" {
		return errors.New("save local workspace state: key must not be empty")
	}
	return bootstrap.WithStateLock(func() error {
		sc, err := bootstrap.LoadStateCache()
		if err != nil {
			return fmt.Errorf("load local workspace state: %w", err)
		}
		if sc.Workspaces == nil {
			sc.Workspaces = make(map[string]bootstrap.WorkspaceLocalState)
		}
		local := sc.Workspaces[key]
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
		sc.Workspaces[key] = local
		if makeActive {
			sc.LastWorkspace = key
		}
		if err := bootstrap.SaveStateCache(sc); err != nil {
			return fmt.Errorf("save local workspace state: %w", err)
		}
		return nil
	})
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
