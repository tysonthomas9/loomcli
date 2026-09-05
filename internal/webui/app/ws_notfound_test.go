package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// setupWorkspaceTestRoutes builds a Server with a non-nil multiPool so that
// registerWorkspaceRoutes actually runs. The existing catch-all test uses
// &Server{} with a nil multiPool, which is precisely why the workspace sub-mux
// never got exercised and its text/plain 404 went unnoticed.
func setupWorkspaceTestRoutes(t *testing.T) *Server {
	t.Helper()
	app := &Server{}
	app.mux = http.NewServeMux()
	app.multiPool = daemon.NewMultiPool(middleware.WorkspaceFromContext, 1)
	t.Cleanup(func() { _ = app.multiPool.Close() })
	app.buildHandlers()
	app.buildModules()
	app.wsExistsFn = func(string) bool { return true }
	app.registerRoutes()
	t.Cleanup(func() {
		if app.handlers != nil {
			if app.handlers.ClientErrLimiter != nil {
				app.handlers.ClientErrLimiter.Stop()
			}
			if app.handlers.AuthCfgLimiter != nil {
				app.handlers.AuthCfgLimiter.Stop()
			}
		}
	})
	return app
}

func decodeJSONError(t *testing.T, rr *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v (body=%q)", err, rr.Body.String())
	}
	return body
}

// TestUnknownWorkspacePathReturnsJSONNotFound covers the reported bug: once the
// outer mux dispatches into the workspace sub-mux, an unmatched path used to be
// answered by Go's built-in "404 page not found" in text/plain, which no JSON
// client can decode. The workspace exists (wsExistsFn returns true), so these
// requests reach the sub-mux rather than being rejected by workspace
// middleware.
func TestUnknownWorkspacePathReturnsJSONNotFound(t *testing.T) {
	app := setupWorkspaceTestRoutes(t)

	paths := []string{
		"/api/workspaces/WS/made-up-sub",
		"/api/workspaces/WS/issues/X/made-up",
		"/api/workspaces/WS/terminal/sessions",
	}
	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			rr := httptest.NewRecorder()
			app.mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, p, nil))

			if rr.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404 (body=%q)", rr.Code, rr.Body.String())
			}
			if got := decodeJSONError(t, rr)["error"]; got != "not found" {
				t.Errorf("error = %q, want %q", got, "not found")
			}
		})
	}
}

// TestWorkspaceMethodMismatchReturnsJSON405 is the guard against the one-line
// wsMux.Handle("/", ...) fix: registering a bare catch-all pattern makes Go's
// mux always match a node, which stops it synthesizing 405 and silently turns
// every wrong-method workspace request into a 404.
func TestWorkspaceMethodMismatchReturnsJSON405(t *testing.T) {
	app := setupWorkspaceTestRoutes(t)

	rr := httptest.NewRecorder()
	app.mux.ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/api/workspaces/WS/stats", nil))

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405 (body=%q)", rr.Code, rr.Body.String())
	}
	if allow := rr.Header().Get("Allow"); !strings.Contains(allow, http.MethodGet) {
		t.Errorf("Allow = %q, want it to list GET", allow)
	}
	if got := decodeJSONError(t, rr)["error"]; got != "method not allowed" {
		t.Errorf("error = %q, want %q", got, "method not allowed")
	}
}

// TestWorkspaceRegisteredRoutesStillReachable verifies the fallback only fires
// on unmatched paths — /readyz bypasses workspace middleware but still goes
// through the wrapped sub-mux, and must not turn into a 404.
func TestWorkspaceRegisteredRoutesStillReachable(t *testing.T) {
	app := setupWorkspaceTestRoutes(t)

	for _, p := range []string{"/api/workspaces/WS/readyz", "/api/workspaces/WS/stats"} {
		t.Run(p, func(t *testing.T) {
			rr := httptest.NewRecorder()
			app.mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, p, nil))
			if rr.Code == http.StatusNotFound {
				t.Errorf("status = 404 for registered route %s (body=%q)", p, rr.Body.String())
			}
		})
	}
}
