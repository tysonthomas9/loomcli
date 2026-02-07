package webui

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const apiKeyLength = 32 // 32 bytes = 64 hex characters

// AuthConfig holds configuration for the authentication middleware.
type AuthConfig struct {
	APIKey  string // Pre-shared API key for bearer token auth
	Enabled bool   // Whether authentication is enabled
}

// NewAuthMiddleware creates a middleware that enforces bearer token authentication
// on protected routes. Public routes (health checks, frontend assets, auth token
// bootstrap) pass through without auth. WebSocket upgrade requests accept the
// token via query parameter since browsers cannot set custom headers on WebSocket
// connections.
func NewAuthMiddleware(config AuthConfig) func(http.Handler) http.Handler {
	if !config.Enabled {
		return func(next http.Handler) http.Handler {
			return next
		}
	}

	keyBytes := []byte(config.APIKey)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// OPTIONS requests pass through for CORS preflight
			if r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}

			// Public routes don't require auth
			if isPublicRoute(r.Method, r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			// Extract and validate token
			token := extractToken(r)
			if token == "" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]string{
					"error": "authentication required",
				})
				return
			}

			// Constant-time comparison to prevent timing attacks
			if subtle.ConstantTimeCompare([]byte(token), keyBytes) != 1 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]string{
					"error": "invalid token",
				})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// isPublicRoute returns true if the given method+path combination should be
// accessible without authentication.
func isPublicRoute(method, path string) bool {
	if method != http.MethodGet {
		return false
	}

	switch {
	case path == "/health":
		return true
	case path == "/api/health":
		return true
	case path == "/api/auth/token":
		return true
	case !strings.HasPrefix(path, "/api/"):
		// Frontend static files and SPA routes
		return true
	}

	return false
}

// extractToken extracts the bearer token from the request. It checks the
// Authorization header first, then falls back to the "token" query parameter
// for WebSocket and SSE connections that can't set custom headers.
func extractToken(r *http.Request) string {
	// Check Authorization header
	auth := r.Header.Get("Authorization")
	if auth != "" {
		const prefix = "Bearer "
		if strings.HasPrefix(auth, prefix) {
			return auth[len(prefix):]
		}
		return ""
	}

	// Fallback to query parameter (for WebSocket and EventSource)
	return r.URL.Query().Get("token")
}

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

// handleAuthToken returns a handler that serves the API key to same-origin
// requests. This allows the frontend (served from the same origin) to
// bootstrap authentication without the user manually copying the key.
func handleAuthToken(apiKey string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Check Sec-Fetch-Site header to ensure same-origin request.
		// Non-browser clients (curl, etc.) won't send this header — allow
		// those through since they need the key to call any other endpoint.
		secFetchSite := r.Header.Get("Sec-Fetch-Site")
		if secFetchSite != "" && secFetchSite != "same-origin" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "cross-origin requests not allowed",
			})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(map[string]string{
			"token": apiKey,
		})
	}
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
