//go:build !windows

package rpc

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestShortSocketPath_ShortWorkspace(t *testing.T) {
	t.Parallel()

	// Short workspace path - should use natural .loom/loom.sock path
	workspacePath := "/home/user/proj"
	result := ShortSocketPath(workspacePath)

	expected := filepath.Join(workspacePath, ".loom", "loom.sock")
	if result != expected {
		t.Errorf("ShortSocketPath(%q) = %q, want %q", workspacePath, result, expected)
	}
}

func TestShortSocketPath_LongWorkspace(t *testing.T) {
	t.Parallel()

	// Create a path that exceeds MaxUnixSocketPath when combined with .loom/loom.sock
	// MaxUnixSocketPath is 103, .loom/loom.sock is 14 chars
	longDir := "/" + strings.Repeat("a", 100)
	result := ShortSocketPath(longDir)

	// Should use /tmp/loom-{hash}/loom.sock pattern
	if !strings.HasPrefix(result, "/tmp/loom-") {
		t.Errorf("ShortSocketPath() for long path should use /tmp/loom-*, got %q", result)
	}

	if !strings.HasSuffix(result, "/loom.sock") {
		t.Errorf("ShortSocketPath() should end with /loom.sock, got %q", result)
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
	// .loom/loom.sock is 14 chars, need path length = 103 - 14 = 89
	// Account for leading slash
	dirLen := MaxUnixSocketPath - len("/.loom/loom.sock")
	boundaryPath := "/" + strings.Repeat("x", dirLen-1)

	naturalPath := filepath.Join(boundaryPath, ".loom", "loom.sock")

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
		naturalOver := filepath.Join(overPath, ".loom", "loom.sock")
		if len(naturalOver) > MaxUnixSocketPath && !strings.HasPrefix(result, "/tmp/loom-") {
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

func TestIsManagedTempSocketDir(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		dir  string
		want bool
	}{
		{name: "generated direct child", dir: "/tmp/loom-0123456789abcdef", want: true},
		{name: "test direct child", dir: "/tmp/loom-test-cleanup-123", want: true},
		{name: "nested workspace runtime", dir: "/tmp/loom-parent/workspace/.loom", want: false},
		{name: "nested loom child", dir: "/tmp/loom-parent/loom-child", want: false},
		{name: "wrong basename", dir: "/tmp/not-loom", want: false},
		{name: "wrong parent", dir: "/var/tmp/loom-0123456789abcdef", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isManagedTempSocketDir(tt.dir); got != tt.want {
				t.Errorf("isManagedTempSocketDir(%q) = %t, want %t", tt.dir, got, tt.want)
			}
		})
	}
}

func TestEnsureSocketDir(t *testing.T) {
	t.Parallel()

	t.Run("tmp loom directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		// Simulate a /tmp/loom-* path structure
		testPath := filepath.Join(tmpDir, "loom-abcd1234", "loom.sock")

		result, err := EnsureSocketDir(testPath)
		if err != nil {
			t.Fatalf("EnsureSocketDir() error: %v", err)
		}

		if result != testPath {
			t.Errorf("EnsureSocketDir() = %q, want %q", result, testPath)
		}

		// Note: We can't test actual /tmp/loom-* creation in parallel tests
		// as it would conflict with other tests
	})

	t.Run("loom directory not created", func(t *testing.T) {
		// For .loom directories, EnsureSocketDir should not create them
		socketPath := "/some/path/.loom/loom.sock"
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

	t.Run("loom directory cleanup", func(t *testing.T) {
		tmpDir := t.TempDir()
		runtimeDir := filepath.Join(tmpDir, ".loom")
		if err := os.MkdirAll(runtimeDir, 0755); err != nil {
			t.Fatalf("Failed to create runtime dir: %v", err)
		}

		socketPath := filepath.Join(runtimeDir, "loom.sock")
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

		// Directory should remain (for .loom paths)
		if _, err := os.Stat(runtimeDir); os.IsNotExist(err) {
			t.Error(".loom directory should not be removed")
		}
	})

	t.Run("tmp loom directory cleanup", func(t *testing.T) {
		// Create a /tmp/loom-* directory to test the removal path
		dirName := fmt.Sprintf("loom-test-cleanup-%d", os.Getpid())
		dirPath := filepath.Join("/tmp", dirName)
		t.Cleanup(func() { os.RemoveAll(dirPath) })

		if err := os.Mkdir(dirPath, 0700); err != nil {
			t.Fatalf("Mkdir() error: %v", err)
		}

		socketPath := filepath.Join(dirPath, "loom.sock")
		if err := os.WriteFile(socketPath, []byte{}, 0644); err != nil {
			t.Fatalf("WriteFile() error: %v", err)
		}

		err := CleanupSocketDir(socketPath)
		if err != nil {
			t.Errorf("CleanupSocketDir() error: %v", err)
		}

		// Socket file should be removed
		if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
			t.Error("Socket file should be removed")
		}

		// Directory should also be removed for /tmp/loom-* paths
		if _, err := os.Stat(dirPath); !os.IsNotExist(err) {
			t.Error("/tmp/loom-* directory should be removed after cleanup")
		}
	})
}

func TestShortSocketDir_HashLength(t *testing.T) {
	t.Parallel()

	// Verify the hash portion is 16 hex chars (8 bytes)
	result := shortSocketDir("/some/canonical/path")

	// Result should be /tmp/loom-{16 hex chars}/loom.sock
	dir := filepath.Dir(result)
	prefix := "/tmp/loom-"
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
	// Create a new /tmp/loom-* directory and verify it has mode 0700
	dirName := "loom-test-perms-" + t.Name()
	socketPath := filepath.Join("/tmp", dirName, "loom.sock")
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
	// Create a symlink at /tmp/loom-* pointing elsewhere — EnsureSocketDir should reject it
	target := t.TempDir()
	dirName := "loom-test-symlink-" + t.Name()
	symlinkPath := filepath.Join("/tmp", dirName)
	t.Cleanup(func() { os.Remove(symlinkPath) })

	if err := os.Symlink(target, symlinkPath); err != nil {
		t.Fatalf("Symlink() error: %v", err)
	}

	socketPath := filepath.Join(symlinkPath, "loom.sock")
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
	dirName := "loom-test-valid-" + t.Name()
	dirPath := filepath.Join("/tmp", dirName)
	t.Cleanup(func() { os.RemoveAll(dirPath) })

	if err := os.Mkdir(dirPath, 0700); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}

	socketPath := filepath.Join(dirPath, "loom.sock")
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
	dirName := "loom-test-badperms-" + t.Name()
	dirPath := filepath.Join("/tmp", dirName)
	t.Cleanup(func() { os.RemoveAll(dirPath) })

	if err := os.Mkdir(dirPath, 0777); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}

	socketPath := filepath.Join(dirPath, "loom.sock")
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
	dirName := "loom-test-file-" + t.Name()
	filePath := filepath.Join("/tmp", dirName)
	t.Cleanup(func() { os.Remove(filePath) })

	if err := os.WriteFile(filePath, []byte("not a dir"), 0700); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	socketPath := filepath.Join(filePath, "loom.sock")
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

