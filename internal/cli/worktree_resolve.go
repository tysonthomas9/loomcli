package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// Resolver abstracts worktree/repo discovery behind legacy and workspace modes.
type Resolver struct {
	Mode      ResolverMode
	Config    *config.LoomConfig
	Workspace string // active workspace name (workspace mode only)
}

// NewResolver creates a Resolver, selecting workspace mode if a config with
// workspaces exists, otherwise falling back to legacy mode.
func NewResolver() (*Resolver, error) {
	cfg, err := config.LoadConfig()
	if err != nil {
		return &Resolver{Mode: ModeLegacy}, nil
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
			Mode:      ModeWorkspace,
			Config:    cfg,
			Workspace: ws,
		}, nil
	}
	return &Resolver{Mode: ModeLegacy}, nil
}

// Mode returns the resolver's current mode.
// GetMode returns the resolver mode.
func (r *Resolver) GetMode() ResolverMode {
	return r.Mode
}

// WorkspaceName returns the active workspace name (empty in legacy mode).
func (r *Resolver) WorkspaceName() string {
	return r.Workspace
}

// SetWorkspace switches the active workspace. Returns an error if the
// workspace name is not found in the config.
func (r *Resolver) SetWorkspace(name string) error {
	if r.Config == nil {
		return fmt.Errorf("no config loaded; cannot set workspace")
	}
	if _, ok := r.Config.Workspaces[name]; !ok {
		return fmt.Errorf("workspace %q not found in config", name)
	}
	r.Workspace = name
	return nil
}

// DiscoverWorktrees returns discovered worktrees using the resolver's mode.
func (r *Resolver) DiscoverWorktrees() ([]WorktreeInfo, error) {
	if r.Mode == ModeWorkspace {
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
	ws, ok := r.Config.Workspaces[r.Workspace]
	if !ok {
		return nil, fmt.Errorf("workspace %q not found in config", r.Workspace)
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
			Workspace: r.Workspace,
			Repo:      repo,
		})
	}

	return worktrees, nil
}

// ResolveWorktreePath converts a worktree name to its full path using the
// resolver's mode.
func (r *Resolver) ResolveWorktreePath(name string) (string, error) {
	if r.Mode == ModeWorkspace {
		return r.ResolveWorkspacePath(name)
	}
	return resolveLegacyPath(name)
}

// ResolveWorkspaceByName checks if name matches a workspace name and returns
// the workspace root path. Returns (path, true) if found, ("", false) if not.
func (r *Resolver) ResolveWorkspaceByName(name string) (string, bool) {
	if r.Mode != ModeWorkspace || r.Config == nil || name == "" {
		return "", false
	}
	if ws, ok := r.Config.Workspaces[name]; ok && ws.Path != "" {
		return ws.Path, true
	}
	return "", false
}

// WorkspaceNames returns the names of all configured workspaces.
// Returns nil in legacy mode.
func (r *Resolver) WorkspaceNames() []string {
	if r.Mode != ModeWorkspace || r.Config == nil {
		return nil
	}
	names := make([]string, 0, len(r.Config.Workspaces))
	for name := range r.Config.Workspaces {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// resolveWorkspacePath looks up a repo by name in the active workspace config.
func (r *Resolver) ResolveWorkspacePath(name string) (string, error) {
	if name == "" {
		return os.Getwd()
	}
	if filepath.IsAbs(name) {
		if _, err := os.Stat(name); err != nil {
			return "", fmt.Errorf("worktree path does not exist: %s", name)
		}
		return name, nil
	}

	ws, ok := r.Config.Workspaces[r.Workspace]
	if !ok {
		return "", fmt.Errorf("workspace %q not found in config", r.Workspace)
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

	// Check if the worktree exists on disk but isn't registered in config.
	// Give an actionable error so the user can fix their config.
	if ws.Path != "" {
		candidate := filepath.Join(ws.Path, "worktrees", name)
		gitFile := filepath.Join(candidate, ".git")
		if _, err := os.Stat(gitFile); err == nil {
			return "", fmt.Errorf("worktree '%s' exists on disk but is not registered in workspace %q.\n  Add it with: loom config add-repo %s --workspace %s --path %s",
				name, r.Workspace, name, r.Workspace, candidate)
		}
	}

	return "", fmt.Errorf("repo '%s' not found in workspace %q", name, r.Workspace)
}

// GetWorktreesDir returns the worktrees directory path using the resolver's mode.
// In workspace mode, returns the active workspace's path.
func (r *Resolver) GetWorktreesDir() string {
	if r.Mode == ModeWorkspace {
		if ws, ok := r.Config.Workspaces[r.Workspace]; ok {
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
	if r.Mode == ModeWorkspace {
		ws, ok := r.Config.Workspaces[r.Workspace]
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
// Reloads config inside the lock to avoid stale-read races.
func (r *Resolver) SetRepoDefaultBranch(repoName, branch string) error {
	if r.Mode != ModeWorkspace || r.Config == nil {
		return fmt.Errorf("target branch update only supported in workspace mode")
	}
	return config.WithConfigLock(func() error {
		cfg, err := config.LoadConfigUnlocked()
		if err != nil {
			return err
		}
		if cfg == nil {
			return fmt.Errorf("no config found")
		}
		ws, ok := cfg.Workspaces[r.Workspace]
		if !ok {
			return fmt.Errorf("workspace %q not found", r.Workspace)
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
			return fmt.Errorf("repo %q not found in workspace %q", repoName, r.Workspace)
		}
		cfg.Workspaces[r.Workspace] = ws
		return config.SaveConfigUnlocked(cfg)
	})
}

// GetBeadsDir returns the directory where .beads/ lives.
// In workspace mode, this is the workspace root path (shared across repos).
// In legacy mode, this returns "." (current directory).
// The result is cached for the lifetime of the process.
func GetBeadsDir() string {
	beadsDirOnce.Do(func() {
		cfg, err := config.LoadConfig()
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

func GetDefaultResolver() *Resolver {
	if defaultResolver == nil {
		r, err := NewResolver()
		if err != nil {
			r = &Resolver{Mode: ModeLegacy}
		}
		defaultResolver = r
	}
	return defaultResolver
}
