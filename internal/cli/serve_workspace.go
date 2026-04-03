package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/workspaceerrors"
)

// deleteWorkspace removes a workspace from config without deleting git worktrees.
// Returns an error if the workspace is not found or has running agents.
func deleteWorkspace(name string) error {
	cfg, err := LoadConfig()
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

	// Check for running agents (lock files)
	for _, repo := range ws.Repos {
		repoPath := repo.Path
		if !filepath.IsAbs(repoPath) {
			repoPath = filepath.Join(ws.Path, repoPath)
		}
		lockPath := filepath.Join(repoPath, LockFileName)
		if _, err := os.Stat(lockPath); err == nil {
			return fmt.Errorf("workspace %q has running agents", name)
		}
	}

	// Remove from config
	delete(cfg.Workspaces, name)

	// Remove from workspace order
	filtered := cfg.WorkspaceOrder[:0]
	for _, n := range cfg.WorkspaceOrder {
		if n != name {
			filtered = append(filtered, n)
		}
	}
	cfg.WorkspaceOrder = filtered

	// Update default workspace if needed
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

	if err := SaveConfig(cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	return nil
}

// setDefaultWorkspace sets the default workspace in config.
// Returns an error if the workspace is not found.
func setDefaultWorkspace(name string) error {
	cfg, err := LoadConfig()
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
	if err := SaveConfig(cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	return nil
}

// clearDefaultWorkspace clears the default workspace, reverting to first-workspace behavior.
func clearDefaultWorkspace() error {
	cfg, err := LoadConfig()
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
	if err := SaveConfig(cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	return nil
}

// createWorkspace creates a new workspace from the API request.
// Supports "empty" (git worktree from existing repos) and "clone" (git clone first) types.
func createWorkspace(ctx context.Context, req webui.WorkspaceCreateRequest) (webui.WorkspaceCreateResult, error) {
	// Load or create config
	cfg, err := LoadConfig()
	if err != nil {
		return webui.WorkspaceCreateResult{}, fmt.Errorf("failed to load config: %w", err)
	}
	if cfg == nil {
		cfg = &LoomConfig{Workspaces: make(map[string]WorkspaceConfig)}
	}
	if cfg.Workspaces == nil {
		cfg.Workspaces = make(map[string]WorkspaceConfig)
	}

	if _, exists := cfg.Workspaces[req.Name]; exists {
		return webui.WorkspaceCreateResult{}, workspaceerrors.New(workspaceerrors.AlreadyExists, fmt.Sprintf("workspace %q already exists", req.Name), nil)
	}

	// Determine workspace directory
	wsDir := req.Path
	if wsDir == "" {
		wsDir = GetWorkspaceDir(req.Name)
	}
	wsDir = filepath.Clean(wsDir)

	// Security: ensure path is under allowed base directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return webui.WorkspaceCreateResult{}, fmt.Errorf("cannot determine home directory: %w", err)
	}
	allowedBase := filepath.Join(homeDir, ".loom", "workspaces")
	if !strings.HasPrefix(wsDir, allowedBase+string(filepath.Separator)) && wsDir != allowedBase {
		return webui.WorkspaceCreateResult{}, workspaceerrors.New(workspaceerrors.SecurityViolation, fmt.Sprintf("workspace path must be under %s", allowedBase), nil)
	}

	branch := req.Branch
	if branch == "" {
		branch = req.Name
	}

	switch req.Type {
	case "empty":
		return createEmptyWorkspace(ctx, cfg, req.Name, wsDir, branch, req.Repos)
	case "clone":
		// Normalize: merge single clone_url into clone_urls
		cloneURLs := req.CloneURLs
		if len(cloneURLs) == 0 && req.CloneURL != "" {
			cloneURLs = []string{req.CloneURL}
		}
		return createCloneWorkspace(ctx, cfg, req.Name, wsDir, cloneURLs)
	default:
		return webui.WorkspaceCreateResult{}, fmt.Errorf("unsupported workspace type: %s", req.Type)
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

// initWorkspaceBeads initializes beads in each repo and at the workspace root,
// then registers repos with relative paths for correct source_repo values.
func initWorkspaceBeads(wsDir string, repos []RepoConfig) {
	for _, repo := range repos {
		if result := execCommand(repo.Path, "bd", "init", "--quiet"); result.Err != nil {
			slog.Warn("bd init failed for repo", "repo", repo.Name, "path", repo.Path, "err", result.Err)
		}
	}
	if result := execCommand(wsDir, "bd", "init", "--quiet"); result.Err != nil {
		slog.Warn("bd init failed for workspace root", "path", wsDir, "err", result.Err)
	}
	for _, repo := range repos {
		result := execCommand(wsDir, "bd", "repo", "add", repo.Name)
		if result.Err != nil {
			slog.Warn("failed to add repo to workspace beads", "repo", repo.Name, "err", result.Err)
		}
	}
}

// createEmptyWorkspace creates worktrees from existing repos.
func createEmptyWorkspace(ctx context.Context, cfg *LoomConfig, wsName, wsDir, branch string, repoPaths []string) (webui.WorkspaceCreateResult, error) {
	// Security: ensure path is under allowed base directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return webui.WorkspaceCreateResult{}, fmt.Errorf("cannot determine home directory: %w", err)
	}
	allowedBase := filepath.Join(homeDir, ".loom", "workspaces")
	if !strings.HasPrefix(wsDir, allowedBase+string(filepath.Separator)) && wsDir != allowedBase {
		return webui.WorkspaceCreateResult{}, workspaceerrors.New(workspaceerrors.SecurityViolation, fmt.Sprintf("workspace path must be under %s", allowedBase), nil)
	}

	resolved, err := resolveRepoPaths(repoPaths)
	if err != nil {
		return webui.WorkspaceCreateResult{}, err
	}

	// Phase 1: Write config with state=creating BEFORE filesystem ops.
	wsID := NewWorkspaceID()
	cfg.Workspaces[wsName] = WorkspaceConfig{
		ID: wsID, Path: wsDir, State: WorkspaceStateCreating,
	}
	if len(cfg.Workspaces) == 1 {
		cfg.DefaultWorkspace = wsName
	}
	if err := SaveConfig(cfg); err != nil {
		return webui.WorkspaceCreateResult{}, workspaceerrors.New(workspaceerrors.ConfigFailed, "failed to save config", err)
	}

	markError := func(msg string) {
		if err := setWorkspaceState(wsName, WorkspaceStateError, msg); err != nil {
			slog.Error("failed to mark workspace error", "workspace", wsName, "err", err)
		}
	}

	if err := os.MkdirAll(wsDir, 0755); err != nil {
		markError(fmt.Sprintf("cannot create directory: %v", err))
		return webui.WorkspaceCreateResult{}, fmt.Errorf("cannot create workspace directory: %w", err)
	}

	// Phase 2: Creating worktrees
	if err := setWorkspaceState(wsName, WorkspaceStateCloning, ""); err != nil {
		slog.Error("state transition failed", "err", err)
	}

	repos, cleanupWT, err := createRepoWorktrees(ctx, wsDir, resolved, branch)
	if err != nil {
		cleanupWT()
		markError(err.Error())
		return webui.WorkspaceCreateResult{}, err
	}
	_ = cleanupWT // kept for error paths only

	// Update config with repos.
	if loadedCfg, err := LoadConfig(); err == nil && loadedCfg != nil {
		ws := loadedCfg.Workspaces[wsName]
		ws.Repos = repos
		loadedCfg.Workspaces[wsName] = ws
		_ = SaveConfig(loadedCfg)
	}

	// Phase 3: Initializing
	if err := setWorkspaceState(wsName, WorkspaceStateInitializing, ""); err != nil {
		slog.Error("state transition failed", "err", err)
	}

	initWorkspaceBeads(wsDir, repos)
	agentNames, err := writeLoomYaml(wsDir)
	if err != nil {
		slog.Warn("failed to write loom.yaml for workspace", "workspace", wsName, "err", err)
	}

	timeout := cfg.Daemon.GetStartupTimeout(defaultDaemonStartupTimeout)
	go func() { //nolint:gosec // G118: intentional — goroutine must outlive the HTTP request
		createAgentWorktrees(wsDir, repos, agentNames)
		if err := ensureDaemonForWorkspace(context.Background(), wsDir, timeout); err != nil {
			slog.Warn("failed to start daemon for workspace", "workspace", wsName, "err", err)
		}
		if result := execCommand(wsDir, "bd", "repo", "sync"); result.Err != nil {
			slog.Warn("bd repo sync failed", "workspace", wsName, "err", result.Err)
		}
		// Phase 4: Ready
		if err := setWorkspaceState(wsName, WorkspaceStateReady, ""); err != nil {
			slog.Error("failed to mark workspace ready", "workspace", wsName, "err", err)
		}
	}()

	return webui.WorkspaceCreateResult{WorkspaceID: wsID, WorkspacePath: wsDir}, nil
}

// createRepoWorktrees creates git worktrees for each resolved repo. Returns
// the repo configs and a cleanup function to remove worktrees on error.

// createCloneWorkspace clones one or more repos and creates a workspace from them.
func createCloneWorkspace(ctx context.Context, cfg *LoomConfig, wsName, wsDir string, cloneURLs []string) (webui.WorkspaceCreateResult, error) {
	// Security: ensure path is under allowed base directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return webui.WorkspaceCreateResult{}, fmt.Errorf("cannot determine home directory: %w", err)
	}
	allowedBase := filepath.Join(homeDir, ".loom", "workspaces")
	if !strings.HasPrefix(wsDir, allowedBase+string(filepath.Separator)) && wsDir != allowedBase {
		return webui.WorkspaceCreateResult{}, workspaceerrors.New(workspaceerrors.SecurityViolation, fmt.Sprintf("workspace path must be under %s", allowedBase), nil)
	}

	// Phase 1: Write config with state=creating BEFORE filesystem ops.
	wsID := NewWorkspaceID()
	cfg.Workspaces[wsName] = WorkspaceConfig{
		ID: wsID, Path: wsDir, State: WorkspaceStateCreating, CloneURLs: cloneURLs,
	}
	if len(cfg.Workspaces) == 1 {
		cfg.DefaultWorkspace = wsName
	}
	if err := SaveConfig(cfg); err != nil {
		return webui.WorkspaceCreateResult{}, workspaceerrors.New(workspaceerrors.ConfigFailed, "failed to save config", err)
	}

	markError := func(msg string) {
		if err := setWorkspaceState(wsName, WorkspaceStateError, msg); err != nil {
			slog.Error("failed to mark workspace error", "workspace", wsName, "err", err)
		}
	}

	if err := os.MkdirAll(wsDir, 0755); err != nil {
		markError(fmt.Sprintf("cannot create directory: %v", err))
		return webui.WorkspaceCreateResult{}, fmt.Errorf("cannot create workspace directory: %w", err)
	}

	// Phase 2: Cloning
	if err := setWorkspaceState(wsName, WorkspaceStateCloning, ""); err != nil {
		slog.Error("state transition failed", "err", err)
	}

	repos, err := cloneRepos(ctx, wsDir, cloneURLs)
	if err != nil {
		markError(err.Error())
		return webui.WorkspaceCreateResult{}, err
	}

	// Update config with repos now that clone succeeded.
	if loadedCfg, err := LoadConfig(); err == nil && loadedCfg != nil {
		ws := loadedCfg.Workspaces[wsName]
		ws.Repos = repos
		loadedCfg.Workspaces[wsName] = ws
		_ = SaveConfig(loadedCfg)
	}

	// Phase 3: Initializing (async — beads, agents, daemon)
	if err := setWorkspaceState(wsName, WorkspaceStateInitializing, ""); err != nil {
		slog.Error("state transition failed", "err", err)
	}

	initWorkspaceBeads(wsDir, repos)
	cloneAgentNames, err := writeLoomYaml(wsDir)
	if err != nil {
		slog.Warn("failed to write loom.yaml for workspace", "workspace", wsName, "err", err)
	}

	timeout := cfg.Daemon.GetStartupTimeout(defaultDaemonStartupTimeout)
	go func() { //nolint:gosec // G118: intentional — goroutine must outlive the HTTP request
		createAgentWorktrees(wsDir, repos, cloneAgentNames)
		if err := ensureDaemonForWorkspace(context.Background(), wsDir, timeout); err != nil {
			slog.Warn("failed to start daemon for workspace", "workspace", wsName, "err", err)
		}
		if result := execCommand(wsDir, "bd", "repo", "sync"); result.Err != nil {
			slog.Warn("bd repo sync failed", "workspace", wsName, "err", result.Err)
		}
		// Phase 4: Ready
		if err := setWorkspaceState(wsName, WorkspaceStateReady, ""); err != nil {
			slog.Error("failed to mark workspace ready", "workspace", wsName, "err", err)
		}
	}()

	return webui.WorkspaceCreateResult{WorkspaceID: wsID, WorkspacePath: wsDir}, nil
}

// resolveInitialWorkspaceID returns the stable UUID for the current working
// directory's workspace. Falls back to filepath.Base(cwd) if config is
// unavailable or the workspace has no UUID (pre-migration config).
func resolveInitialWorkspaceID() string {
	cfg, err := LoadConfig()
	if err == nil && cfg != nil && cfg.DefaultWorkspaceID != "" {
		return cfg.DefaultWorkspaceID
	}
	// Fallback: CWD basename (pre-UUID config or load failure)
	if cwd, err := os.Getwd(); err == nil {
		return filepath.Base(cwd)
	}
	return "default"
}

// resolveWorkspaceID loads config and resolves a workspace name to its UUID.
func resolveWorkspaceID(name string) (string, error) {
	cfg, err := LoadConfig()
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

// ensureCurrentProjectRegistered adds the current working directory as a
// workspace in the global config if it isn't already registered. This ensures
// buildWorkspaceInfo and buildWorkspaceInfoForName are in serve_workspace_info.go
