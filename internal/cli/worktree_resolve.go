package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Resolver abstracts worktree/repo discovery behind legacy and workspace modes.
type Resolver struct {
	mode      ResolverMode
	config    *LoomConfig
	workspace string // active workspace name (workspace mode only)
}

// NewResolver creates a Resolver, selecting workspace mode if a config with
// workspaces exists, otherwise falling back to legacy mode.
func NewResolver() (*Resolver, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	if cfg != nil && len(cfg.Workspaces) > 0 {
		ws := cfg.DefaultWorkspace
		if ws == "" {
			// Use first workspace alphabetically for determinism
			names := make([]string, 0, len(cfg.Workspaces))
			for name := range cfg.Workspaces {
				names = append(names, name)
			}
			sort.Strings(names)
			ws = names[0]
		}
		return &Resolver{
			mode:      ModeWorkspace,
			config:    cfg,
			workspace: ws,
		}, nil
	}
	return &Resolver{mode: ModeLegacy}, nil
}

// Mode returns the resolver's current mode.
func (r *Resolver) Mode() ResolverMode {
	return r.mode
}

// WorkspaceName returns the active workspace name (empty in legacy mode).
func (r *Resolver) WorkspaceName() string {
	return r.workspace
}

// SetWorkspace switches the active workspace. Returns an error if the
// workspace name is not found in the config.
func (r *Resolver) SetWorkspace(name string) error {
	if r.config == nil {
		return fmt.Errorf("no config loaded; cannot set workspace")
	}
	if _, ok := r.config.Workspaces[name]; !ok {
		return fmt.Errorf("workspace %q not found in config", name)
	}
	r.workspace = name
	return nil
}

// DiscoverWorktrees returns discovered worktrees using the resolver's mode.
func (r *Resolver) DiscoverWorktrees() ([]WorktreeInfo, error) {
	if r.mode == ModeWorkspace {
		return r.discoverWorkspace()
	}
	return r.discoverLegacy()
}

// discoverLegacy scans the ./worktrees/ directory (existing behavior).
func (r *Resolver) discoverLegacy() ([]WorktreeInfo, error) {
	worktreesDir, err := ResolveWorktreesDir()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(worktreesDir); err != nil {
		return nil, fmt.Errorf("worktrees directory not found: %s", worktreesDir)
	}

	entries, err := os.ReadDir(worktreesDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read worktrees directory: %w", err)
	}

	var worktrees []WorktreeInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		worktreePath := filepath.Join(worktreesDir, entry.Name())

		// Verify it's a git directory
		gitDir := filepath.Join(worktreePath, ".git")
		if _, err := os.Stat(gitDir); err != nil {
			continue // Not a git worktree
		}

		// Get the current branch
		branch, err := GetCurrentBranch(worktreePath)
		if err != nil {
			branch = "unknown"
		}

		worktrees = append(worktrees, WorktreeInfo{
			Name:   entry.Name(),
			Path:   worktreePath,
			Branch: branch,
		})
	}

	return worktrees, nil
}

// discoverWorkspace reads repos from the active workspace config.
func (r *Resolver) discoverWorkspace() ([]WorktreeInfo, error) {
	ws, ok := r.config.Workspaces[r.workspace]
	if !ok {
		return nil, fmt.Errorf("workspace %q not found in config", r.workspace)
	}

	var worktrees []WorktreeInfo
	for i := range ws.Repos {
		repo := &ws.Repos[i]
		repoPath := repo.Path
		if !filepath.IsAbs(repoPath) {
			repoPath = filepath.Join(ws.Path, repoPath)
		}

		// Verify .git exists
		gitDir := filepath.Join(repoPath, ".git")
		if _, err := os.Stat(gitDir); err != nil {
			continue // Skip repos where .git is missing
		}

		branch, err := GetCurrentBranch(repoPath)
		if err != nil {
			branch = "unknown"
		}

		worktrees = append(worktrees, WorktreeInfo{
			Name:      repo.Name,
			Path:      repoPath,
			Branch:    branch,
			Workspace: r.workspace,
			Repo:      repo,
		})
	}

	return worktrees, nil
}

// ResolveWorktreePath converts a worktree name to its full path using the
// resolver's mode.
func (r *Resolver) ResolveWorktreePath(name string) (string, error) {
	if r.mode == ModeWorkspace {
		return r.resolveWorkspacePath(name)
	}
	return resolveLegacyPath(name)
}

// ResolveWorkspaceByName checks if name matches a workspace name and returns
// the workspace root path. Returns (path, true) if found, ("", false) if not.
func (r *Resolver) ResolveWorkspaceByName(name string) (string, bool) {
	if r.mode != ModeWorkspace || r.config == nil || name == "" {
		return "", false
	}
	if ws, ok := r.config.Workspaces[name]; ok && ws.Path != "" {
		return ws.Path, true
	}
	return "", false
}

