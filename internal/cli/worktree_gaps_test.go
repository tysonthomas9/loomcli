package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// --- Group 1: GetCurrentBranch() unit tests ---

func TestGetCurrentBranch_NormalBranch(t *testing.T) {
	t.Parallel()

	deps, _, _, _, _ := NewTestDeps(t)
	mock := NewCommandMock(t, []CommandStub{
		{
			Name:   "git",
			Args:   []string{"branch", "--show-current"},
			Stdout: "feature-x\n",
		},
	})
	mock.InstallOn(deps)

	branch, err := getCurrentBranchDeps(deps, "/some/repo")
	if err != nil {
		t.Fatalf("getCurrentBranchDeps: unexpected error: %v", err)
	}
	if branch != "feature-x" {
		t.Errorf("expected 'feature-x', got %q", branch)
	}
}

func TestGetCurrentBranch_DetachedHEAD(t *testing.T) {
	t.Parallel()

	deps, _, _, _, _ := NewTestDeps(t)
	// git branch --show-current returns empty string on detached HEAD
	mock := NewCommandMock(t, []CommandStub{
		{
			Name:   "git",
			Args:   []string{"branch", "--show-current"},
			Stdout: "",
		},
	})
	mock.InstallOn(deps)

	branch, err := getCurrentBranchDeps(deps, "/some/repo")
	if err != nil {
		t.Fatalf("getCurrentBranchDeps: unexpected error: %v", err)
	}
	if branch != "" {
		t.Errorf("expected empty string for detached HEAD, got %q", branch)
	}
}

func TestGetCurrentBranch_GitError(t *testing.T) {
	t.Parallel()

	deps, _, _, _, _ := NewTestDeps(t)
	mock := NewCommandMock(t, []CommandStub{
		{
			Name:   "git",
			Args:   []string{"branch", "--show-current"},
			Stderr: "fatal: not a git repository",
			Err:    errors.New("exit status 128"),
		},
	})
	mock.InstallOn(deps)

	_, err := getCurrentBranchDeps(deps, "/not/a/repo")
	if err == nil {
		t.Fatal("expected error when git command fails")
	}
}

// --- Group 2: discoverWorkspace() edge cases ---

