package webui

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
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
	APIKey  string `json:"-"` // Pre-shared API key for bearer token auth
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
				respondError(w, http.StatusUnauthorized, "authentication required")
				return
			}

			// Constant-time comparison to prevent timing attacks
			if subtle.ConstantTimeCompare([]byte(token), keyBytes) != 1 {
				respondError(w, http.StatusUnauthorized, "invalid token")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// isPublicRoute returns true if the given method+path combination should be
// accessible without authentication.
func isPublicRoute(method, path string) bool {
	// Normalize workspace-scoped paths: strip /api/workspaces/{ws}/ prefix
	// so that workspace-scoped routes match the same patterns as global routes.
	// e.g. /api/workspaces/my-ws/fleet/... → /api/fleet/...
	normalizedPath := stripWorkspacePrefix(path)

	// Fleet endpoints use their own authentication (API key for register, JWT for claim/heartbeat)
	if strings.HasPrefix(normalizedPath, "/api/fleet/") {
		return true
	}

	// Client error reporting is public so errors during auth bootstrap are captured
	if method == http.MethodPost && normalizedPath == "/api/client-errors" {
		return true
	}

	// CSP violation reports are sent by browsers automatically without auth headers
	if method == http.MethodPost && normalizedPath == "/api/csp-report" {
		return true
	}

	if method != http.MethodGet {
		return false
	}

	switch {
	case normalizedPath == "/health":
		return true
	case normalizedPath == "/api/health":
		return true
	case normalizedPath == "/api/auth/token":
		return true
	case normalizedPath == "/api/terminal/ws":
		// Terminal WebSocket uses its own one-time token auth (validated in handler)
		return true
	case strings.HasPrefix(normalizedPath, "/api/agents/") && strings.HasSuffix(normalizedPath, "/terminal/ws"):
		// Agent terminal WebSocket uses one-time token auth (validated in handler)
		return true
	case !strings.HasPrefix(normalizedPath, "/api/"):
		// Frontend static files and SPA routes
		return true
	}

	return false
}

// stripWorkspacePrefix strips the /api/workspaces/{ws}/ prefix from a path,
// returning the equivalent global API path. If the path does not have a
// workspace prefix, it is returned unchanged.
// e.g. "/api/workspaces/my-ws/fleet/claim" → "/api/fleet/claim"
// e.g. "/api/fleet/claim" → "/api/fleet/claim"
func stripWorkspacePrefix(path string) string {
	const prefix = "/api/workspaces/"
	if !strings.HasPrefix(path, prefix) {
		return path
	}
	// Find the workspace ID segment and strip it
	rest := path[len(prefix):]
	idx := strings.Index(rest, "/")
	if idx < 0 {
		// Path is just /api/workspaces/{ws} with no trailing path
		return path
	}
	return "/api" + rest[idx:]
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

// handleAuthTokenDisabled returns a handler that explicitly responds with
// 404 JSON when auth is disabled. This prevents the SPA catch-all from
// returning 200 HTML, which the frontend would misparse as a JSON error.
func handleAuthTokenDisabled() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		respondError(w, http.StatusNotFound, "authentication not enabled")
	}
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
			respondError(w, http.StatusForbidden, "cross-origin requests not allowed")
			return
		}

		w.Header().Set("Cache-Control", "no-store")
		respondJSON(w, http.StatusOK, map[string]string{"token": apiKey})
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
