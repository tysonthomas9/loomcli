package middleware

import (
	"net/http"
	"testing"
)

// Agent browser routes bypass the user-JWT gate because their handler
// requires the Loom agent-session bearer; the operator routes do not.
func TestBrowserRouteAuthExemptions(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		public       bool
	}{
		{http.MethodPost, "/api/agent/browsers", true},
		{http.MethodGet, "/api/agent/browsers/b1", true},
		{http.MethodPost, "/api/agent/browsers/b1/select", true},
		{http.MethodGet, "/api/agent/browsersX", false},
		{http.MethodGet, "/api/workspaces/ws/agents/lead/browsers", false},
		{http.MethodPost, "/api/workspaces/ws/agents/lead/browsers/b1/select", false},
	} {
		if got := isPublicRoute(tc.method, tc.path); got != tc.public {
			t.Errorf("%s %s public=%v, want %v", tc.method, tc.path, got, tc.public)
		}
	}
}
