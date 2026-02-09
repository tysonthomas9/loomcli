package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunComplete_WithEnvVar(t *testing.T) {
	// Set LOOM_WORKTREE_PATH to a known directory so runComplete uses it
	tmpDir := t.TempDir()
	worktreePath := filepath.Join(tmpDir, "test-worktree")
	if err := os.MkdirAll(worktreePath, 0755); err != nil {
		t.Fatalf("failed to create worktree dir: %v", err)
	}

	// Resolve the path the same way runComplete does (Abs + EvalSymlinks)
	absPath, _ := filepath.Abs(worktreePath)
	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		resolvedPath = absPath
	}

	t.Setenv("LOOM_WORKTREE_PATH", worktreePath)

	// Run the command
	runComplete(nil, nil)

	// Verify signal file was created using the resolved path
	signalFile := GetSignalFilePath(resolvedPath)
	t.Cleanup(func() { os.Remove(signalFile) })

	if _, err := os.Stat(signalFile); err != nil {
		t.Fatalf("signal file should exist after runComplete: %v", err)
	}

	// Verify signal file content contains the worktree path
	content, err := os.ReadFile(signalFile)
	if err != nil {
		t.Fatalf("failed to read signal file: %v", err)
	}

	if string(content) != resolvedPath {
		t.Errorf("signal file content = %q, want %q", string(content), resolvedPath)
	}
}

func TestRunComplete_FallbackToCwd(t *testing.T) {
	// Unset LOOM_WORKTREE_PATH to trigger fallback
	t.Setenv("LOOM_WORKTREE_PATH", "")

	// Create a temp dir with .loom.lock to simulate finding the worktree root
	tmpDir := t.TempDir()
	lockFile := filepath.Join(tmpDir, ".loom.lock")
	if err := os.WriteFile(lockFile, []byte("{}"), 0644); err != nil {
		t.Fatalf("failed to create lock file: %v", err)
	}

	// Resolve the path the same way runComplete does
	absPath, _ := filepath.Abs(tmpDir)
	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		resolvedPath = absPath
	}

	// Change to the temp dir so findWorktreeRoot can find the lock file
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	t.Cleanup(func() {
		os.Chdir(origDir)
	})

	runComplete(nil, nil)

	// Verify signal file was created for the resolved worktree path
	signalFile := GetSignalFilePath(resolvedPath)
	t.Cleanup(func() { os.Remove(signalFile) })

	if _, err := os.Stat(signalFile); err != nil {
		t.Fatalf("signal file should exist after runComplete (fallback): %v", err)
	}
}

func TestRunComplete_SignalFileContent(t *testing.T) {
	tmpDir := t.TempDir()
	worktreePath := filepath.Join(tmpDir, "my-wt")
	if err := os.MkdirAll(worktreePath, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Resolve the path the same way runComplete does
	absPath, _ := filepath.Abs(worktreePath)
	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		resolvedPath = absPath
	}

	t.Setenv("LOOM_WORKTREE_PATH", worktreePath)

	runComplete(nil, nil)

	signalFile := GetSignalFilePath(resolvedPath)
	t.Cleanup(func() { os.Remove(signalFile) })

	content, err := os.ReadFile(signalFile)
	if err != nil {
		t.Fatalf("failed to read signal file: %v", err)
	}

	// Content should be the resolved path
	if string(content) != resolvedPath {
		t.Errorf("signal file content = %q, want %q", string(content), resolvedPath)
	}
}
