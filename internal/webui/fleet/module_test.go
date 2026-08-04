package fleet

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Compile-time assertion: *Module implements the Register method.
var _ interface{ Register(*http.ServeMux) } = (*Module)(nil)

func TestFleetModule_RegisterRoutes(t *testing.T) {
	storeFn := func(_ string) (*Store, bool) { return nil, false }
	mod := NewModule(storeFn, nil, nil, nil, nil)

	mux := http.NewServeMux()
	mod.Register(mux)

	routes := []struct {
		method string
		path   string
	}{
		{"POST", "/api/workspaces/test-ws/fleet/register"},
		{"POST", "/api/workspaces/test-ws/fleet/claim"},
		{"POST", "/api/workspaces/test-ws/fleet/done/job1"},
		{"POST", "/api/workspaces/test-ws/fleet/heartbeat"},
	}

	for _, rt := range routes {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(rt.method, rt.path, nil)
		mux.ServeHTTP(rec, req)

		if rec.Code == http.StatusNotFound {
			t.Errorf("%s %s: got 404, route not registered", rt.method, rt.path)
		}
		if rec.Code == http.StatusMethodNotAllowed {
			t.Errorf("%s %s: got 405, wrong method registered", rt.method, rt.path)
		}
	}
}

func TestFleetModule_WithSigningKey(t *testing.T) {
	storeFn := func(_ string) (*Store, bool) { return nil, false }
	tokenCfg := &TokenConfig{SigningKey: []byte("test-signing-key")}
	mod := NewModule(storeFn, tokenCfg, nil, nil, nil)

	mux := http.NewServeMux()
	mod.Register(mux)

	// All routes should still be registered (with middleware wrapping)
	routes := []struct {
		method string
		path   string
	}{
		{"POST", "/api/workspaces/test-ws/fleet/register"},
		{"POST", "/api/workspaces/test-ws/fleet/claim"},
		{"POST", "/api/workspaces/test-ws/fleet/done/job1"},
		{"POST", "/api/workspaces/test-ws/fleet/heartbeat"},
	}

	for _, rt := range routes {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(rt.method, rt.path, nil)
		mux.ServeHTTP(rec, req)

		if rec.Code == http.StatusNotFound {
			t.Errorf("%s %s: got 404, route not registered", rt.method, rt.path)
		}
		if rec.Code == http.StatusMethodNotAllowed {
			t.Errorf("%s %s: got 405, wrong method registered", rt.method, rt.path)
		}
	}
}

func TestFleetModule_WrongMethod_Returns405(t *testing.T) {
	storeFn := func(_ string) (*Store, bool) { return nil, false }
	mod := NewModule(storeFn, nil, nil, nil, nil)

	mux := http.NewServeMux()
	mod.Register(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/workspaces/test-ws/fleet/claim", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET .../fleet/claim: expected 405, got %d", rec.Code)
	}
}

func TestFleetModule_WithSigningKey_RejectsUnauthenticated(t *testing.T) {
	storeFn := func(_ string) (*Store, bool) { return nil, false }
	tokenCfg := &TokenConfig{SigningKey: []byte("test-signing-key")}
	mod := NewModule(storeFn, tokenCfg, nil, nil, nil)

	mux := http.NewServeMux()
	mod.Register(mux)

	// Routes protected by FleetAuthMiddleware should reject requests without auth
	protectedRoutes := []struct {
		method string
		path   string
	}{
		{"POST", "/api/workspaces/test-ws/fleet/claim"},
		{"POST", "/api/workspaces/test-ws/fleet/done/job1"},
		{"POST", "/api/workspaces/test-ws/fleet/heartbeat"},
	}

	for _, rt := range protectedRoutes {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(rt.method, rt.path, nil)
		// No Authorization header — should be rejected by FleetAuthMiddleware
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: expected 401 (auth middleware), got %d", rt.method, rt.path, rec.Code)
		}
	}

	// Register route should NOT require auth (self-registration)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/workspaces/test-ws/fleet/register", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code == http.StatusUnauthorized {
		t.Error("POST .../fleet/register should not require auth, but got 401")
	}
}

func TestFleetModule_WithoutSigningKey_NoAuthRequired(t *testing.T) {
	storeFn := func(_ string) (*Store, bool) { return nil, false }
	mod := NewModule(storeFn, nil, nil, nil, nil)

	mux := http.NewServeMux()
	mod.Register(mux)

	// Without signing key, claim/done/heartbeat should not return 401
	routes := []struct {
		method string
		path   string
	}{
		{"POST", "/api/workspaces/test-ws/fleet/claim"},
		{"POST", "/api/workspaces/test-ws/fleet/done/job1"},
		{"POST", "/api/workspaces/test-ws/fleet/heartbeat"},
	}

	for _, rt := range routes {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(rt.method, rt.path, nil)
		mux.ServeHTTP(rec, req)

		if rec.Code == http.StatusUnauthorized {
			t.Errorf("%s %s: got 401 without signing key — auth middleware should not be applied", rt.method, rt.path)
		}
	}
}
