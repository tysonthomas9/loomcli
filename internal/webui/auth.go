package webui

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

const apiKeyLength = 32 // 32 bytes = 64 hex characters

// GenerateAPIKey generates a cryptographically random API key as a hex string.
func GenerateAPIKey() (string, error) {
	b := make([]byte, apiKeyLength)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate random key: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// LoadOrCreateAPIKey reads the API key from the given file path, or generates
// a new one and saves it if the file doesn't exist. The file is created with
// 0600 permissions (owner read/write only).
func LoadOrCreateAPIKey(path string) (string, error) {
	// Try to read existing key
	data, err := os.ReadFile(path)
	if err == nil {
		key := strings.TrimSpace(string(data))
		if key != "" {
			return key, nil
		}
	}

	// Generate new key
	key, err := GenerateAPIKey()
	if err != nil {
		return "", err
	}

	// Ensure parent directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	// Write key to file
	if err := os.WriteFile(path, []byte(key+"\n"), 0600); err != nil {
		return "", fmt.Errorf("failed to write API key file: %w", err)
	}

	return key, nil
}

// DefaultAPIKeyPath returns the default path for the WebUI API key file.
func DefaultAPIKeyPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Printf("Warning: cannot determine home directory: %v", err)
		return ""
	}
	return filepath.Join(home, ".loom", "webui-api-key")
}
