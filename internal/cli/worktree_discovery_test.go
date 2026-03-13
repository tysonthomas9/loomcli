package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// resetIntegrationBranchCache clears the integration branch cache for testing.
func resetIntegrationBranchCache() {
	integrationBranchMu.Lock()
	integrationBranchCache = ""
	integrationBranchCacheAt = time.Time{}
	integrationBranchMu.Unlock()
}

func TestGetDefaultBranchForWorktrees_EnvOverride(t *testing.T) {
	resetIntegrationBranchCache()
	t.Setenv("LOOM_DEFAULT_BRANCH", "develop")

	worktrees := []WorktreeInfo{
		{Name: "falcon", Path: "/tmp/falcon", Branch: "falcon"},
		{Name: "nova", Path: "/tmp/nova", Branch: "nova"},
	}

	result := GetDefaultBranchForWorktrees(worktrees)
	if result != "develop" {
		t.Errorf("expected 'develop', got %q", result)
	}
}

func TestGetDefaultBranchForWorktrees_TooFewWorktrees(t *testing.T) {
	resetIntegrationBranchCache()
	t.Setenv("LOOM_DEFAULT_BRANCH", "")

	// Zero worktrees
	result := GetDefaultBranchForWorktrees(nil)
	if result != "main" {
		t.Errorf("expected 'main' for nil worktrees, got %q", result)
	}

	result = GetDefaultBranchForWorktrees([]WorktreeInfo{})
	if result != "main" {
		t.Errorf("expected 'main' for empty worktrees, got %q", result)
	}

	// One worktree
	result = GetDefaultBranchForWorktrees([]WorktreeInfo{
		{Name: "falcon", Path: "/tmp/falcon", Branch: "falcon"},
	})
	if result != "main" {
		t.Errorf("expected 'main' for single worktree, got %q", result)
	}
}

func TestGetDefaultBranchForWorktrees_CacheHit(t *testing.T) {
	resetIntegrationBranchCache()
	t.Setenv("LOOM_DEFAULT_BRANCH", "")

	// Pre-populate cache with a known value
	integrationBranchMu.Lock()
	integrationBranchCache = "cached-branch"
	integrationBranchCacheAt = time.Now()
	integrationBranchMu.Unlock()
	t.Cleanup(resetIntegrationBranchCache)

	worktrees := []WorktreeInfo{
		{Name: "falcon", Path: "/tmp/falcon", Branch: "falcon"},
		{Name: "nova", Path: "/tmp/nova", Branch: "nova"},
	}

	// No command mock installed - if cache is missed, RunGitCommand will fail
	// This proves the cache is being used
	result := GetDefaultBranchForWorktrees(worktrees)
	if result != "cached-branch" {
		t.Errorf("expected 'cached-branch', got %q", result)
	}
}

func TestGetDefaultBranchForWorktrees_CacheExpired(t *testing.T) {
	resetIntegrationBranchCache()
	t.Setenv("LOOM_DEFAULT_BRANCH", "")

	// Pre-populate cache with expired value
	integrationBranchMu.Lock()
	integrationBranchCache = "stale-branch"
	integrationBranchCacheAt = time.Now().Add(-2 * integrationBranchTTL)
	integrationBranchMu.Unlock()
	t.Cleanup(resetIntegrationBranchCache)

	// Mock git commands for DetectIntegrationBranch - branch -r returns no candidates
	mock := NewCommandMock(t, []CommandStub{
		{
			Name:   "git",
			Args:   []string{"branch", "-r", "--format=%(refname:short)"},
			Stdout: "origin/main\n",
		},
	})
	mock.Install()

	worktrees := []WorktreeInfo{
		{Name: "falcon", Path: "/tmp/repo", Branch: "falcon"},
		{Name: "nova", Path: "/tmp/repo", Branch: "nova"},
	}

	result := GetDefaultBranchForWorktrees(worktrees)
	// DetectIntegrationBranch returns "" (no candidates besides main), so result is "main"
	if result != "main" {
		t.Errorf("expected 'main' after cache expiry, got %q", result)
	}
}

func TestDetectIntegrationBranch_GitBranchFails(t *testing.T) {
	mock := NewCommandMock(t, []CommandStub{
		{
			Name:   "git",
			Args:   []string{"branch", "-r", "--format=%(refname:short)"},
			Stderr: "fatal: not a git repository",
			Err:    errors.New("exit status 128"),
		},
	})
	mock.Install()

	worktrees := []WorktreeInfo{
		{Name: "falcon", Path: "/tmp/repo", Branch: "falcon"},
		{Name: "nova", Path: "/tmp/repo", Branch: "nova"},
	}

	result := DetectIntegrationBranch(worktrees)
	if result != "" {
		t.Errorf("expected empty string when git branch fails, got %q", result)
	}
}

