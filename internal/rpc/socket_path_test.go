//go:build !windows

package rpc

import (
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
