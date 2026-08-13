package issues

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func TestHandleAddComment_UsesCtxActorAttribution(t *testing.T) {
	var got service.AddCommentParams
	svc := &mockIssueService{
		addCommentFunc: func(_ context.Context, params service.AddCommentParams) (*types.Comment, error) {
			got = params
			return &types.Comment{}, nil
		},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/issues/issue-1/comments", strings.NewReader(`{"body":"hello"}`))
	req.SetPathValue("id", "issue-1")
	req = req.WithContext(middleware.WithActor(req.Context(), testOccupantActor(t)))
	rec := httptest.NewRecorder()

	HandleAddComment(svc).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if got.Author != "lead-occupant:p1" {
		t.Fatalf("Author = %q, want lead-occupant:p1", got.Author)
	}
}

func TestHandleAddComment_InvalidActorRejected(t *testing.T) {
	called := false
	svc := &mockIssueService{
		addCommentFunc: func(_ context.Context, _ service.AddCommentParams) (*types.Comment, error) {
			called = true
			return &types.Comment{}, nil
		},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/issues/issue-1/comments", strings.NewReader(`{"body":"hello"}`))
	req.SetPathValue("id", "issue-1")
	req = req.WithContext(middleware.WithActor(req.Context(), middleware.Actor{}))
	rec := httptest.NewRecorder()

	HandleAddComment(svc).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	var resp CommentResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Success || resp.Error != "invalid request principal" || resp.Data != nil {
		t.Fatalf("response = %#v, want {Success:false Error:%q Data:nil}", resp, "invalid request principal")
	}
	if called {
		t.Fatal("AddComment service was called for an invalid actor")
	}
}

func TestHandleCreateIssue_OccupantOverridesClientAttribution(t *testing.T) {
	got := performCreateAttributionRequest(t, middleware.WithActor(context.Background(), testOccupantActor(t)), `{
		"title":"test issue",
		"issue_type":"task",
		"priority":2,
		"created_by":"attacker",
		"assignee":"someone-else",
		"owner":"someone-else"
	}`)

	if got.CreatedBy != "lead-occupant:p1" || got.Assignee != "lead-occupant:p1" || got.Owner != "lead-occupant:p1" {
		t.Fatalf("attribution = {CreatedBy:%q Assignee:%q Owner:%q}, want all lead-occupant:p1", got.CreatedBy, got.Assignee, got.Owner)
	}
}

func TestHandleCreateIssue_OccupantLeavesEmptyFieldsEmpty(t *testing.T) {
	got := performCreateAttributionRequest(t, middleware.WithActor(context.Background(), testOccupantActor(t)), `{
		"title":"test issue",
		"issue_type":"task",
		"priority":2,
		"created_by":"attacker"
	}`)

	if got.CreatedBy != "lead-occupant:p1" {
		t.Fatalf("CreatedBy = %q, want lead-occupant:p1", got.CreatedBy)
	}
	if got.Assignee != "" || got.Owner != "" {
		t.Fatalf("Assignee/Owner = %q/%q, want both empty", got.Assignee, got.Owner)
	}
}

func TestHandleCreateIssue_InvalidActorRejected(t *testing.T) {
	called := false
	svc := &mockIssueService{
		createIssueFunc: func(_ context.Context, _ service.CreateIssueParams) (json.RawMessage, error) {
			called = true
			return json.RawMessage(`{"id":"issue-1"}`), nil
		},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/issues", strings.NewReader(`{
		"title":"test issue",
		"issue_type":"task",
		"priority":2
	}`))
	req = req.WithContext(middleware.WithActor(req.Context(), middleware.Actor{}))
	rec := httptest.NewRecorder()

	HandleCreateIssue(svc).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	var resp IssuesResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Success || resp.Error != "invalid request principal" || resp.Code != "INVALID_PRINCIPAL" || len(resp.Data) != 0 {
		t.Fatalf("response = %#v, want {Success:false Error:%q Code:%q Data:nil}", resp, "invalid request principal", "INVALID_PRINCIPAL")
	}
	if called {
		t.Fatal("CreateIssue service was called for an invalid actor")
	}
}

func TestHandleCreateIssue_WebUIStillTrustsAllThreeFields(t *testing.T) {
	got := performCreateAttributionRequest(t, middleware.WithActor(context.Background(), middleware.WebUIActor()), `{
		"title":"test issue",
		"issue_type":"task",
		"priority":2,
		"created_by":"alice",
		"assignee":"bob",
		"owner":"carol"
	}`)

	if got.CreatedBy != "alice" || got.Assignee != "bob" || got.Owner != "carol" {
		t.Fatalf("attribution = {CreatedBy:%q Assignee:%q Owner:%q}, want alice/bob/carol", got.CreatedBy, got.Assignee, got.Owner)
	}
}

func performCreateAttributionRequest(t *testing.T, ctx context.Context, body string) service.CreateIssueParams {
	t.Helper()
	var got service.CreateIssueParams
	svc := &mockIssueService{
		createIssueFunc: func(_ context.Context, params service.CreateIssueParams) (json.RawMessage, error) {
			got = params
			return json.RawMessage(`{"id":"issue-1"}`), nil
		},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/issues", strings.NewReader(body)).WithContext(ctx)
	rec := httptest.NewRecorder()

	HandleCreateIssue(svc).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	return got
}

func testOccupantActor(t *testing.T) middleware.Actor {
	t.Helper()
	actor, err := middleware.OccupantActorFor("lead-occupant:p1")
	if err != nil {
		t.Fatalf("OccupantActorFor: %v", err)
	}
	return actor
}