func TestDetectIntegrationBranch_NoCandidates(t *testing.T) {
	// Only origin/main and worktree branches - no candidates left
	mock := NewCommandMock(t, []CommandStub{
		{
			Name:   "git",
			Args:   []string{"branch", "-r", "--format=%(refname:short)"},
			Stdout: "origin/main\norigin/falcon\norigin/nova\norigin/HEAD\n",
		},
	})
	mock.Install()

	worktrees := []WorktreeInfo{
		{Name: "falcon", Path: "/tmp/repo", Branch: "falcon"},
		{Name: "nova", Path: "/tmp/repo", Branch: "nova"},
	}

	result := DetectIntegrationBranch(worktrees)
	if result != "" {
		t.Errorf("expected empty string when no candidates, got %q", result)
	}
}

func TestDetectIntegrationBranch_MergeBaseFails(t *testing.T) {
	// A candidate exists, but merge-base --is-ancestor fails for it
	mock := NewCommandMock(t, []CommandStub{
		{
			Name:   "git",
			Args:   []string{"branch", "-r", "--format=%(refname:short)"},
			Stdout: "origin/main\norigin/falcon\norigin/nova\norigin/feature/ui\n",
		},
		// merge-base --is-ancestor for feature/ui with first worktree fails
		{
			Name: "git",
			Args: []string{"merge-base", "--is-ancestor", "origin/feature/ui", "origin/falcon"},
			Err:  errors.New("exit status 1"),
		},
	})
	mock.Install()

	worktrees := []WorktreeInfo{
		{Name: "falcon", Path: "/tmp/repo", Branch: "falcon"},
		{Name: "nova", Path: "/tmp/repo", Branch: "nova"},
	}

	result := DetectIntegrationBranch(worktrees)
	if result != "" {
		t.Errorf("expected empty string when merge-base fails, got %q", result)
	}
}

func TestDetectIntegrationBranch_NoMainOrMaster(t *testing.T) {
	// Candidate is an ancestor of all worktrees, but origin/main and origin/master don't exist
	mock := NewCommandMock(t, []CommandStub{
		{
			Name:   "git",
			Args:   []string{"branch", "-r", "--format=%(refname:short)"},
			Stdout: "origin/falcon\norigin/nova\norigin/feature/base\n",
		},
		// merge-base --is-ancestor for feature/base + falcon
		{
			Name: "git",
			Args: []string{"merge-base", "--is-ancestor", "origin/feature/base", "origin/falcon"},
		},
		// rev-list --count for distance
		{
			Name:   "git",
			Args:   []string{"rev-list", "--count", "origin/feature/base..origin/falcon"},
			Stdout: "2\n",
		},
		// merge-base --is-ancestor for feature/base + nova
		{
			Name: "git",
			Args: []string{"merge-base", "--is-ancestor", "origin/feature/base", "origin/nova"},
		},
		// rev-list --count for distance
		{
			Name:   "git",
			Args:   []string{"rev-list", "--count", "origin/feature/base..origin/nova"},
			Stdout: "3\n",
		},
		// rev-parse --verify origin/main fails
		{
			Name: "git",
			Args: []string{"rev-parse", "--verify", "origin/main"},
			Err:  errors.New("fatal: Needed a single revision"),
		},
		// rev-parse --verify origin/master also fails
		{
			Name: "git",
			Args: []string{"rev-parse", "--verify", "origin/master"},
			Err:  errors.New("fatal: Needed a single revision"),
		},
	})
	mock.Install()

	worktrees := []WorktreeInfo{
		{Name: "falcon", Path: "/tmp/repo", Branch: "falcon"},
		{Name: "nova", Path: "/tmp/repo", Branch: "nova"},
	}

	result := DetectIntegrationBranch(worktrees)
	if result != "" {
		t.Errorf("expected empty string when no main/master exists, got %q", result)
	}
}

func TestResolveWorktreesDir_AbsolutePath(t *testing.T) {
	origFlag := worktreesFlag
	defer func() { worktreesFlag = origFlag }()

	// Set an absolute path via the flag
	worktreesFlag = "/absolute/worktrees/dir"
	t.Setenv("LOOM_WORKTREES_DIR", "")

	dir, err := ResolveWorktreesDir()
	if err != nil {
		t.Fatalf("ResolveWorktreesDir() error: %v", err)
	}
	if dir != "/absolute/worktrees/dir" {
		t.Errorf("expected '/absolute/worktrees/dir', got %q", dir)
	}
}

