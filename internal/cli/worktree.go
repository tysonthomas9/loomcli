package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// WorktreeInfo holds information about a discovered worktree
type WorktreeInfo struct {
	Name      string
	Path      string
	Branch    string
	Workspace string      // workspace name (empty in legacy mode)
	Repo      *RepoConfig // source repo config (nil in legacy mode)
}

// ResolverMode indicates how the Resolver discovers worktrees
type ResolverMode int

const (
	ModeLegacy    ResolverMode = iota // scan ./worktrees/ directory
	ModeWorkspace                     // read from ~/.loom/config.yaml
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
		return &Resolver{mode: ModeLegacy}, nil
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

// validateWorktreeName checks that a worktree name does not contain path
// traversal sequences. Returns an error if the name is unsafe.
func validateWorktreeName(name string) error {
	cleaned := filepath.Clean(name)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf("invalid worktree name %q: path traversal not allowed", name)
	}
	return nil
}

// resolveLegacyPath is the original ResolveWorktreePath logic.
func resolveLegacyPath(name string) (string, error) {
	if name == "" {
		return os.Getwd()
	}

	// Absolute path - use as-is
	if filepath.IsAbs(name) {
		if _, err := os.Stat(name); err != nil {
			return "", fmt.Errorf("worktree path does not exist: %s", name)
		}
		return name, nil
	}

	// Validate name does not traverse outside worktrees directory
	if err := validateWorktreeName(name); err != nil {
		return "", err
	}

	// Relative name - resolve to worktrees directory
	worktreesDir, err := ResolveWorktreesDir()
	if err != nil {
		return "", err
	}

	worktreePath := filepath.Clean(filepath.Join(worktreesDir, name))

	// Defense-in-depth: verify resolved path is within worktrees directory
	absWorktreesDir := filepath.Clean(worktreesDir)
	if !strings.HasPrefix(worktreePath, absWorktreesDir+string(filepath.Separator)) &&
		worktreePath != absWorktreesDir {
		return "", fmt.Errorf("invalid worktree name %q: resolved path escapes worktrees directory", name)
	}

	if _, err := os.Stat(worktreePath); err != nil {
		return "", fmt.Errorf("worktree '%s' not found at %s", name, worktreePath)
	}

	return worktreePath, nil
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

// getWorktreesDirLegacy is the original GetWorktreesDir logic.
func getWorktreesDirLegacy() string {
	if worktreesFlag != "" {
		return filepath.Clean(worktreesFlag)
	}
	if dir := os.Getenv("LOOM_WORKTREES_DIR"); dir != "" {
		return filepath.Clean(dir)
	}
	return "worktrees"
}

// GetWorktreesDir returns the worktrees directory path
// Priority: --worktrees flag > LOOM_WORKTREES_DIR env var > default "worktrees"
func GetWorktreesDir() string {
	return getDefaultResolver().GetWorktreesDir()
}

// GetDefaultBranch returns the default integration branch.
// Resolution order: LOOM_DEFAULT_BRANCH env var > auto-detected from worktree topology > "main"
// This is a convenience wrapper that discovers worktrees automatically.
// When worktrees are already available, use GetDefaultBranchForWorktrees instead.
func GetDefaultBranch() string {
	return getDefaultResolver().GetDefaultBranch()
}

// integrationBranchCache caches the result of DetectIntegrationBranch.
// The integration branch changes rarely (only when someone pushes a new
// remote branch), so a 60s TTL avoids the O(candidates × worktrees) git
// commands that otherwise run on every monitor data collection.
var (
	integrationBranchMu      sync.Mutex
	integrationBranchCache   string
	integrationBranchCacheAt time.Time
	integrationBranchTTL     = 60 * time.Second
)

// DefaultBranchForWorktree returns the default branch for a single worktree.
// In workspace mode (Repo != nil), uses RepoConfig.DefaultBranch with "main" fallback.
// In legacy mode, returns "main" (caller should use GetDefaultBranchForWorktrees for auto-detection).
func DefaultBranchForWorktree(wt WorktreeInfo) string {
	if branch := os.Getenv("LOOM_DEFAULT_BRANCH"); branch != "" {
		return branch
	}
	if wt.Repo != nil && wt.Repo.DefaultBranch != "" {
		return wt.Repo.DefaultBranch
	}
	return "main"
}

// GetDefaultBranchForWorktrees returns the default integration branch using
// pre-discovered worktrees to avoid redundant filesystem/git operations.
func GetDefaultBranchForWorktrees(worktrees []WorktreeInfo) string {
	if branch := os.Getenv("LOOM_DEFAULT_BRANCH"); branch != "" {
		return branch
	}
	// In workspace mode, worktrees may span multiple repos.
	// DetectIntegrationBranch uses worktrees[0].Path for all git ops,
	// which is meaningless across repos. Use per-repo config instead.
	for _, wt := range worktrees {
		if wt.Repo != nil {
			// Workspace mode: return first non-empty DefaultBranch, or "main"
			for _, w := range worktrees {
				if w.Repo != nil && w.Repo.DefaultBranch != "" {
					return w.Repo.DefaultBranch
				}
			}
			return "main"
		}
	}

	if len(worktrees) < 2 {
		return "main"
	}

	integrationBranchMu.Lock()
	if integrationBranchCache != "" && time.Since(integrationBranchCacheAt) < integrationBranchTTL {
		result := integrationBranchCache
		integrationBranchMu.Unlock()
		return result
	}
	integrationBranchMu.Unlock()

	result := "main"
	if detected := DetectIntegrationBranch(worktrees); detected != "" {
		result = detected
	}

	integrationBranchMu.Lock()
	integrationBranchCache = result
	integrationBranchCacheAt = time.Now()
	integrationBranchMu.Unlock()

	return result
}

// DetectIntegrationBranch analyzes worktree branches to find a common integration
// branch that is closer than main. Returns empty string if no such branch is found.
func DetectIntegrationBranch(worktrees []WorktreeInfo) string {
	if len(worktrees) < 2 {
		return ""
	}

	// Safety: if any worktree has Repo set (workspace mode), cross-repo
	// git operations are meaningless. Return empty to skip detection.
	for _, wt := range worktrees {
		if wt.Repo != nil {
			return ""
		}
	}

	repoPath := worktrees[0].Path

	// Get all remote branches as candidates
	output, err := RunGitCommand(repoPath, "branch", "-r", "--format=%(refname:short)")
	if err != nil {
		return ""
	}

	// Build set of worktree branch names (with origin/ prefix) to exclude
	wtBranches := make(map[string]bool)
	for _, wt := range worktrees {
		wtBranches["origin/"+wt.Branch] = true
	}

	var candidates []string
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		branch := strings.TrimSpace(line)
		if branch == "" || branch == "origin/HEAD" {
			continue
		}
		// Skip worktree branches themselves and main/master
		if wtBranches[branch] || branch == "origin/main" || branch == "origin/master" {
			continue
		}
		candidates = append(candidates, branch)
	}

	// For each candidate, check if it's an ancestor of ALL worktree branches
	bestBranch := ""
	bestMaxDist := -1

	for _, candidate := range candidates {
		isAncestorOfAll := true
		maxDist := 0

		for _, wt := range worktrees {
			// Check if candidate is ancestor of worktree branch
			_, err := RunGitCommand(repoPath, "merge-base", "--is-ancestor", candidate, "origin/"+wt.Branch)
			if err != nil {
				isAncestorOfAll = false
				break
			}

			// Get distance (commits between candidate and worktree branch)
			distOutput, err := RunGitCommand(repoPath, "rev-list", "--count", candidate+"..origin/"+wt.Branch)
			if err != nil {
				isAncestorOfAll = false
				break
			}
			dist, _ := strconv.Atoi(strings.TrimSpace(distOutput))
			if dist > maxDist {
				maxDist = dist
			}
		}

		if !isAncestorOfAll {
			continue
		}

		// Pick the candidate closest to all worktrees (smallest max distance)
		if bestBranch == "" || maxDist < bestMaxDist {
			bestBranch = candidate
			bestMaxDist = maxDist
		}
	}

	if bestBranch == "" {
		return ""
	}

	// Only use detected branch if it's actually closer than main/master
	// Try origin/main first, fall back to origin/master
	mainRef := "origin/main"
	if _, err := RunGitCommand(repoPath, "rev-parse", "--verify", "origin/main"); err != nil {
		if _, err := RunGitCommand(repoPath, "rev-parse", "--verify", "origin/master"); err != nil {
			// Neither main nor master exists as remote branch; can't compare
			return ""
		}
		mainRef = "origin/master"
	}
	mainMaxDist := 0
	for _, wt := range worktrees {
		distOutput, err := RunGitCommand(repoPath, "rev-list", "--count", mainRef+"..origin/"+wt.Branch)
		if err != nil {
			return ""
		}
		dist, _ := strconv.Atoi(strings.TrimSpace(distOutput))
		if dist > mainMaxDist {
			mainMaxDist = dist
		}
	}
	if bestMaxDist >= mainMaxDist {
		return ""
	}

	return strings.TrimPrefix(bestBranch, "origin/")
}

// ResolveWorktreesDir returns the absolute path to the worktrees directory
// If the configured path is absolute, use it directly; otherwise join with scriptDir
func ResolveWorktreesDir() (string, error) {
	dir := getWorktreesDirLegacy()
	if filepath.IsAbs(dir) {
		return dir, nil
	}
	scriptDir, err := GetScriptDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(scriptDir, dir), nil
}

// GetScriptDir returns the directory where loom is run from
func GetScriptDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current directory: %w", err)
	}
	return cwd, nil
}

// ResolveWorktreePath converts a worktree name to its full path
// Accepts:
//   - A worktree name (e.g., "falcon") -> ./worktrees/falcon
//   - An absolute path (e.g., /path/to/worktree) -> as-is
//   - Empty string -> current directory
func ResolveWorktreePath(name string) (string, error) {
	return getDefaultResolver().ResolveWorktreePath(name)
}

// DiscoverWorktrees finds all worktrees in the worktrees directory
func DiscoverWorktrees() ([]WorktreeInfo, error) {
	return getDefaultResolver().DiscoverWorktrees()
}

// GetCurrentBranch returns the current branch for a git directory
func GetCurrentBranch(path string) (string, error) {
	output, err := RunGitCommand(path, "branch", "--show-current")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}
