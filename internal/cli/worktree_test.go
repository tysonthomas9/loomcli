package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetWorktreesDir(t *testing.T) {
	// Save original flag value and reset after test
	origFlag := worktreesFlag
	defer func() { worktreesFlag = origFlag }()
	worktreesFlag = ""

	// Test default value
	os.Unsetenv("LOOM_WORKTREES_DIR")
	dir := GetWorktreesDir()
	if dir != "worktrees" {
		t.Errorf("GetWorktreesDir() = %q, want 'worktrees'", dir)
	}

	// Test environment variable override
	os.Setenv("LOOM_WORKTREES_DIR", "custom-dir")
	defer os.Unsetenv("LOOM_WORKTREES_DIR")

	dir = GetWorktreesDir()
	if dir != "custom-dir" {
		t.Errorf("GetWorktreesDir() = %q, want 'custom-dir'", dir)
	}
}

func TestGetWorktreesDirFlagPrecedence(t *testing.T) {
	// Save original flag value
	origFlag := worktreesFlag
	defer func() { worktreesFlag = origFlag }()

	// Test 1: Flag takes precedence over env var
	os.Setenv("LOOM_WORKTREES_DIR", "env-dir")
	defer os.Unsetenv("LOOM_WORKTREES_DIR")
	worktreesFlag = "flag-dir"

	dir := GetWorktreesDir()
	if dir != "flag-dir" {
		t.Errorf("GetWorktreesDir() = %q, want 'flag-dir' (flag should override env)", dir)
	}

	// Test 2: Env var used when flag is empty
	worktreesFlag = ""
	dir = GetWorktreesDir()
	if dir != "env-dir" {
		t.Errorf("GetWorktreesDir() = %q, want 'env-dir'", dir)
	}

	// Test 3: Default used when both are empty
	os.Unsetenv("LOOM_WORKTREES_DIR")
	dir = GetWorktreesDir()
	if dir != "worktrees" {
		t.Errorf("GetWorktreesDir() = %q, want 'worktrees'", dir)
	}

	// Test 4: Paths are cleaned (trailing slashes removed)
	worktreesFlag = "my-agents/"
	dir = GetWorktreesDir()
	if dir != "my-agents" {
		t.Errorf("GetWorktreesDir() = %q, want 'my-agents' (trailing slash should be removed)", dir)
	}

	// Test 5: Env var paths are also cleaned
	worktreesFlag = ""
	os.Setenv("LOOM_WORKTREES_DIR", "env-agents/")
	dir = GetWorktreesDir()
	if dir != "env-agents" {
		t.Errorf("GetWorktreesDir() = %q, want 'env-agents' (trailing slash should be removed)", dir)
	}
}

func TestGetDefaultBranch(t *testing.T) {
	// Test default value
	os.Unsetenv("LOOM_DEFAULT_BRANCH")
	branch := GetDefaultBranch()
	if branch != "main" {
		t.Errorf("GetDefaultBranch() = %q, want 'main'", branch)
	}

	// Test environment variable override
	os.Setenv("LOOM_DEFAULT_BRANCH", "main")
	defer os.Unsetenv("LOOM_DEFAULT_BRANCH")

	branch = GetDefaultBranch()
	if branch != "main" {
		t.Errorf("GetDefaultBranch() = %q, want 'main'", branch)
	}
}

func TestGetWorktreeName(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/path/to/worktrees/falcon", "falcon"},
		{"worktrees/nova", "nova"},
		{"single", "single"},
		{"/absolute/path", "path"},
	}

	for _, tc := range tests {
		got := GetWorktreeName(tc.path)
		if got != tc.expected {
			t.Errorf("GetWorktreeName(%q) = %q, want %q", tc.path, got, tc.expected)
		}
	}
}

func TestResolveWorktreePathAbsolute(t *testing.T) {
	tmpDir := t.TempDir()

	// Absolute path that exists
	path, err := ResolveWorktreePath(tmpDir)
	if err != nil {
		t.Fatalf("ResolveWorktreePath failed: %v", err)
	}
	if path != tmpDir {
		t.Errorf("Expected %s, got %s", tmpDir, path)
	}

	// Absolute path that doesn't exist
	_, err = ResolveWorktreePath("/nonexistent/path/12345")
	if err == nil {
		t.Error("Expected error for non-existent absolute path")
	}
}