func TestResolveWorktreesDir_AbsolutePathFromEnv(t *testing.T) {
	origFlag := worktreesFlag
	defer func() { worktreesFlag = origFlag }()
	worktreesFlag = ""

	t.Setenv("LOOM_WORKTREES_DIR", "/env/absolute/path")

	dir, err := ResolveWorktreesDir()
	if err != nil {
		t.Fatalf("ResolveWorktreesDir() error: %v", err)
	}
	if dir != "/env/absolute/path" {
		t.Errorf("expected '/env/absolute/path', got %q", dir)
	}
}

func TestResolveWorktreesDir_RelativePath(t *testing.T) {
	origFlag := worktreesFlag
	defer func() { worktreesFlag = origFlag }()
	worktreesFlag = "my-worktrees"
	t.Setenv("LOOM_WORKTREES_DIR", "")

	dir, err := ResolveWorktreesDir()
	if err != nil {
		t.Fatalf("ResolveWorktreesDir() error: %v", err)
	}
	cwd, _ := os.Getwd()
	expected := filepath.Join(cwd, "my-worktrees")
	if dir != expected {
		t.Errorf("expected %q, got %q", expected, dir)
	}
}

func TestDiscoverLegacy_NonDirEntry(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(tmpDir)

	wtDir := filepath.Join(tmpDir, "worktrees")

	// Create a git repo directory
	createGitRepo(t, filepath.Join(wtDir, "falcon"))

	// Create a plain file in the worktrees directory (should be skipped)
	os.WriteFile(filepath.Join(wtDir, "not-a-dir.txt"), []byte("text"), 0644)

	r := &Resolver{mode: ModeLegacy}
	worktrees, err := r.DiscoverWorktrees()
	if err != nil {
		t.Fatalf("DiscoverWorktrees: %v", err)
	}

	if len(worktrees) != 1 {
		t.Fatalf("expected 1 worktree (file should be skipped), got %d", len(worktrees))
	}
	if worktrees[0].Name != "falcon" {
		t.Errorf("expected worktree 'falcon', got %q", worktrees[0].Name)
	}
}

func TestDiscoverLegacy_BranchErrorFallback(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(tmpDir)

	wtDir := filepath.Join(tmpDir, "worktrees")

	// Create a directory with .git (but not a real repo, so branch detection fails)
	brokenRepo := filepath.Join(wtDir, "broken")
	os.MkdirAll(filepath.Join(brokenRepo, ".git"), 0755)

	r := &Resolver{mode: ModeLegacy}
	worktrees, err := r.DiscoverWorktrees()
	if err != nil {
		t.Fatalf("DiscoverWorktrees: %v", err)
	}

	if len(worktrees) != 1 {
		t.Fatalf("expected 1 worktree, got %d", len(worktrees))
	}
	if worktrees[0].Branch != "unknown" {
		t.Errorf("expected branch 'unknown' for broken repo, got %q", worktrees[0].Branch)
	}
}

func TestDiscoverLegacy_WorktreesDirMissing(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(tmpDir)

	// Don't create the worktrees directory
	r := &Resolver{mode: ModeLegacy}
	_, err := r.DiscoverWorktrees()
	if err == nil {
		t.Fatal("expected error when worktrees directory missing")
	}
	if got := err.Error(); !strings.Contains(got, "worktrees directory not found") {
		t.Errorf("expected error containing 'worktrees directory not found', got %q", got)
	}
}

func TestDiscoverLegacy_ReadDirError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("skipping test when running as root")
	}

	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(tmpDir)

	wtDir := filepath.Join(tmpDir, "worktrees")
	os.MkdirAll(wtDir, 0755)

	// Make the directory unreadable
	os.Chmod(wtDir, 0000)
	t.Cleanup(func() { os.Chmod(wtDir, 0755) })

	r := &Resolver{mode: ModeLegacy}
	_, err := r.DiscoverWorktrees()
	if err == nil {
		t.Fatal("expected error when worktrees directory is unreadable")
	}
	if got := err.Error(); !strings.Contains(got, "failed to read worktrees directory") {
		t.Errorf("expected error containing 'failed to read worktrees directory', got %q", got)
	}
}

