package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestGetSignalFilePath(t *testing.T) {
	// Test that the same path always returns the same signal file
	path1 := GetSignalFilePath("/some/worktree/path")
	path2 := GetSignalFilePath("/some/worktree/path")

	if path1 != path2 {
		t.Errorf("GetSignalFilePath not deterministic: got %s and %s", path1, path2)
	}

	// Test that different paths return different signal files
	path3 := GetSignalFilePath("/different/path")
	if path1 == path3 {
		t.Errorf("GetSignalFilePath returned same path for different inputs: %s", path1)
	}

	// Test that the signal file is in the temp directory
	tempDir := os.TempDir()
	if !filepath.HasPrefix(path1, tempDir) {
		t.Errorf("Signal file not in temp dir: got %s, expected prefix %s", path1, tempDir)
	}

	// Test that the path includes loom-signals-{uid} directory
	expectedDir := filepath.Join(tempDir, fmt.Sprintf("loom-signals-%d", os.Getuid()))
	if !filepath.HasPrefix(path1, expectedDir) {
		t.Errorf("Signal file not in loom-signals-{uid} subdir: got %s, expected prefix %s", path1, expectedDir)
	}
}

func TestGetSignalFilePath_Consistency(t *testing.T) {
	// Verify hash is based on path content
	tests := []struct {
		path1 string
		path2 string
		same  bool
	}{
		{"/path/to/worktree", "/path/to/worktree", true},
		{"/path/to/worktree", "/path/to/other", false},
		{"/a", "/b", false},
		{"", "", true},
	}

	for _, tt := range tests {
		sig1 := GetSignalFilePath(tt.path1)
		sig2 := GetSignalFilePath(tt.path2)
		if (sig1 == sig2) != tt.same {
			t.Errorf("GetSignalFilePath(%q) vs GetSignalFilePath(%q): expected same=%v, got %s vs %s",
				tt.path1, tt.path2, tt.same, sig1, sig2)
		}
	}
}

func TestSignalFileCreationAndDetection(t *testing.T) {
	// Create a temporary worktree path
	tmpDir := t.TempDir()
	worktreePath := filepath.Join(tmpDir, "test-worktree")
	os.MkdirAll(worktreePath, 0755)

	// Get the signal file path
	signalFile := GetSignalFilePath(worktreePath)

	// Verify signal file doesn't exist yet
	if _, err := os.Stat(signalFile); err == nil {
		t.Fatalf("Signal file should not exist yet: %s", signalFile)
	}

	// Create the signal directory and file (simulating loom complete)
	signalDir := filepath.Dir(signalFile)
	if err := os.MkdirAll(signalDir, 0700); err != nil {
		t.Fatalf("Failed to create signal dir: %v", err)
	}
	if err := os.WriteFile(signalFile, []byte(worktreePath), 0600); err != nil {
		t.Fatalf("Failed to write signal file: %v", err)
	}

	// Verify signal file exists and can be detected
	if _, err := os.Stat(signalFile); err != nil {
		t.Errorf("Signal file should exist: %v", err)
	}

	// Verify contents
	content, err := os.ReadFile(signalFile)
	if err != nil {
		t.Fatalf("Failed to read signal file: %v", err)
	}
	if string(content) != worktreePath {
		t.Errorf("Signal file content mismatch: got %q, want %q", string(content), worktreePath)
	}

	// Clean up
	os.Remove(signalFile)
}

func TestSignalFileIsolation(t *testing.T) {
	// Test that signal files for different worktrees don't interfere
	tmpDir := t.TempDir()
	worktree1 := filepath.Join(tmpDir, "worktree1")
	worktree2 := filepath.Join(tmpDir, "worktree2")

	signal1 := GetSignalFilePath(worktree1)
	signal2 := GetSignalFilePath(worktree2)

	if signal1 == signal2 {
		t.Fatalf("Different worktrees should have different signal files")
	}

	// Create signal for worktree1
	signalDir := filepath.Dir(signal1)
	os.MkdirAll(signalDir, 0700)
	os.WriteFile(signal1, []byte(worktree1), 0600)

	// Verify worktree2's signal doesn't exist
	if _, err := os.Stat(signal2); err == nil {
		t.Errorf("Signal for worktree2 should not exist")
	}

	// Verify worktree1's signal exists
	if _, err := os.Stat(signal1); err != nil {
		t.Errorf("Signal for worktree1 should exist: %v", err)
	}

	// Clean up
	os.Remove(signal1)
}

