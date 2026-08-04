package rpc

import (
	"os"
	"path/filepath"
	"strings"
)

// tokenFileName is the name of the auth token file stored alongside the socket.
const tokenFileName = "rpc-token"

// tokenFilePath returns the path to the auth token file for a given socket path.
func tokenFilePath(socketPath string) string {
	return filepath.Join(filepath.Dir(socketPath), tokenFileName)
}

// loadAuthToken reads the auth token from the token file next to the socket.
// Returns empty string if the file doesn't exist or can't be read.
func loadAuthToken(socketPath string) string {
	path := tokenFilePath(socketPath)
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
