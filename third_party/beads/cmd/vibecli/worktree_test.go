package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetWorktreesDir(t *testing.T) {
	// Test default value
	os.Unsetenv("VIBECLI_WORKTREES_DIR")
	dir := GetWorktreesDir()
	if dir != "worktrees" {
		t.Errorf("GetWorktreesDir() = %q, want 'worktrees'", dir)
	}

	// Test environment variable override
	os.Setenv("VIBECLI_WORKTREES_DIR", "custom-dir")
	defer os.Unsetenv("VIBECLI_WORKTREES_DIR")

	dir = GetWorktreesDir()
	if dir != "custom-dir" {
		t.Errorf("GetWorktreesDir() = %q, want 'custom-dir'", dir)
	}
}

func TestGetDefaultBranch(t *testing.T) {
	// Test default value
	os.Unsetenv("VIBECLI_DEFAULT_BRANCH")
	branch := GetDefaultBranch()
	if branch != "feature/web-ui" {
		t.Errorf("GetDefaultBranch() = %q, want 'feature/web-ui'", branch)
	}

	// Test environment variable override
	os.Setenv("VIBECLI_DEFAULT_BRANCH", "main")
	defer os.Unsetenv("VIBECLI_DEFAULT_BRANCH")

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