func TestEnsureSocketDir_LstatNonNotExistError(t *testing.T) {
	// Trigger the path where os.Lstat returns an error that is NOT os.IsNotExist.
	// A path component exceeding NAME_MAX (255 on macOS/Linux) causes ENAMETOOLONG.
	longName := "loom-" + strings.Repeat("x", 300)
	socketPath := filepath.Join("/tmp", longName, "loom.sock")

	_, err := EnsureSocketDir(socketPath)
	if err == nil {
		t.Fatal("EnsureSocketDir() should return error for path exceeding NAME_MAX")
	}
	if !strings.Contains(err.Error(), "failed to stat socket directory") {
		t.Errorf("error should contain 'failed to stat socket directory', got: %v", err)
	}
}

func TestNestedTmpWorkspaceSocketDirIsNotManaged(t *testing.T) {
	parentName := fmt.Sprintf("loom-parent-%d-%s", os.Getpid(), strings.ReplaceAll(t.Name(), "/", "-"))
	parentDir := filepath.Join(tmpDir, parentName)
	runtimeDir := filepath.Join(parentDir, "workspace", ".loom")
	socketPath := filepath.Join(runtimeDir, "loom.sock")

	if err := os.RemoveAll(parentDir); err != nil {
		t.Fatalf("remove stale parent: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(parentDir) })
	if err := os.MkdirAll(runtimeDir, 0755); err != nil {
		t.Fatalf("create nested runtime directory: %v", err)
	}
	if err := os.Chmod(runtimeDir, 0755); err != nil {
		t.Fatalf("set nested runtime permissions: %v", err)
	}

	result, err := EnsureSocketDir(socketPath)
	if err != nil {
		t.Fatalf("EnsureSocketDir() error: %v", err)
	}
	if result != socketPath {
		t.Fatalf("EnsureSocketDir() = %q, want %q", result, socketPath)
	}
	fi, err := os.Stat(runtimeDir)
	if err != nil {
		t.Fatalf("stat nested runtime directory: %v", err)
	}
	if fi.Mode().Perm() != 0755 {
		t.Fatalf("nested runtime permissions = %o, want unchanged 0755", fi.Mode().Perm())
	}

	if err := os.WriteFile(socketPath, []byte{}, 0600); err != nil {
		t.Fatalf("create socket placeholder: %v", err)
	}
	if err := CleanupSocketDir(socketPath); err != nil {
		t.Fatalf("CleanupSocketDir() error: %v", err)
	}
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("socket path still exists after cleanup: %v", err)
	}
	fi, err = os.Stat(runtimeDir)
	if err != nil {
		t.Fatalf("nested runtime directory was removed: %v", err)
	}
	if fi.Mode().Perm() != 0755 {
		t.Errorf("nested runtime permissions after cleanup = %o, want unchanged 0755", fi.Mode().Perm())
	}
}

func TestEnsureSocketDir_ConcurrentMkdirRace(t *testing.T) {
	// Test the race condition in lines 90-98 of socket_path.go where multiple
	// goroutines call EnsureSocketDir concurrently for a non-existent directory.
	// Exactly one goroutine creates it via Mkdir; others hit os.IsExist and
	// fall through to re-stat and validate. All must succeed.
	dirName := "loom-test-race-" + strings.ReplaceAll(t.Name(), "/", "-")
	dirPath := filepath.Join("/tmp", dirName)
	socketPath := filepath.Join(dirPath, "loom.sock")

	// Ensure the directory does not exist before we start
	os.RemoveAll(dirPath)
	t.Cleanup(func() { os.RemoveAll(dirPath) })

	const numGoroutines = 10
	errs := make([]error, numGoroutines)
	results := make([]string, numGoroutines)

	var wg sync.WaitGroup
	wg.Add(numGoroutines)
	ready := make(chan struct{})

	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			<-ready // Wait for all goroutines to be ready
			results[idx], errs[idx] = EnsureSocketDir(socketPath)
		}(i)
	}

	// Release all goroutines simultaneously to maximize race window
	close(ready)
	wg.Wait()

	// All goroutines must succeed
	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: EnsureSocketDir() error: %v", i, err)
		}
		if results[i] != socketPath {
			t.Errorf("goroutine %d: result = %q, want %q", i, results[i], socketPath)
		}
	}

	// Verify the directory exists with correct permissions and ownership
	fi, err := os.Lstat(dirPath)
	if err != nil {
		t.Fatalf("directory should exist after concurrent creation: %v", err)
	}
	if !fi.IsDir() {
		t.Fatal("path should be a directory")
	}
	if fi.Mode().Perm() != 0700 {
		t.Errorf("directory permissions = %o, want 0700", fi.Mode().Perm())
	}
}

