package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// setupWorkspaceConfig seeds the FleetDB-backed workspace projection and the
// machine-local state cache used for repo paths.
func setupWorkspaceConfig(t *testing.T, cfg *LoomConfig) {
	t.Helper()
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	t.Setenv("LOOM_FLEET_DB_ACTOR", "test")
	cfgpkg.InvalidateConfigCache()
	oldResolver := TestingResetDefaultResolver()

	ctx := context.Background()
	handle, err := bootstrap.OpenStore(ctx, configDir, nil)
	if err != nil {
		t.Fatalf("open fleet-db store: %v", err)
	}
	t.Cleanup(func() {
		_ = handle.Close()
		TestingSetDefaultResolver(oldResolver)
		cfgpkg.InvalidateConfigCache()
		ResetWorkspaceRuntimeDirCache()
	})

	state := &bootstrap.StateCache{Workspaces: make(map[string]bootstrap.WorkspaceLocalState)}
	if cfg.DefaultWorkspace != "" {
		key := strings.ToUpper(cfg.DefaultWorkspace)
		state.LastWorkspace = key
		t.Setenv(bootstrap.EnvWorkspace, key)
	}

	names := make([]string, 0, len(cfg.Workspaces))
	for name := range cfg.Workspaces {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		ws := cfg.Workspaces[name]
		key := strings.ToUpper(name)
		if _, err := handle.Store.Workspaces().Create(ctx, store.WorkspaceCreate{
			Key:           key,
			Name:          name,
			DefaultBranch: firstRepoDefaultBranch(ws.Repos),
		}); err != nil {
			t.Fatalf("create workspace %s: %v", key, err)
		}

		localRepos := make(map[string]string, len(ws.Repos))
		for _, repo := range ws.Repos {
			sourceRepoID := repo.SourceRepoID
			if sourceRepoID == "" {
				sourceRepoID = repo.Name
			}
			if _, err := handle.Store.Repos().Create(ctx, store.RepoCreate{
				WorkspaceKey:  key,
				Name:          repo.Name,
				Remote:        repo.Remote,
				DefaultBranch: repo.DefaultBranch,
				Groups:        repo.Groups,
				SourceRepoID:  sourceRepoID,
			}); err != nil {
				t.Fatalf("create repo %s/%s: %v", key, repo.Name, err)
			}
			localRepos[repo.Name] = repo.Path
		}
		state.Workspaces[key] = bootstrap.WorkspaceLocalState{
			Path:  ws.Path,
			Repos: localRepos,
		}
	}

	if err := bootstrap.SaveStateCache(state); err != nil {
		t.Fatalf("save state cache: %v", err)
	}
	cfgpkg.InvalidateConfigCache()
}

func firstRepoDefaultBranch(repos []RepoConfig) string {
	for _, repo := range repos {
		if repo.DefaultBranch != "" {
			return repo.DefaultBranch
		}
	}
	return ""
}

