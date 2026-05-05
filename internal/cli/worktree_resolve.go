package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// Resolver abstracts workspace repo discovery.
type Resolver struct {
	Mode      ResolverMode
	Config    *config.LoomConfig
	Workspace string // active workspace name (workspace mode only)
}

// NewResolver creates a workspace resolver from configured workspaces.
func NewResolver() (*Resolver, error) {
	cfg, err := config.LoadConfigCached()
	if err != nil {
		return nil, fmt.Errorf("load workspace config: %w", err)
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
	return nil, fmt.Errorf("no workspaces configured")
}

// Mode returns the resolver's current mode.
// GetMode returns the resolver mode.
func (r *Resolver) GetMode() ResolverMode {
	return r.Mode
}

// WorkspaceName returns the active workspace name.
func (r *Resolver) WorkspaceName() string {
	return r.Workspace
}

// SetWorkspace switches the active workspace. Returns an error if the
// workspace name is not found in the config.
func (r *Resolver) SetWorkspace(name string) error {
	if r.Config == nil {
		return fmt.Errorf("no config loaded; cannot set workspace")
	}
	if _, ok := r.Config.Workspaces[name]; ok {
		r.Workspace = name
		return nil
	}
	normalized := strings.ToUpper(name)
	if _, ok := r.Config.Workspaces[normalized]; ok {
		r.Workspace = normalized
		return nil
	}
	return fmt.Errorf("workspace %q not found in config", name)
}

// DiscoverWorktrees returns repos from the active workspace.
func (r *Resolver) DiscoverWorktrees() ([]WorktreeInfo, error) {
	return r.discoverWorkspace()
}

// discoverWorkspace reads repos from the active workspace config.
// Returns repo-level entries only — agent worktrees are discovered
// separately via DiscoverAgentWorktrees.
func (r *Resolver) discoverWorkspace() ([]WorktreeInfo, error) {
	if r == nil || r.Config == nil || len(r.Config.Workspaces) == 0 {
		return nil, nil
	}
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
// Only valid when a workspace config is loaded.
// Each returned WorktreeInfo has Repo set to the parent repo's config,
// giving callers access to DefaultBranch and Remote.
func (r *Resolver) DiscoverAgentWorktrees() ([]WorktreeInfo, error) {
	if r.Mode != ModeWorkspace || r.Config == nil {
		return nil, fmt.Errorf("agent worktree discovery requires workspace mode")
	}
	ws, ok := r.Config.Workspaces[r.Workspace]
	if !ok {
		return nil, fmt.Errorf("workspace %q not found in config", r.Workspace)
	}

	candidates := r.agentWorktreeCandidates(ws)
	if len(candidates) == 0 {
		return nil, nil
	}
	return r.agentWorktreeInfos(candidates), nil
}

type agentWorktreeCandidate struct {
	name string
	path string
	repo *config.RepoConfig
}

func (r *Resolver) agentWorktreeCandidates(ws config.WorkspaceConfig) []agentWorktreeCandidate {
	var candidates []agentWorktreeCandidate
	seen := make(map[string]struct{})
	for _, c := range stateAgentWorktreeCandidates(r.Workspace, ws) {
		candidates = appendAgentCandidate(candidates, seen, c)
	}
	for _, c := range nestedAgentWorktreeCandidates(ws) {
		candidates = appendAgentCandidate(candidates, seen, c)
	}
	for _, c := range linkedRepoAgentWorktreeCandidates(ws, seen) {
		candidates = appendAgentCandidate(candidates, seen, c)
	}
	return candidates
}

func stateAgentWorktreeCandidates(workspace string, ws config.WorkspaceConfig) []agentWorktreeCandidate {
	var candidates []agentWorktreeCandidate
	if sc, err := bootstrap.LoadStateCache(); err == nil && sc != nil {
		local := sc.Workspaces[workspace]
		names := make([]string, 0, len(local.Agents))
		for name := range local.Agents {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			agentPath := local.Agents[name].Worktree
			if agentPath == "" {
				continue
			}
			if _, err := os.Stat(filepath.Join(agentPath, ".git")); err != nil {
				continue
			}
			candidates = append(candidates, agentWorktreeCandidate{name: name, path: agentPath, repo: repoForAgentWorktree(ws, name, agentPath)})
		}
	}
	return candidates
}

func nestedAgentWorktreeCandidates(ws config.WorkspaceConfig) []agentWorktreeCandidate {
	var candidates []agentWorktreeCandidate
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
			candidates = append(candidates, agentWorktreeCandidate{entry.Name(), agentPath, repo})
		}
	}
	return candidates
}

func linkedRepoAgentWorktreeCandidates(ws config.WorkspaceConfig, seen map[string]struct{}) []agentWorktreeCandidate {
	var candidates []agentWorktreeCandidate
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
		candidates = append(candidates, agentWorktreeCandidate{repo.Name, repoPath, repo})
	}
	return candidates
}

