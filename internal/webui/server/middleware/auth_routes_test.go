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
		{"workspace auth sign-in GET", http.MethodGet, "/api/workspaces/ws1/auth/sign-in", false},
		{"workspace auth sign-up POST", http.MethodPost, "/api/workspaces/ws1/auth/sign-up", false},
		{"workspace auth session GET", http.MethodGet, "/api/workspaces/ws1/auth/session", false},
		{"workspace auth callback GET", http.MethodGet, "/api/workspaces/ws1/auth/callback/github", false},

		{"flat auth sign-in GET", http.MethodGet, "/api/auth/sign-in", true},
		{"flat auth sign-up POST", http.MethodPost, "/api/auth/sign-up", true},
		{"flat auth session GET", http.MethodGet, "/api/auth/session", true},

		{"workspace fleet claim POST", http.MethodPost, "/api/workspaces/ws1/fleet/claim", true},
		{"workspace fleet heartbeat GET", http.MethodGet, "/api/workspaces/ws1/fleet/heartbeat", true},
		{"workspace internal worker POST", http.MethodPost, "/api/workspaces/ws1/internal/workers/foo", true},
		{"workspace terminal ws", http.MethodGet, "/api/workspaces/ws1/terminal/ws", true},
		{"workspace agent terminal ws", http.MethodGet, "/api/workspaces/ws1/agents/foo/terminal/ws", true},

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
