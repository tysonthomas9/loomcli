package workspacemgr

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/workspace"
	"github.com/tysonthomas9/loomcli/internal/domain"
	storepkg "github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/workspaceerrors"
)

// mirrorWorkspaceToStore writes a freshly-created workspace + its repos
// to the fleet-db store. Best-effort: fleet-db unavailability or schema
// errors log a warning but do not fail the create — the legacy yaml
// path remains the source of truth during the migration window.
//
// wsKey must satisfy fleet-db's `^[A-Z]([A-Z0-9-]{0,30}[A-Z0-9])?$`
// regex; non-conforming workspace names skip the mirror entirely (the
// noun-verb commands pre-validate, but legacy `loom workspace create`
// accepts arbitrary names).
func mirrorWorkspaceToStore(wsName string, wsID string, repos []config.RepoConfig) {
	if !isValidStoreKey(wsID) && !isValidStoreKey(wsName) {
		slog.Debug("skipping store mirror: workspace name not a valid fleet-db key",
			"workspace", wsName, "id", wsID)
		return
	}
	key := wsID
	if !isValidStoreKey(key) {
		key = wsName
	}
	ctx, cancel := cmdstore.SignalContext()
	defer cancel()
	h, err := cmdstore.OpenStore(ctx)
	if err != nil {
		slog.Debug("store mirror skipped (open store)", "workspace", wsName, "err", err)
		return
	}
	defer func() { _ = h.Close() }()
	if _, err := h.Store.Workspaces().Create(ctx, storepkg.WorkspaceCreate{
		Key:           key,
		Name:          wsName,
		DefaultBranch: "main",
	}); err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
		slog.Debug("store mirror failed (workspace create)", "workspace", wsName, "err", err)
		return
	}
	for _, r := range repos {
		if _, err := h.Store.Repos().Create(ctx, storepkg.RepoCreate{
			WorkspaceKey: key,
			Name:         r.Name,
			SourceRepoID: r.SourceRepoID,
		}); err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
			slog.Debug("store mirror failed (repo create)",
				"workspace", wsName, "repo", r.Name, "err", err)
		}
	}
}

// mirrorWorkspaceDelete removes a workspace from the fleet-db store
// when the legacy yaml-backed delete path runs. Best-effort.
func mirrorWorkspaceDelete(wsName, wsID string) {
	key := wsID
	if !isValidStoreKey(key) {
		key = wsName
	}
	if !isValidStoreKey(key) {
		return
	}
	ctx, cancel := cmdstore.SignalContext()
	defer cancel()
	h, err := cmdstore.OpenStore(ctx)
	if err != nil {
		slog.Debug("store mirror delete skipped (open store)", "workspace", wsName, "err", err)
		return
	}
	defer func() { _ = h.Close() }()
	if err := h.Store.Workspaces().Delete(ctx, key); err != nil && !errors.Is(err, domain.ErrNotFound) {
		slog.Debug("store mirror delete failed", "workspace", wsName, "err", err)
	}
}

// isValidStoreKey reports whether s satisfies fleet-db's workspace key
// regex `^[A-Z]([A-Z0-9-]{0,30}[A-Z0-9])?$`.
func isValidStoreKey(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if r < 'A' || r > 'Z' {
				return false
			}
			continue
		}
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return s[len(s)-1] != '-'
}

// DeleteWorkspace removes a workspace from config without deleting git worktrees.
// Returns an error if the workspace is not found or has running agents.
//
// Phase 4 of the loom -> fleet-db migration: this function continues to
// be the legacy yaml writer; on success it also fires a best-effort
// store mirror delete so the fleet-db view stays in sync.
func DeleteWorkspace(name string) error {
	var deletedID string
	err := config.WithConfigLock(func() error {
		cfg, err := config.LoadConfigUnlocked()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		if cfg == nil || len(cfg.Workspaces) == 0 {
			return fmt.Errorf("workspace %q not found", name)
		}

		ws, ok := cfg.Workspaces[name]
		if !ok {
			return fmt.Errorf("workspace %q not found", name)
		}
		deletedID = ws.ID

		if err := checkNoRunningAgents(ws, name); err != nil {
			return err
		}

		removeWorkspaceFromConfig(cfg, name)

		if err := config.SaveConfigUnlocked(cfg); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}
		return nil
	})
	if err == nil {
		mirrorWorkspaceDelete(name, deletedID)
	}
	return err
}