func TestDiscoverWorkspace_EmptyReposList(t *testing.T) {
	cfg := &LoomConfig{
		DefaultWorkspace: "ws",
		Workspaces: map[string]WorkspaceConfig{
			"ws": {
				Path:  "/tmp/ws",
				Repos: []RepoConfig{}, // empty repos list
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := TestingResetDefaultResolver()
	defer func() { TestingSetDefaultResolver(old) }()

	r, err := NewResolver()
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	worktrees, err := r.DiscoverWorktrees()
	if err != nil {
		t.Fatalf("DiscoverWorktrees: unexpected error: %v", err)
	}
	if len(worktrees) != 0 {
		t.Errorf("expected 0 worktrees for empty repos list, got %d", len(worktrees))
	}
}

func TestDiscoverWorkspace_RelativePathNoWorkspacePath(t *testing.T) {
	// When workspace Path is "" and repo has relative path,
	// filepath.Join("", "relative") = "relative", and .git won't exist there
	cfg := &LoomConfig{
		DefaultWorkspace: "ws",
		Workspaces: map[string]WorkspaceConfig{
			"ws": {
				Path: "", // empty workspace path
				Repos: []RepoConfig{
					{Name: "myrepo", Path: "relative/path"},
				},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := TestingResetDefaultResolver()
	defer func() { TestingSetDefaultResolver(old) }()

	r, err := NewResolver()
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	worktrees, err := r.DiscoverWorktrees()
	if err != nil {
		t.Fatalf("DiscoverWorktrees: unexpected error: %v", err)
	}
	// The repo's .git won't exist at "relative/path/.git", so it gets skipped
	if len(worktrees) != 0 {
		t.Errorf("expected 0 worktrees (repo skipped due to missing .git), got %d", len(worktrees))
	}
}

func TestDiscoverWorkspace_MultipleWorkspaces_SwitchAndDiscover(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	// Create repos for two workspaces
	repo1Path := filepath.Join(tmpDir, "ws1", "repo1")
	repo2Path := filepath.Join(tmpDir, "ws2", "repo2")
	createGitRepo(t, repo1Path)
	createGitRepo(t, repo2Path)

	cfg := &LoomConfig{
		DefaultWorkspace: "workspace1",
		Workspaces: map[string]WorkspaceConfig{
			"workspace1": {
				Path: filepath.Join(tmpDir, "ws1"),
				Repos: []RepoConfig{
					{Name: "repo1", Path: repo1Path},
				},
			},
			"workspace2": {
				Path: filepath.Join(tmpDir, "ws2"),
				Repos: []RepoConfig{
					{Name: "repo2", Path: repo2Path},
				},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := TestingResetDefaultResolver()
	defer func() { TestingSetDefaultResolver(old) }()

	r, err := NewResolver()
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	// Discover from workspace1
	wt1, err := r.DiscoverWorktrees()
	if err != nil {
		t.Fatalf("DiscoverWorktrees (ws1): %v", err)
	}
	if len(wt1) != 1 {
		t.Fatalf("expected 1 worktree from workspace1, got %d", len(wt1))
	}
	if wt1[0].Name != "repo1" {
		t.Errorf("expected repo1, got %q", wt1[0].Name)
	}
	if wt1[0].Workspace != "workspace1" {
		t.Errorf("expected workspace 'workspace1', got %q", wt1[0].Workspace)
	}

	// Switch to workspace2 and discover
	if err := r.SetWorkspace("workspace2"); err != nil {
		t.Fatalf("SetWorkspace: %v", err)
	}

	wt2, err := r.DiscoverWorktrees()
	if err != nil {
		t.Fatalf("DiscoverWorktrees (ws2): %v", err)
	}
	if len(wt2) != 1 {
		t.Fatalf("expected 1 worktree from workspace2, got %d", len(wt2))
	}
	if wt2[0].Name != "repo2" {
		t.Errorf("expected repo2, got %q", wt2[0].Name)
	}
	if wt2[0].Workspace != "workspace2" {
		t.Errorf("expected workspace 'workspace2', got %q", wt2[0].Workspace)
	}
}

// ResolveAgentTarget tests have been moved to internal/cli/workspace/workspace_resolve_test.go
// where ResolveAgentTarget lives.

// --- Group 4: DiscoverWorktrees() public API delegation ---

func TestDiscoverWorktrees_PublicAPI_DelegatesLegacy(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	old := TestingResetDefaultResolver()
	defer func() { TestingSetDefaultResolver(old) }()

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(tmpDir)

	// Create worktrees directory with a git repo
	createGitRepo(t, filepath.Join(tmpDir, "worktrees", "falcon"))

	// Call the package-level function
	worktrees, err := DiscoverWorktrees()
	if err != nil {
		t.Fatalf("DiscoverWorktrees: %v", err)
	}

	if len(worktrees) != 1 {
		t.Fatalf("expected 1 worktree, got %d", len(worktrees))
	}
	if worktrees[0].Name != "falcon" {
		t.Errorf("expected name 'falcon', got %q", worktrees[0].Name)
	}
	// Legacy mode: Workspace and Repo should be empty/nil
	if worktrees[0].Workspace != "" {
		t.Errorf("expected empty Workspace in legacy mode, got %q", worktrees[0].Workspace)
	}
	if worktrees[0].Repo != nil {
		t.Errorf("expected nil Repo in legacy mode, got %+v", worktrees[0].Repo)
	}
}

func TestDiscoverWorktrees_PublicAPI_DelegatesWorkspace(t *testing.T) {
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

	old := TestingResetDefaultResolver()
	defer func() { TestingSetDefaultResolver(old) }()

	// Call the package-level function
	worktrees, err := DiscoverWorktrees()
	if err != nil {
		t.Fatalf("DiscoverWorktrees: %v", err)
	}

	if len(worktrees) != 1 {
		t.Fatalf("expected 1 worktree, got %d", len(worktrees))
	}
	if worktrees[0].Name != "myrepo" {
		t.Errorf("expected name 'myrepo', got %q", worktrees[0].Name)
	}
	// Workspace mode: Workspace and Repo should be populated
	if worktrees[0].Workspace != "ws" {
		t.Errorf("expected Workspace 'ws', got %q", worktrees[0].Workspace)
	}
	if worktrees[0].Repo == nil {
		t.Error("expected non-nil Repo in workspace mode")
	}
}
