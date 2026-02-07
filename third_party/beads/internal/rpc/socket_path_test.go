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

func TestShortSocketPath_ShortPath(t *testing.T) {
	// Short paths should use the natural .beads/bd.sock location
	workspacePath := "/tmp/myrepo"
	socketPath := ShortSocketPath(workspacePath)

	expected := filepath.Join(workspacePath, ".beads", "bd.sock")
	if socketPath != expected {
		t.Errorf("ShortSocketPath(%q) = %q, want %q", workspacePath, socketPath, expected)
	}
}

func TestShortSocketPath_LongPath(t *testing.T) {
	// Long paths should use /tmp/beads-{hash}/bd.sock
	// Create a path that's definitely over 103 chars when .beads/bd.sock is added
	longPath := "/Volumes/External Drive/Dropbox/Projects/Clients/Company/product-name-with-extra-long-name"
	socketPath := ShortSocketPath(longPath)

	// Should be relocated to /tmp
	if !strings.HasPrefix(socketPath, "/tmp/beads-") {
		t.Errorf("ShortSocketPath(%q) = %q, want path starting with /tmp/beads-", longPath, socketPath)
	}

	// Should end with bd.sock
	if !strings.HasSuffix(socketPath, "/bd.sock") {
		t.Errorf("ShortSocketPath(%q) = %q, want path ending with /bd.sock", longPath, socketPath)
	}

	// Path should be short enough
	if len(socketPath) > MaxUnixSocketPath {
		t.Errorf("ShortSocketPath(%q) = %q (len=%d), want len <= %d", longPath, socketPath, len(socketPath), MaxUnixSocketPath)
	}
}

func TestShortSocketPath_Deterministic(t *testing.T) {
	// Same workspace should always produce same socket path
	workspacePath := "/Volumes/External Drive/Some/Long/Path/To/A/Repository"
	path1 := ShortSocketPath(workspacePath)
	path2 := ShortSocketPath(workspacePath)

	if path1 != path2 {
		t.Errorf("ShortSocketPath is not deterministic: %q != %q", path1, path2)
	}
}

func TestShortSocketPath_DifferentWorkspaces(t *testing.T) {
	// Different workspaces should produce different socket paths
	workspace1 := "/Volumes/External/Project1/With/Long/Path/Here"
	workspace2 := "/Volumes/External/Project2/With/Long/Path/Here"

	path1 := ShortSocketPath(workspace1)
	path2 := ShortSocketPath(workspace2)

	if path1 == path2 {
		t.Errorf("Different workspaces should produce different socket paths: both got %q", path1)
	}
}

func TestNeedsShortPath(t *testing.T) {
	tests := []struct {
		name      string
		workspace string
		want      bool
	}{
		{
			name:      "short path",
			workspace: "/tmp/myrepo",
			want:      false,
		},
		{
			name:      "medium path",
			workspace: "/Users/john/projects/myrepo",
			want:      false,
		},
		{
			name:      "long path exceeding limit",
			workspace: "/Volumes/External Drive/Dropbox/Projects/Clients/Company/product-name-with-extra-characters-to-exceed-limit",
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NeedsShortPath(tt.workspace)
			if got != tt.want {
				naturalPath := filepath.Join(tt.workspace, ".beads", "bd.sock")
				t.Errorf("NeedsShortPath(%q) = %v, want %v (natural path len=%d, limit=%d)",
					tt.workspace, got, tt.want, len(naturalPath), MaxUnixSocketPath)
			}
		})
	}
}

func TestEnsureSocketDir(t *testing.T) {
	// Test creating a /tmp/beads-* directory
	// Manually simulate the condition where we need to create the directory
	// by using a path format that matches our pattern
	testSocketPath := filepath.Join("/tmp", "beads-testxyz", "bd.sock")

	result, err := EnsureSocketDir(testSocketPath)
	if err != nil {
		t.Fatalf("EnsureSocketDir failed: %v", err)
	}

	if result != testSocketPath {
		t.Errorf("EnsureSocketDir returned %q, want %q", result, testSocketPath)
	}

	// Clean up
	_ = os.RemoveAll(filepath.Dir(testSocketPath))
}

func TestCleanupSocketDir(t *testing.T) {
	// Create a test directory in /tmp
	testDir := filepath.Join("/tmp", "beads-cleanup-test")
	if err := os.MkdirAll(testDir, 0700); err != nil {
		t.Fatalf("Failed to create test dir: %v", err)
	}

	socketPath := filepath.Join(testDir, "bd.sock")
	if err := os.WriteFile(socketPath, []byte("test"), 0600); err != nil {
		t.Fatalf("Failed to create test socket file: %v", err)
	}

	// Clean up
	if err := CleanupSocketDir(socketPath); err != nil {
		t.Errorf("CleanupSocketDir failed: %v", err)
	}

	// Directory should be removed
	if _, err := os.Stat(testDir); !os.IsNotExist(err) {
		t.Errorf("Directory %s should have been removed", testDir)
		_ = os.RemoveAll(testDir) // Clean up for next run
	}
}

func TestShortSocketPath_EdgeCase_ExactLimit(t *testing.T) {
	// Test a path that's exactly at the limit
	// .beads/bd.sock adds 15 characters
	// So a workspace path of 88 chars + 15 = 103 (exactly at limit)
	workspace := strings.Repeat("x", 88)
	socketPath := ShortSocketPath(workspace)

	// Should use natural path since it's exactly at the limit
	expected := filepath.Join(workspace, ".beads", "bd.sock")
	if socketPath != expected {
		t.Errorf("Path at exact limit should use natural path.\nGot: %q\nWant: %q\nLen: %d", socketPath, expected, len(expected))
	}
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

	fi, err := os.Stat(dirPath)
	if err != nil {
		t.Fatalf("Stat() error: %v", err)
	}
	if fi.Mode().Perm() != 0700 {
		t.Errorf("directory permissions = %o, want 0700 (should have been fixed)", fi.Mode().Perm())
	}
}

func TestEnsureSocketDir_RejectsNonDirectory(t *testing.T) {
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
