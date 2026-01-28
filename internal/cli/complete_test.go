package cli

import (
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

	// Test that the path includes loom-signals directory
	if !filepath.HasPrefix(path1, filepath.Join(tempDir, "loom-signals")) {
		t.Errorf("Signal file not in loom-signals subdir: %s", path1)
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
	if err := os.MkdirAll(signalDir, 0755); err != nil {
		t.Fatalf("Failed to create signal dir: %v", err)
	}
	if err := os.WriteFile(signalFile, []byte(worktreePath), 0644); err != nil {
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
	os.MkdirAll(signalDir, 0755)
	os.WriteFile(signal1, []byte(worktree1), 0644)

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