// createGitRepo creates a minimal git repo at the given path with an initial commit.
func createGitRepo(t *testing.T, path string) {
	t.Helper()
	clearGitEnvVars(t)

	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if _, err := RunGitCommand(path, "init", "--initial-branch=main"); err != nil {
		t.Fatalf("git init %s: %v", path, err)
	}
	RunGitCommand(path, "config", "user.email", "test@test.com")
	RunGitCommand(path, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(path, "README.md"), []byte("test"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := RunGitCommand(path, "add", "."); err != nil {
		t.Fatalf("git add %s: %v", path, err)
	}
	if _, err := RunGitCommand(path, "commit", "-m", "initial commit"); err != nil {
		t.Fatalf("git commit %s: %v", path, err)
	}
}

func TestNewResolver_NoWorkspaceConfig(t *testing.T) {
	// No FleetDB workspaces configured.
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	// Reset defaultResolver so it doesn't interfere
	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	if _, err := NewResolver(); err == nil {
		t.Fatal("expected error when no workspaces are configured")
	}
}

func TestNewResolver_WorkspaceMode(t *testing.T) {
	cfg := &LoomConfig{
		DefaultWorkspace: "myws",
		Workspaces: map[string]WorkspaceConfig{
			"myws": {
				Path:  "/tmp/ws",
				Repos: []RepoConfig{{Name: "repo1", Path: "/tmp/ws/repo1"}},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	r, err := NewResolver()
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.GetMode() != ModeWorkspace {
		t.Errorf("expected ModeWorkspace, got %d", r.GetMode())
	}
	if r.WorkspaceName() != "MYWS" {
		t.Errorf("expected workspace 'MYWS', got %q", r.WorkspaceName())
	}
}

func TestNewResolver_WorkspaceMode_NoExplicitWorkspace(t *testing.T) {
	cfg := &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"zebra": {Path: "/tmp/z"},
			"alpha": {Path: "/tmp/a"},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	if _, err := NewResolver(); !errors.Is(err, bootstrap.ErrNoActiveWorkspace) {
		t.Fatalf("NewResolver() error = %v, want ErrNoActiveWorkspace", err)
	}
}

func TestNewResolver_InfersWorkspaceFromCWD(t *testing.T) {
	alphaDir := t.TempDir()
	betaDir := t.TempDir()
	childDir := filepath.Join(betaDir, "repo")
	if err := os.MkdirAll(childDir, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"alpha": {Path: alphaDir},
			"beta":  {Path: betaDir},
		},
	}
	setupWorkspaceConfig(t, cfg)
	t.Chdir(childDir)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	r, err := NewResolver()
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.WorkspaceName() != "BETA" {
		t.Errorf("expected workspace 'BETA', got %q", r.WorkspaceName())
	}
}

func TestNewResolver_HonorsEnvWorkspace(t *testing.T) {
	cfg := &LoomConfig{
		DefaultWorkspace: "alpha",
		Workspaces: map[string]WorkspaceConfig{
			"alpha": {Path: "/tmp/a"},
			"beta":  {Path: "/tmp/b"},
		},
	}
	setupWorkspaceConfig(t, cfg)
	t.Setenv(bootstrap.EnvWorkspace, "BETA")

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	r, err := NewResolver()
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.WorkspaceName() != "BETA" {
		t.Errorf("expected workspace 'BETA', got %q", r.WorkspaceName())
	}
}

func TestResolver_SetWorkspace(t *testing.T) {
	cfg := &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"ws1": {Path: "/tmp/ws1"},
			"ws2": {Path: "/tmp/ws2"},
		},
	}
	setupWorkspaceConfig(t, cfg)
	t.Setenv(bootstrap.EnvWorkspace, "WS1")

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	r, err := NewResolver()
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	// Valid workspace switch
	if err := r.SetWorkspace("WS2"); err != nil {
		t.Fatalf("SetWorkspace(WS2): %v", err)
	}
	if r.WorkspaceName() != "WS2" {
		t.Errorf("expected 'WS2', got %q", r.WorkspaceName())
	}

	// Invalid workspace
	if err := r.SetWorkspace("nonexistent"); err == nil {
		t.Error("expected error for nonexistent workspace")
	}

	// No config resolver
	noConfigR := &Resolver{Mode: ModeWorkspace}
	if err := noConfigR.SetWorkspace("any"); err == nil {
		t.Error("expected error when no config loaded")
	}
}

func TestResolver_DiscoverWorktrees_Workspace(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	// Create git repos
	repo1Path := filepath.Join(tmpDir, "repo1")
	repo2Path := filepath.Join(tmpDir, "repo2")
	createGitRepo(t, repo1Path)
	createGitRepo(t, repo2Path)

	cfg := &LoomConfig{
		DefaultWorkspace: "myws",
		Workspaces: map[string]WorkspaceConfig{
			"myws": {
				Path: tmpDir,
				Repos: []RepoConfig{
					{Name: "repo1", Path: repo1Path},
					{Name: "repo2", Path: repo2Path},
					{Name: "missing", Path: filepath.Join(tmpDir, "nonexistent")},
				},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	r, err := NewResolver()
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	worktrees, err := r.DiscoverWorktrees()
	if err != nil {
		t.Fatalf("DiscoverWorktrees: %v", err)
	}

	// Should find 2 repos (missing one skipped)
	if len(worktrees) != 2 {
		t.Fatalf("expected 2 worktrees, got %d", len(worktrees))
	}

	for _, wt := range worktrees {
		if wt.Workspace != "MYWS" {
			t.Errorf("expected Workspace 'MYWS', got %q", wt.Workspace)
		}
		if wt.Repo == nil {
			t.Error("expected non-nil Repo in workspace mode")
		}
		if wt.Branch != "main" {
			t.Errorf("expected branch 'main', got %q", wt.Branch)
		}
	}
}

func TestResolver_ResolveWorktreePath_Workspace(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	repoPath := filepath.Join(tmpDir, "myrepo")
	createGitRepo(t, repoPath)

	cfg := &LoomConfig{
		DefaultWorkspace: "ws",
		Workspaces: map[string]WorkspaceConfig{
			"ws": {
				Path: tmpDir,
				Repos: []RepoConfig{
					{Name: "myrepo", Path: repoPath},
				},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	r, err := NewResolver()
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	// Resolve by repo name
	path, err := r.ResolveWorktreePath("myrepo")
	if err != nil {
		t.Fatalf("ResolveWorktreePath: %v", err)
	}
	if path != repoPath {
		t.Errorf("expected %s, got %s", repoPath, path)
	}

	// Non-existent repo name
	_, err = r.ResolveWorktreePath("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent repo")
	}

	// Empty string returns cwd
	cwd, _ := os.Getwd()
	path, err = r.ResolveWorktreePath("")
	if err != nil {
		t.Fatalf("ResolveWorktreePath empty: %v", err)
	}
	if path != cwd {
		t.Errorf("expected %s, got %s", cwd, path)
	}

	// Absolute path
	path, err = r.ResolveWorktreePath(repoPath)
	if err != nil {
		t.Fatalf("ResolveWorktreePath absolute: %v", err)
	}
	if path != repoPath {
		t.Errorf("expected %s, got %s", repoPath, path)
	}
}

func TestResolver_ResolveWorktreePath_Workspace_UnregisteredWorktree(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	// Create a registered repo
	registeredPath := filepath.Join(tmpDir, "worktrees", "registered")
	createGitRepo(t, registeredPath)

	// Create an unregistered worktree on disk (exists in worktrees/ but not in config)
	unregisteredPath := filepath.Join(tmpDir, "worktrees", "ember")
	createGitRepo(t, unregisteredPath)

	cfg := &LoomConfig{
		DefaultWorkspace: "ws",
		Workspaces: map[string]WorkspaceConfig{
			"ws": {
				Path: tmpDir,
				Repos: []RepoConfig{
					{Name: "registered", Path: registeredPath},
				},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	r, err := NewResolver()
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	// Registered repo resolves normally
	path, err := r.ResolveWorktreePath("registered")
	if err != nil {
		t.Fatalf("ResolveWorktreePath(registered): %v", err)
	}
	if path != registeredPath {
		t.Errorf("expected %s, got %s", registeredPath, path)
	}

	// Unregistered worktree that exists on disk should error with actionable message
	_, err = r.ResolveWorktreePath("ember")
	if err == nil {
		t.Fatal("expected error for unregistered worktree, got nil")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "exists on disk but is not registered") {
		t.Errorf("expected 'exists on disk but is not registered' in error, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "loom config add-repo") {
		t.Errorf("expected 'loom config add-repo' hint in error, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "ember") {
		t.Errorf("expected worktree name 'ember' in error, got: %s", errMsg)
	}
}

func TestResolver_ResolveWorktreePath_Workspace_TrulyMissing(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	repoPath := filepath.Join(tmpDir, "worktrees", "myrepo")
	createGitRepo(t, repoPath)

	cfg := &LoomConfig{
		DefaultWorkspace: "ws",
		Workspaces: map[string]WorkspaceConfig{
			"ws": {
				Path: tmpDir,
				Repos: []RepoConfig{
					{Name: "myrepo", Path: repoPath},
				},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	r, err := NewResolver()
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	// Name that doesn't exist on disk or in config gets the generic error
	_, err = r.ResolveWorktreePath("ghost")
	if err == nil {
		t.Fatal("expected error for nonexistent worktree, got nil")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "not found in workspace") {
		t.Errorf("expected 'not found in workspace' in error, got: %s", errMsg)
	}
	// Should NOT contain the add-repo hint since it doesn't exist on disk
	if strings.Contains(errMsg, "loom config add-repo") {
		t.Errorf("should not suggest add-repo for worktree that doesn't exist on disk, got: %s", errMsg)
	}
}

func TestResolver_GetDefaultBranch_Workspace(t *testing.T) {
	cfg := &LoomConfig{
		DefaultWorkspace: "ws",
		Workspaces: map[string]WorkspaceConfig{
			"ws": {
				Path: "/tmp/ws",
				Repos: []RepoConfig{
					{Name: "repo1", Path: "/tmp/ws/repo1", DefaultBranch: "develop"},
					{Name: "repo2", Path: "/tmp/ws/repo2"},
				},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	r, err := NewResolver()
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	branch := r.GetDefaultBranch()
	if branch != "develop" {
		t.Errorf("expected 'develop', got %q", branch)
	}
}

func TestResolver_GetDefaultBranch_Workspace_NoOverride(t *testing.T) {
	cfg := &LoomConfig{
		DefaultWorkspace: "ws",
		Workspaces: map[string]WorkspaceConfig{
			"ws": {
				Path: "/tmp/ws",
				Repos: []RepoConfig{
					{Name: "repo1", Path: "/tmp/ws/repo1"},
				},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	r, err := NewResolver()
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	branch := r.GetDefaultBranch()
	if branch != "main" {
		t.Errorf("expected 'main', got %q", branch)
	}
}

func TestResolver_GetWorktreesDir_Workspace(t *testing.T) {
	cfg := &LoomConfig{
		DefaultWorkspace: "ws",
		Workspaces: map[string]WorkspaceConfig{
			"ws": {
				Path: "/home/user/workspace",
				Repos: []RepoConfig{
					{Name: "r", Path: "/home/user/workspace/r"},
				},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	r, err := NewResolver()
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	dir := r.GetWorktreesDir()
	if dir != "/home/user/workspace" {
		t.Errorf("expected '/home/user/workspace', got %q", dir)
	}
}

func TestResolver_WorkspaceRepoRelativePaths(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	// Create repo using relative path
	repoPath := filepath.Join(tmpDir, "repos", "myrepo")
	createGitRepo(t, repoPath)

	cfg := &LoomConfig{
		DefaultWorkspace: "ws",
		Workspaces: map[string]WorkspaceConfig{
			"ws": {
				Path: tmpDir,
				Repos: []RepoConfig{
					{Name: "myrepo", Path: "repos/myrepo"}, // relative to workspace path
				},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	r, err := NewResolver()
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	// Discovery should resolve relative paths
	worktrees, err := r.DiscoverWorktrees()
	if err != nil {
		t.Fatalf("DiscoverWorktrees: %v", err)
	}
	if len(worktrees) != 1 {
		t.Fatalf("expected 1 worktree, got %d", len(worktrees))
	}
	if worktrees[0].Path != repoPath {
		t.Errorf("expected path %s, got %s", repoPath, worktrees[0].Path)
	}

	// ResolveWorktreePath should also resolve relative paths
	path, err := r.ResolveWorktreePath("myrepo")
	if err != nil {
		t.Fatalf("ResolveWorktreePath: %v", err)
	}
	if path != repoPath {
		t.Errorf("expected %s, got %s", repoPath, path)
	}
}

func TestResolver_ResolveWorktreePath_WorkspaceAgentWorktree(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	repoPath := filepath.Join(tmpDir, "source-repo")
	createGitRepo(t, repoPath)

	agentPath := filepath.Join(tmpDir, "worktrees", "source-repo", "local-planner")
	if err := os.MkdirAll(filepath.Dir(agentPath), 0755); err != nil {
		t.Fatalf("mkdir agent parent: %v", err)
	}
	if _, err := RunGitCommand(repoPath, "worktree", "add", agentPath, "-b", "local-planner"); err != nil {
		t.Fatalf("git worktree add agent: %v", err)
	}

	cfg := &LoomConfig{
		DefaultWorkspace: "ws",
		Workspaces: map[string]WorkspaceConfig{
			"ws": {
				Path: tmpDir,
				Repos: []RepoConfig{
					{Name: "source-repo", Path: repoPath},
				},
			},
		},
	}
	r := &Resolver{Mode: ModeWorkspace, Config: cfg, Workspace: "ws"}

	path, err := r.ResolveWorktreePath("local-planner")
	if err != nil {
		t.Fatalf("ResolveWorktreePath: %v", err)
	}
	if path != agentPath {
		t.Errorf("ResolveWorktreePath() = %q, want %q", path, agentPath)
	}
}

// --- DefaultBranchForWorktree tests ---

func TestDefaultBranchForWorktree_WithRepoDefaultBranch(t *testing.T) {
	t.Setenv("LOOM_DEFAULT_BRANCH", "")
	wt := WorktreeInfo{
		Name:   "api",
		Path:   "/tmp/api",
		Branch: "feature-x",
		Repo:   &RepoConfig{Name: "api", Path: "/tmp/api", DefaultBranch: "develop"},
	}
	got := DefaultBranchForWorktree(wt)
	if got != "develop" {
		t.Errorf("DefaultBranchForWorktree() = %q, want %q", got, "develop")
	}
}

func TestDefaultBranchForWorktree_WithRepoEmptyDefaultBranch(t *testing.T) {
	t.Setenv("LOOM_DEFAULT_BRANCH", "")
	wt := WorktreeInfo{
		Name:   "api",
		Path:   "/tmp/api",
		Branch: "feature-x",
		Repo:   &RepoConfig{Name: "api", Path: "/tmp/api", DefaultBranch: ""},
	}
	got := DefaultBranchForWorktree(wt)
	if got != "main" {
		t.Errorf("DefaultBranchForWorktree() = %q, want %q (fallback)", got, "main")
	}
}

func TestDefaultBranchForWorktree_WithoutRepo(t *testing.T) {
	t.Setenv("LOOM_DEFAULT_BRANCH", "")
	wt := WorktreeInfo{
		Name:   "falcon",
		Path:   "/tmp/falcon",
		Branch: "falcon",
	}
	got := DefaultBranchForWorktree(wt)
	if got != "main" {
		t.Errorf("DefaultBranchForWorktree() = %q, want %q (default fallback)", got, "main")
	}
}

func TestDefaultBranchForWorktree_EnvOverride(t *testing.T) {
	t.Setenv("LOOM_DEFAULT_BRANCH", "env-branch")
	// Even with a Repo that has DefaultBranch set, env var wins
	wt := WorktreeInfo{
		Name:   "api",
		Path:   "/tmp/api",
		Branch: "feature-x",
		Repo:   &RepoConfig{Name: "api", Path: "/tmp/api", DefaultBranch: "develop"},
	}
	got := DefaultBranchForWorktree(wt)
	if got != "env-branch" {
		t.Errorf("DefaultBranchForWorktree() = %q, want %q (env override)", got, "env-branch")
	}
}

func TestDefaultBranchForWorktree_EnvOverride_NoRepo(t *testing.T) {
	t.Setenv("LOOM_DEFAULT_BRANCH", "env-branch")
	wt := WorktreeInfo{
		Name:   "falcon",
		Path:   "/tmp/falcon",
		Branch: "falcon",
	}
	got := DefaultBranchForWorktree(wt)
	if got != "env-branch" {
		t.Errorf("DefaultBranchForWorktree() = %q, want %q (env override)", got, "env-branch")
	}
}

// --- GetDefaultBranchForWorktrees workspace-mode guard tests ---

func TestGetDefaultBranchForWorktrees_WorkspaceMode_ReturnsFirstRepoDefaultBranch(t *testing.T) {
	t.Setenv("LOOM_DEFAULT_BRANCH", "")
	resetIntegrationBranchCache()
	t.Cleanup(resetIntegrationBranchCache)

	worktrees := []WorktreeInfo{
		{
			Name:   "api",
			Path:   "/tmp/api",
			Branch: "feature-a",
			Repo:   &RepoConfig{Name: "api", Path: "/tmp/api", DefaultBranch: ""},
		},
		{
			Name:   "web",
			Path:   "/tmp/web",
			Branch: "feature-b",
			Repo:   &RepoConfig{Name: "web", Path: "/tmp/web", DefaultBranch: "develop"},
		},
	}
	got := GetDefaultBranchForWorktrees(worktrees)
	if got != "develop" {
		t.Errorf("GetDefaultBranchForWorktrees() = %q, want %q (first non-empty repo default)", got, "develop")
	}
}

func TestGetDefaultBranchForWorktrees_WorkspaceMode_AllEmpty_ReturnMain(t *testing.T) {
	t.Setenv("LOOM_DEFAULT_BRANCH", "")
	resetIntegrationBranchCache()
	t.Cleanup(resetIntegrationBranchCache)

	worktrees := []WorktreeInfo{
		{
			Name:   "api",
			Path:   "/tmp/api",
			Branch: "feature-a",
			Repo:   &RepoConfig{Name: "api", Path: "/tmp/api"},
		},
		{
			Name:   "web",
			Path:   "/tmp/web",
			Branch: "feature-b",
			Repo:   &RepoConfig{Name: "web", Path: "/tmp/web"},
		},
	}
	got := GetDefaultBranchForWorktrees(worktrees)
	if got != "main" {
		t.Errorf("GetDefaultBranchForWorktrees() = %q, want %q (all empty defaults)", got, "main")
	}
}

func TestGetDefaultBranchForWorktrees_WorkspaceMode_SingleWorktree(t *testing.T) {
	t.Setenv("LOOM_DEFAULT_BRANCH", "")
	// Single workspace worktree should still use repo's DefaultBranch
	worktrees := []WorktreeInfo{
		{
			Name:   "api",
			Path:   "/tmp/api",
			Branch: "feature-a",
			Repo:   &RepoConfig{Name: "api", Path: "/tmp/api", DefaultBranch: "develop"},
		},
	}
	got := GetDefaultBranchForWorktrees(worktrees)
	if got != "develop" {
		t.Errorf("GetDefaultBranchForWorktrees() = %q, want %q (single workspace worktree)", got, "develop")
	}
}

func TestGetDefaultBranchForWorktrees_NoWorkspaceConfig_PreservedBehavior(t *testing.T) {
	t.Setenv("LOOM_DEFAULT_BRANCH", "")
	resetIntegrationBranchCache()
	t.Cleanup(resetIntegrationBranchCache)

	// Worktrees without repo config have Repo == nil; with fewer than 2 worktrees, returns "main".
	worktrees := []WorktreeInfo{
		{Name: "falcon", Path: "/tmp/falcon", Branch: "falcon"},
	}
	got := GetDefaultBranchForWorktrees(worktrees)
	if got != "main" {
		t.Errorf("GetDefaultBranchForWorktrees() = %q, want %q (default single)", got, "main")
	}
}

func TestGetDefaultBranchForWorktrees_WorkspaceMode_EnvOverride(t *testing.T) {
	t.Setenv("LOOM_DEFAULT_BRANCH", "env-override")
	worktrees := []WorktreeInfo{
		{
			Name:   "api",
			Path:   "/tmp/api",
			Branch: "feature-a",
			Repo:   &RepoConfig{Name: "api", Path: "/tmp/api", DefaultBranch: "develop"},
		},
		{
			Name:   "web",
			Path:   "/tmp/web",
			Branch: "feature-b",
			Repo:   &RepoConfig{Name: "web", Path: "/tmp/web", DefaultBranch: "staging"},
		},
	}
	got := GetDefaultBranchForWorktrees(worktrees)
	if got != "env-override" {
		t.Errorf("GetDefaultBranchForWorktrees() = %q, want %q (env override)", got, "env-override")
	}
}

// --- DetectIntegrationBranch workspace-mode guard tests ---

func TestDetectIntegrationBranch_WorkspaceMode_ReturnsEmpty(t *testing.T) {
	// When any worktree has Repo set, DetectIntegrationBranch should bail out
	worktrees := []WorktreeInfo{
		{
			Name:   "api",
			Path:   "/tmp/api",
			Branch: "feature-a",
			Repo:   &RepoConfig{Name: "api", Path: "/tmp/api", DefaultBranch: "develop"},
		},
		{
			Name:   "web",
			Path:   "/tmp/web",
			Branch: "feature-b",
			Repo:   &RepoConfig{Name: "web", Path: "/tmp/web"},
		},
	}
	got := DetectIntegrationBranch(worktrees)
	if got != "" {
		t.Errorf("DetectIntegrationBranch() = %q, want empty string for workspace-mode worktrees", got)
	}
}

func TestDetectIntegrationBranch_WorkspaceMode_MixedRepoNil(t *testing.T) {
	// Even if only one worktree has Repo set, should return empty
	worktrees := []WorktreeInfo{
		{
			Name:   "api",
			Path:   "/tmp/api",
			Branch: "feature-a",
			Repo:   &RepoConfig{Name: "api", Path: "/tmp/api"},
		},
		{
			Name:   "unassigned-wt",
			Path:   "/tmp/unassigned",
			Branch: "feature-b",
			// Repo is nil when no workspace metadata is present.
		},
	}
	got := DetectIntegrationBranch(worktrees)
	if got != "" {
		t.Errorf("DetectIntegrationBranch() = %q, want empty string when any worktree has Repo set", got)
	}
}

func TestDetectIntegrationBranch_NoWorkspaceConfig_NoRepoSet(t *testing.T) {
	// With no git repo available, DetectIntegrationBranch will fail git commands
	// and return empty, but the important thing is it doesn't bail out early
	// (the workspace-mode guard does not trigger)
	worktrees := []WorktreeInfo{
		{Name: "falcon", Path: "/nonexistent/path", Branch: "falcon"},
		{Name: "nova", Path: "/nonexistent/path", Branch: "nova"},
	}
	// This will return "" because git commands fail, but critically it
	// does NOT bail out due to the workspace-mode guard (Repo is nil)
	got := DetectIntegrationBranch(worktrees)
	if got != "" {
		t.Errorf("DetectIntegrationBranch() = %q, want empty (git commands fail)", got)
	}
}

func TestWorktreeInfo_WorkspaceFields(t *testing.T) {
	// No workspace config: Workspace and Repo should be zero values
	unassignedInfo := WorktreeInfo{
		Name:   "test",
		Path:   "/tmp/test",
		Branch: "main",
	}
	if unassignedInfo.Workspace != "" {
		t.Errorf("expected empty Workspace, got %q", unassignedInfo.Workspace)
	}
	if unassignedInfo.Repo != nil {
		t.Errorf("expected nil Repo, got %+v", unassignedInfo.Repo)
	}

	// Workspace mode: fields populated
	repo := &RepoConfig{Name: "myrepo", Path: "/tmp/myrepo"}
	wsInfo := WorktreeInfo{
		Name:      "myrepo",
		Path:      "/tmp/myrepo",
		Branch:    "feature",
		Workspace: "myws",
		Repo:      repo,
	}
	if wsInfo.Workspace != "myws" {
		t.Errorf("expected 'myws', got %q", wsInfo.Workspace)
	}
	if wsInfo.Repo != repo {
		t.Errorf("Repo pointer mismatch")
	}
}