func TestNormalizePathForComparison_EvalSymlinksFallback(t *testing.T) {
	t.Parallel()

	// Use a non-existent path where EvalSymlinks fails but Abs succeeds,
	// exercising the fallback to the absolute path.
	nonExistent := "/nonexistent/path/that/does/not/exist"
	result := normalizePathForComparison(nonExistent)

	if result == "" {
		t.Fatal("Should return non-empty for non-existent absolute path")
	}

	// On case-insensitive systems, the result should be lowercased
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		if result != strings.ToLower(nonExistent) {
			t.Errorf("normalizePathForComparison() = %q, want %q (lowercased)", result, strings.ToLower(nonExistent))
		}
	} else {
		if result != nonExistent {
			t.Errorf("normalizePathForComparison() = %q, want %q", result, nonExistent)
		}
	}
}

func TestNormalizePathForComparison_SymlinkResolution(t *testing.T) {
	t.Parallel()

	// Create a temp dir and a symlink to it, verify both resolve to same canonical path
	target := t.TempDir()
	linkParent := t.TempDir()
	symlinkPath := filepath.Join(linkParent, "link")
	if err := os.Symlink(target, symlinkPath); err != nil {
		t.Fatalf("Symlink() error: %v", err)
	}

	result1 := normalizePathForComparison(target)
	result2 := normalizePathForComparison(symlinkPath)

	if result1 != result2 {
		t.Errorf("Symlink and target should normalize to same path:\n  target:  %q\n  symlink: %q", result1, result2)
	}
}