func TestResolveWorktreePathEmpty(t *testing.T) {
	// Empty string should return current directory
	cwd, _ := os.Getwd()
	path, err := ResolveWorktreePath("")
	if err != nil {
		t.Fatalf("ResolveWorktreePath failed: %v", err)
	}
	if path != cwd {
		t.Errorf("Expected %s, got %s", cwd, path)
	}
}

func TestResolveWorktreePathRelative(t *testing.T) {
	// Save and restore working directory
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)

	tmpDir := t.TempDir()
	// Resolve symlinks (macOS /var -> /private/var)
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	os.Chdir(tmpDir)

	// Create worktrees directory structure
	wtDir := filepath.Join(tmpDir, "worktrees", "falcon")
	if err := os.MkdirAll(wtDir, 0755); err != nil {
		t.Fatalf("Failed to create test dir: %v", err)
	}

	// Test resolution by name
	path, err := ResolveWorktreePath("falcon")
	if err != nil {
		t.Fatalf("ResolveWorktreePath failed: %v", err)
	}
	// Resolve symlinks in returned path for comparison
	path, _ = filepath.EvalSymlinks(path)
	if path != wtDir {
		t.Errorf("Expected %s, got %s", wtDir, path)
	}

	// Non-existent worktree
	_, err = ResolveWorktreePath("nonexistent")
	if err == nil {
		t.Error("Expected error for non-existent worktree")
	}
}

// setupTestRepo creates a bare origin repo and a clone with the given branch topology.
// It returns the clone path. The caller can use this path for WorktreeInfo entries.
// branchTopology maps branch names to their parent branch. All branches are pushed to origin.
// Commits are created in order so that the topology is correct.
func setupTestRepo(t *testing.T, branches []struct{ name, parent string }) string {
	t.Helper()

	clearGitEnvVars(t)

	tmpDir := t.TempDir()
	originPath := filepath.Join(tmpDir, "origin.git")
	clonePath := filepath.Join(tmpDir, "clone")

	// Create bare origin repo with main as default branch
	_, err := RunGitCommand(tmpDir, "init", "--bare", "--initial-branch=main", originPath)
	if err != nil {
		t.Fatalf("failed to init bare repo: %v", err)
	}

	// Clone it
	_, err = RunGitCommand(tmpDir, "clone", originPath, clonePath)
	if err != nil {
		t.Fatalf("failed to clone: %v", err)
	}

	// Configure git user for commits
	RunGitCommand(clonePath, "config", "user.email", "test@test.com")
	RunGitCommand(clonePath, "config", "user.name", "Test")

	// Ensure we are on main branch
	RunGitCommand(clonePath, "checkout", "-b", "main")

	// Create initial commit on main
	initialFile := filepath.Join(clonePath, "init.txt")
	os.WriteFile(initialFile, []byte("init"), 0644)
	RunGitCommand(clonePath, "add", "init.txt")
	RunGitCommand(clonePath, "commit", "-m", "initial commit")
	RunGitCommand(clonePath, "push", "-u", "origin", "main")

	// Create each branch off its parent with a unique commit
	for i, b := range branches {
		// Checkout parent
		_, err := RunGitCommand(clonePath, "checkout", b.parent)
		if err != nil {
			t.Fatalf("failed to checkout parent %s: %v", b.parent, err)
		}

		// Create new branch
		_, err = RunGitCommand(clonePath, "checkout", "-b", b.name)
		if err != nil {
			t.Fatalf("failed to create branch %s: %v", b.name, err)
		}

		// Create a unique commit
		fname := filepath.Join(clonePath, fmt.Sprintf("file-%d.txt", i))
		os.WriteFile(fname, []byte(b.name), 0644)
		RunGitCommand(clonePath, "add", ".")
		RunGitCommand(clonePath, "commit", "-m", fmt.Sprintf("commit on %s", b.name))

		// Push to origin
		RunGitCommand(clonePath, "push", "origin", b.name)
	}

	return clonePath
}

