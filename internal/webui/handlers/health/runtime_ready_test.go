package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

type statsQueryFunc func(context.Context) (*workitems.Stats, error)

func (query statsQueryFunc) Stats(ctx context.Context) (*workitems.Stats, error) { return query(ctx) }

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
	if body.Mode != "workflow-catalog" || body.Reason != "Work Items service not configured" {
		t.Fatalf("body = %+v", body)
	}
}

func TestWorkspaceRuntimeReadySurfacesBackendFailure(t *testing.T) {
	h := HandleWorkspaceRuntimeReadyWithLocalPath(statsQueryFunc(func(context.Context) (*workitems.Stats, error) {
		return nil, errors.New("backend down")
	}), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, runtimeReadyRequest("LOOM"))
	if rec.Code != http.StatusServiceUnavailable || decodeReady(t, rec).Reason != "backend down" {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestWorkspaceRuntimeReadySuccess(t *testing.T) {
	h := HandleWorkspaceRuntimeReadyWithLocalPath(statsQueryFunc(func(context.Context) (*workitems.Stats, error) {
		return &workitems.Stats{}, nil
	}), func(string) string { return t.TempDir() })
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, runtimeReadyRequest("LOOM"))
	body := decodeReady(t, rec)
	if rec.Code != http.StatusOK || !body.Ready || body.Mode != "workflow-catalog" {
		t.Fatalf("status=%d body=%+v", rec.Code, body)
	}
}

func TestWorkspaceRuntimeReadyRejectsMissingLocalPath(t *testing.T) {
	called := false
	h := HandleWorkspaceRuntimeReadyWithLocalPath(statsQueryFunc(func(context.Context) (*workitems.Stats, error) {
		called = true
		return &workitems.Stats{}, nil
	}), func(string) string { return "" })
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
