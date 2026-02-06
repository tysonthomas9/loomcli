package daemon

import (
	"os"
	"path/filepath"

	internaldaemon "github.com/steveyegge/beads/internal/daemon"
	"github.com/steveyegge/beads/internal/rpc"
)

// DiscoverSocketPath finds the daemon socket for a workspace.
// It uses the internal daemon discovery utilities to locate the socket.
func DiscoverSocketPath(workspacePath string) (string, error) {
	if workspacePath == "" {
		return "", ErrInvalidSocketPath
	}

	// Use internal daemon discovery to find the daemon
	daemonInfo, err := internaldaemon.FindDaemonByWorkspace(workspacePath)
	if err != nil {
		return "", ErrDaemonNotRunning
	}

	if daemonInfo.SocketPath == "" {
		return "", ErrDaemonNotRunning
	}

	return daemonInfo.SocketPath, nil
}

// DiscoverSocketPathFromEnv finds the socket from BEADS_SOCKET env var or current directory.
// It checks in order:
// 1. BEADS_SOCKET environment variable (explicit socket path)
// 2. Current working directory (auto-discover daemon for workspace)
func DiscoverSocketPathFromEnv() (string, error) {
	// Check for explicit socket path in environment
	if socketPath := os.Getenv("BEADS_SOCKET"); socketPath != "" {
		// Validate the path exists
		if _, err := os.Stat(socketPath); err != nil {
			if os.IsNotExist(err) {
				return "", ErrDaemonNotRunning
			}
			return "", ErrInvalidSocketPath
		}
		return socketPath, nil
	}

	// Try to discover from current directory
	cwd, err := os.Getwd()
	if err != nil {
		return "", ErrInvalidSocketPath
	}

	return DiscoverSocketPath(cwd)
}

// ComputeSocketPath computes the socket path for a workspace without checking if daemon is running.
// This is useful for connecting to a daemon that may start lazily.
func ComputeSocketPath(workspacePath string) (string, error) {
	if workspacePath == "" {
		return "", ErrInvalidSocketPath
	}

	// Use the RPC package's ShortSocketPath which handles macOS path length limits
	socketPath := rpc.ShortSocketPath(workspacePath)
	return socketPath, nil
}

// FindBeadsDir locates the .beads directory for a workspace.
// For worktrees, this returns the main repository's .beads directory.
func FindBeadsDir(workspacePath string) (string, error) {
	if workspacePath == "" {
		return "", ErrInvalidSocketPath
	}

	// Walk up the directory tree looking for .beads
	current := workspacePath
	for {
		beadsDir := filepath.Join(current, ".beads")
		if info, err := os.Stat(beadsDir); err == nil && info.IsDir() {
			return beadsDir, nil
		}

		parent := filepath.Dir(current)
		if parent == current {
			// Reached root without finding .beads
			return "", ErrInvalidSocketPath
		}
		current = parent
	}
}
