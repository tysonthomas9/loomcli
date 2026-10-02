package middleware

import (
	"net/http"
	"testing"
)

// TestAgentSSERouteIsPublicOnlyForGet: the Agent API event stream skips the
// JWT check (it takes a one-time token instead); its other methods and the
// REST routes do not.
func TestAgentSSERouteIsPublicOnlyForGet(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		public       bool
	}{
		{http.MethodGet, "/api/workspaces/ws/v1/events", true},
		{http.MethodPost, "/api/workspaces/ws/v1/events", false},
		{http.MethodGet, "/api/workspaces/ws/v1/agents", false},
		{http.MethodGet, "/api/workspaces/ws/v1/agents/a1/events", false},
	} {
		if got := isPublicRoute(tc.method, tc.path); got != tc.public {
			t.Errorf("%s %s public = %v; want %v", tc.method, tc.path, got, tc.public)
		}
	}
}

// TestBridgeIdentityAuthBypass: only an Agent API path carrying a bridge
// token skips the user-JWT check; agentsv1 then verifies the token itself.
func TestBridgeIdentityAuthBypass(t *testing.T) {
	for _, tc := range []struct {
		path, token string
		want        bool
	}{
		{"/api/workspaces/ws/v1/agents", BridgeTokenPrefix + "x.y", true},
		{"/api/workspaces/ws/v1/agents/a1/messages", BridgeTokenPrefix + "x.y", true},
		{"/api/workspaces/ws/v1/agents", "eyJhbGciOi.jwt", false},
		{"/api/workspaces/ws/issues", BridgeTokenPrefix + "x.y", false},
		{"/api/v1/agents", BridgeTokenPrefix + "x.y", false},
		{"/api/workspaces/ws/fleet/v1/agents", BridgeTokenPrefix + "x.y", false},
	} {
		if got := isBridgeCall(tc.path, tc.token); got != tc.want {
			t.Errorf("isBridgeCall(%q, %q) = %v; want %v", tc.path, tc.token, got, tc.want)
		}
	}
}
