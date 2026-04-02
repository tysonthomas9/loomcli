package workspacemgr

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/workspace"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/workspaceerrors"
)

// DeleteWorkspace removes a workspace from config without deleting git worktrees.
// Returns an error if the workspace is not found or has running agents.
func DeleteWorkspace(name string) error {
	return config.WithConfigLock(func() error {
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

		if err := checkNoRunningAgents(ws, name); err != nil {
			return err
		}

		removeWorkspaceFromConfig(cfg, name)

		if err := config.SaveConfigUnlocked(cfg); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}
		return nil
	})
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

		result, err = dispatchWorkspaceCreate(ctx, cfg, req, wsDir, branch)
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
		startDaemonAsync(daemonTimeout, req.Name, result.WorkspacePath)
	}

	return result, err
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
func dispatchWorkspaceCreate(ctx context.Context, cfg *config.LoomConfig, req service.WorkspaceCreateRequest, wsDir, branch string) (service.WorkspaceCreateResult, error) {
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
		return service.WorkspaceCreateResult{}, fmt.Errorf("unsupported workspace type: %s", req.Type)
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
}

func createEmptyWorkspace(ctx context.Context, cfg *config.LoomConfig, wsName, wsDir, branch string, repoPaths []string, save func(*config.LoomConfig) error) (service.WorkspaceCreateResult, error) {
	if err := validateWorkspacePath(wsDir); err != nil {
		return service.WorkspaceCreateResult{}, err
	}

	resolved, err := resolveRepoPaths(repoPaths)
	if err != nil {
		return service.WorkspaceCreateResult{}, err
	}

	if err := os.MkdirAll(wsDir, 0755); err != nil {
		return service.WorkspaceCreateResult{}, fmt.Errorf("cannot create workspace directory: %w", err)
	}

	created, repos, err := addWorktrees(ctx, resolved, wsDir, branch)
	if err != nil {
		cleanupWorktrees(wsDir, created)
		return service.WorkspaceCreateResult{}, err
	}

	return finalizeWorkspace(cfg, wsName, wsDir, repos, created, save)
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
		created = append(created, createdWorktree{origRepoPath: repo.path, worktreePath: worktreePath})
		repos = append(repos, config.RepoConfig{Name: repo.name, Path: worktreePath})
	}
	return created, repos, nil
}

// finalizeWorkspace performs bd init, config save, and daemon start for a new workspace.
func finalizeWorkspace(cfg *config.LoomConfig, wsName, wsDir string, repos []config.RepoConfig, created []createdWorktree, save func(*config.LoomConfig) error) (service.WorkspaceCreateResult, error) {
	exec := cli.GetDeps(nil).Exec
	// Initialize beads in each repo so it has issues to hydrate from.
	for _, repo := range repos {
		_ = exec.Run(repo.Path, "bd", "init", "--quiet")
	}
	// Initialize beads at the workspace root and register each repo using
	// relative paths. Relative paths ensure source_repo values match the
	// repo names used by the frontend's source_repos filter.
	_ = exec.Run(wsDir, "bd", "init", "--quiet")
	for _, repo := range repos {
		if result := exec.Run(wsDir, "bd", "repo", "add", repo.Name); result.Err != nil {
			slog.Warn("failed to add repo to workspace beads", "repo", repo.Name, "err", result.Err)
		}
	}
	_ = exec.Run(wsDir, "bd", "repo", "sync")
	if err := workspace.WriteLoomYaml(wsDir); err != nil {
		slog.Warn("failed to write loom.yaml for workspace", "workspace", wsName, "err", err)
	}

	wsID := config.NewWorkspaceID()
	cfg.Workspaces[wsName] = config.WorkspaceConfig{ID: wsID, Path: wsDir, Repos: repos}
	if len(cfg.Workspaces) == 1 {
		cfg.DefaultWorkspace = wsName
	}

	if err := save(cfg); err != nil {
		cleanupWorktrees(wsDir, created)
		return service.WorkspaceCreateResult{}, workspaceerrors.New(workspaceerrors.ConfigFailed, "failed to save config", err)
	}

	return service.WorkspaceCreateResult{WorkspaceID: wsID, WorkspacePath: wsDir, DeferDaemonStart: true}, nil
}

// cleanupWorktrees removes created worktrees and the workspace directory on failure.
func cleanupWorktrees(wsDir string, created []createdWorktree) {
	workspace.StopDaemonForWorkspace(cli.GetDeps(nil), wsDir)
	for _, c := range created {
		_, _ = cli.RunGitCommand(c.origRepoPath, "worktree", "remove", c.worktreePath)
	}
	_ = os.RemoveAll(wsDir)
}

// validateWorkspacePath ensures the workspace directory is under the allowed base.
func validateWorkspacePath(wsDir string) error {
	allowedBase := filepath.Join(config.GetConfigDir(), "workspaces")
	if !strings.HasPrefix(wsDir, allowedBase+string(filepath.Separator)) && wsDir != allowedBase {
		return workspaceerrors.New(workspaceerrors.SecurityViolation, fmt.Sprintf("workspace path must be under %s", allowedBase), nil)
	}
	return nil
}

// startDaemonAsync starts the bd daemon for a workspace in the background.
func startDaemonAsync(timeout time.Duration, wsName, wsDir string) {
	go func() { //nolint:gosec // G118 — intentional: daemon outlives request
		if err := workspace.EnsureDaemonForWorkspace(cli.GetDeps(nil), context.Background(), wsDir, timeout); err != nil {
			slog.Warn("failed to start daemon for workspace", "workspace", wsName, "err", err)
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
func createCloneWorkspace(ctx context.Context, cfg *config.LoomConfig, wsName, wsDir string, cloneURLs []string, save func(*config.LoomConfig) error) (service.WorkspaceCreateResult, error) {
	if err := validateWorkspacePath(wsDir); err != nil {
		return service.WorkspaceCreateResult{}, err
	}

	if err := os.MkdirAll(wsDir, 0755); err != nil {
		return service.WorkspaceCreateResult{}, fmt.Errorf("cannot create workspace directory: %w", err)
	}

	repos, err := cloneRepos(ctx, cloneURLs, wsDir)
	if err != nil {
		cleanupCloneDir(wsDir)
		return service.WorkspaceCreateResult{}, err
	}

	return finalizeWorkspace(cfg, wsName, wsDir, repos, nil, save)
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
