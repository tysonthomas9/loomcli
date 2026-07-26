package middleware

import (
	"net/http"
	"testing"
)

// TestIsPublicRoute_FleetDBProxy pins that the fleet-db config proxy prefix
// (/api/v1/) bypasses serve's JWT auth for every method — the sandboxed caller
// authenticates to fleet-db with X-API-Key, not to serve. Regression guard for
// the 2C config proxy.
func TestIsPublicRoute_FleetDBProxy(t *testing.T) {
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete} {
		if !isPublicRoute(m, "/api/v1/E2E/issues") {
			t.Errorf("%s /api/v1/E2E/issues must be public (its own X-API-Key auth)", m)
		}
	}
	if !isPublicRoute(http.MethodGet, "/api/v1/E2E/workspace") {
		t.Error("GET /api/v1/E2E/workspace (scoped meta route) must be public")
	}
	if !isPublicRoute(http.MethodPost, "/api/v1/admin/apikeys") {
		t.Error("POST /api/v1/admin/apikeys must be public at serve (fleet-db RBAC still gates it)")
	}
	// A non-proxy POST API route must still require auth (sanity: the prefix
	// clause didn't accidentally open everything).
	if isPublicRoute(http.MethodPost, "/api/workspaces/E2E/issues") {
		t.Error("POST /api/workspaces/... must NOT be public")
	}
}
