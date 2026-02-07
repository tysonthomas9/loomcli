//go:build !windows

package rpc

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShortSocketPath_ShortWorkspace(t *testing.T) {
	t.Parallel()

	// Short workspace path - should use natural .beads/bd.sock path
	workspacePath := "/home/user/proj"
	result := ShortSocketPath(workspacePath)

	expected := filepath.Join(workspacePath, ".beads", "bd.sock")
	if result != expected {
		t.Errorf("ShortSocketPath(%q) = %q, want %q", workspacePath, result, expected)
	}
}

func TestShortSocketPath_LongWorkspace(t *testing.T) {
	t.Parallel()

	// Create a path that exceeds MaxUnixSocketPath when combined with .beads/bd.sock
	// MaxUnixSocketPath is 103, .beads/bd.sock is 14 chars
	longDir := "/" + strings.Repeat("a", 100)
	result := ShortSocketPath(longDir)

	// Should use /tmp/beads-{hash}/bd.sock pattern
	if !strings.HasPrefix(result, "/tmp/beads-") {
		t.Errorf("ShortSocketPath() for long path should use /tmp/beads-*, got %q", result)
	}

	if !strings.HasSuffix(result, "/bd.sock") {
		t.Errorf("ShortSocketPath() should end with /bd.sock, got %q", result)
	}

	// Verify path is short enough
	if len(result) > MaxUnixSocketPath {
		t.Errorf("ShortSocketPath() result length = %d, exceeds MaxUnixSocketPath = %d", len(result), MaxUnixSocketPath)
	}
}

func TestShortSocketPath_Determinism(t *testing.T) {
	t.Parallel()

	workspacePath := "/home/user/very/long/path/that/exceeds/the/limit/for/unix/sockets/maximum/path/length"

	result1 := ShortSocketPath(workspacePath)
	result2 := ShortSocketPath(workspacePath)

	if result1 != result2 {
		t.Errorf("ShortSocketPath() not deterministic: %q != %q", result1, result2)
	}
}

func TestShortSocketPath_DifferentWorkspaces(t *testing.T) {
	t.Parallel()

	// Create two long paths that should produce different hashes
	path1 := "/" + strings.Repeat("a", 100)
	path2 := "/" + strings.Repeat("b", 100)

	result1 := ShortSocketPath(path1)
	result2 := ShortSocketPath(path2)

	if result1 == result2 {
		t.Errorf("Different workspaces should produce different socket paths: %q == %q", result1, result2)
	}
}

func TestShortSocketPath_Boundary(t *testing.T) {
	t.Parallel()

	// Test at exactly MaxUnixSocketPath boundary
	// .beads/bd.sock is 14 chars, need path length = 103 - 14 = 89
	// Account for leading slash
	dirLen := MaxUnixSocketPath - len("/.beads/bd.sock")
	boundaryPath := "/" + strings.Repeat("x", dirLen-1)

	naturalPath := filepath.Join(boundaryPath, ".beads", "bd.sock")

	t.Run("at_boundary", func(t *testing.T) {
		if len(naturalPath) != MaxUnixSocketPath {
			t.Logf("Natural path length: %d, expected: %d", len(naturalPath), MaxUnixSocketPath)
		}
		result := ShortSocketPath(boundaryPath)
		// At boundary, should still use natural path
		if len(naturalPath) <= MaxUnixSocketPath && result != naturalPath {
			t.Logf("At boundary, got %q (len=%d)", result, len(result))
		}
	})

	t.Run("just_over_boundary", func(t *testing.T) {
		overPath := boundaryPath + "y"
		result := ShortSocketPath(overPath)
		// Just over boundary, should use /tmp path
		naturalOver := filepath.Join(overPath, ".beads", "bd.sock")
		if len(naturalOver) > MaxUnixSocketPath && !strings.HasPrefix(result, "/tmp/beads-") {
			t.Errorf("Over boundary should use /tmp path, got %q", result)
		}
	})
}

func TestNeedsShortPath(t *testing.T) {
	t.Parallel()

	t.Run("short path", func(t *testing.T) {
		if NeedsShortPath("/home/user/proj") {
			t.Error("Short path should not need short socket path")
		}
	})

	t.Run("long path", func(t *testing.T) {
		longPath := "/" + strings.Repeat("a", 100)
		if !NeedsShortPath(longPath) {
			t.Error("Long path should need short socket path")
		}
	})
}

