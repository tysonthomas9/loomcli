package cli_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/issues"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

type issueWireRequest struct {
	method string
	path   string
	actor  string
	body   []byte
}

type issueWireRecorder struct {
	requests []issueWireRequest
}

func TestIssueBackendWire_BrowserCreate(t *testing.T) {
	svc, recorder := newIssueWireService(t)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/issues", strings.NewReader(`{
		"title":"browser issue",
		"issue_type":"task",
		"priority":2,
		"created_by":"alice",
		"assignee":"bob",
		"owner":"carol"
	}`))
	req = withIssueWireActor(req, middleware.WebUIActor())
	rec := httptest.NewRecorder()

	issues.HandleCreateIssue(svc).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	wire := recorder.find(t, http.MethodPost, "/api/v1/WS/issues")
	assertIssueWireActor(t, wire, "serve-actor")
	assertCreateIssueWireBody(t, wire.body, "bob", "carol")
}

func TestIssueBackendWire_BrowserAddComment(t *testing.T) {
	svc, recorder := newIssueWireService(t)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/issues/issue-1/comments", strings.NewReader(`{"body":"browser comment"}`))
	req.SetPathValue("id", "issue-1")
	req = withIssueWireActor(req, middleware.WebUIActor())
	rec := httptest.NewRecorder()

	issues.HandleAddComment(svc).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	wire := recorder.find(t, http.MethodPost, "/api/v1/WS/issues/issue-1/comments")
	assertIssueWireActor(t, wire, "serve-actor")
	if got, want := string(wire.body), `{"body":"browser comment"}`; got != want {
		t.Fatalf("raw body = %s, want %s", got, want)
	}
}

func TestIssueBackendWire_OccupantCreate(t *testing.T) {
	svc, recorder := newIssueWireService(t)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/issues", strings.NewReader(`{
		"title":"occupant issue",
		"issue_type":"task",
		"priority":2,
		"created_by":"attacker",
		"assignee":"someone-else",
		"owner":"someone-else"
	}`))
	req = withIssueWireActor(req, issueWireOccupantActor(t))
	rec := httptest.NewRecorder()

	issues.HandleCreateIssue(svc).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	wire := recorder.find(t, http.MethodPost, "/api/v1/WS/issues")
	assertIssueWireActor(t, wire, "lead-occupant:p1")
	assertCreateIssueWireBody(t, wire.body, "lead-occupant:p1", "lead-occupant:p1")
}

func TestIssueBackendWire_OccupantAddComment(t *testing.T) {
	svc, recorder := newIssueWireService(t)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/issues/issue-1/comments", strings.NewReader(`{"body":"occupant comment"}`))
	req.SetPathValue("id", "issue-1")
	req = withIssueWireActor(req, issueWireOccupantActor(t))
	rec := httptest.NewRecorder()

	issues.HandleAddComment(svc).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	wire := recorder.find(t, http.MethodPost, "/api/v1/WS/issues/issue-1/comments")
	assertIssueWireActor(t, wire, "lead-occupant:p1")
	if got, want := string(wire.body), `{"body":"occupant comment"}`; got != want {
		t.Fatalf("raw body = %s, want %s", got, want)
	}
}

func newIssueWireService(t *testing.T) (service.IssueService, *issueWireRecorder) {
	t.Helper()
	recorder := &issueWireRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			return
		}
		recorder.requests = append(recorder.requests, issueWireRequest{
			method: r.Method,
			path:   r.URL.Path,
			actor:  r.Header.Get("X-Actor"),
			body:   body,
		})

		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/issues":
			writeIssueWireResponse(t, w, map[string]any{
				"id":         "issue-1",
				"title":      "created issue",
				"issue_type": "task",
				"priority":   2,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/issues/issue-1/comments":
			writeIssueWireResponse(t, w, map[string]any{
				"id":       "1",
				"issue_id": "issue-1",
				"author":   "server",
				"body":     "comment",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
			writeIssueWireResponse(t, w, map[string]any{
				"error": map[string]any{
					"code":    "not_found",
					"message": "not found",
				},
			})
		}
	}))
	t.Cleanup(server.Close)

	factory := cli.WorkspaceAwareIssueBackendForURL(server.URL, "serve-actor")
	svc := service.NewIssueServiceWithBackend(nil, nil, middleware.WithWorkspace, factory)
	return svc, recorder
}

func withIssueWireActor(req *http.Request, actor middleware.Actor) *http.Request {
	ctx := middleware.WithWorkspace(req.Context(), "WS")
	ctx = middleware.WithActor(ctx, actor)
	return req.WithContext(ctx)
}

func issueWireOccupantActor(t *testing.T) middleware.Actor {
	t.Helper()
	actor, err := middleware.OccupantActorFor("lead-occupant:p1")
	if err != nil {
		t.Fatalf("OccupantActorFor: %v", err)
	}
	return actor
}

func (r *issueWireRecorder) find(t *testing.T, method, path string) issueWireRequest {
	t.Helper()
	var matches []issueWireRequest
	for _, request := range r.requests {
		if request.method == method && request.path == path {
			matches = append(matches, request)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("found %d requests for %s %s, want 1; all requests = %#v", len(matches), method, path, r.requests)
	}
	return matches[0]
}

func assertIssueWireActor(t *testing.T, request issueWireRequest, want string) {
	t.Helper()
	if request.actor != want {
		t.Fatalf("X-Actor = %q, want %q", request.actor, want)
	}
}

func assertCreateIssueWireBody(t *testing.T, raw []byte, wantAssignee, wantOwner string) {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal raw create body %q: %v", raw, err)
	}
	if got := body["assignee"]; got != wantAssignee {
		t.Fatalf("raw body assignee = %#v, want %q; body = %s", got, wantAssignee, raw)
	}
	if got := body["owner"]; got != wantOwner {
		t.Fatalf("raw body owner = %#v, want %q; body = %s", got, wantOwner, raw)
	}
	if got, ok := body["created_by"]; ok {
		t.Fatalf("raw body contains created_by = %#v; body = %s", got, raw)
	}
}

func writeIssueWireResponse(t *testing.T, w http.ResponseWriter, body any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Errorf("encode response: %v", err)
		return
	}
}
