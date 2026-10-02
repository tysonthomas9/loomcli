package middleware

import (
	"net/http"
	"net/http/httptest"
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

// TestBridgeIdentityAuthBypass: in JWT mode a bridge token skips the user
// check only on a clean /api/workspaces/{ws}/v1/… path, where agentsv1
// verifies it. Every other route, including the one-time token exchange,
// dot segments, double slashes and encoded slashes, still needs a JWT.
func TestBridgeIdentityAuthBypass(t *testing.T) {
	cache := NewJWKSCacheNoFetch("http://127.0.0.1:1/jwks", nil, nil)
	reached := false
	h := Auth(AuthConfig{JWKSCache: cache, Issuer: "i", Audience: "a"})(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	for _, tc := range []struct {
		method, target, token string
		want                  bool
	}{
		{"GET", "/api/workspaces/ws/v1/agents", BridgeTokenPrefix + "x.y", true},
		{"POST", "/api/workspaces/ws/v1/agents/a1/messages", BridgeTokenPrefix + "x.y", true},
		{"GET", "/api/workspaces/ws/v1/agents", "eyJhbGciOi.jwt.sig", false},
		{"GET", "/api/workspaces/ws/events/token", BridgeTokenPrefix + "x.y", false},
		{"GET", "/api/workspaces/ws/issues", BridgeTokenPrefix + "x.y", false},
		{"GET", "/api/v1/agents", BridgeTokenPrefix + "x.y", false},
		{"GET", "/api/workspaces/ws/agents/v1/x", BridgeTokenPrefix + "x.y", false},
		{"GET", "/api/workspaces/x/api/v1/../", BridgeTokenPrefix + "x.y", false},
		{"GET", "/api/workspaces/ws/v1/../issues", BridgeTokenPrefix + "x.y", false},
		{"GET", "/api/workspaces/ws/v1/./agents", BridgeTokenPrefix + "x.y", false},
		{"GET", "/api/workspaces//v1/agents", BridgeTokenPrefix + "x.y", false},
		{"GET", "/api/workspaces/ws//v1/agents", BridgeTokenPrefix + "x.y", false},
		{"GET", "/api/workspaces/ws/v1//agents", BridgeTokenPrefix + "x.y", false},
		{"GET", "/api/workspaces/ws%2Fv1/agents", BridgeTokenPrefix + "x.y", false},
		{"GET", "/api/workspaces/ws/v1%2Fagents", BridgeTokenPrefix + "x.y", false},
		{"GET", "/api/workspaces/ws/v1/agents%2F..%2F..%2Fissues", BridgeTokenPrefix + "x.y", false},
	} {
		reached = false
		r := httptest.NewRequest(tc.method, tc.target, nil)
		r.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if reached != tc.want || (!tc.want && w.Code != http.StatusUnauthorized) {
			t.Errorf("%s %s with %q: reached=%v status=%d; want reached=%v", tc.method, tc.target, tc.token,
				reached, w.Code, tc.want)
		}
	}
}