// WorkspaceNames returns the names of all configured workspaces.
// Returns nil in legacy mode.
func (r *Resolver) WorkspaceNames() []string {
	if r.mode != ModeWorkspace || r.config == nil {
		return nil
	}
	names := make([]string, 0, len(r.config.Workspaces))
	for name := range r.config.Workspaces {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// resolveWorkspacePath looks up a repo by name in the active workspace config.
func (r *Resolver) resolveWorkspacePath(name string) (string, error) {
	if name == "" {
		return os.Getwd()
	}
	if filepath.IsAbs(name) {
		if _, err := os.Stat(name); err != nil {
			return "", fmt.Errorf("worktree path does not exist: %s", name)
		}
		return name, nil
	}

	ws, ok := r.config.Workspaces[r.workspace]
	if !ok {
		return "", fmt.Errorf("workspace %q not found in config", r.workspace)
	}

	for _, repo := range ws.Repos {
		if repo.Name == name {
			repoPath := repo.Path
			if !filepath.IsAbs(repoPath) {
				repoPath = filepath.Join(ws.Path, repoPath)
			}
			if _, err := os.Stat(repoPath); err != nil {
				return "", fmt.Errorf("repo '%s' path does not exist: %s", name, repoPath)
			}
			return repoPath, nil
		}
	}

	return "", fmt.Errorf("repo '%s' not found in workspace %q", name, r.workspace)
}

// GetWorktreesDir returns the worktrees directory path using the resolver's mode.
// In workspace mode, returns the active workspace's path.
func (r *Resolver) GetWorktreesDir() string {
	if r.mode == ModeWorkspace {
		if ws, ok := r.config.Workspaces[r.workspace]; ok {
			return ws.Path
		}
	}
	return getWorktreesDirLegacy()
}

// GetDefaultBranch returns the default integration branch using the resolver's mode.
// Resolution order: LOOM_DEFAULT_BRANCH env var > mode-specific logic > "main"
func (r *Resolver) GetDefaultBranch() string {
	if branch := os.Getenv("LOOM_DEFAULT_BRANCH"); branch != "" {
		return branch
	}
	if r.mode == ModeWorkspace {
		ws, ok := r.config.Workspaces[r.workspace]
		if ok {
			for _, repo := range ws.Repos {
				if repo.DefaultBranch != "" {
					return repo.DefaultBranch
				}
			}
		}
		return "main"
	}
	worktrees, _ := r.DiscoverWorktrees()
	return GetDefaultBranchForWorktrees(worktrees)
}

// SetRepoDefaultBranch updates the default branch for a named repo in the config.
// Only works in workspace mode with a persisted config.
func (r *Resolver) SetRepoDefaultBranch(repoName, branch string) error {
	if r.mode != ModeWorkspace || r.config == nil {
		return fmt.Errorf("target branch update only supported in workspace mode")
	}
	ws, ok := r.config.Workspaces[r.workspace]
	if !ok {
		return fmt.Errorf("workspace %q not found", r.workspace)
	}
	found := false
	for i, repo := range ws.Repos {
		if repo.Name == repoName {
			ws.Repos[i].DefaultBranch = branch
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("repo %q not found in workspace %q", repoName, r.workspace)
	}
	r.config.Workspaces[r.workspace] = ws
	return SaveConfig(r.config)
}

// GetBeadsDir returns the directory where .beads/ lives.
// In workspace mode, this is the workspace root path (shared across repos).
// In legacy mode, this returns "." (current directory).
// The result is cached for the lifetime of the process.
func GetBeadsDir() string {
	beadsDirOnce.Do(func() {
		cfg, err := LoadConfig()
		if err != nil || cfg == nil || len(cfg.Workspaces) == 0 {
			beadsDirCache = "."
			return
		}

		ws := cfg.DefaultWorkspace
		if ws == "" {
			names := make([]string, 0, len(cfg.Workspaces))
			for name := range cfg.Workspaces {
				names = append(names, name)
			}
			sort.Strings(names)
			ws = names[0]
		}

		if wsConfig, ok := cfg.Workspaces[ws]; ok && wsConfig.Path != "" {
			beadsDirCache = wsConfig.Path
		} else {
			beadsDirCache = "."
		}
	})
	return beadsDirCache
}

// ResetBeadsDirCache clears the cached beads directory value. For testing only.
func ResetBeadsDirCache() {
	beadsDirOnce = sync.Once{}
	beadsDirCache = ""
}

var (
	beadsDirCache string
	beadsDirOnce  sync.Once
)

// Package-level default resolver (lazily initialized)
var defaultResolver *Resolver

func getDefaultResolver() *Resolver {
	if defaultResolver == nil {
		r, err := NewResolver()
		if err != nil {
			r = &Resolver{mode: ModeLegacy}
		}
		defaultResolver = r
	}
	return defaultResolver
}