// checkNoRunningAgents returns an error if any repo in the workspace has an active agent lock.
func checkNoRunningAgents(ws config.WorkspaceConfig, name string) error {
	for _, repo := range ws.Repos {
		repoPath := repo.Path
		if !filepath.IsAbs(repoPath) {
			repoPath = filepath.Join(ws.Path, repoPath)
		}
		lockPath := filepath.Join(repoPath, cli.LockFileName)
		if _, err := os.Stat(lockPath); err == nil {
			return fmt.Errorf("workspace %q has running agents", name)
		}
	}
	return nil
}

// removeWorkspaceFromConfig deletes a workspace and updates ordering and default.
func removeWorkspaceFromConfig(cfg *config.LoomConfig, name string) {
	delete(cfg.Workspaces, name)

	filtered := cfg.WorkspaceOrder[:0]
	for _, n := range cfg.WorkspaceOrder {
		if n != name {
			filtered = append(filtered, n)
		}
	}
	cfg.WorkspaceOrder = filtered

	if cfg.DefaultWorkspace == name {
		cfg.DefaultWorkspace = ""
		names := make([]string, 0, len(cfg.Workspaces))
		for n := range cfg.Workspaces {
			names = append(names, n)
		}
		if len(names) > 0 {
			sort.Strings(names)
			cfg.DefaultWorkspace = names[0]
		}
	}
}

// SetDefaultWorkspace sets the default workspace in config.
// Returns an error if the workspace is not found.
func SetDefaultWorkspace(name string) error {
	return config.WithConfigLock(func() error {
		cfg, err := config.LoadConfigUnlocked()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		if cfg == nil || len(cfg.Workspaces) == 0 {
			return fmt.Errorf("workspace %q not found", name)
		}
		if _, ok := cfg.Workspaces[name]; !ok {
			return fmt.Errorf("workspace %q not found", name)
		}
		cfg.DefaultWorkspace = name
		if err := config.SaveConfigUnlocked(cfg); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}
		return nil
	})
}

// ClearDefaultWorkspace clears the default workspace, reverting to first-workspace behavior.
func ClearDefaultWorkspace() error {
	return config.WithConfigLock(func() error {
		cfg, err := config.LoadConfigUnlocked()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		if cfg == nil {
			return nil
		}
		cfg.DefaultWorkspace = ""
		if len(cfg.Workspaces) > 0 {
			names := make([]string, 0, len(cfg.Workspaces))
			for n := range cfg.Workspaces {
				names = append(names, n)
			}
			sort.Strings(names)
			cfg.DefaultWorkspace = names[0]
		}
		if err := config.SaveConfigUnlocked(cfg); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}
		return nil
	})
}

