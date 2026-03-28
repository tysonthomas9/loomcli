package webui

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestIsPublicRoute verifies classification of various paths as public or protected.
func TestIsPublicRoute(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		// Public GET routes
		{"GET /health", http.MethodGet, "/health", true},
		{"GET /api/health", http.MethodGet, "/api/health", true},
		{"GET / (frontend root)", http.MethodGet, "/", true},
		{"GET /assets/main.js (frontend asset)", http.MethodGet, "/assets/main.js", true},
		{"GET /issues/123 (SPA route)", http.MethodGet, "/issues/123", true},
		{"GET /favicon.ico", http.MethodGet, "/favicon.ico", true},

		// Terminal WS is public (uses its own one-time token auth in the handler)
		{"GET /api/terminal/ws", http.MethodGet, "/api/terminal/ws", true},

		// Protected API routes (require auth)
		{"GET /api/issues", http.MethodGet, "/api/issues", false},
		{"GET /api/stats", http.MethodGet, "/api/stats", false},
		{"GET /api/issues/123", http.MethodGet, "/api/issues/123", false},
		{"GET /api/events (SSE, own auth)", http.MethodGet, "/api/events", true},

		// Non-GET methods on public paths should not be public
		{"POST /health", http.MethodPost, "/health", false},
		{"POST /api/health", http.MethodPost, "/api/health", false},
		{"PUT /health", http.MethodPut, "/health", false},
		{"DELETE /api/health", http.MethodDelete, "/api/health", false},
		{"POST /", http.MethodPost, "/", false},

		// Non-GET on protected routes
		{"POST /api/issues", http.MethodPost, "/api/issues", false},
		{"PATCH /api/issues/123", http.MethodPatch, "/api/issues/123", false},
		{"DELETE /api/issues/123", http.MethodDelete, "/api/issues/123", false},

		// Fleet routes are public (they use their own auth: API key for register, JWT for claim/heartbeat)
		{"POST /api/fleet/register", http.MethodPost, "/api/fleet/register", true},
		{"POST /api/fleet/claim", http.MethodPost, "/api/fleet/claim", true},
		{"POST /api/fleet/heartbeat", http.MethodPost, "/api/fleet/heartbeat", true},
		{"POST /api/fleet/done/worker-1", http.MethodPost, "/api/fleet/done/worker-1", true},
		{"GET /api/fleet/register", http.MethodGet, "/api/fleet/register", true},

		// /api/config has no handler — not public
		{"GET /api/config (no handler)", http.MethodGet, "/api/config", false},

		// Session notification uses its own auth
		{"POST /api/sessions/notify", http.MethodPost, "/api/sessions/notify", true},

		// Worker API routes use their own LOOM_WORKER_TOKEN auth
		{"GET /api/internal/workers/test", http.MethodGet, "/api/internal/workers/test", true},
		{"POST /api/internal/workers/test/result", http.MethodPost, "/api/internal/workers/test/result", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isPublicRoute(tt.method, tt.path)
			if got != tt.want {
				t.Errorf("isPublicRoute(%q, %q) = %v, want %v", tt.method, tt.path, got, tt.want)
			}
		})
	}
}

// TestExtractToken_AuthorizationHeader verifies that the token is correctly
// extracted from a properly formed Authorization: Bearer header.
func TestExtractToken_AuthorizationHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.Header.Set("Authorization", "Bearer my-secret-token")

	token := extractToken(req)
	if token != "my-secret-token" {
		t.Errorf("extractToken() = %q, want %q", token, "my-secret-token")
	}
}

// TestExtractToken_QueryParameter verifies that the token is extracted from
// the "token" query parameter when no Authorization header is present.
func TestExtractToken_QueryParameter(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/terminal/ws?token=query-token-value", nil)

	token := extractToken(req)
	if token != "query-token-value" {
		t.Errorf("extractToken() = %q, want %q", token, "query-token-value")
	}
}

// TestExtractToken_MalformedAuthorizationHeader verifies that a malformed
// Authorization header (not starting with "Bearer ") returns empty string.
func TestExtractToken_MalformedAuthorizationHeader(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"Basic auth", "Basic dXNlcjpwYXNz"},
		{"no prefix", "my-token-without-bearer"},
		{"lowercase bearer", "bearer my-token"},
		{"empty bearer value", "Bearer"},
		{"just space", " "},
		{"token scheme", "Token abc123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
			req.Header.Set("Authorization", tt.value)

			token := extractToken(req)
			if token != "" {
				t.Errorf("extractToken() = %q, want empty string", token)
			}
		})
	}
}

