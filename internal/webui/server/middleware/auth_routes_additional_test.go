package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/sessions"
)

func TestPublicRouteMatrixAndWorkspacePrefixNormalization(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		{"fleet", http.MethodPost, "/api/fleet/register", true},
		{"worker", http.MethodPost, "/api/internal/workers/claim", true},
		{"auth", http.MethodPost, "/api/auth/sign-in", true},
		{"client errors", http.MethodPost, "/api/client-errors", true},
		{"session notify", http.MethodPost, sessions.NotifyPath, true},
		{"health", http.MethodGet, "/health", true},
		{"api health", http.MethodGet, "/api/health", true},
		{"metrics", http.MethodGet, "/metrics", true},
		{"config", http.MethodGet, "/api/config", true},
		{"events", http.MethodGet, "/api/workspaces/ws-1/events", true},
		{"terminal ws", http.MethodGet, "/api/terminal/ws", true},
		{"agent terminal ws", http.MethodGet, "/api/workspaces/ws-1/agents/nova/terminal/ws", true},
		{"spa", http.MethodGet, "/workspaces/ws-1", true},
		{"protected post", http.MethodPost, "/api/issues", false},
		{"protected api get", http.MethodGet, "/api/issues", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPublicRoute(tt.method, tt.path); got != tt.want {
				t.Fatalf("isPublicRoute(%s, %q) = %v, want %v", tt.method, tt.path, got, tt.want)
			}
		})
	}

	if got := stripWorkspacePrefix("/api/workspaces/ws-1/fleet/claim"); got != "/api/fleet/claim" {
		t.Fatalf("workspace prefix stripped to %q", got)
	}
	if got := stripWorkspacePrefix("/api/workspaces/ws-1"); got != "/api/workspaces/ws-1" {
		t.Fatalf("workspace root stripped to %q", got)
	}
	if got := stripWorkspacePrefix("/api/fleet/claim"); got != "/api/fleet/claim" {
		t.Fatalf("non-workspace path changed to %q", got)
	}
}

func TestWorkspaceContextHelpersAndMiddlewareBranches(t *testing.T) {
	base := context.Background()
	if got := WorkspaceFromContext(base); got != "" {
		t.Fatalf("empty context workspace = %q", got)
	}
	if got := WorkspaceRefFromContext(WithWorkspace(base, "ws-1")); got != (WorkspaceRef{RequestedID: "ws-1", CanonicalID: "ws-1"}) {
		t.Fatalf("WithWorkspace ref = %#v", got)
	}
	if got := WorkspaceRefFromContext(WithWorkspaceRef(base, WorkspaceRef{RequestedID: "friendly", CanonicalID: "WS"})); got.CanonicalID != "WS" || got.RequestedID != "friendly" {
		t.Fatalf("WithWorkspaceRef = %#v", got)
	}
	if got := WorkspaceRefFromContext(WithWorkspaceRef(base, WorkspaceRef{RequestedID: "fallback"})); got.CanonicalID != "fallback" {
		t.Fatalf("canonical fallback = %#v", got)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ref := WorkspaceRefFromContext(r.Context())
		_ = json.NewEncoder(w).Encode(ref)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/friendly/issues", nil)
	req.SetPathValue("ws", "friendly")
	rr := httptest.NewRecorder()
	WorkspaceResolved(func(_ context.Context, requestedID string) (WorkspaceRef, bool) {
		return WorkspaceRef{RequestedID: requestedID, CanonicalID: "WS"}, true
	})(next).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"CanonicalID":"WS"`) {
		t.Fatalf("resolved response = %d %s", rr.Code, rr.Body.String())
	}

	missingParam := httptest.NewRecorder()
	WorkspaceResolved(nil)(next).ServeHTTP(missingParam, httptest.NewRequest(http.MethodGet, "/api/workspaces//issues", nil))
	if missingParam.Code != http.StatusBadRequest {
		t.Fatalf("missing workspace status = %d", missingParam.Code)
	}

	notFoundReq := httptest.NewRequest(http.MethodGet, "/api/workspaces/missing/issues", nil)
	notFoundReq.SetPathValue("ws", "missing")
	notFound := httptest.NewRecorder()
	Workspace(func(id string) bool { return false })(next).ServeHTTP(notFound, notFoundReq)
	if notFound.Code != http.StatusNotFound {
		t.Fatalf("not found status = %d", notFound.Code)
	}
}

func TestSecurityHeadersAndExtractOrigin(t *testing.T) {
	rr := httptest.NewRecorder()
	called := false
	SecurityHeaders(SecurityConfig{HSTSEnabled: true, ExtAuthOrigin: "https://auth.example.test"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if !called {
		t.Fatal("next handler was not called")
	}
	if csp := rr.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "https://auth.example.test") {
		t.Fatalf("CSP missing auth origin: %q", csp)
	}
	if got := rr.Header().Get("Strict-Transport-Security"); !strings.Contains(got, "includeSubDomains") {
		t.Fatalf("HSTS header = %q", got)
	}
	if got := ExtractOrigin("https://example.test/path?q=1"); got != "https://example.test" {
		t.Fatalf("ExtractOrigin = %q", got)
	}
	if got := ExtractOrigin("://bad"); got != "" {
		t.Fatalf("invalid origin = %q", got)
	}
}