func TestDetectIntegrationBranch_TooFewWorktrees(t *testing.T) {
	// Zero worktrees
	result := DetectIntegrationBranch(nil)
	if result != "" {
		t.Errorf("expected empty string for nil worktrees, got %q", result)
	}

	result = DetectIntegrationBranch([]WorktreeInfo{})
	if result != "" {
		t.Errorf("expected empty string for empty worktrees, got %q", result)
	}

	// One worktree
	result = DetectIntegrationBranch([]WorktreeInfo{
		{Name: "falcon", Path: "/tmp/fake", Branch: "falcon"},
	})
	if result != "" {
		t.Errorf("expected empty string for single worktree, got %q", result)
	}
}

func TestDetectIntegrationBranch_CommonParent(t *testing.T) {
	// Topology:
	//   main -> feature/web-ui -> falcon
	//                          -> nova
	// Expected: DetectIntegrationBranch should return "feature/web-ui"
	clonePath := setupTestRepo(t, []struct{ name, parent string }{
		{"feature/web-ui", "main"},
		{"falcon", "feature/web-ui"},
		{"nova", "feature/web-ui"},
	})

	worktrees := []WorktreeInfo{
		{Name: "falcon", Path: clonePath, Branch: "falcon"},
		{Name: "nova", Path: clonePath, Branch: "nova"},
	}

	result := DetectIntegrationBranch(worktrees)
	if result != "feature/web-ui" {
		t.Errorf("expected 'feature/web-ui', got %q", result)
	}
}

func TestDetectIntegrationBranch_OnlyMain(t *testing.T) {
	// Topology:
	//   main -> falcon
	//        -> nova
	// No intermediate branch exists, so result should be ""
	clonePath := setupTestRepo(t, []struct{ name, parent string }{
		{"falcon", "main"},
		{"nova", "main"},
	})

	worktrees := []WorktreeInfo{
		{Name: "falcon", Path: clonePath, Branch: "falcon"},
		{Name: "nova", Path: clonePath, Branch: "nova"},
	}

	result := DetectIntegrationBranch(worktrees)
	if result != "" {
		t.Errorf("expected empty string when branches are directly off main, got %q", result)
	}
}

func TestDetectIntegrationBranch_DetectedFartherThanMain(t *testing.T) {
	clearGitEnvVars(t)

	// Topology:
	//   main -> feature/old (with extra commits after branching falcon/nova)
	//        -> falcon (directly off main)
	//        -> nova (directly off main)
	// feature/old is an ancestor of falcon and nova only if we set it up that way.
	// Actually, we need feature/old to be an ancestor of both falcon and nova,
	// but farther away than main. The trick: branch feature/old from a point
	// BEFORE main's tip, then branch falcon and nova from main (after main has
	// additional commits). That way feature/old is NOT an ancestor of falcon/nova.
	//
	// Simpler approach: create a topology where an intermediate branch exists
	// but is at the same distance as main (not closer).
	//
	// Topology:
	//   initial commit (A)
	//     -> feature/base (B) [branch from A, with 1 commit]
	//       -> main gets commit (C) [merge or advance main past A]
	//
	// Actually the simplest: create feature/base off root, then main gets more
	// commits, then falcon and nova branch off feature/base. But feature/base
	// must be ancestor of falcon/nova AND main must also be ancestor.
	// For main to be ancestor too, we need main to be at or before feature/base.
	// But then main would be farther, not closer.
	//
	// Let's think differently: we want bestMaxDist >= mainMaxDist.
	// That means feature/base is at least as far from the worktrees as main is.
	// Topology:
	//   A (main, feature/base) -> B (falcon, nova each have 1 commit)
	// Both main and feature/base point to the same commit A, so distances are equal.
	// bestMaxDist == mainMaxDist, so the condition bestMaxDist >= mainMaxDist is true -> return "".

	tmpDir := t.TempDir()
	originPath := filepath.Join(tmpDir, "origin.git")
	clonePath := filepath.Join(tmpDir, "clone")

	RunGitCommand(tmpDir, "init", "--bare", "--initial-branch=main", originPath)
	RunGitCommand(tmpDir, "clone", originPath, clonePath)
	RunGitCommand(clonePath, "config", "user.email", "test@test.com")
	RunGitCommand(clonePath, "config", "user.name", "Test")

	// Initial commit on main
	RunGitCommand(clonePath, "checkout", "-b", "main")
	os.WriteFile(filepath.Join(clonePath, "init.txt"), []byte("init"), 0644)
	RunGitCommand(clonePath, "add", "init.txt")
	RunGitCommand(clonePath, "commit", "-m", "initial")
	RunGitCommand(clonePath, "push", "-u", "origin", "main")

	// Create feature/base at same point as main (same commit)
	RunGitCommand(clonePath, "checkout", "-b", "feature/base")
	RunGitCommand(clonePath, "push", "origin", "feature/base")

	// Create falcon off main with 1 commit
	RunGitCommand(clonePath, "checkout", "main")
	RunGitCommand(clonePath, "checkout", "-b", "falcon")
	os.WriteFile(filepath.Join(clonePath, "falcon.txt"), []byte("falcon"), 0644)
	RunGitCommand(clonePath, "add", ".")
	RunGitCommand(clonePath, "commit", "-m", "falcon commit")
	RunGitCommand(clonePath, "push", "origin", "falcon")

	// Create nova off main with 1 commit
	RunGitCommand(clonePath, "checkout", "main")
	RunGitCommand(clonePath, "checkout", "-b", "nova")
	os.WriteFile(filepath.Join(clonePath, "nova.txt"), []byte("nova"), 0644)
	RunGitCommand(clonePath, "add", ".")
	RunGitCommand(clonePath, "commit", "-m", "nova commit")
	RunGitCommand(clonePath, "push", "origin", "nova")

	worktrees := []WorktreeInfo{
		{Name: "falcon", Path: clonePath, Branch: "falcon"},
		{Name: "nova", Path: clonePath, Branch: "nova"},
	}

	result := DetectIntegrationBranch(worktrees)
	if result != "" {
		t.Errorf("expected empty string when candidate is not closer than main, got %q", result)
	}
}

