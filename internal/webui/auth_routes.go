package webui

import (
	"net/http"
	"strings"
)

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
