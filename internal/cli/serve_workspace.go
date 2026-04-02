package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/workspaceerrors"
)

// deleteWorkspace removes a workspace from config without deleting git worktrees.
// Returns an error if the workspace is not found or has running agents.
func deleteWorkspace(name string) error {
	return WithConfigLock(func() error {
		cfg, err := loadConfigUnlocked()
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

		if err := saveConfigUnlocked(cfg); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		return nil
	})
}

// setDefaultWorkspace sets the default workspace in config.
// Returns an error if the workspace is not found.
func setDefaultWorkspace(name string) error {
	return WithConfigLock(func() error {
		cfg, err := loadConfigUnlocked()
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
		if err := saveConfigUnlocked(cfg); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}
		return nil
	})
}

// clearDefaultWorkspace clears the default workspace, reverting to first-workspace behavior.
func clearDefaultWorkspace() error {
	return WithConfigLock(func() error {
		cfg, err := loadConfigUnlocked()
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
		if err := saveConfigUnlocked(cfg); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}
		return nil
	})
}

// createWorkspace creates a new workspace from the API request.
// Supports "empty" (git worktree from existing repos) and "clone" (git clone first) types.
// The entire load-check-create-save sequence is serialized under the config lock
// to prevent concurrent creates from clobbering each other.
func createWorkspace(ctx context.Context, req webui.WorkspaceCreateRequest) (webui.WorkspaceCreateResult, error) {
	var result webui.WorkspaceCreateResult
	err := WithConfigLock(func() error {
		// Load or create config
		cfg, err := loadConfigUnlocked()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		if cfg == nil {
			cfg = &LoomConfig{Workspaces: make(map[string]WorkspaceConfig)}
		}
		if cfg.Workspaces == nil {
			cfg.Workspaces = make(map[string]WorkspaceConfig)
		}

		if _, exists := cfg.Workspaces[req.Name]; exists {
			return workspaceerrors.New(workspaceerrors.AlreadyExists, fmt.Sprintf("workspace %q already exists", req.Name), nil)
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
			return fmt.Errorf("cannot determine home directory: %w", err)
		}
		allowedBase := filepath.Join(homeDir, ".loom", "workspaces")
		if !strings.HasPrefix(wsDir, allowedBase+string(filepath.Separator)) && wsDir != allowedBase {
			return workspaceerrors.New(workspaceerrors.SecurityViolation, fmt.Sprintf("workspace path must be under %s", allowedBase), nil)
		}

		branch := req.Branch
		if branch == "" {
			branch = req.Name
		}

		switch req.Type {
		case "empty":
			result, err = createEmptyWorkspace(ctx, cfg, req.Name, wsDir, branch, req.Repos, saveConfigUnlocked)
			return err
		case "clone":
			// Normalize: merge single clone_url into clone_urls
			cloneURLs := req.CloneURLs
			if len(cloneURLs) == 0 && req.CloneURL != "" {
				cloneURLs = []string{req.CloneURL}
			}
			result, err = createCloneWorkspace(ctx, cfg, req.Name, wsDir, cloneURLs, saveConfigUnlocked)
			return err
		default:
			return fmt.Errorf("unsupported workspace type: %s", req.Type)
		}
	})
	return result, err
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
func createEmptyWorkspace(ctx context.Context, cfg *LoomConfig, wsName, wsDir, branch string, repoPaths []string, save func(*LoomConfig) error) (webui.WorkspaceCreateResult, error) {
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

	if err := os.MkdirAll(wsDir, 0755); err != nil {
		return webui.WorkspaceCreateResult{}, fmt.Errorf("cannot create workspace directory: %w", err)
	}

	type createdWorktree struct {
		origRepoPath string
		worktreePath string
	}
	var created []createdWorktree
	var repos []RepoConfig

	cleanup := func() {
		stopDaemonForWorkspace(defaultDeps, wsDir)
		for _, c := range created {
			_, _ = RunGitCommand(c.origRepoPath, "worktree", "remove", c.worktreePath)
		}
		_ = os.RemoveAll(wsDir)
	}

	for _, repo := range resolved {
		if ctx.Err() != nil {
			cleanup()
			return webui.WorkspaceCreateResult{}, ctx.Err()
		}

		worktreePath := filepath.Join(wsDir, repo.name)
		if _, err := RunGitCommand(repo.path, "worktree", "add", worktreePath, "-b", branch); err != nil {
			cleanup()
			return webui.WorkspaceCreateResult{}, workspaceerrors.New(workspaceerrors.GitFailed, fmt.Sprintf("git worktree add failed for %s", repo.name), err)
		}

		created = append(created, createdWorktree{origRepoPath: repo.path, worktreePath: worktreePath})
		repos = append(repos, RepoConfig{Name: repo.name, Path: worktreePath})
	}

	// bd init (best-effort)
	_ = defaultDeps.Exec.Run(wsDir, "bd", "init")

	// Write default loom.yaml with agents (best-effort; non-fatal)
	if err := writeLoomYaml(wsDir); err != nil {
		slog.Warn("failed to write loom.yaml for workspace", "workspace", wsName, "err", err)
	}

	wsID := NewWorkspaceID()
	cfg.Workspaces[wsName] = WorkspaceConfig{ID: wsID, Path: wsDir, Repos: repos}
	if len(cfg.Workspaces) == 1 {
		cfg.DefaultWorkspace = wsName
	}

	if err := save(cfg); err != nil {
		cleanup()
		return webui.WorkspaceCreateResult{}, workspaceerrors.New(workspaceerrors.ConfigFailed, "failed to save config", err)
	}

	// Start bd daemon for the workspace asynchronously (best-effort; non-fatal).
	// Uses context.Background() because the request context is cancelled when the handler returns.
	timeout := cfg.Daemon.GetStartupTimeout(defaultDaemonStartupTimeout)
	go func() { //nolint:gosec // G118 — intentional: daemon outlives request
		if err := ensureDaemonForWorkspace(defaultDeps, context.Background(), wsDir, timeout); err != nil {
			slog.Warn("failed to start daemon for workspace", "workspace", wsName, "err", err)
		}
	}()

	return webui.WorkspaceCreateResult{WorkspaceID: wsID, WorkspacePath: wsDir}, nil
}

// repoNameFromURL derives a directory name from a git clone URL.
// e.g. "https://github.com/foo/bar.git" → "bar"
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
func createCloneWorkspace(ctx context.Context, cfg *LoomConfig, wsName, wsDir string, cloneURLs []string, save func(*LoomConfig) error) (webui.WorkspaceCreateResult, error) {
	// Security: ensure path is under allowed base directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return webui.WorkspaceCreateResult{}, fmt.Errorf("cannot determine home directory: %w", err)
	}
	allowedBase := filepath.Join(homeDir, ".loom", "workspaces")
	if !strings.HasPrefix(wsDir, allowedBase+string(filepath.Separator)) && wsDir != allowedBase {
		return webui.WorkspaceCreateResult{}, workspaceerrors.New(workspaceerrors.SecurityViolation, fmt.Sprintf("workspace path must be under %s", allowedBase), nil)
	}

	if err := os.MkdirAll(wsDir, 0755); err != nil {
		return webui.WorkspaceCreateResult{}, fmt.Errorf("cannot create workspace directory: %w", err)
	}

	cleanupDir := func() {
		stopDaemonForWorkspace(defaultDeps, wsDir)
		_ = os.RemoveAll(wsDir)
	}

	var repos []RepoConfig
	seenNames := make(map[string]bool)

	for _, cloneURL := range cloneURLs {
		if ctx.Err() != nil {
			cleanupDir()
			return webui.WorkspaceCreateResult{}, ctx.Err()
		}

		repoName := repoNameFromURL(cloneURL)
		// Deduplicate names
		if seenNames[repoName] {
			for i := 2; ; i++ {
				candidate := fmt.Sprintf("%s-%d", repoName, i)
				if !seenNames[candidate] {
					repoName = candidate
					break
				}
			}
		}
		seenNames[repoName] = true

		clonePath := filepath.Join(wsDir, repoName)
		cmd := exec.CommandContext(ctx, "git", "clone", cloneURL, clonePath) //nolint:gosec // URL validated: prefix (https://|git@), no control chars, no dash-prefixed path segments, SSRF hostname blocklist
		if output, err := cmd.CombinedOutput(); err != nil {
			cleanupDir()
			return webui.WorkspaceCreateResult{}, workspaceerrors.New(workspaceerrors.GitFailed, fmt.Sprintf("git clone failed for %s: %s", cloneURL, strings.TrimSpace(string(output))), err)
		}

		repos = append(repos, RepoConfig{Name: repoName, Path: clonePath})
	}

	// bd init (best-effort)
	_ = defaultDeps.Exec.Run(wsDir, "bd", "init")

	// Write default loom.yaml with agents (best-effort; non-fatal)
	if err := writeLoomYaml(wsDir); err != nil {
		slog.Warn("failed to write loom.yaml for workspace", "workspace", wsName, "err", err)
	}

	wsID := NewWorkspaceID()
	cfg.Workspaces[wsName] = WorkspaceConfig{ID: wsID, Path: wsDir, Repos: repos}
	if len(cfg.Workspaces) == 1 {
		cfg.DefaultWorkspace = wsName
	}

	if err := save(cfg); err != nil {
		cleanupDir()
		return webui.WorkspaceCreateResult{}, workspaceerrors.New(workspaceerrors.ConfigFailed, "failed to save config", err)
	}

	// Start bd daemon for the workspace asynchronously (best-effort; non-fatal).
	// Uses context.Background() because the request context is cancelled when the handler returns.
	timeout := cfg.Daemon.GetStartupTimeout(defaultDaemonStartupTimeout)
	go func() { //nolint:gosec // G118 — intentional: daemon outlives request
		if err := ensureDaemonForWorkspace(defaultDeps, context.Background(), wsDir, timeout); err != nil {
			slog.Warn("failed to start daemon for workspace", "workspace", wsName, "err", err)
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