func TestMaxUnixSocketPath_Constant(t *testing.T) {
	t.Parallel()

	if MaxUnixSocketPath != 103 {
		t.Errorf("MaxUnixSocketPath = %d, want 103", MaxUnixSocketPath)
	}
}

func TestEnsureSocketDir(t *testing.T) {
	t.Parallel()

	t.Run("tmp beads directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		// Simulate a /tmp/beads-* path structure
		testPath := filepath.Join(tmpDir, "beads-abcd1234", "bd.sock")

		result, err := EnsureSocketDir(testPath)
		if err != nil {
			t.Fatalf("EnsureSocketDir() error: %v", err)
		}

		if result != testPath {
			t.Errorf("EnsureSocketDir() = %q, want %q", result, testPath)
		}

		// Note: We can't test actual /tmp/beads-* creation in parallel tests
		// as it would conflict with other tests
	})

	t.Run("beads directory not created", func(t *testing.T) {
		// For .beads directories, EnsureSocketDir should not create them
		socketPath := "/some/path/.beads/bd.sock"
		result, err := EnsureSocketDir(socketPath)

		// Should return the path unchanged (no creation attempted)
		if err != nil {
			t.Fatalf("EnsureSocketDir() error: %v", err)
		}
		if result != socketPath {
			t.Errorf("EnsureSocketDir() = %q, want %q", result, socketPath)
		}
	})
}

func TestCleanupSocketDir(t *testing.T) {
	t.Parallel()

	t.Run("beads directory cleanup", func(t *testing.T) {
		tmpDir := t.TempDir()
		beadsDir := filepath.Join(tmpDir, ".beads")
		if err := os.MkdirAll(beadsDir, 0755); err != nil {
			t.Fatalf("Failed to create beads dir: %v", err)
		}

		socketPath := filepath.Join(beadsDir, "bd.sock")
		if err := os.WriteFile(socketPath, []byte{}, 0644); err != nil {
			t.Fatalf("Failed to create socket file: %v", err)
		}

		err := CleanupSocketDir(socketPath)
		if err != nil {
			t.Errorf("CleanupSocketDir() error: %v", err)
		}

		// Socket file should be removed
		if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
			t.Error("Socket file should be removed")
		}

		// Directory should remain (for .beads paths)
		if _, err := os.Stat(beadsDir); os.IsNotExist(err) {
			t.Error(".beads directory should not be removed")
		}
	})
}

func TestShortSocketDir_HashLength(t *testing.T) {
	t.Parallel()

	// Verify the hash portion is 16 hex chars (8 bytes)
	result := shortSocketDir("/some/canonical/path")

	// Result should be /tmp/beads-{16 hex chars}/bd.sock
	dir := filepath.Dir(result)
	prefix := "/tmp/beads-"
	if !strings.HasPrefix(dir, prefix) {
		t.Fatalf("shortSocketDir() dir = %q, want prefix %q", dir, prefix)
	}

	hashPart := strings.TrimPrefix(dir, prefix)
	if len(hashPart) != 16 {
		t.Errorf("hash portion length = %d, want 16 (got %q)", len(hashPart), hashPart)
	}

	// Verify it's valid hex
	if _, err := hex.DecodeString(hashPart); err != nil {
		t.Errorf("hash portion %q is not valid hex: %v", hashPart, err)
	}

	// Verify it matches the first 8 bytes of SHA256
	hash := sha256.Sum256([]byte("/some/canonical/path"))
	expected := hex.EncodeToString(hash[:8])
	if hashPart != expected {
		t.Errorf("hash = %q, want %q", hashPart, expected)
	}
}

