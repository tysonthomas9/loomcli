package middleware

import (
	"net/http"
	"testing"
)

func TestIsPublicRoute_OwnAuthStreams(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		{
			name:   "workspace events",
			method: http.MethodGet,
			path:   "/api/workspaces/ws-1/events",
			want:   true,
		},
		{
			name:   "agent terminal websocket",
			method: http.MethodGet,
			path:   "/api/workspaces/ws-1/agents/ember/terminal/ws",
			want:   true,
		},
		{
			name:   "agent log stream",
			method: http.MethodGet,
			path:   "/api/workspaces/ws-1/agents/ember/logs/stream",
			want:   true,
		},
		{
			name:   "normalized agent log stream",
			method: http.MethodGet,
			path:   "/api/agents/ember/logs/stream",
			want:   true,
		},
		{
			name:   "agent log stream is GET only",
			method: http.MethodPost,
			path:   "/api/workspaces/ws-1/agents/ember/logs/stream",
			want:   false,
		},
		{
			name:   "agent log archive still uses middleware auth",
			method: http.MethodGet,
			path:   "/api/workspaces/ws-1/agents/ember/logs",
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPublicRoute(tt.method, tt.path); got != tt.want {
				t.Fatalf("isPublicRoute(%q, %q) = %v, want %v", tt.method, tt.path, got, tt.want)
			}
		})
	}
}
