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
	"syscall"
)

// MaxUnixSocketPath is the maximum length for Unix socket paths.
// macOS has a 104-byte limit (including null terminator), Linux has 108.
// We use 103 to be safe across platforms.
const MaxUnixSocketPath = 103

// ShortSocketPath returns a short socket path suitable for Unix sockets.
// On Unix systems with socket path length limits (macOS: 104 chars, Linux: 108),
// this function returns a path in /tmp/loom-{hash}/ to avoid exceeding limits.
//
// The hash is derived from the canonicalized workspace path, ensuring:
// - Different workspaces get different socket directories
// - The same workspace always gets the same hash (deterministic)
// - Symlinks and case differences resolve to the same hash
//
// If the computed .loom/loom.sock path is short enough, it returns that directly.
func ShortSocketPath(workspacePath string) string {
	// Canonicalize path for consistent hashing across symlinks and case
	canonical := normalizePathForComparison(workspacePath)
	if canonical == "" {
		canonical = workspacePath
	}

	// Compute the natural socket path in the workspace runtime directory.
	naturalPath := filepath.Join(workspacePath, ".loom", "loom.sock")

	// If natural path is short enough, use it.
	if len(naturalPath) <= MaxUnixSocketPath {
		return naturalPath
	}

	// Path too long - use /tmp with hash
	return shortSocketDir(canonical)
}

// shortSocketDir returns a socket path in /tmp/loom-{hash}/.
// The hash is 16 hex characters derived from SHA256 of the workspace path.
func shortSocketDir(canonicalPath string) string {
	hash := sha256.Sum256([]byte(canonicalPath))
	hashStr := hex.EncodeToString(hash[:8]) // 16 hex chars from 8 bytes

	dir := filepath.Join(tmpDir, "loom-"+hashStr)
	return filepath.Join(dir, "loom.sock")
}

// tmpDir returns the temp directory for sockets.
// We always use /tmp because:
// - On macOS, $TMPDIR is very long (/var/folders/xx/xxxxxxxxxxxx/T/)
// - On Linux, /tmp is standard
// - We need short paths due to Unix socket length limits
const tmpDir = "/tmp"

// EnsureSocketDir creates the socket directory if it doesn't exist.
// Returns the socket path (unchanged) and any error.
// This should be called by the daemon before listening.
//
// For /tmp/loom-* directories, this function validates that the directory
// is not a symlink and is owned by the current user with mode 0700, to
// prevent symlink attacks where an attacker pre-creates the directory.
func EnsureSocketDir(socketPath string) (string, error) {
	dir := filepath.Dir(socketPath)

	// Only manage /tmp/loom-* directories.
	// Workspace runtime directories live inside the workspace and should already exist.
	if !strings.HasPrefix(dir, filepath.Join(tmpDir, "loom-")) {
		return socketPath, nil
	}

	fi, err := os.Lstat(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("failed to stat socket directory %s: %w", dir, err)
		}
		// Directory doesn't exist — create it atomically
		if mkErr := os.Mkdir(dir, 0700); mkErr != nil {
			if !os.IsExist(mkErr) {
				return "", fmt.Errorf("failed to create socket directory %s: %w", dir, mkErr)
			}
			// Another process created it between our Lstat and Mkdir — re-stat and validate
			fi, err = os.Lstat(dir)
			if err != nil {
				return "", fmt.Errorf("failed to stat socket directory %s: %w", dir, err)
			}
			// Fall through to validation below
		} else {
			// We created it ourselves — safe to use
			return socketPath, nil
		}
	}

	// Directory exists — validate it's safe to use
	if fi.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("socket directory %s is a symlink (possible symlink attack)", dir)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("socket directory path %s is not a directory", dir)
	}

	// Verify ownership matches current user
	stat, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("failed to get ownership info for socket directory %s", dir)
	}
	if stat.Uid != uint32(os.Getuid()) { //nolint:gosec // G115: uid fits uint32 on supported platforms
		return "", fmt.Errorf("socket directory %s is owned by uid %d, expected %d (possible attack)", dir, stat.Uid, os.Getuid())
	}

	// Ensure permissions are restrictive
	if fi.Mode().Perm() != 0700 {
		if err := os.Chmod(dir, 0700); err != nil {
			return "", fmt.Errorf("failed to fix permissions on socket directory %s: %w", dir, err)
		}
	}

	return socketPath, nil
}

// CleanupSocketDir removes the socket directory if it's in /tmp/loom-*.
// This should be called when the daemon shuts down.
func CleanupSocketDir(socketPath string) error {
	dir := filepath.Dir(socketPath)

	// Only remove if it's a /tmp/loom-* directory we created.
	if strings.HasPrefix(dir, filepath.Join(tmpDir, "loom-")) {
		// Remove socket file first
		_ = os.Remove(socketPath)
		// Remove directory (will fail if not empty, which is fine)
		return os.Remove(dir)
	}

	// For workspace runtime directories, just remove the socket file.
	return os.Remove(socketPath)
}

// NeedsShortPath returns true if the workspace path would result in a socket
// path exceeding Unix limits.
func NeedsShortPath(workspacePath string) bool {
	naturalPath := filepath.Join(workspacePath, ".loom", "loom.sock")
	return len(naturalPath) > MaxUnixSocketPath
}

// normalizePathForComparison canonicalizes a path for consistent hashing.
// Resolves symlinks and lowercases on case-insensitive filesystems (macOS, Windows).
func normalizePathForComparison(path string) string {
	if path == "" {
		return ""
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}
	canonical, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		canonical = absPath
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		canonical = strings.ToLower(canonical)
	}
	return canonical
}