func appendAgentCandidate(
	candidates []agentWorktreeCandidate,
	seen map[string]struct{},
	candidate agentWorktreeCandidate,
) []agentWorktreeCandidate {
	key := filepath.Clean(candidate.path)
	if _, ok := seen[key]; ok {
		return candidates
	}
	seen[key] = struct{}{}
	return append(candidates, candidate)
}

func (r *Resolver) agentWorktreeInfos(candidates []agentWorktreeCandidate) []WorktreeInfo {
	agents := make([]WorktreeInfo, len(candidates))
	var wg sync.WaitGroup
	wg.Add(len(candidates))
	for i, c := range candidates {
		go func(idx int, cand agentWorktreeCandidate) {
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
				IsLinkedWorktree: IsGitLinkedWorktree(cand.path),
			}
		}(i, c)
	}
	wg.Wait()
	return agents
}

func repoForAgentWorktree(ws config.WorkspaceConfig, agentName, agentPath string) *config.RepoConfig {
	for i := range ws.Repos {
		repo := &ws.Repos[i]
		nestedPath := filepath.Join(ws.Path, "worktrees", repo.Name, agentName)
		if filepath.Clean(agentPath) == filepath.Clean(nestedPath) {
			return repo
		}
	}
	if len(ws.Repos) > 0 {
		return &ws.Repos[0]
	}
	return nil
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

	if sc, err := bootstrap.LoadStateCache(); err == nil && sc != nil {
		if localAgent := sc.Workspaces[r.Workspace].Agents[name]; localAgent.Worktree != "" {
			if _, err := os.Stat(filepath.Join(localAgent.Worktree, ".git")); err == nil {
				branch, err := GetCurrentBranch(localAgent.Worktree)
				if err != nil {
					branch = "unknown"
				}
				return WorktreeInfo{
					Name:             name,
					Path:             localAgent.Worktree,
					Branch:           branch,
					Workspace:        r.Workspace,
					Repo:             repoForAgentWorktree(ws, name, localAgent.Worktree),
					IsLinkedWorktree: IsGitLinkedWorktree(localAgent.Worktree),
				}, nil
			}
		}
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
// active workspace.
func (r *Resolver) ResolveWorktreePath(name string) (string, error) {
	if name != "" {
		if wt, err := r.ResolveAgentByName(name); err == nil {
			return wt.Path, nil
		}
	}
	return r.ResolveWorkspacePath(name)
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
	if r == nil || r.Config == nil || len(r.Config.Workspaces) == 0 {
		return "", fmt.Errorf("repo %q not found: no workspace configured", name)
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

// GetWorktreesDir returns the active workspace root path.
func (r *Resolver) GetWorktreesDir() string {
	if r != nil && r.Config != nil {
		if ws, ok := r.Config.Workspaces[r.Workspace]; ok && ws.Path != "" {
			return ws.Path
		}
	}
	return "."
}

// GetDefaultBranch returns the default integration branch.
// Resolution order: LOOM_DEFAULT_BRANCH env var > workspace config > "main"
func (r *Resolver) GetDefaultBranch() string {
	if branch := os.Getenv("LOOM_DEFAULT_BRANCH"); branch != "" {
		return branch
	}
	if r != nil && r.Config != nil {
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
	return "main"
}

// SetRepoDefaultBranch updates the default branch for a named repo in FleetDB.
func (r *Resolver) SetRepoDefaultBranch(repoName, branch string) error {
	if r.Mode != ModeWorkspace || r.Config == nil {
		return fmt.Errorf("target branch update only supported in workspace mode")
	}
	ctx := context.Background()
	dataDir := bootstrap.LoomDir()
	if dataDir == "" {
		return fmt.Errorf("cannot determine loom data directory")
	}
	handle, err := bootstrap.OpenStore(ctx, dataDir, nil)
	if err != nil {
		return fmt.Errorf("open fleet-db store: %w", err)
	}
	defer func() { _ = handle.Close() }()
	if _, err := handle.Store.Repos().Update(ctx, r.Workspace, repoName, store.RepoUpdate{DefaultBranch: &branch}); err != nil {
		return fmt.Errorf("update repo %q default branch in workspace %q: %w", repoName, r.Workspace, err)
	}
	if ws, ok := r.Config.Workspaces[r.Workspace]; ok {
		for i := range ws.Repos {
			if ws.Repos[i].Name == repoName {
				ws.Repos[i].DefaultBranch = branch
				r.Config.Workspaces[r.Workspace] = ws
				break
			}
		}
	}
	return nil
}

// GetWorkspaceRuntimeDir returns the workspace root used for runtime files.
// This is the configured workspace root path shared across repos.
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
			r = &Resolver{Mode: ModeWorkspace, Config: &config.LoomConfig{Workspaces: map[string]config.WorkspaceConfig{}}}
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
