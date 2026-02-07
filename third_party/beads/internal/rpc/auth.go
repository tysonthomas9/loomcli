package rpc

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// tokenFileName is the name of the auth token file stored alongside the socket.
const tokenFileName = "rpc-token"

// generateToken generates a 256-bit (32-byte) random token and returns it as a 64-character hex string.
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate random token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// tokenFilePath returns the path to the auth token file for a given socket path.
func tokenFilePath(socketPath string) string {
	return filepath.Join(filepath.Dir(socketPath), tokenFileName)
}

// writeTokenFile writes the token to the given path with 0600 permissions.
func writeTokenFile(path, token string) error {
	return os.WriteFile(path, []byte(token+"\n"), 0600)
}

// readTokenFile reads the token from the given path, trimming whitespace.
func readTokenFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// validateAuthToken checks the request's auth token against the server's token.
// Returns nil if auth is disabled (empty server token) or if the token matches.
// Uses constant-time comparison to prevent timing side-channels.
func validateAuthToken(serverToken string, req *Request) error {
	if serverToken == "" {
		// Auth disabled (BEADS_RPC_NO_AUTH=1)
		return nil
	}

	// Always use constant-time comparison, even for empty request tokens.
	// This prevents timing attacks that distinguish "missing token" from "wrong token".
	if subtle.ConstantTimeCompare([]byte(serverToken), []byte(req.AuthToken)) != 1 {
		if req.AuthToken == "" {
			return fmt.Errorf("authentication required: no auth token provided. Upgrade your bd CLI or set BEADS_RPC_NO_AUTH=1 on the daemon")
		}
		return fmt.Errorf("authentication failed: invalid auth token")
	}

	return nil
}
