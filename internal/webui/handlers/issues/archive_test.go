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

func TestHandleArchiveIssue_WithReason(t *testing.T) {
	svc := &mockIssueService{
		archiveIssueFunc: func(ctx context.Context, params service.ArchiveIssueParams) error {
			if params.IssueID != "arch-1" {
				t.Errorf("ArchiveIssue() IssueID = %q, want %q", params.IssueID, "arch-1")
			}
			if params.Reason != "superseded" {
				t.Errorf("ArchiveIssue() Reason = %q, want %q", params.Reason, "superseded")
			}
			return nil
		},
	}
	handler := HandleArchiveIssue(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/issues/arch-1/archive", strings.NewReader(`{"reason":"superseded"}`))
	req.SetPathValue("id", "arch-1")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var resp ArchiveResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !resp.Success {
		t.Error("expected success=true")
	}
}

// The workspace tree's Archive menu item sends no body at all; an empty body
// must be a valid status-only archive, not a 400.
func TestHandleArchiveIssue_EmptyBody(t *testing.T) {
	called := false
	svc := &mockIssueService{
		archiveIssueFunc: func(ctx context.Context, params service.ArchiveIssueParams) error {
			called = true
			if params.Reason != "" {
				t.Errorf("ArchiveIssue() Reason = %q, want empty", params.Reason)
			}
			return nil
		},
	}
	handler := HandleArchiveIssue(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/issues/arch-2/archive", nil)
	req.SetPathValue("id", "arch-2")
	req.ContentLength = 0
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !called {
		t.Error("expected ArchiveIssue to be called")
	}
}

func TestHandleArchiveIssue_MissingID(t *testing.T) {
	handler := HandleArchiveIssue(&mockIssueService{})

	req := httptest.NewRequest(http.MethodPost, "/api/issues//archive", nil)
	req.ContentLength = 0
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleArchiveIssue_InvalidJSON(t *testing.T) {
	handler := HandleArchiveIssue(&mockIssueService{})

	req := httptest.NewRequest(http.MethodPost, "/api/issues/arch-3/archive", strings.NewReader(`{`))
	req.SetPathValue("id", "arch-3")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleArchiveIssue_NotFound(t *testing.T) {
	svc := &mockIssueService{
		archiveIssueFunc: func(ctx context.Context, params service.ArchiveIssueParams) error {
			return service.ErrNotFound("issue not found: ghost-1")
		},
	}
	handler := HandleArchiveIssue(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/issues/ghost-1/archive", nil)
	req.SetPathValue("id", "ghost-1")
	req.ContentLength = 0
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleUnarchiveIssue_Success(t *testing.T) {
	called := false
	svc := &mockIssueService{
		unarchiveIssueFunc: func(ctx context.Context, params service.UnarchiveIssueParams) error {
			called = true
			if params.IssueID != "arch-1" {
				t.Errorf("UnarchiveIssue() IssueID = %q, want %q", params.IssueID, "arch-1")
			}
			return nil
		},
	}
	handler := HandleUnarchiveIssue(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/issues/arch-1/unarchive", nil)
	req.SetPathValue("id", "arch-1")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !called {
		t.Error("expected UnarchiveIssue to be called")
	}
}

func TestHandleUnarchiveIssue_MissingID(t *testing.T) {
	handler := HandleUnarchiveIssue(&mockIssueService{})

	req := httptest.NewRequest(http.MethodPost, "/api/issues//unarchive", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}