func TestValidateWorktreeName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		// Valid names
		{"falcon", false},
		{"nova", false},
		{"my-agent", false},
		{"agent_1", false},
		{"sub/dir", false},

		// Invalid names (path traversal)
		{".", true},
		{"..", true},
		{"../secret", true},
		{"../../etc", true},
		{"foo/../../bar", true},
		{"foo/../..", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateWorktreeName(tc.name)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateWorktreeName(%q) error = %v, wantErr %v", tc.name, err, tc.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "path traversal") {
				t.Errorf("validateWorktreeName(%q) error = %v, want error containing 'path traversal'", tc.name, err)
			}
		})
	}
}

func TestResolveLegacyPathTraversal(t *testing.T) {
	// Save and restore working directory
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	os.Chdir(tmpDir)

	// Create worktrees directory and a target outside it
	os.MkdirAll(filepath.Join(tmpDir, "worktrees", "falcon"), 0755)
	os.MkdirAll(filepath.Join(tmpDir, "secret"), 0755)

	// Valid name should work
	path, err := resolveLegacyPath("falcon")
	if err != nil {
		t.Fatalf("resolveLegacyPath(\"falcon\") failed: %v", err)
	}
	path, _ = filepath.EvalSymlinks(path)
	expected := filepath.Join(tmpDir, "worktrees", "falcon")
	if path != expected {
		t.Errorf("resolveLegacyPath(\"falcon\") = %q, want %q", path, expected)
	}

	// Path traversal should be blocked
	traversalNames := []string{"..", "../secret", "../../etc"}
	for _, name := range traversalNames {
		_, err := resolveLegacyPath(name)
		if err == nil {
			t.Errorf("resolveLegacyPath(%q) should have returned an error", name)
		} else if !strings.Contains(err.Error(), "path traversal") {
			t.Errorf("resolveLegacyPath(%q) error = %v, want error containing 'path traversal'", name, err)
		}
	}
}

func TestGetScriptDir(t *testing.T) {
	dir, err := GetScriptDir()
	if err != nil {
		t.Fatalf("GetScriptDir failed: %v", err)
	}

	cwd, _ := os.Getwd()
	if dir != cwd {
		t.Errorf("GetScriptDir() = %s, want %s", dir, cwd)
	}
}
