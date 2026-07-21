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

func TestHandleSetIssueRepository_ReturnsCanonicalIssue(t *testing.T) {
	var gotIssueID, gotRepo string
	svc := stubIssueRepositoryCommand{
		setRepoFunc: func(_ context.Context, issueID, repo string) (json.RawMessage, error) {
			gotIssueID, gotRepo = issueID, repo
			return json.RawMessage(`{"id":"task-11","status":"open","source_repo":"hello-world","repo":"hello-world"}`), nil
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/workspaces/{ws}/issues/{id}/repository", HandleSetIssueRepository(svc))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/workspaces/test-ws/issues/task-11/repository", strings.NewReader(`{"repo":"hello-world"}`))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if gotIssueID != "task-11" || gotRepo != "hello-world" {
		t.Fatalf("issue ID = %q, repo = %q", gotIssueID, gotRepo)
	}
	var body struct {
		Success bool `json:"success"`
		Data    struct {
			ID         string `json:"id"`
			Status     string `json:"status"`
			SourceRepo string `json:"source_repo"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !body.Success || body.Data.ID != "task-11" || body.Data.Status != "open" || body.Data.SourceRepo != "hello-world" {
		t.Fatalf("response = %+v", body)
	}
}

func TestHandleSetIssueRepository_RejectsUnknownFields(t *testing.T) {
	called := false
	svc := stubIssueRepositoryCommand{
		setRepoFunc: func(context.Context, string, string) (json.RawMessage, error) {
			called = true
			return nil, nil
		},
	}
	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"repo":"hello-world","status":"open"}`))
	req.SetPathValue("id", "task-11")
	rec := httptest.NewRecorder()

	HandleSetIssueRepository(svc).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if called {
		t.Fatal("service called for invalid request")
	}
}

func TestHandleSetIssueRepository_MapsServiceConflict(t *testing.T) {
	svc := stubIssueRepositoryCommand{
		setRepoFunc: func(context.Context, string, string) (json.RawMessage, error) {
			return nil, service.ErrConflict("issue changed concurrently")
		},
	}
	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"repo":"hello-world"}`))
	req.SetPathValue("id", "task-11")
	rec := httptest.NewRecorder()

	HandleSetIssueRepository(svc).ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

type stubIssueRepositoryCommand struct {
	setRepoFunc func(context.Context, string, string) (json.RawMessage, error)
}

func (s stubIssueRepositoryCommand) SetIssueRepository(ctx context.Context, issueID, repo string) (json.RawMessage, error) {
	return s.setRepoFunc(ctx, issueID, repo)
}
