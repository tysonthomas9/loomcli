package middleware

import (
	"net/http"
	"testing"
)

func TestIsPublicRoute(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		// Workspace-scoped auth paths must NOT be public.
		// These would be silently unauthenticated if the /api/auth/ check ran
		// after stripWorkspacePrefix, since stripping would turn them into /api/auth/*.
		{"workspace auth sign-in GET", http.MethodGet, "/api/workspaces/ws1/auth/sign-in", false},
		{"workspace auth sign-up POST", http.MethodPost, "/api/workspaces/ws1/auth/sign-up", false},
		{"workspace auth session GET", http.MethodGet, "/api/workspaces/ws1/auth/session", false},
		{"workspace auth callback GET", http.MethodGet, "/api/workspaces/ws1/auth/callback/github", false},

		// Flat /api/auth/* is the BetterAuth proxy — public.
		{"flat auth sign-in GET", http.MethodGet, "/api/auth/sign-in", true},
		{"flat auth sign-up POST", http.MethodPost, "/api/auth/sign-up", true},
		{"flat auth session GET", http.MethodGet, "/api/auth/session", true},

		// Workspace-scoped fleet routes are public via normalization (API key / JWT auth handled downstream).
		{"workspace fleet claim POST", http.MethodPost, "/api/workspaces/ws1/fleet/claim", true},
		{"workspace fleet heartbeat GET", http.MethodGet, "/api/workspaces/ws1/fleet/heartbeat", true},

		// Workspace-scoped internal worker routes are public via normalization (LOOM_WORKER_TOKEN auth).
		{"workspace internal worker POST", http.MethodPost, "/api/workspaces/ws1/internal/workers/foo", true},

		// Workspace-scoped terminal WS endpoints remain public (one-time token auth).
		{"workspace terminal ws", http.MethodGet, "/api/workspaces/ws1/terminal/ws", true},
		{"workspace agent terminal ws", http.MethodGet, "/api/workspaces/ws1/agents/foo/terminal/ws", true},

		// Workspace-scoped protected routes must NOT be public.
		{"workspace issues GET", http.MethodGet, "/api/workspaces/ws1/issues", false},
		{"workspace agent start POST", http.MethodPost, "/api/workspaces/ws1/agents/foo/start", false},
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
