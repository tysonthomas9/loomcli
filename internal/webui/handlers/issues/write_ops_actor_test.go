package issues

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// ===========================================================================
// X-Actor threading through HandleClaimIssue
// ===========================================================================

func TestHandleClaimIssue_ActorHeaderForwarded(t *testing.T) {
	var gotActor string
	svc := &mockIssueService{
		claimIssueFunc: func(_ context.Context, params service.ClaimIssueParams) (json.RawMessage, error) {
			gotActor = params.Actor
			return json.RawMessage(`{"id":"claim-1"}`), nil
		},
	}
	h := HandleClaimIssue(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws/issues/claim-1/claim", nil)
	req.SetPathValue("id", "claim-1")
	req.Header.Set("X-Actor", "worker-a")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", w.Code, http.StatusOK, w.Body.String())
	}
	if gotActor != "worker-a" {
		t.Errorf("params.Actor = %q, want worker-a", gotActor)
	}
}

func TestHandleClaimIssue_NoActorHeader_EmptyActor(t *testing.T) {
	called := false
	svc := &mockIssueService{
		claimIssueFunc: func(_ context.Context, params service.ClaimIssueParams) (json.RawMessage, error) {
			called = true
			if params.Actor != "" {
				t.Errorf("params.Actor = %q, want empty", params.Actor)
			}
			return json.RawMessage(`{"id":"claim-1"}`), nil
		},
	}
	h := HandleClaimIssue(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws/issues/claim-1/claim", nil)
	req.SetPathValue("id", "claim-1")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !called {
		t.Fatal("service was not called")
	}
}

func TestHandleClaimIssue_ActorHeaderTooLong_400(t *testing.T) {
	svc := &mockIssueService{
		claimIssueFunc: func(_ context.Context, _ service.ClaimIssueParams) (json.RawMessage, error) {
			t.Fatal("service should not be called for an invalid X-Actor")
			return nil, nil
		},
	}
	h := HandleClaimIssue(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws/issues/claim-1/claim", nil)
	req.SetPathValue("id", "claim-1")
	req.Header.Set("X-Actor", strings.Repeat("a", maxActorHeaderLen+1))
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleClaimIssue_ActorHeaderControlChars_400(t *testing.T) {
	svc := &mockIssueService{
		claimIssueFunc: func(_ context.Context, _ service.ClaimIssueParams) (json.RawMessage, error) {
			t.Fatal("service should not be called for an invalid X-Actor")
			return nil, nil
		},
	}
	h := HandleClaimIssue(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws/issues/claim-1/claim", nil)
	req.SetPathValue("id", "claim-1")
	req.Header.Set("X-Actor", "worker\x01a")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// ===========================================================================
// HandleReleaseIssue
// ===========================================================================

func TestHandleReleaseIssue_Success(t *testing.T) {
	var got service.ReleaseIssueParams
	svc := &mockIssueService{
		releaseIssueFunc: func(_ context.Context, params service.ReleaseIssueParams) error {
			got = params
			return nil
		},
	}
	h := HandleReleaseIssue(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws/issues/rel-1/release", nil)
	req.SetPathValue("id", "rel-1")
	req.Header.Set("X-Actor", "worker-a")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", w.Code, http.StatusOK, w.Body.String())
	}
	if got.IssueID != "rel-1" {
		t.Errorf("params.IssueID = %q, want rel-1", got.IssueID)
	}
	if got.Actor != "worker-a" {
		t.Errorf("params.Actor = %q, want worker-a", got.Actor)
	}
	var resp IssuesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success {
		t.Error("expected success=true")
	}
}

func TestHandleReleaseIssue_Conflict_409(t *testing.T) {
	svc := &mockIssueService{
		releaseIssueFunc: func(_ context.Context, _ service.ReleaseIssueParams) error {
			return service.ErrConflict("lock held by another worker")
		},
	}
	h := HandleReleaseIssue(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws/issues/rel-1/release", nil)
	req.SetPathValue("id", "rel-1")
	req.Header.Set("X-Actor", "worker-a")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusConflict)
	}
}

func TestHandleReleaseIssue_MissingID_400(t *testing.T) {
	svc := &mockIssueService{
		releaseIssueFunc: func(_ context.Context, _ service.ReleaseIssueParams) error {
			t.Fatal("service should not be called with empty ID")
			return nil
		},
	}
	h := HandleReleaseIssue(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws/issues//release", nil)
	req.SetPathValue("id", "")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleReleaseIssue_InvalidActor_400(t *testing.T) {
	svc := &mockIssueService{
		releaseIssueFunc: func(_ context.Context, _ service.ReleaseIssueParams) error {
			t.Fatal("service should not be called for an invalid X-Actor")
			return nil
		},
	}
	h := HandleReleaseIssue(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws/issues/rel-1/release", nil)
	req.SetPathValue("id", "rel-1")
	req.Header.Set("X-Actor", strings.Repeat("x", maxActorHeaderLen+1))
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}
