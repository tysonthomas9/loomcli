package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

type stubIssueBackend struct {
	backend.IssueBackend
	stats *backend.StatsData
	err   error
}

func (s *stubIssueBackend) Stats(context.Context) (*backend.StatsData, error) { return s.stats, s.err }

func runtimeReadyRequest(ws string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+ws+"/readyz", nil)
	if ws != "" {
		req.SetPathValue("ws", ws)
	}
	return req
}

func decodeReady(t *testing.T, rec *httptest.ResponseRecorder) RuntimeReadyResponse {
	t.Helper()
	var body RuntimeReadyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body
}

func TestWorkspaceRuntimeReadyRequiresBackend(t *testing.T) {
	h := HandleWorkspaceRuntimeReadyWithLocalPath(nil, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, runtimeReadyRequest("LOOM"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	body := decodeReady(t, rec)
	if body.Mode != "workflow-catalog" || body.Reason != "issue backend not configured" {
		t.Fatalf("body = %+v", body)
	}
}

func TestWorkspaceRuntimeReadySurfacesBackendFailure(t *testing.T) {
	h := HandleWorkspaceRuntimeReadyWithLocalPath(func(context.Context) backend.IssueBackend {
		return &stubIssueBackend{err: errors.New("backend down")}
	}, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, runtimeReadyRequest("LOOM"))
	if rec.Code != http.StatusServiceUnavailable || decodeReady(t, rec).Reason != "backend down" {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestWorkspaceRuntimeReadySuccess(t *testing.T) {
	h := HandleWorkspaceRuntimeReadyWithLocalPath(func(context.Context) backend.IssueBackend {
		return &stubIssueBackend{stats: &backend.StatsData{}}
	}, func(string) string { return t.TempDir() })
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, runtimeReadyRequest("LOOM"))
	body := decodeReady(t, rec)
	if rec.Code != http.StatusOK || !body.Ready || body.Mode != "workflow-catalog" {
		t.Fatalf("status=%d body=%+v", rec.Code, body)
	}
}

func TestWorkspaceRuntimeReadyRejectsMissingLocalPath(t *testing.T) {
	called := false
	h := HandleWorkspaceRuntimeReadyWithLocalPath(func(context.Context) backend.IssueBackend {
		called = true
		return &stubIssueBackend{stats: &backend.StatsData{}}
	}, func(string) string { return "" })
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, runtimeReadyRequest("LOOM"))
	if rec.Code != http.StatusServiceUnavailable || called {
		t.Fatalf("status=%d backend_called=%v", rec.Code, called)
	}
}

func TestWorkspaceRuntimeReadyRequiresWorkspace(t *testing.T) {
	h := HandleWorkspaceRuntimeReadyWithLocalPath(nil, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, runtimeReadyRequest(""))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
