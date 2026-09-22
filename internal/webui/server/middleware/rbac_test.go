package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWorkspaceRBACOpenModePassthroughWithoutIdentity(t *testing.T) {
	rr := serveWorkspaceRBACTest(t, WorkspaceRBACConfig{Enabled: false}, http.MethodPost, "/api/workspaces/WS/issues", nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusNoContent, rr.Body.String())
	}
}

func TestWorkspaceRBACRequiresIdentityInOIDCMode(t *testing.T) {
	rr := serveWorkspaceRBACTest(t, WorkspaceRBACConfig{Enabled: true}, http.MethodGet, "/api/workspaces/WS/issues", nil)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusUnauthorized, rr.Body.String())
	}
}

func TestWorkspaceRBACViewerCanReadButCannotWrite(t *testing.T) {
	identity := &UserIdentity{UserID: "user-1", Role: "viewer"}

	read := serveWorkspaceRBACTest(t, WorkspaceRBACConfig{Enabled: true}, http.MethodGet, "/api/workspaces/WS/issues", identity)
	if read.Code != http.StatusNoContent {
		t.Fatalf("GET status = %d, want %d; body=%s", read.Code, http.StatusNoContent, read.Body.String())
	}

	write := serveWorkspaceRBACTest(t, WorkspaceRBACConfig{Enabled: true}, http.MethodPost, "/api/workspaces/WS/issues", identity)
	if write.Code != http.StatusForbidden {
		t.Fatalf("POST status = %d, want %d; body=%s", write.Code, http.StatusForbidden, write.Body.String())
	}
}

func TestWorkspaceRBACDeveloperCanWrite(t *testing.T) {
	identity := &UserIdentity{UserID: "user-1", Role: "developer"}

	rr := serveWorkspaceRBACTest(t, WorkspaceRBACConfig{Enabled: true}, http.MethodPost, "/api/workspaces/WS/issues", identity)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusNoContent, rr.Body.String())
	}
}

func TestWorkspaceRBACTerminalTokenRequiresWrite(t *testing.T) {
	viewer := &UserIdentity{UserID: "viewer-1", Role: "viewer"}
	rr := serveWorkspaceRBACTest(t, WorkspaceRBACConfig{Enabled: true}, http.MethodGet, "/api/workspaces/WS/terminal/token", viewer)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("viewer status = %d, want %d; body=%s", rr.Code, http.StatusForbidden, rr.Body.String())
	}

	developer := &UserIdentity{UserID: "dev-1", Role: "developer"}
	rr = serveWorkspaceRBACTest(t, WorkspaceRBACConfig{Enabled: true}, http.MethodGet, "/api/workspaces/WS/terminal/token", developer)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("developer status = %d, want %d; body=%s", rr.Code, http.StatusNoContent, rr.Body.String())
	}
}

func TestWorkspaceRBACPublicWorkspaceRouteBypassesIdentity(t *testing.T) {
	rr := serveWorkspaceRBACTest(t, WorkspaceRBACConfig{Enabled: true}, http.MethodGet, "/api/workspaces/WS/events", nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusNoContent, rr.Body.String())
	}
}

func TestWorkspaceRBACResolverOverridesJWTRole(t *testing.T) {
	var gotWorkspace, gotUser string
	cfg := WorkspaceRBACConfig{
		Enabled: true,
		ResolveRole: func(_ context.Context, workspaceID string, identity UserIdentity) (string, error) {
			gotWorkspace = workspaceID
			gotUser = identity.UserID
			return "viewer", nil
		},
	}

	identity := &UserIdentity{UserID: "user-1", Role: "admin"}
	rr := serveWorkspaceRBACTest(t, cfg, http.MethodPost, "/api/workspaces/WS/issues", identity)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusForbidden, rr.Body.String())
	}
	if gotWorkspace != "WS" || gotUser != "user-1" {
		t.Fatalf("resolver saw workspace=%q user=%q, want WS/user-1", gotWorkspace, gotUser)
	}
}

func TestWorkspaceRBACResolverFailureDenies(t *testing.T) {
	cfg := WorkspaceRBACConfig{
		Enabled: true,
		ResolveRole: func(context.Context, string, UserIdentity) (string, error) {
			return "", errors.New("role store unavailable")
		},
	}

	identity := &UserIdentity{UserID: "user-1", Role: "admin"}
	rr := serveWorkspaceRBACTest(t, cfg, http.MethodPost, "/api/workspaces/WS/issues", identity)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusForbidden, rr.Body.String())
	}
}

func serveWorkspaceRBACTest(t *testing.T, cfg WorkspaceRBACConfig, method, path string, identity *UserIdentity) *httptest.ResponseRecorder {
	t.Helper()

	handler := Workspace(func(id string) bool { return id == "WS" })(
		WorkspaceRBAC(cfg)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})),
	)

	req := httptest.NewRequest(method, path, nil)
	req.SetPathValue("ws", "WS")
	if identity != nil {
		req = req.WithContext(WithUserIdentity(req.Context(), *identity))
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}
