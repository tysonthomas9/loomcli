//go:build !windows

package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureSignalDir_CreatesWithCorrectPermissions(t *testing.T) {
	tmpDir := t.TempDir()
	dir := filepath.Join(tmpDir, "signal-dir")

	if err := ensureSignalDir(dir); err != nil {
		t.Fatalf("ensureSignalDir failed: %v", err)
	}

	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("directory not created: %v", err)
	}

	perm := fi.Mode().Perm()
	if perm != 0700 {
		t.Errorf("directory permissions = %o, want 0700", perm)
	}
}

func TestEnsureSignalDir_RejectsSymlink(t *testing.T) {
	tmpDir := t.TempDir()
	target := filepath.Join(tmpDir, "real-dir")
	link := filepath.Join(tmpDir, "symlink-dir")

	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatalf("failed to create target dir: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	err := ensureSignalDir(link)
	if err == nil {
		t.Fatal("ensureSignalDir should reject symlinks")
	}
	if !contains(err.Error(), "symlink") {
		t.Errorf("error should mention symlink, got: %v", err)
	}
}

func TestEnsureSignalDir_AcceptsValidDir(t *testing.T) {
	tmpDir := t.TempDir()
	dir := filepath.Join(tmpDir, "valid-dir")

	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	if err := ensureSignalDir(dir); err != nil {
		t.Errorf("ensureSignalDir should accept valid directory, got: %v", err)
	}
}

func TestEnsureSignalDir_RejectsFile(t *testing.T) {
	tmpDir := t.TempDir()
	file := filepath.Join(tmpDir, "not-a-dir")

	if err := os.WriteFile(file, []byte("data"), 0600); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	err := ensureSignalDir(file)
	if err == nil {
		t.Fatal("ensureSignalDir should reject regular files")
	}
	if !contains(err.Error(), "not a directory") {
		t.Errorf("error should mention 'not a directory', got: %v", err)
	}
}

func TestValidateSignalDir_RejectsSymlink(t *testing.T) {
	tmpDir := t.TempDir()
	target := filepath.Join(tmpDir, "real-dir")
	link := filepath.Join(tmpDir, "symlink-dir")

	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatalf("failed to create target dir: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	err := validateSignalDir(link)
	if err == nil {
		t.Fatal("validateSignalDir should reject symlinks")
	}
	if !contains(err.Error(), "symlink") {
		t.Errorf("error should mention symlink, got: %v", err)
	}
}

func TestValidateSignalDir_AcceptsValidDir(t *testing.T) {
	tmpDir := t.TempDir()
	dir := filepath.Join(tmpDir, "valid-dir")

	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	if err := validateSignalDir(dir); err != nil {
		t.Errorf("validateSignalDir should accept valid directory, got: %v", err)
	}
}

func TestValidateSignalDir_RejectsNonexistent(t *testing.T) {
	tmpDir := t.TempDir()
	dir := filepath.Join(tmpDir, "nonexistent")

	err := validateSignalDir(dir)
	if err == nil {
		t.Fatal("validateSignalDir should reject nonexistent paths")
	}
}

// contains checks if s contains substr (avoids importing strings in test)
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
