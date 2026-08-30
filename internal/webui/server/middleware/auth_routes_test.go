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
		{
			name:   "task phase log stream",
			method: http.MethodGet,
			path:   "/api/workspaces/ws-1/tasks/loomcli-123/logs/planning/stream",
			want:   true,
		},
		{
			name:   "normalized task phase log stream",
			method: http.MethodGet,
			path:   "/api/tasks/loomcli-123/logs/implementation/stream",
			want:   true,
		},
		{
			name:   "task phase log stream is GET only",
			method: http.MethodPost,
			path:   "/api/workspaces/ws-1/tasks/loomcli-123/logs/planning/stream",
			want:   false,
		},
		{
			name:   "task log phase archive still uses middleware auth",
			method: http.MethodGet,
			path:   "/api/workspaces/ws-1/tasks/loomcli-123/logs/planning",
			want:   false,
		},
		{
			name:   "task log list archive still uses middleware auth",
			method: http.MethodGet,
			path:   "/api/workspaces/ws-1/tasks/loomcli-123/logs",
			want:   false,
		},
		{
			name:   "decoded slash in task ID still reaches stream handler validation",
			method: http.MethodGet,
			path:   "/api/workspaces/ws-1/tasks/bad/id/logs/planning/stream",
			want:   true,
		},
		{
			name:   "wrong task stream route does not gain exemption",
			method: http.MethodGet,
			path:   "/api/workspaces/ws-1/tasks/loomcli-123/archive/planning/stream",
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
