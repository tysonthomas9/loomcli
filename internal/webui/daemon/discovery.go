package daemon

import (
	"os"

	"github.com/tysonthomas9/loomcli/internal/rpc"
)

// ComputeSocketPath returns the daemon RPC socket path for a workspace.
func ComputeSocketPath(workspacePath string) (string, error) {
	if workspacePath == "" {
		return "", ErrInvalidSocketPath
	}
	return rpc.ShortSocketPath(workspacePath), nil
}

// DiscoverSocketPath returns the daemon RPC socket path if it currently exists.
func DiscoverSocketPath(workspacePath string) (string, error) {
	socketPath, err := ComputeSocketPath(workspacePath)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(socketPath); err != nil {
		if os.IsNotExist(err) {
			return "", ErrDaemonNotRunning
		}
		return "", err
	}
	return socketPath, nil
}