// TestExtractToken_NoAuth verifies that extractToken returns an empty string
// when neither an Authorization header nor a token query parameter is present.
func TestExtractToken_NoAuth(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)

	token := extractToken(req)
	if token != "" {
		t.Errorf("extractToken() = %q, want empty string", token)
	}
}

// TestExtractToken_HeaderTakesPrecedenceOverQuery verifies that the
// Authorization header is preferred over the query parameter.
func TestExtractToken_HeaderTakesPrecedenceOverQuery(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/issues?token=query-token", nil)
	req.Header.Set("Authorization", "Bearer header-token")

	token := extractToken(req)
	if token != "header-token" {
		t.Errorf("extractToken() = %q, want %q", token, "header-token")
	}
}

// --- stripWorkspacePrefix coverage ---

func TestStripWorkspacePrefix_Various(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		// With workspace prefix
		{"workspace fleet claim", "/api/workspaces/my-ws/fleet/claim", "/api/fleet/claim"},
		{"workspace fleet register", "/api/workspaces/prod/fleet/register", "/api/fleet/register"},
		{"workspace nested path", "/api/workspaces/ws-1/issues/123/comments", "/api/issues/123/comments"},
		{"workspace health", "/api/workspaces/test-ws/health", "/api/health"},
		{"workspace auth token", "/api/workspaces/dev/auth/token", "/api/auth/token"},

		// Without workspace prefix (unchanged)
		{"global fleet claim", "/api/fleet/claim", "/api/fleet/claim"},
		{"global health", "/api/health", "/api/health"},
		{"global issues", "/api/issues", "/api/issues"},
		{"root health", "/health", "/health"},
		{"frontend root", "/", "/"},
		{"frontend asset", "/assets/main.js", "/assets/main.js"},

		// Edge cases
		{"empty path", "", ""},
		{"just api", "/api", "/api"},
		{"api workspaces no ws id", "/api/workspaces/", "/api/workspaces/"},
		{"workspace id only no trailing", "/api/workspaces/my-ws", "/api/workspaces/my-ws"},
		{"workspace with slash only", "/api/workspaces/my-ws/", "/api/"},
		{"workspace deeply nested", "/api/workspaces/ws/a/b/c/d", "/api/a/b/c/d"},
		{"partial prefix", "/api/workspace/foo/bar", "/api/workspace/foo/bar"},
		{"different api path", "/other/workspaces/ws/foo", "/other/workspaces/ws/foo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripWorkspacePrefix(tt.path)
			if got != tt.want {
				t.Errorf("stripWorkspacePrefix(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

// --- isPublicRoute with workspace-scoped paths ---

func TestIsPublicRoute_WorkspaceScoped(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		// Workspace-scoped fleet routes should be public
		{"ws fleet register POST", "POST", "/api/workspaces/ws1/fleet/register", true},
		{"ws fleet claim POST", "POST", "/api/workspaces/ws1/fleet/claim", true},
		{"ws fleet heartbeat POST", "POST", "/api/workspaces/prod/fleet/heartbeat", true},
		{"ws fleet GET", "GET", "/api/workspaces/ws1/fleet/status", true},

		// Workspace-scoped client-errors should be public
		{"ws client-errors POST", "POST", "/api/workspaces/ws1/client-errors", true},

		// Workspace-scoped csp-report should be public
		{"ws csp-report POST", "POST", "/api/workspaces/ws1/csp-report", true},

		// Workspace-scoped protected routes should remain protected
		{"ws issues GET", "GET", "/api/workspaces/ws1/issues", false},
		{"ws stats GET", "GET", "/api/workspaces/ws1/stats", false},

		// Workspace-scoped health should be public via GET
		{"ws health GET", "GET", "/api/workspaces/ws1/health", true},

		// Workspace-scoped terminal ws should be public
		{"ws terminal ws GET", "GET", "/api/workspaces/ws1/terminal/ws", true},

		// Workspace-scoped agent terminal ws
		{"ws agent terminal ws GET", "GET", "/api/workspaces/ws1/agents/a1/terminal/ws", true},

		// Workspace-scoped config and events should be public
		{"ws config GET", "GET", "/api/workspaces/ws1/config", false},
		{"ws events GET", "GET", "/api/workspaces/ws1/events", true},

		// SSE token exchange must NOT be public — requires JWT auth from ExtAuth middleware
		{"ws events token GET - requires auth", "GET", "/api/workspaces/ws1/events/token", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isPublicRoute(tt.method, tt.path)
			if got != tt.want {
				t.Errorf("isPublicRoute(%q, %q) = %v, want %v", tt.method, tt.path, got, tt.want)
			}
		})
	}
}
