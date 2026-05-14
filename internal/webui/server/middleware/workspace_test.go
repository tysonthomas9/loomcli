package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWorkspaceResolvedInjectsCanonicalWorkspaceRef(t *testing.T) {
	mw := WorkspaceResolved(func(_ context.Context, requestedID string) (WorkspaceRef, bool) {
		if requestedID != "alias-ws" {
			t.Fatalf("requested workspace = %q, want alias-ws", requestedID)
		}
		return WorkspaceRef{RequestedID: requestedID, CanonicalID: "canonical-ws"}, true
	})

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := WorkspaceFromContext(r.Context()); got != "canonical-ws" {
			t.Errorf("WorkspaceFromContext = %q, want canonical-ws", got)
		}
		ref := WorkspaceRefFromContext(r.Context())
		if ref.RequestedID != "alias-ws" {
			t.Errorf("WorkspaceRef.RequestedID = %q, want alias-ws", ref.RequestedID)
		}
		if ref.CanonicalID != "canonical-ws" {
			t.Errorf("WorkspaceRef.CanonicalID = %q, want canonical-ws", ref.CanonicalID)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/alias-ws/issues", nil)
	req.SetPathValue("ws", "alias-ws")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusNoContent, rr.Body.String())
	}
}

func TestWorkspaceLegacyMiddlewareInjectsIdentityWorkspaceRef(t *testing.T) {
	mw := Workspace(func(id string) bool {
		return id == "identity-ws"
	})

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := WorkspaceFromContext(r.Context()); got != "identity-ws" {
			t.Errorf("WorkspaceFromContext = %q, want identity-ws", got)
		}
		ref := WorkspaceRefFromContext(r.Context())
		if ref.RequestedID != "identity-ws" || ref.CanonicalID != "identity-ws" {
			t.Errorf("WorkspaceRef = %+v, want identity workspace ref", ref)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/identity-ws/issues", nil)
	req.SetPathValue("ws", "identity-ws")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusNoContent, rr.Body.String())
	}
}