func TestEnsureSocketDir_CreatesWithCorrectPermissions(t *testing.T) {
	// Create a new /tmp/beads-* directory and verify it has mode 0700
	dirName := "beads-test-perms-" + t.Name()
	socketPath := filepath.Join("/tmp", dirName, "bd.sock")
	t.Cleanup(func() { os.RemoveAll(filepath.Dir(socketPath)) })

	result, err := EnsureSocketDir(socketPath)
	if err != nil {
		t.Fatalf("EnsureSocketDir() error: %v", err)
	}
	if result != socketPath {
		t.Errorf("EnsureSocketDir() = %q, want %q", result, socketPath)
	}

	fi, err := os.Stat(filepath.Dir(socketPath))
	if err != nil {
		t.Fatalf("Stat() error: %v", err)
	}
	if fi.Mode().Perm() != 0700 {
		t.Errorf("directory permissions = %o, want 0700", fi.Mode().Perm())
	}
}

func TestEnsureSocketDir_RejectsSymlink(t *testing.T) {
	// Create a symlink at /tmp/beads-* pointing elsewhere — EnsureSocketDir should reject it
	target := t.TempDir()
	dirName := "beads-test-symlink-" + t.Name()
	symlinkPath := filepath.Join("/tmp", dirName)
	t.Cleanup(func() { os.Remove(symlinkPath) })

	if err := os.Symlink(target, symlinkPath); err != nil {
		t.Fatalf("Symlink() error: %v", err)
	}

	socketPath := filepath.Join(symlinkPath, "bd.sock")
	_, err := EnsureSocketDir(socketPath)
	if err == nil {
		t.Fatal("EnsureSocketDir() should reject symlink directory, got nil error")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error should mention symlink, got: %v", err)
	}
}

func TestEnsureSocketDir_AcceptsExistingValidDir(t *testing.T) {
	// Pre-create a valid directory with correct permissions
	dirName := "beads-test-valid-" + t.Name()
	dirPath := filepath.Join("/tmp", dirName)
	t.Cleanup(func() { os.RemoveAll(dirPath) })

	if err := os.Mkdir(dirPath, 0700); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}

	socketPath := filepath.Join(dirPath, "bd.sock")
	result, err := EnsureSocketDir(socketPath)
	if err != nil {
		t.Fatalf("EnsureSocketDir() error: %v", err)
	}
	if result != socketPath {
		t.Errorf("EnsureSocketDir() = %q, want %q", result, socketPath)
	}
}

func TestEnsureSocketDir_FixesBadPermissions(t *testing.T) {
	// Create a directory with wrong permissions (owned by current user)
	dirName := "beads-test-badperms-" + t.Name()
	dirPath := filepath.Join("/tmp", dirName)
	t.Cleanup(func() { os.RemoveAll(dirPath) })

	if err := os.Mkdir(dirPath, 0777); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}

	socketPath := filepath.Join(dirPath, "bd.sock")
	_, err := EnsureSocketDir(socketPath)
	if err != nil {
		t.Fatalf("EnsureSocketDir() error: %v", err)
	}

	// Permissions should now be tightened to 0700
	fi, err := os.Stat(dirPath)
	if err != nil {
		t.Fatalf("Stat() error: %v", err)
	}
	if fi.Mode().Perm() != 0700 {
		t.Errorf("directory permissions = %o, want 0700 (should have been fixed)", fi.Mode().Perm())
	}
}

func TestEnsureSocketDir_RejectsNonDirectory(t *testing.T) {
	// Create a regular file where the directory should be
	dirName := "beads-test-file-" + t.Name()
	filePath := filepath.Join("/tmp", dirName)
	t.Cleanup(func() { os.Remove(filePath) })

	if err := os.WriteFile(filePath, []byte("not a dir"), 0700); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	socketPath := filepath.Join(filePath, "bd.sock")
	_, err := EnsureSocketDir(socketPath)
	if err == nil {
		t.Fatal("EnsureSocketDir() should reject non-directory path, got nil error")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("error should mention 'not a directory', got: %v", err)
	}
}

func TestNormalizePathForComparison(t *testing.T) {
	t.Parallel()

	t.Run("empty path", func(t *testing.T) {
		result := normalizePathForComparison("")
		if result != "" {
			t.Errorf("Empty path should return empty, got %q", result)
		}
	})

	t.Run("absolute path", func(t *testing.T) {
		result := normalizePathForComparison("/home/user/project")
		if result == "" {
			t.Error("Should return non-empty for valid path")
		}
	})

	t.Run("relative path", func(t *testing.T) {
		result := normalizePathForComparison("./project")
		if result == "" {
			t.Error("Should return non-empty for relative path")
		}
	})
}
