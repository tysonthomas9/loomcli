package httpclient

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// tokenCacheEntry is the JSON format stored in ~/.loom/tokens/.
type tokenCacheEntry struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	ServerURL string    `json:"server_url"`
}

// expiryBuffer is subtracted from token expiry to avoid using tokens
// that are about to expire.
const expiryBuffer = 60 * time.Second

// cacheDir returns ~/.loom/tokens/, creating it with 0700 if needed.
// Respects LOOM_CONFIG_DIR env var for the base directory.
func cacheDir() (string, error) {
	base := os.Getenv("LOOM_CONFIG_DIR")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine home directory: %w", err)
		}
		base = filepath.Join(home, ".loom")
	}
	dir := filepath.Join(base, "tokens")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("cannot create token cache directory: %w", err)
	}
	return dir, nil
}

// cacheKey returns the sha256 hash of the server URL (filesystem-safe).
func cacheKey(serverURL string) string {
	h := sha256.Sum256([]byte(serverURL))
	return hex.EncodeToString(h[:])
}

// loadCachedToken reads a cached token for the given server URL.
// Returns ("", time.Time{}, nil) if no cached token exists, it's expired,
// or the cache directory is inaccessible (treated as cache miss).
func loadCachedToken(serverURL string) (string, time.Time, error) {
	dir, err := cacheDir()
	if err != nil {
		// Cache directory inaccessible — treat as cache miss, not fatal error.
		return "", time.Time{}, nil
	}
	path := filepath.Join(dir, cacheKey(serverURL)+".json")

	data, err := os.ReadFile(path) //nolint:gosec // G304: path derived from fixed cache dir + sha256 hash, not raw user input
	if os.IsNotExist(err) {
		return "", time.Time{}, nil
	}
	if err != nil {
		return "", time.Time{}, fmt.Errorf("cannot read token cache: %w", err)
	}

	var entry tokenCacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		// Corrupted cache file — treat as cache miss and clean up.
		_ = os.Remove(path)
		return "", time.Time{}, nil
	}

	// Check expiry with buffer.
	if time.Now().Add(expiryBuffer).After(entry.ExpiresAt) {
		return "", time.Time{}, nil
	}

	return entry.Token, entry.ExpiresAt, nil
}

// saveCachedToken writes a token to the cache with its expiry.
func saveCachedToken(serverURL, token string, expiresAt time.Time) error {
	dir, err := cacheDir()
	if err != nil {
		return err
	}

	entry := tokenCacheEntry{
		Token:     token,
		ExpiresAt: expiresAt,
		ServerURL: serverURL,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("cannot marshal token cache entry: %w", err)
	}

	path := filepath.Join(dir, cacheKey(serverURL)+".json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("cannot write token cache: %w", err)
	}
	return nil
}

// clearCachedToken removes the cached token for the given server URL.
func clearCachedToken(serverURL string) error {
	dir, err := cacheDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, cacheKey(serverURL)+".json")
	err = os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
