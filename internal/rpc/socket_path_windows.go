//go:build windows

package rpc

import (
	"os"
	"path/filepath"
)

// MaxUnixSocketPath is not applicable on Windows.
const MaxUnixSocketPath = 103

// ShortSocketPath returns the socket path for Windows.
// Windows uses a named-pipe endpoint file, so path length is not a concern.
func ShortSocketPath(workspacePath string) string {
	return filepath.Join(workspacePath, ".loom", "loom.sock")
}

// EnsureSocketDir is a no-op on Windows since the .loom directory
// should already exist.
func EnsureSocketDir(socketPath string) (string, error) {
	return socketPath, nil
}

// CleanupSocketDir removes the socket file on Windows.
func CleanupSocketDir(socketPath string) error {
	return os.Remove(socketPath)
}

// NeedsShortPath always returns false on Windows.
func NeedsShortPath(workspacePath string) bool {
	return false
}