func TestSignalFileSurvivesGitClean(t *testing.T) {
	// This test verifies the signal file is outside the worktree
	// so git clean can't delete it
	tmpDir := t.TempDir()
	worktreePath := filepath.Join(tmpDir, "my-worktree")
	os.MkdirAll(worktreePath, 0755)

	signalFile := GetSignalFilePath(worktreePath)

	// Verify signal file path is NOT inside the worktree
	if filepath.HasPrefix(signalFile, worktreePath) {
		t.Errorf("Signal file should be outside worktree: signal=%s, worktree=%s",
			signalFile, worktreePath)
	}

	// Verify signal file is in temp directory
	if !filepath.HasPrefix(signalFile, os.TempDir()) {
		t.Errorf("Signal file should be in temp dir: %s", signalFile)
	}
}

func TestGetSignalFilePath_UsesWorkspaceHash(t *testing.T) {
	// Verify that GetSignalFilePath produces a path whose basename matches
	// the workspaceHash of the input path. This confirms the refactoring
	// from inline sha256 to the shared workspaceHash helper is correct.
	worktreePath := "/home/user/project"
	signalPath := GetSignalFilePath(worktreePath)
	expectedHash := workspaceHash(worktreePath)
	actualBasename := filepath.Base(signalPath)

	if actualBasename != expectedHash {
		t.Errorf("GetSignalFilePath basename = %q, want workspaceHash = %q", actualBasename, expectedHash)
	}
}

func TestGetSignalFilePath_MatchesExpectedFormat(t *testing.T) {
	// Verify the full path structure: <tmpdir>/loom-signals-{uid}/<hash>
	worktreePath := "/some/worktree"
	signalPath := GetSignalFilePath(worktreePath)
	expectedDir := filepath.Join(os.TempDir(), fmt.Sprintf("loom-signals-%d", os.Getuid()))
	expectedPath := filepath.Join(expectedDir, workspaceHash(worktreePath))

	if signalPath != expectedPath {
		t.Errorf("GetSignalFilePath(%q) = %q, want %q", worktreePath, signalPath, expectedPath)
	}
}

func TestFindWorktreeRoot_WithLockFile(t *testing.T) {
	// Create a directory structure: root/subdir1/subdir2
	tmpDir := t.TempDir()
	root := filepath.Join(tmpDir, "worktree-root")
	subdir1 := filepath.Join(root, "subdir1")
	subdir2 := filepath.Join(subdir1, "subdir2")
	os.MkdirAll(subdir2, 0755)

	// Create .loom.lock in the root
	lockFile := filepath.Join(root, ".loom.lock")
	if err := os.WriteFile(lockFile, []byte("{}"), 0644); err != nil {
		t.Fatalf("Failed to create lock file: %v", err)
	}

	// findWorktreeRoot from subdir2 should find root
	found, err := findWorktreeRoot(subdir2)
	if err != nil {
		t.Errorf("Expected to find worktree root, got error: %v", err)
	}
	if found != root {
		t.Errorf("Expected %s, got %s", root, found)
	}

	// findWorktreeRoot from subdir1 should also find root
	found, err = findWorktreeRoot(subdir1)
	if err != nil {
		t.Errorf("Expected to find worktree root, got error: %v", err)
	}
	if found != root {
		t.Errorf("Expected %s, got %s", root, found)
	}

	// findWorktreeRoot from root should find root
	found, err = findWorktreeRoot(root)
	if err != nil {
		t.Errorf("Expected to find worktree root, got error: %v", err)
	}
	if found != root {
		t.Errorf("Expected %s, got %s", root, found)
	}
}

func TestFindWorktreeRoot_NoLockFile(t *testing.T) {
	// Create a directory structure without .loom.lock
	tmpDir := t.TempDir()
	subdir := filepath.Join(tmpDir, "no-lock", "subdir")
	os.MkdirAll(subdir, 0755)

	// findWorktreeRoot should return error
	_, err := findWorktreeRoot(subdir)
	if err == nil {
		t.Error("Expected error when no .loom.lock found")
	}
}

func TestFindWorktreeRoot_MultipleRoots(t *testing.T) {
	// Create nested worktrees (inner .loom.lock should win)
	tmpDir := t.TempDir()
	outerRoot := filepath.Join(tmpDir, "outer")
	innerRoot := filepath.Join(outerRoot, "nested", "inner")
	subdir := filepath.Join(innerRoot, "src", "pkg")
	os.MkdirAll(subdir, 0755)

	// Create .loom.lock in both outer and inner
	os.WriteFile(filepath.Join(outerRoot, ".loom.lock"), []byte("{}"), 0644)
	os.WriteFile(filepath.Join(innerRoot, ".loom.lock"), []byte("{}"), 0644)

	// findWorktreeRoot from subdir should find inner (closest)
	found, err := findWorktreeRoot(subdir)
	if err != nil {
		t.Errorf("Expected to find worktree root, got error: %v", err)
	}
	if found != innerRoot {
		t.Errorf("Expected innerRoot %s, got %s", innerRoot, found)
	}
}
