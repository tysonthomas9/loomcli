package middleware

import (
	"net/http"
	"testing"
)

func TestOwnAuthRoutesArePublicAfterWorkspaceNormalization(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{"fleet", "/api/workspaces/WS/fleet/heartbeat"},
		{"internal workers", "/api/workspaces/WS/internal/workers/heartbeat"},
		{"webhooks", "/api/workspaces/WS/webhooks/github"},
		{"driver", "/api/workspaces/WS/driver/heartbeat"},
		{"lead", "/api/workspaces/WS/lead/heartbeat"},
		{"task run", "/api/workspaces/WS/task-run/heartbeat"},
		{"auth", "/api/workspaces/WS/auth/sign-in/email"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			normalized := stripWorkspacePrefix(tt.path)
			if !hasOwnAuthPrefix(normalized) {
				t.Fatalf("hasOwnAuthPrefix(%q) = false", normalized)
			}
			if !isPublicRoute(http.MethodPost, tt.path) {
				t.Fatalf("isPublicRoute(POST, %q) = false", tt.path)
			}
		})
	}
}