// CreateWorkspace creates a new workspace from the API request.
// Supports "empty" (git worktree from existing repos) and "clone" (git clone first) types.
// The entire load-check-create-save sequence is serialized under the config lock
// to prevent concurrent creates from clobbering each other.
func CreateWorkspace(ctx context.Context, req service.WorkspaceCreateRequest) (service.WorkspaceCreateResult, error) {
	var result service.WorkspaceCreateResult
	var daemonCfg *config.LoomConfig
	var createdRepos []config.RepoConfig
	var agentNames []string
	err := config.WithConfigLock(func() error {
		cfg, err := loadOrCreateConfig()
		if err != nil {
			return err
		}

		if _, exists := cfg.Workspaces[req.Name]; exists {
			return workspaceerrors.New(workspaceerrors.AlreadyExists, fmt.Sprintf("workspace %q already exists", req.Name), nil)
		}

		wsDir, err := resolveSecureWorkspaceDir(req.Path, req.Name)
		if err != nil {
			return err
		}

		branch := req.Branch
		if branch == "" {
			branch = req.Name
		}

		result, createdRepos, agentNames, err = dispatchWorkspaceCreate(ctx, cfg, req, wsDir, branch)
		daemonCfg = cfg
		return err
	})

	// Start daemon AFTER config lock is released to prevent deadlock.
	// The daemon start goroutine polls for the socket which can take 30+ seconds;
	// holding the lock during that time blocks all other config operations.
	// Copy the timeout value before launching the goroutine to avoid racing
	// on the config pointer which may be reloaded by concurrent callers.
	if err == nil && result.DeferDaemonStart {
		daemonTimeout := daemonCfg.Daemon.GetStartupTimeout(workspace.DefaultDaemonStartupTimeout)
		startDaemonAsync(daemonTimeout, req.Name, result.WorkspacePath, createdRepos, agentNames)
		// Best-effort fleet-db mirror so the new workspace surfaces in
		// store-driven endpoints (Phase 4 migration).
		mirrorWorkspaceToStore(req.Name, result.WorkspaceID, createdRepos)
	}

	return result, err
}

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

//nolint:cyclop,funlen // Orchestrates filesystem, git, and store rollback steps for one workflow.
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
	if err := updateStoreWorkspaceState(ctx, s, key, domain.WorkspaceStateReady, ""); err != nil {
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
	_ = updateStoreWorkspaceState(ctx, s, key, domain.WorkspaceStateCreating, "")

	markErr := func(msg string) {
		if err := updateStoreWorkspaceState(context.Background(), s, key, domain.WorkspaceStateError, msg); err != nil {
			slog.Warn("failed to mark store workspace error", "workspace", key, "err", err)
		}
	}
	if err := os.MkdirAll(wsDir, 0755); err != nil {
		markErr(fmt.Sprintf("cannot create directory: %v", err))
		return service.WorkspaceCreateResult{}, fmt.Errorf("cannot create workspace directory: %w", err)
	}

	if err := updateStoreWorkspaceState(ctx, s, key, domain.WorkspaceStateCloning, ""); err != nil {
		cleanupCloneDir(wsDir)
		return service.WorkspaceCreateResult{}, fmt.Errorf("mark workspace cloning: %w", err)
	}
	repos, err := cloneRepos(ctx, cloneURLs, wsDir)
	if err != nil {
		cleanupCloneDir(wsDir)
		markErr(err.Error())
		return service.WorkspaceCreateResult{}, err
	}

	if err := updateStoreWorkspaceState(ctx, s, key, domain.WorkspaceStateInitializing, ""); err != nil {
		cleanupCloneDir(wsDir)
		markErr(err.Error())
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
			markErr(err.Error())
			return service.WorkspaceCreateResult{}, fmt.Errorf("create repo %q in store: %w", r.Name, err)
		}
	}
	if err := saveLocalWorkspaceState(key, wsDir, repos, true); err != nil {
		cleanupCloneDir(wsDir)
		markErr(err.Error())
		return service.WorkspaceCreateResult{}, err
	}
	if err := updateStoreWorkspaceState(ctx, s, key, domain.WorkspaceStateReady, ""); err != nil {
		return service.WorkspaceCreateResult{}, fmt.Errorf("mark workspace ready: %w", err)
	}

	return service.WorkspaceCreateResult{WorkspaceID: key, WorkspacePath: wsDir}, nil
}

