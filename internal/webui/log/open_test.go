// This file tests functions (getWorkspaceLogDir, listTaskPhases, fileExists)
// that have been moved to the handlers/misc package. These tests should be
// migrated to handlers/misc or adapted to use the misc package's exports.

package log

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOpenLogFileSecure_RegularFile(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.log")
	if err := os.WriteFile(testFile, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	f, err := openLogFileSecure(testFile, tmpDir)
	if err != nil {
		t.Fatalf("openLogFileSecure() error = %v", err)
	}
	defer f.Close()

	buf := make([]byte, 64)
	n, err := f.Read(buf)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if string(buf[:n]) != "hello\n" {
		t.Errorf("Read() = %q, want %q", string(buf[:n]), "hello\n")
	}
}

func TestOpenLogFileSecure_RejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink tests not supported on Windows")
	}

	tmpDir := t.TempDir()
	realFile := filepath.Join(tmpDir, "real.log")
	if err := os.WriteFile(realFile, []byte("secret\n"), 0o644); err != nil {
		t.Fatalf("failed to create real file: %v", err)
	}

	symlinkFile := filepath.Join(tmpDir, "link.log")
	if err := os.Symlink(realFile, symlinkFile); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	_, err := openLogFileSecure(symlinkFile, tmpDir)
	if err == nil {
		t.Fatal("openLogFileSecure() should reject symlink, got nil error")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error = %q, want error containing 'symlink'", err.Error())
	}
}

func TestOpenLogFileSecure_RejectsSymlinkOutsideDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink tests not supported on Windows")
	}

	tmpDir := t.TempDir()
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("secret\n"), 0o644); err != nil {
		t.Fatalf("failed to create outside file: %v", err)
	}

	symlinkFile := filepath.Join(tmpDir, "evil.log")
	if err := os.Symlink(outsideFile, symlinkFile); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	_, err := openLogFileSecure(symlinkFile, tmpDir)
	if err == nil {
		t.Fatal("openLogFileSecure() should reject symlink pointing outside dir, got nil error")
	}
}

func TestOpenLogFileSecure_NonexistentFile(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := openLogFileSecure(filepath.Join(tmpDir, "nonexistent.log"), tmpDir)
	if err == nil {
		t.Fatal("openLogFileSecure() should return error for non-existent file")
	}
}

func TestOpenLogFileSecure_AllowedDirCheck(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("post-open /proc/self/fd verification only available on Linux")
	}

	tmpDir := t.TempDir()
	wrongDir := t.TempDir()

	testFile := filepath.Join(tmpDir, "test.log")
	if err := os.WriteFile(testFile, []byte("data\n"), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Open with wrong allowed dir — post-open verification should catch it
	_, err := openLogFileSecure(testFile, wrongDir)
	if err == nil {
		t.Fatal("openLogFileSecure() should reject file outside allowed dir")
	}
	if !strings.Contains(err.Error(), "escapes allowed directory") {
		t.Errorf("error = %q, want error containing 'escapes allowed directory'", err.Error())
	}
}

func TestFileExists_Lstat(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink tests not supported on Windows")
	}

	tmpDir := t.TempDir()

	// Regular file
	regularFile := filepath.Join(tmpDir, "regular.txt")
	if err := os.WriteFile(regularFile, []byte("test"), 0o644); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}
	if !fileExists(regularFile) {
		t.Error("fileExists() returned false for regular file")
	}

	// Dangling symlink — Lstat reports it exists (the link itself exists)
	danglingLink := filepath.Join(tmpDir, "dangling")
	if err := os.Symlink("/nonexistent/target", danglingLink); err != nil {
		t.Fatalf("failed to create dangling symlink: %v", err)
	}
	if !fileExists(danglingLink) {
		t.Error("fileExists() with Lstat should return true for dangling symlink")
	}

	// Non-existent path
	if fileExists(filepath.Join(tmpDir, "nope")) {
		t.Error("fileExists() returned true for non-existent path")
	}
}

func TestListTaskPhases_RejectsSymlinkDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink tests not supported on Windows")
	}

	// Get the workspace-scoped log dir structure to set up the symlink in the expected location
	wsLogDir, err := getWorkspaceLogDir("")
	if err != nil {
		t.Fatalf("getWorkspaceLogDir() error = %v", err)
	}
	tasksDir := filepath.Join(wsLogDir, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatalf("failed to create tasks dir: %v", err)
	}

	// Create a real task dir within the log directory
	realTaskID := "symlink-real-target"
	realDir := filepath.Join(tasksDir, realTaskID)
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatalf("failed to create real dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(realDir, "planning.log"), []byte("data"), 0o644); err != nil {
		t.Fatalf("failed to create log file: %v", err)
	}
	defer os.RemoveAll(realDir)

	// Create a symlink for a fake task ID pointing to the real task dir (within logDir).
	// validatePathWithinDir will pass (target is within logDir), but Lstat catches the symlink.
	taskID := "symlink-test-task"
	symlinkPath := filepath.Join(tasksDir, taskID)
	defer os.Remove(symlinkPath)

	if err := os.Symlink(realDir, symlinkPath); err != nil {
		t.Fatalf("failed to create symlink dir: %v", err)
	}

	_, err = listTaskPhases("", taskID)
	if err == nil {
		t.Fatal("listTaskPhases() should reject symlink directory")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error = %q, want error containing 'symlink'", err.Error())
	}
}

func TestReadLastNLinesFromFile_SecureOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink tests not supported on Windows")
	}

	tmpDir := t.TempDir()

	// Create a regular file inside the allowed dir
	testFile := filepath.Join(tmpDir, "valid.log")
	if err := os.WriteFile(testFile, []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Secure open with correct allowedDir succeeds
	secureDir := tmpDir
	lines, _, err := readLastNLinesFromFile(testFile, 10, &secureDir, 0)
	if err != nil {
		t.Fatalf("readLastNLinesFromFile() error = %v", err)
	}
	if len(lines) != 2 {
		t.Errorf("got %d lines, want 2", len(lines))
	}

	// Create a symlink to a file outside the allowed dir
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("secret data\n"), 0o644); err != nil {
		t.Fatalf("failed to create outside file: %v", err)
	}
	symlinkFile := filepath.Join(tmpDir, "evil.log")
	if err := os.Symlink(outsideFile, symlinkFile); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	// Secure open should reject the symlink (O_NOFOLLOW)
	_, _, err = readLastNLinesFromFile(symlinkFile, 10, &secureDir, 0)
	if err == nil {
		t.Fatal("readLastNLinesFromFile() should reject symlink")
	}
}
