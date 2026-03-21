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
func createWorkspace(ctx context.Context, req webui.WorkspaceCreateRequest) error {
	// Load or create config
	cfg, err := LoadConfig()
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
		return fmt.Errorf("workspace %q already exists", req.Name)
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
		return fmt.Errorf("workspace path must be under %s", allowedBase)
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
		return fmt.Errorf("unsupported workspace type: %s", req.Type)
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
			return nil, fmt.Errorf("cannot resolve path %q: %w", rp, err)
		}

		info, err := os.Stat(absPath)
		if err != nil {
			return nil, fmt.Errorf("repo path does not exist: %s", absPath)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("repo path is not a directory: %s", absPath)
		}

		gitDir := filepath.Join(absPath, ".git")
		if _, err := os.Stat(gitDir); err != nil {
			return nil, fmt.Errorf("not a git repository: %s", absPath)
		}

		baseName := filepath.Base(absPath)
		if prev, exists := seenNames[baseName]; exists {
			return nil, fmt.Errorf("duplicate repo name %q from paths %s and %s", baseName, prev, absPath)
		}
		seenNames[baseName] = absPath
		resolved = append(resolved, resolvedRepo{path: absPath, name: baseName})
	}

	if len(resolved) == 0 {
		return nil, fmt.Errorf("no valid repos specified")
	}
	return resolved, nil
}

// createEmptyWorkspace creates worktrees from existing repos.
func createEmptyWorkspace(ctx context.Context, cfg *LoomConfig, wsName, wsDir, branch string, repoPaths []string) error {
	// Security: ensure path is under allowed base directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	allowedBase := filepath.Join(homeDir, ".loom", "workspaces")
	if !strings.HasPrefix(wsDir, allowedBase+string(filepath.Separator)) && wsDir != allowedBase {
		return fmt.Errorf("workspace path must be under %s", allowedBase)
	}

	resolved, err := resolveRepoPaths(repoPaths)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(wsDir, 0755); err != nil {
		return fmt.Errorf("cannot create workspace directory: %w", err)
	}

	type createdWorktree struct {
		origRepoPath string
		worktreePath string
	}
	var created []createdWorktree
	var repos []RepoConfig

	cleanup := func() {
		for _, c := range created {
			_, _ = RunGitCommand(c.origRepoPath, "worktree", "remove", c.worktreePath)
		}
		_ = os.RemoveAll(wsDir)
	}

	for _, repo := range resolved {
		if ctx.Err() != nil {
			cleanup()
			return ctx.Err()
		}

		worktreePath := filepath.Join(wsDir, repo.name)
		if _, err := RunGitCommand(repo.path, "worktree", "add", worktreePath, "-b", branch); err != nil {
			cleanup()
			return fmt.Errorf("git worktree add failed for %s: %w", repo.name, err)
		}

		created = append(created, createdWorktree{origRepoPath: repo.path, worktreePath: worktreePath})
		repos = append(repos, RepoConfig{Name: repo.name, Path: worktreePath})
	}

	// bd init (best-effort)
	_ = execCommand(wsDir, "bd", "init")

	// Write default loom.yaml with agents (best-effort; non-fatal)
	if err := writeLoomYaml(wsDir); err != nil {
		slog.Warn("failed to write loom.yaml for workspace", "workspace", wsName, "err", err)
	}

	// Start bd daemon for the workspace (best-effort; non-fatal)
	timeout := cfg.Daemon.GetStartupTimeout(defaultDaemonStartupTimeout)
	if err := ensureDaemonForWorkspace(ctx, wsDir, timeout); err != nil {
		slog.Warn("failed to start daemon for workspace", "workspace", wsName, "err", err)
	}

	cfg.Workspaces[wsName] = WorkspaceConfig{Path: wsDir, Repos: repos}
	if len(cfg.Workspaces) == 1 {
		cfg.DefaultWorkspace = wsName
	}

	if err := SaveConfig(cfg); err != nil {
		cleanup()
		return fmt.Errorf("failed to save config: %w", err)
	}
	return nil
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
func createCloneWorkspace(ctx context.Context, cfg *LoomConfig, wsName, wsDir string, cloneURLs []string) error {
	// Security: ensure path is under allowed base directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	allowedBase := filepath.Join(homeDir, ".loom", "workspaces")
	if !strings.HasPrefix(wsDir, allowedBase+string(filepath.Separator)) && wsDir != allowedBase {
		return fmt.Errorf("workspace path must be under %s", allowedBase)
	}

	if err := os.MkdirAll(wsDir, 0755); err != nil {
		return fmt.Errorf("cannot create workspace directory: %w", err)
	}

	cleanupDir := func() { _ = os.RemoveAll(wsDir) }

	var repos []RepoConfig
	seenNames := make(map[string]bool)

	for _, cloneURL := range cloneURLs {
		if ctx.Err() != nil {
			cleanupDir()
			return ctx.Err()
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
		cmd := exec.CommandContext(ctx, "git", "clone", cloneURL, clonePath) //nolint:gosec // URL validated: prefix (https://|git@), no control chars, no dash-prefixed path segments
		if output, err := cmd.CombinedOutput(); err != nil {
			cleanupDir()
			return fmt.Errorf("git clone failed for %s: %s", cloneURL, strings.TrimSpace(string(output)))
		}

		repos = append(repos, RepoConfig{Name: repoName, Path: clonePath})
	}

	// bd init (best-effort)
	_ = execCommand(wsDir, "bd", "init")

	// Write default loom.yaml with agents (best-effort; non-fatal)
	if err := writeLoomYaml(wsDir); err != nil {
		slog.Warn("failed to write loom.yaml for workspace", "workspace", wsName, "err", err)
	}

	// Start bd daemon for the workspace (best-effort; non-fatal)
	timeout := cfg.Daemon.GetStartupTimeout(defaultDaemonStartupTimeout)
	if err := ensureDaemonForWorkspace(ctx, wsDir, timeout); err != nil {
		slog.Warn("failed to start daemon for workspace", "workspace", wsName, "err", err)
	}

	cfg.Workspaces[wsName] = WorkspaceConfig{Path: wsDir, Repos: repos}
	if len(cfg.Workspaces) == 1 {
		cfg.DefaultWorkspace = wsName
	}

	if err := SaveConfig(cfg); err != nil {
		cleanupDir()
		return fmt.Errorf("failed to save config: %w", err)
	}
	return nil
}

// ensureCurrentProjectRegistered adds the current working directory as a
// workspace in the global config if it isn't already registered. This ensures
// buildWorkspaceInfo and buildWorkspaceInfoForName are in serve_workspace_info.go