func updateStoreWorkspaceState(ctx context.Context, s storepkg.Store, key string, state domain.WorkspaceState, msg string) error {
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

// loadOrCreateConfig loads the config, creating a fresh one if nil.
func loadOrCreateConfig() (*config.LoomConfig, error) {
	cfg, err := config.LoadConfigUnlocked()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	if cfg == nil {
		cfg = &config.LoomConfig{Workspaces: make(map[string]config.WorkspaceConfig)}
	}
	if cfg.Workspaces == nil {
		cfg.Workspaces = make(map[string]config.WorkspaceConfig)
	}
	return cfg, nil
}

// resolveSecureWorkspaceDir resolves and validates the workspace directory path.
func resolveSecureWorkspaceDir(reqPath, wsName string) (string, error) {
	wsDir := reqPath
	if wsDir == "" {
		wsDir = config.GetWorkspaceDir(wsName)
	}
	wsDir = filepath.Clean(wsDir)

	allowedBase := filepath.Join(config.GetConfigDir(), "workspaces")
	if !strings.HasPrefix(wsDir, allowedBase+string(filepath.Separator)) && wsDir != allowedBase {
		return "", workspaceerrors.New(workspaceerrors.SecurityViolation, fmt.Sprintf("workspace path must be under %s", allowedBase), nil)
	}
	return wsDir, nil
}

// dispatchWorkspaceCreate dispatches to the appropriate workspace creation strategy.
// Returns the created repos and generated agent names so callers can create
// per-agent worktrees once the daemon is up.
func dispatchWorkspaceCreate(ctx context.Context, cfg *config.LoomConfig, req service.WorkspaceCreateRequest, wsDir, branch string) (service.WorkspaceCreateResult, []config.RepoConfig, []string, error) {
	switch req.Type {
	case "empty":
		return createEmptyWorkspace(ctx, cfg, req.Name, wsDir, branch, req.Repos, config.SaveConfigUnlocked)
	case "clone":
		cloneURLs := req.CloneURLs
		if len(cloneURLs) == 0 && req.CloneURL != "" {
			cloneURLs = []string{req.CloneURL}
		}
		return createCloneWorkspace(ctx, cfg, req.Name, wsDir, cloneURLs, config.SaveConfigUnlocked)
	default:
		return service.WorkspaceCreateResult{}, nil, nil, fmt.Errorf("unsupported workspace type: %s", req.Type)
	}
}

type resolvedRepo struct {
	path string
	name string
}

// resolveRepoPaths validates and resolves a list of repo paths, checking that
// each exists, is a directory, contains a .git directory, and has a unique name.
func resolveRepoPaths(repoPaths []string) ([]resolvedRepo, error) {
	var resolved []resolvedRepo
	seenNames := make(map[string]string)

	for _, rp := range repoPaths {
		rp = strings.TrimSpace(rp)
		if rp == "" {
			continue
		}

		absPath, err := filepath.Abs(rp)
		if err != nil {
			return nil, workspaceerrors.New(workspaceerrors.PathNotFound, fmt.Sprintf("cannot resolve path %q", rp), err)
		}

		info, err := os.Stat(absPath)
		if err != nil {
			return nil, workspaceerrors.New(workspaceerrors.PathNotFound, fmt.Sprintf("repo path does not exist: %s", absPath), err)
		}
		if !info.IsDir() {
			return nil, workspaceerrors.New(workspaceerrors.PathNotFound, fmt.Sprintf("repo path is not a directory: %s", absPath), nil)
		}

		gitDir := filepath.Join(absPath, ".git")
		if _, err := os.Stat(gitDir); err != nil {
			return nil, workspaceerrors.New(workspaceerrors.NotGitRepo, fmt.Sprintf("not a git repository: %s", absPath), err)
		}

		baseName := filepath.Base(absPath)
		if prev, exists := seenNames[baseName]; exists {
			return nil, workspaceerrors.New(workspaceerrors.PathNotFound, fmt.Sprintf("duplicate repo name %q from paths %s and %s", baseName, prev, absPath), nil)
		}
		seenNames[baseName] = absPath
		resolved = append(resolved, resolvedRepo{path: absPath, name: baseName})
	}

	if len(resolved) == 0 {
		return nil, workspaceerrors.New(workspaceerrors.PathNotFound, "no valid repos specified", nil)
	}
	return resolved, nil
}

// createEmptyWorkspace creates worktrees from existing repos.
// The save parameter is the config save function to use (locked or unlocked,
// depending on whether the caller already holds the config lock).
// createdWorktree tracks a worktree that was created during workspace setup for cleanup.
type createdWorktree struct {
	origRepoPath string
	worktreePath string
	branch       string
}

func createEmptyWorkspace(ctx context.Context, cfg *config.LoomConfig, wsName, wsDir, branch string, repoPaths []string, save func(*config.LoomConfig) error) (service.WorkspaceCreateResult, []config.RepoConfig, []string, error) {
	if err := validateWorkspacePath(wsDir); err != nil {
		return service.WorkspaceCreateResult{}, nil, nil, err
	}

	resolved, err := resolveRepoPaths(repoPaths)
	if err != nil {
		return service.WorkspaceCreateResult{}, nil, nil, err
	}

	// Phase 1: persist a creating-state record BEFORE filesystem ops so a crash
	// mid-creation leaves a recoverable surface for RecoverIncompleteWorkspaces.
	wsID, err := beginWorkspaceCreate(cfg, wsName, wsDir, nil, save)
	if err != nil {
		return service.WorkspaceCreateResult{}, nil, nil, err
	}
	markErr := makeErrorMarker(cfg, wsName, save)

	if err := os.MkdirAll(wsDir, 0755); err != nil {
		markErr(fmt.Sprintf("cannot create directory: %v", err))
		return service.WorkspaceCreateResult{}, nil, nil, fmt.Errorf("cannot create workspace directory: %w", err)
	}

	transitionState(cfg, wsName, config.WorkspaceStateCloning, save)

	created, repos, err := addWorktrees(ctx, resolved, wsDir, branch)
	if err != nil {
		cleanupWorktrees(wsDir, created)
		markErr(err.Error())
		return service.WorkspaceCreateResult{}, nil, nil, err
	}

	return finalizeWorkspace(cfg, wsName, wsDir, wsID, repos, created, save)
}

// addWorktrees creates git worktrees for each resolved repo in the workspace directory.
func addWorktrees(ctx context.Context, resolved []resolvedRepo, wsDir, branch string) ([]createdWorktree, []config.RepoConfig, error) {
	var created []createdWorktree
	var repos []config.RepoConfig

	for _, repo := range resolved {
		if ctx.Err() != nil {
			return created, nil, ctx.Err()
		}
		worktreePath := filepath.Join(wsDir, repo.name)
		if _, err := cli.RunGitCommand(repo.path, "worktree", "add", worktreePath, "-b", branch); err != nil {
			return created, nil, workspaceerrors.New(workspaceerrors.GitFailed, fmt.Sprintf("git worktree add failed for %s", repo.name), err)
		}
		created = append(created, createdWorktree{origRepoPath: repo.path, worktreePath: worktreePath, branch: branch})
		repos = append(repos, config.RepoConfig{
			Name:          repo.name,
			Path:          worktreePath,
			Remote:        "origin",
			DefaultBranch: branch,
			SourceRepoID:  repo.name,
		})
	}
	return created, repos, nil
}

// initWorkspaceBeads initializes beads in each repo and at the workspace root,
// then registers repos with relative paths for correct source_repo values.
func initWorkspaceBeads(wsDir string, repos []config.RepoConfig) {
	runner := cli.GetDeps(nil).Exec
	for _, repo := range repos {
		if result := runner.Run(repo.Path, "bd", "init", "--quiet"); result.Err != nil {
			slog.Warn("bd init failed for repo", "repo", repo.Name, "path", repo.Path, "err", result.Err)
		}
	}
	if result := runner.Run(wsDir, "bd", "init", "--quiet"); result.Err != nil {
		slog.Warn("bd init failed for workspace root", "path", wsDir, "err", result.Err)
	}
	for _, repo := range repos {
		if result := runner.Run(wsDir, "bd", "repo", "add", repo.Name); result.Err != nil {
			slog.Warn("failed to add repo to workspace beads", "repo", repo.Name, "err", result.Err)
		}
	}
}

// finalizeWorkspace performs bd init and writes the workspace into config with
// state=initializing. Async daemon startup will transition it to ready.
// Returns the repos and generated agent names so the daemon-start goroutine can
// create per-agent worktrees before running bd repo sync.
func finalizeWorkspace(cfg *config.LoomConfig, wsName, wsDir, wsID string, repos []config.RepoConfig, created []createdWorktree, save func(*config.LoomConfig) error) (service.WorkspaceCreateResult, []config.RepoConfig, []string, error) {
	transitionState(cfg, wsName, config.WorkspaceStateInitializing, save)

	initWorkspaceBeads(wsDir, repos)
	agentNames, err := workspace.WriteLoomYaml(wsDir)
	if err != nil {
		slog.Warn("failed to write loom.yaml for workspace", "workspace", wsName, "err", err)
	}

	// Update existing workspace entry with repos; keep state=initializing.
	ws := cfg.Workspaces[wsName]
	ws.Repos = repos
	cfg.Workspaces[wsName] = ws

	if err := save(cfg); err != nil {
		cleanupWorktrees(wsDir, created)
		// Best-effort: surface the failure on disk so the frontend reflects
		// reality without waiting for a process restart + RecoverIncompleteWorkspaces.
		makeErrorMarker(cfg, wsName, save)(fmt.Sprintf("failed to save config: %v", err))
		return service.WorkspaceCreateResult{}, nil, nil, workspaceerrors.New(workspaceerrors.ConfigFailed, "failed to save config", err)
	}

	return service.WorkspaceCreateResult{WorkspaceID: wsID, WorkspacePath: wsDir, DeferDaemonStart: true}, repos, agentNames, nil
}

// beginWorkspaceCreate persists the workspace with state=creating before any
// filesystem operations. Stores cloneURLs (if any) for future retry support.
// Returns the generated workspace UUID.
func beginWorkspaceCreate(cfg *config.LoomConfig, wsName, wsDir string, cloneURLs []string, save func(*config.LoomConfig) error) (string, error) {
	wsID := config.NewWorkspaceID()
	cfg.Workspaces[wsName] = config.WorkspaceConfig{
		ID: wsID, Path: wsDir, State: config.WorkspaceStateCreating, CloneURLs: cloneURLs,
	}
	if len(cfg.Workspaces) == 1 {
		cfg.DefaultWorkspace = wsName
	}
	if !containsString(cfg.WorkspaceOrder, wsName) {
		cfg.WorkspaceOrder = append([]string{wsName}, cfg.WorkspaceOrder...)
	}
	if err := save(cfg); err != nil {
		delete(cfg.Workspaces, wsName)
		return "", workspaceerrors.New(workspaceerrors.ConfigFailed, "failed to save config", err)
	}
	return wsID, nil
}

// containsString reports whether s appears in xs.
func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// transitionState updates the workspace state in cfg and saves. Logs and
// continues on save failure, rolling back the in-memory mutation so a later
// whole-config save (e.g. finalizeWorkspace) does not silently rewrite the
// failed transition to disk.
func transitionState(cfg *config.LoomConfig, wsName string, state config.WorkspaceState, save func(*config.LoomConfig) error) {
	ws, ok := cfg.Workspaces[wsName]
	if !ok {
		return
	}
	prev := ws.State
	ws.State = state
	cfg.Workspaces[wsName] = ws
	if err := save(cfg); err != nil {
		slog.Error("workspace state transition save failed", "workspace", wsName, "state", state, "err", err)
		ws.State = prev
		cfg.Workspaces[wsName] = ws
		return
	}
	slog.Info("workspace state transition", "workspace", wsName, "state", state)
}

// makeErrorMarker returns a closure that marks the workspace as errored with
// the given message. Used by failure paths during synchronous create.
func makeErrorMarker(cfg *config.LoomConfig, wsName string, save func(*config.LoomConfig) error) func(string) {
	return func(msg string) {
		ws, ok := cfg.Workspaces[wsName]
		if !ok {
			return
		}
		ws.State = config.WorkspaceStateError
		ws.ErrorMessage = msg
		cfg.Workspaces[wsName] = ws
		if err := save(cfg); err != nil {
			slog.Error("failed to mark workspace error", "workspace", wsName, "err", err)
		}
	}
}

// cleanupWorktrees removes created worktrees and the workspace directory on failure.
func cleanupWorktrees(wsDir string, created []createdWorktree) {
	workspace.StopDaemonForWorkspace(cli.GetDeps(nil), wsDir)
	cleanupAttachedWorktrees(created)
	_ = os.RemoveAll(wsDir)
}

func cleanupAttachedWorktrees(created []createdWorktree) {
	for _, c := range created {
		_, _ = cli.RunGitCommand(c.origRepoPath, "worktree", "remove", c.worktreePath)
		if c.branch != "" {
			_, _ = cli.RunGitCommand(c.origRepoPath, "branch", "-D", c.branch)
		}
	}
}

// validateWorkspacePath ensures the workspace directory is under the allowed base.
func validateWorkspacePath(wsDir string) error {
	allowedBase := filepath.Join(config.GetConfigDir(), "workspaces")
	if !strings.HasPrefix(wsDir, allowedBase+string(filepath.Separator)) && wsDir != allowedBase {
		return workspaceerrors.New(workspaceerrors.SecurityViolation, fmt.Sprintf("workspace path must be under %s", allowedBase), nil)
	}
	return nil
}

// startDaemonAsync creates per-agent worktrees, starts the bd daemon for a
// workspace in the background, then syncs repos after the daemon is ready
// (sync can be slow for large repos). Worktree creation runs first because
// it's pure git ops and doesn't need the daemon.
func startDaemonAsync(timeout time.Duration, wsName, wsDir string, repos []config.RepoConfig, agentNames []string) {
	go func() { //nolint:gosec // G118 — intentional: daemon outlives request
		deps := cli.GetDeps(nil)
		// Create agent worktrees first (pure git ops, no daemon needed).
		workspace.CreateAgentWorktrees(wsDir, repos, agentNames)
		if err := workspace.EnsureDaemonForWorkspace(deps, context.Background(), wsDir, timeout); err != nil {
			slog.Warn("failed to start daemon for workspace", "workspace", wsName, "err", err)
		} else if result := deps.Exec.Run(wsDir, "bd", "repo", "sync"); result.Err != nil {
			slog.Warn("bd repo sync failed", "workspace", wsName, "err", result.Err)
		}
		// Mark ready even if daemon/sync failed — the workspace itself is created.
		// Daemon health is surfaced separately; trapping a workspace in
		// "initializing" forever would prevent the user from seeing or fixing it.
		if err := workspace.SetWorkspaceState(wsName, config.WorkspaceStateReady, ""); err != nil {
			slog.Error("failed to mark workspace ready", "workspace", wsName, "err", err)
		}
	}()
}

// repoNameFromURL derives a directory name from a git clone URL.
// e.g. "https://github.com/foo/bar.git" -> "bar"
func repoNameFromURL(cloneURL string) string {
	// Strip trailing .git
	u := strings.TrimSuffix(cloneURL, ".git")
	// Strip trailing slashes
	u = strings.TrimRight(u, "/")
	// Take the last path segment
	if idx := strings.LastIndex(u, "/"); idx >= 0 {
		u = u[idx+1:]
	}
	// For SSH URLs like git@github.com:foo/bar
	if idx := strings.LastIndex(u, ":"); idx >= 0 {
		u = u[idx+1:]
	}
	if u == "" {
		return "repo"
	}
	return u
}

// createCloneWorkspace clones one or more repos and creates a workspace from them.
// The save parameter is the config save function to use (locked or unlocked,
// depending on whether the caller already holds the config lock).
func createCloneWorkspace(ctx context.Context, cfg *config.LoomConfig, wsName, wsDir string, cloneURLs []string, save func(*config.LoomConfig) error) (service.WorkspaceCreateResult, []config.RepoConfig, []string, error) {
	if err := validateWorkspacePath(wsDir); err != nil {
		return service.WorkspaceCreateResult{}, nil, nil, err
	}

	wsID, err := beginWorkspaceCreate(cfg, wsName, wsDir, cloneURLs, save)
	if err != nil {
		return service.WorkspaceCreateResult{}, nil, nil, err
	}
	markErr := makeErrorMarker(cfg, wsName, save)

	if err := os.MkdirAll(wsDir, 0755); err != nil {
		markErr(fmt.Sprintf("cannot create directory: %v", err))
		return service.WorkspaceCreateResult{}, nil, nil, fmt.Errorf("cannot create workspace directory: %w", err)
	}

	transitionState(cfg, wsName, config.WorkspaceStateCloning, save)

	repos, err := cloneRepos(ctx, cloneURLs, wsDir)
	if err != nil {
		cleanupCloneDir(wsDir)
		markErr(err.Error())
		return service.WorkspaceCreateResult{}, nil, nil, err
	}

	return finalizeWorkspace(cfg, wsName, wsDir, wsID, repos, nil, save)
}

// cloneRepos clones each URL into the workspace directory, deduplicating names.
func cloneRepos(ctx context.Context, cloneURLs []string, wsDir string) ([]config.RepoConfig, error) {
	var repos []config.RepoConfig
	seenNames := make(map[string]bool)

	for _, cloneURL := range cloneURLs {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		repoName := deduplicateRepoName(repoNameFromURL(cloneURL), seenNames)
		seenNames[repoName] = true

		clonePath := filepath.Join(wsDir, repoName)
		cmd := exec.CommandContext(ctx, "git", "clone", cloneURL, clonePath) //nolint:gosec // URL validated: prefix (https://|git@), no control chars, no dash-prefixed path segments, SSRF hostname blocklist
		if output, err := cmd.CombinedOutput(); err != nil {
			return nil, workspaceerrors.New(workspaceerrors.GitFailed, fmt.Sprintf("git clone failed for %s: %s", cloneURL, strings.TrimSpace(string(output))), err)
		}

		repos = append(repos, config.RepoConfig{Name: repoName, Path: clonePath})
	}
	return repos, nil
}

// deduplicateRepoName appends a numeric suffix if the name is already taken.
func deduplicateRepoName(name string, seen map[string]bool) string {
	if !seen[name] {
		return name
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", name, i)
		if !seen[candidate] {
			return candidate
		}
	}
}

func cleanupCloneDir(wsDir string) {
	workspace.StopDaemonForWorkspace(cli.GetDeps(nil), wsDir)
	_ = os.RemoveAll(wsDir)
}

// ResolveInitialWorkspaceID returns the stable UUID for the current working
// directory's workspace. Falls back to filepath.Base(cwd) if config is
// unavailable or the workspace has no UUID (pre-migration config).
func ResolveInitialWorkspaceID() string {
	cfg, err := config.LoadConfig()
	if err == nil && cfg != nil && cfg.DefaultWorkspaceID != "" {
		return cfg.DefaultWorkspaceID
	}
	// Fallback: CWD basename (pre-UUID config or load failure)
	if cwd, err := os.Getwd(); err == nil {
		return filepath.Base(cwd)
	}
	return "default"
}

// ResolveWorkspaceID loads config and resolves a workspace name to its UUID.
func ResolveWorkspaceID(name string) (string, error) {
	cfg, err := config.LoadConfig()
	if err != nil {
		return "", err
	}
	ws, ok := cfg.Workspaces[name]
	if !ok {
		return "", fmt.Errorf("workspace %q not found in config", name)
	}
	if ws.ID == "" {
		return "", fmt.Errorf("workspace %q has no stable ID", name)
	}
	return ws.ID, nil
}
