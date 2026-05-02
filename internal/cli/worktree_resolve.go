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
	cfg, err := config.LoadConfigCached()
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
			Name:             entry.Name(),
			Path:             worktreePath,
			Branch:           branch,
			IsLinkedWorktree: IsGitLinkedWorktree(worktreePath),
		})
	}

	return worktrees, nil
}

// discoverWorkspace reads repos from the active workspace config.
// Returns repo-level entries only — agent worktrees are discovered
// separately via DiscoverAgentWorktrees.
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
			Name:             repo.Name,
			Path:             repoPath,
			Branch:           branch,
			Workspace:        r.Workspace,
			Repo:             repo,
			IsLinkedWorktree: IsGitLinkedWorktree(repoPath),
		})
	}

	return worktrees, nil
}

// DiscoverAgentWorktrees returns agent worktrees under
// <wsPath>/worktrees/<repo>/<agent> for the resolver's active workspace.
// Only valid in workspace mode; returns an error in legacy mode.
// Each returned WorktreeInfo has Repo set to the parent repo's config,
// giving callers access to DefaultBranch and Remote.
// funlen: the 2-pass scan + parallel git dispatch is intentionally one
// function to keep the candidate lifecycle in scope.
//
//nolint:funlen
func (r *Resolver) DiscoverAgentWorktrees() ([]WorktreeInfo, error) {
	if r.Mode != ModeWorkspace || r.Config == nil {
		return nil, fmt.Errorf("agent worktree discovery requires workspace mode")
	}
	ws, ok := r.Config.Workspaces[r.Workspace]
	if !ok {
		return nil, fmt.Errorf("workspace %q not found in config", r.Workspace)
	}

	// Pass 1: collect candidates with cheap OS reads (no subprocesses).
	type candidate struct {
		name string
		path string
		repo *config.RepoConfig
	}
	var candidates []candidate
	for i := range ws.Repos {
		repo := &ws.Repos[i]
		agentsDir := filepath.Join(ws.Path, "worktrees", repo.Name)
		entries, err := os.ReadDir(agentsDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			agentPath := filepath.Join(agentsDir, entry.Name())
			if _, err := os.Stat(filepath.Join(agentPath, ".git")); err != nil {
				continue
			}
			candidates = append(candidates, candidate{entry.Name(), agentPath, repo})
		}
	}
	// Flat-layout pass: repos registered directly as linked worktrees.
	seen := make(map[string]struct{}, len(candidates))
	for _, c := range candidates {
		seen[c.path] = struct{}{}
	}
	for i := range ws.Repos {
		repo := &ws.Repos[i]
		repoPath := repo.Path
		if !filepath.IsAbs(repoPath) {
			repoPath = filepath.Join(ws.Path, repoPath)
		}
		if _, alreadySeen := seen[repoPath]; alreadySeen {
			continue
		}
		if !IsGitLinkedWorktree(repoPath) {
			continue
		}
		candidates = append(candidates, candidate{repo.Name, repoPath, repo})
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	// Pass 2: fan out git calls in parallel.
	agents := make([]WorktreeInfo, len(candidates))
	var wg sync.WaitGroup
	wg.Add(len(candidates))
	for i, c := range candidates {
		go func(idx int, cand candidate) {
			defer wg.Done()
			branch, err := GetCurrentBranch(cand.path)
			if err != nil {
				branch = "unknown"
			}
			agents[idx] = WorktreeInfo{
				Name:             cand.name,
				Path:             cand.path,
				Branch:           branch,
				Workspace:        r.Workspace,
				Repo:             cand.repo,
				IsLinkedWorktree: true,
			}
		}(i, c)
	}
	wg.Wait()
	return agents, nil
}

// ResolveAgentByName finds a single agent worktree by name via direct path
// lookup. Iterates repos and checks <wsPath>/worktrees/<repo>/<name>/.git,
// avoiding a full scan of all agents. Only spawns one git subprocess.
//
//nolint:funlen
func (r *Resolver) ResolveAgentByName(name string) (WorktreeInfo, error) {
	if r.Mode != ModeWorkspace || r.Config == nil {
		return WorktreeInfo{}, fmt.Errorf("agent worktree resolution requires workspace mode")
	}
	ws, ok := r.Config.Workspaces[r.Workspace]
	if !ok {
		return WorktreeInfo{}, fmt.Errorf("workspace %q not found in config", r.Workspace)
	}

	// First: check nested agent worktrees at <wsPath>/worktrees/<repo>/<name>/
	for i := range ws.Repos {
		repo := &ws.Repos[i]
		agentPath := filepath.Join(ws.Path, "worktrees", repo.Name, name)
		if _, err := os.Stat(filepath.Join(agentPath, ".git")); err != nil {
			continue
		}
		branch, err := GetCurrentBranch(agentPath)
		if err != nil {
			branch = "unknown"
		}
		return WorktreeInfo{
			Name:             name,
			Path:             agentPath,
			Branch:           branch,
			Workspace:        r.Workspace,
			Repo:             repo,
			IsLinkedWorktree: IsGitLinkedWorktree(agentPath),
		}, nil
	}

	// Fallback: check if the name matches a linked worktree registered as a repo.
	// This handles configs where agent worktrees are listed directly as repos.
	for i := range ws.Repos {
		repo := &ws.Repos[i]
		if repo.Name != name {
			continue
		}
		repoPath := repo.Path
		if !filepath.IsAbs(repoPath) {
			repoPath = filepath.Join(ws.Path, repoPath)
		}
		if !IsGitLinkedWorktree(repoPath) {
			continue // source repo, not an agent
		}
		branch, err := GetCurrentBranch(repoPath)
		if err != nil {
			branch = "unknown"
		}
		return WorktreeInfo{
			Name:             name,
			Path:             repoPath,
			Branch:           branch,
			Workspace:        r.Workspace,
			Repo:             repo,
			IsLinkedWorktree: true,
		}, nil
	}

	return WorktreeInfo{}, fmt.Errorf("agent worktree %q not found", name)
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

// GetWorkspaceRuntimeDir returns the workspace root used for runtime files.
// In workspace mode, this is the configured workspace root path (shared across repos).
// In legacy mode, this returns "." (current directory).
// The result is cached for the lifetime of the process.
func GetWorkspaceRuntimeDir() string {
	workspaceRuntimeDirOnce.Do(func() {
		cfg, err := config.LoadConfig()
		if err != nil || cfg == nil || len(cfg.Workspaces) == 0 {
			workspaceRuntimeDirCache = "."
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
			workspaceRuntimeDirCache = wsConfig.Path
		} else {
			workspaceRuntimeDirCache = "."
		}
	})
	return workspaceRuntimeDirCache
}

// ResetWorkspaceRuntimeDirCache clears the cached workspace runtime directory value. For testing only.
func ResetWorkspaceRuntimeDirCache() {
	workspaceRuntimeDirOnce = sync.Once{}
	workspaceRuntimeDirCache = ""
}

// GetBeadsDir is a legacy alias kept for migration-only code paths.
//
// Deprecated: use GetWorkspaceRuntimeDir.
func GetBeadsDir() string {
	return GetWorkspaceRuntimeDir()
}

// ResetBeadsDirCache is a legacy test alias.
//
// Deprecated: use ResetWorkspaceRuntimeDirCache.
func ResetBeadsDirCache() {
	ResetWorkspaceRuntimeDirCache()
}

var (
	workspaceRuntimeDirCache string
	workspaceRuntimeDirOnce  sync.Once
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

// TestingResetDefaultResolver clears the cached default resolver so NewResolver
// will be re-invoked. Returns the old resolver for restoring in cleanup.
func TestingResetDefaultResolver() *Resolver {
	old := defaultResolver
	defaultResolver = nil
	return old
}

// TestingSetDefaultResolver sets the cached default resolver.
func TestingSetDefaultResolver(r *Resolver) {
	defaultResolver = r
}
