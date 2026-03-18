package cli

import (
	"context"
	"fmt"
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
		return createCloneWorkspace(ctx, cfg, req.Name, wsDir, req.CloneURL)
	default:
		return fmt.Errorf("unsupported workspace type: %s", req.Type)
	}
}

// createEmptyWorkspace creates worktrees from existing repos.
func createEmptyWorkspace(ctx context.Context, cfg *LoomConfig, wsName, wsDir, branch string, repoPaths []string) error {
	type resolvedRepo struct {
		path string
		name string
	}
	var resolved []resolvedRepo
	seenNames := make(map[string]string)

	for _, rp := range repoPaths {
		rp = strings.TrimSpace(rp)
		if rp == "" {
			continue
		}

		absPath, err := filepath.Abs(rp)
		if err != nil {
			return fmt.Errorf("cannot resolve path %q: %w", rp, err)
		}

		info, err := os.Stat(absPath)
		if err != nil {
			return fmt.Errorf("repo path does not exist: %s", absPath)
		}
		if !info.IsDir() {
			return fmt.Errorf("repo path is not a directory: %s", absPath)
		}

		gitDir := filepath.Join(absPath, ".git")
		if _, err := os.Stat(gitDir); err != nil {
			return fmt.Errorf("not a git repository: %s", absPath)
		}

		baseName := filepath.Base(absPath)
		if prev, exists := seenNames[baseName]; exists {
			return fmt.Errorf("duplicate repo name %q from paths %s and %s", baseName, prev, absPath)
		}
		seenNames[baseName] = absPath
		resolved = append(resolved, resolvedRepo{path: absPath, name: baseName})
	}

	if len(resolved) == 0 {
		return fmt.Errorf("no valid repos specified")
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

// createCloneWorkspace clones a repo and creates a workspace from it.
func createCloneWorkspace(ctx context.Context, cfg *LoomConfig, wsName, wsDir, cloneURL string) error {
	if err := os.MkdirAll(wsDir, 0755); err != nil {
		return fmt.Errorf("cannot create workspace directory: %w", err)
	}

	cleanupDir := func() { _ = os.RemoveAll(wsDir) }

	// Clone into the workspace directory
	clonePath := filepath.Join(wsDir, "repo")
	cmd := exec.CommandContext(ctx, "git", "clone", cloneURL, clonePath) //nolint:gosec // URL validated by handler
	if output, err := cmd.CombinedOutput(); err != nil {
		cleanupDir()
		return fmt.Errorf("git clone failed: %s", strings.TrimSpace(string(output)))
	}

	repoName := filepath.Base(clonePath)
	repos := []RepoConfig{{Name: repoName, Path: clonePath}}

	// bd init (best-effort)
	_ = execCommand(wsDir, "bd", "init")

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

// buildWorkspaceInfo loads workspace topology from config and daemon config.
// Returns nil when no workspaces are configured (single-repo mode).
func buildWorkspaceInfo() (*webui.WorkspaceData, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	if cfg == nil || len(cfg.Workspaces) == 0 {
		return nil, nil
	}

	// Find the active workspace (use default or first available)
	wsName := cfg.DefaultWorkspace
	ws, ok := cfg.Workspaces[wsName]
	if !ok {
		// Fall back to first workspace
		for name, w := range cfg.Workspaces {
			wsName = name
			ws = w
			break
		}
	}

	groupSet := make(map[string]bool)
	repos := make([]webui.WorkspaceRepo, 0, len(ws.Repos))
	for _, r := range ws.Repos {
		db := r.DefaultBranch
		if db == "" {
			db = "main"
		}
		remote := r.Remote
		if remote == "" {
			remote = "origin"
		}
		repos = append(repos, webui.WorkspaceRepo{
			Name:          r.Name,
			Path:          r.Path,
			DefaultBranch: db,
			Remote:        remote,
			SourceRepoID:  r.SourceRepoID,
			Groups:        r.Groups,
		})
		for _, g := range r.Groups {
			groupSet[g] = true
		}
	}

	// Convert group set to sorted slice
	groups := make([]string, 0, len(groupSet))
	for g := range groupSet {
		groups = append(groups, g)
	}
	sort.Strings(groups)

	// Collect agent assignments from daemon config
	var agents []webui.WorkspaceAgentInfo
	dc, dcErr := LoadDaemonConfig(".")
	if dcErr == nil && dc != nil {
		for _, a := range dc.Agents {
			name := filepath.Base(a.Worktree)
			agents = append(agents, webui.WorkspaceAgentInfo{
				Name:       name,
				Repos:      a.Repos,
				RepoGroups: a.RepoGroups,
				CrossRepo:  a.CrossRepo,
			})
		}
	}

	// Build lightweight summaries for all configured workspaces
	summaries := make([]webui.WorkspaceSummary, 0, len(cfg.Workspaces))
	for name, w := range cfg.Workspaces {
		summaries = append(summaries, webui.WorkspaceSummary{
			Name:      name,
			Path:      w.Path,
			Active:    name == wsName,
			RepoCount: len(w.Repos),
			IsDefault: name == cfg.DefaultWorkspace,
			Backend:   w.Backend,
		})
	}
	sortWorkspaceSummaries(summaries, cfg.WorkspaceOrder)

	return &webui.WorkspaceData{
		Name:             wsName,
		Path:             ws.Path,
		Repos:            repos,
		Groups:           groups,
		Agents:           agents,
		Workspaces:       summaries,
		WorkspaceOrder:   cfg.WorkspaceOrder,
		DefaultWorkspace: cfg.DefaultWorkspace,
	}, nil
}

// sortWorkspaceSummaries sorts workspace summaries using custom order if provided,
// falling back to alphabetical sort. Items in the order list come first (in order),
// followed by unlisted items sorted alphabetically.
func sortWorkspaceSummaries(summaries []webui.WorkspaceSummary, order []string) {
	if len(order) > 0 {
		orderIndex := make(map[string]int, len(order))
		for i, name := range order {
			orderIndex[name] = i
		}
		sort.SliceStable(summaries, func(i, j int) bool {
			oi, okI := orderIndex[summaries[i].Name]
			oj, okJ := orderIndex[summaries[j].Name]
			if okI && okJ {
				return oi < oj
			}
			if okI {
				return true
			}
			if okJ {
				return false
			}
			return summaries[i].Name < summaries[j].Name
		})
	} else {
		sort.Slice(summaries, func(i, j int) bool {
			return summaries[i].Name < summaries[j].Name
		})
	}
}
