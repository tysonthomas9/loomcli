package middleware

import (
	"net/http"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// isPublicRoute returns true if the given method+path combination should be
// accessible without authentication.
func isPublicRoute(method, path string) bool {
	// Normalize workspace-scoped paths: strip /api/workspaces/{ws}/ prefix
	// so that workspace-scoped routes match the same patterns as global routes.
	// e.g. /api/workspaces/my-ws/fleet/... → /api/fleet/...
	normalizedPath := stripWorkspacePrefix(path)

	// Fleet endpoints use their own authentication (API key for register, JWT for claim/done/heartbeat)
	if strings.HasPrefix(normalizedPath, "/api/fleet/") {
		return true
	}

	// Worker API routes use their own LOOM_WORKER_TOKEN auth
	if strings.HasPrefix(normalizedPath, "/api/internal/workers/") {
		return true
	}

	// All auth routes are public — the BetterAuth service handles its own auth.
	// Must be above the GET-only gate because sign-in/sign-up use POST.
	if strings.HasPrefix(normalizedPath, "/api/auth/") {
		return true
	}

	// Client error reporting is public so errors during auth bootstrap are captured
	if method == http.MethodPost && normalizedPath == "/api/client-errors" {
		return true
	}

	// Session notifications use their own auth mechanism
	if method == http.MethodPost && normalizedPath == sessions.NotifyPath {
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
	case normalizedPath == "/metrics":
		return true
	case normalizedPath == "/api/config":
		// Auth discovery endpoint must be accessible without JWT (bootstrap)
		return true
	case normalizedPath == "/api/events":
		// Workspace-scoped SSE endpoint (/api/workspaces/{ws}/events) uses its own auth
		// (sseAuth middleware or token exchange). Matched via stripWorkspacePrefix normalization.
		return true
	case normalizedPath == "/api/terminal/ws":
		// Terminal WebSocket uses its own one-time token auth (validated in handler)
		return true
	case strings.HasPrefix(normalizedPath, "/api/agents/") && strings.HasSuffix(normalizedPath, "/terminal/ws"):
		// Workspace-scoped agent terminal WebSocket (/api/workspaces/{ws}/agents/{name}/terminal/ws)
		// uses one-time token auth (validated in handler). Matched via stripWorkspacePrefix normalization.
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
