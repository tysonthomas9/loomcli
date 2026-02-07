//go:build windows

package rpc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	winio "github.com/Microsoft/go-winio"
)

type endpointInfo struct {
	Network string `json:"network"`
	Address string `json:"address"`
}

func listenRPC(socketPath string) (net.Listener, error) {
	pipePath := pipeName(socketPath)
	sddl := currentUserSDDL()

	listener, err := winio.ListenPipe(pipePath, &winio.PipeConfig{
		SecurityDescriptor: sddl,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create named pipe %s: %w", pipePath, err)
	}

	info := endpointInfo{
		Network: "pipe",
		Address: pipePath,
	}

	data, err := json.Marshal(info)
	if err != nil {
		listener.Close()
		return nil, fmt.Errorf("failed to marshal endpoint info: %w", err)
	}

	if err := os.WriteFile(socketPath, data, 0o600); err != nil {
		listener.Close()
		_ = os.Remove(socketPath) // Clean up partial file
		return nil, err
	}

	return listener, nil
}

func dialRPC(socketPath string, timeout time.Duration) (net.Conn, error) {
	data, err := os.ReadFile(socketPath)
	if err != nil {
		return nil, err
	}

	var info endpointInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, err
	}

	if info.Address == "" {
		return nil, errors.New("invalid RPC endpoint: missing address")
	}

	switch info.Network {
	case "pipe":
		return winio.DialPipe(info.Address, &timeout)
	case "tcp", "":
		// Backward compatibility: connect to old TCP-based daemons
		network := info.Network
		if network == "" {
			network = "tcp"
		}
		return net.DialTimeout(network, info.Address, timeout)
	default:
		return nil, fmt.Errorf("unsupported RPC network type: %q", info.Network)
	}
}

func endpointExists(socketPath string) bool {
	_, err := os.Stat(socketPath)
	return err == nil
}

// pipeName derives a deterministic Named Pipe path from the socket path.
// The workspace path is extracted from socketPath (parent of .beads directory),
// canonicalized, and hashed to produce a unique pipe name.
func pipeName(socketPath string) string {
	// socketPath is like /workspace/.beads/bd.sock
	// workspace is two directories up
	workspacePath := filepath.Dir(filepath.Dir(socketPath))

	canonical := normalizeWorkspacePath(workspacePath)
	hash := sha256.Sum256([]byte(canonical))
	hashHex := hex.EncodeToString(hash[:8]) // 8 bytes = 16 hex chars

	return `\\.\pipe\beads-` + hashHex
}

// normalizeWorkspacePath produces a canonical form of the workspace path
// for deterministic pipe name generation.
func normalizeWorkspacePath(workspacePath string) string {
	// Resolve to absolute path
	abs, err := filepath.Abs(workspacePath)
	if err != nil {
		abs = workspacePath
	}

	// Resolve symlinks
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		resolved = abs
	}

	// Lowercase for case-insensitive Windows filesystem
	return strings.ToLower(resolved)
}

// currentUserSDDL returns an SDDL security descriptor that restricts
// pipe access to the current user only.
func currentUserSDDL() string {
	u, err := user.Current()
	if err != nil {
		// Fallback: Creator Owner - still restrictive but less precise
		return "D:P(A;;GA;;;CO)"
	}

	// On Windows, user.Current().Uid returns the SID string (e.g., S-1-5-21-...)
	return fmt.Sprintf("D:P(A;;GA;;;%s)", u.Uid)
}