func TestDiscoverWorkspace_WorkspaceNotFound(t *testing.T) {
	cfg := &LoomConfig{
		DefaultWorkspace: "existing",
		Workspaces: map[string]WorkspaceConfig{
			"existing": {Path: "/tmp/ws"},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	// Manually create a resolver with a workspace name not in config
	r := &Resolver{
		mode:      ModeWorkspace,
		config:    cfg,
		workspace: "nonexistent",
	}

	_, err := r.discoverWorkspace()
	if err == nil {
		t.Fatal("expected error for nonexistent workspace")
	}
	if got := err.Error(); !strings.Contains(got, "not found in config") {
		t.Errorf("expected error containing 'not found in config', got %q", got)
	}
}

func TestDiscoverWorkspace_BranchErrorFallback(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	// Create directory with .git but not a valid git repo
	brokenRepo := filepath.Join(tmpDir, "broken")
	os.MkdirAll(filepath.Join(brokenRepo, ".git"), 0755)

	cfg := &LoomConfig{
		DefaultWorkspace: "ws",
		Workspaces: map[string]WorkspaceConfig{
			"ws": {
				Path: tmpDir,
				Repos: []RepoConfig{
					{Name: "broken", Path: brokenRepo},
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

	if len(worktrees) != 1 {
		t.Fatalf("expected 1 worktree, got %d", len(worktrees))
	}
	if worktrees[0].Branch != "unknown" {
		t.Errorf("expected branch 'unknown' for broken repo, got %q", worktrees[0].Branch)
	}
}

func TestResolveWorkspacePath_WorkspaceNotFound(t *testing.T) {
	cfg := &LoomConfig{
		DefaultWorkspace: "existing",
		Workspaces: map[string]WorkspaceConfig{
			"existing": {Path: "/tmp/ws"},
		},
	}

	r := &Resolver{
		mode:      ModeWorkspace,
		config:    cfg,
		workspace: "nonexistent",
	}

	_, err := r.resolveWorkspacePath("some-repo")
	if err == nil {
		t.Fatal("expected error for nonexistent workspace")
	}
	if got := err.Error(); !strings.Contains(got, "not found in config") {
		t.Errorf("expected error containing 'not found in config', got %q", got)
	}
}

func TestResolveWorkspacePath_RepoPathNotExist(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &LoomConfig{
		DefaultWorkspace: "ws",
		Workspaces: map[string]WorkspaceConfig{
			"ws": {
				Path: tmpDir,
				Repos: []RepoConfig{
					{Name: "missing-repo", Path: filepath.Join(tmpDir, "nonexistent")},
				},
			},
		},
	}

	r := &Resolver{
		mode:      ModeWorkspace,
		config:    cfg,
		workspace: "ws",
	}

	_, err := r.resolveWorkspacePath("missing-repo")
	if err == nil {
		t.Fatal("expected error for missing repo path")
	}
	if got := err.Error(); !strings.Contains(got, "path does not exist") {
		t.Errorf("expected error containing 'path does not exist', got %q", got)
	}
}

func TestResolveAgentTarget_WorkspaceMode_AbsolutePath(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	cfg := &LoomConfig{
		DefaultWorkspace: "ws",
		Workspaces: map[string]WorkspaceConfig{
			"ws": {
				Path: tmpDir,
				Repos: []RepoConfig{
					{Name: "repo1", Path: filepath.Join(tmpDir, "repo1")},
				},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	// Create a target directory for the absolute path
	absTarget := filepath.Join(tmpDir, "custom-target")
	os.MkdirAll(absTarget, 0755)

	target, err := ResolveAgentTarget(absTarget, "")
	if err != nil {
		t.Fatalf("ResolveAgentTarget: %v", err)
	}
	if target.WorkDir != absTarget {
		t.Errorf("expected WorkDir %q, got %q", absTarget, target.WorkDir)
	}
	if target.AgentName != "custom-target" {
		t.Errorf("expected AgentName 'custom-target', got %q", target.AgentName)
	}
}

func TestResolveAgentTarget_WorkspaceMode_AbsolutePathNotExist(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &LoomConfig{
		DefaultWorkspace: "ws",
		Workspaces: map[string]WorkspaceConfig{
			"ws": {
				Path: tmpDir,
				Repos: []RepoConfig{
					{Name: "repo1", Path: filepath.Join(tmpDir, "repo1")},
				},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	_, err := ResolveAgentTarget("/nonexistent/absolute/path", "")
	if err == nil {
		t.Fatal("expected error for nonexistent absolute path")
	}
	if got := err.Error(); !strings.Contains(got, "path does not exist") {
		t.Errorf("expected error containing 'path does not exist', got %q", got)
	}
}

func TestResolveAgentTarget_WorkspaceMode_NoWorkspacePath(t *testing.T) {
	cfg := &LoomConfig{
		DefaultWorkspace: "ws",
		Workspaces: map[string]WorkspaceConfig{
			"ws": {
				Path:  "", // empty path
				Repos: []RepoConfig{},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	_, err := ResolveAgentTarget("", "")
	if err == nil {
		t.Fatal("expected error when workspace has no path")
	}
	if got := err.Error(); !strings.Contains(got, "has no path configured") {
		t.Errorf("expected error containing 'has no path configured', got %q", got)
	}
}
