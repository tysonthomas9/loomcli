package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/leadtoken"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/issues"
	"github.com/tysonthomas9/loomcli/internal/webui/modbuilder"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/workflows"
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

func TestIssueBackendWire_OccupantAttributionThroughLeadDataMount(t *testing.T) {
	recorder := &issueWireRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			return
		}
		recorder.requests = append(recorder.requests, issueWireRequest{r.Method, r.URL.Path, r.Header.Get("X-Actor"), body})
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/issues":
			writeIssueWireResponse(t, w, map[string]any{"id": "issue-1", "title": "created", "type": "task", "priority": 2})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/issues/issue-1":
			writeIssueWireResponse(t, w, map[string]any{"id": "issue-1", "title": "updated", "type": "task", "priority": 2})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/WS/issues/issue-1":
			writeIssueWireResponse(t, w, map[string]any{"success": true})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/issues/issue-1/assign":
			writeIssueWireResponse(t, w, map[string]any{"success": true})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/issues/issue-1/comments":
			writeIssueWireResponse(t, w, map[string]any{"id": "1", "issue_id": "issue-1", "author": "server", "body": "comment"})
		default:
			w.WriteHeader(http.StatusNotFound)
			writeIssueWireResponse(t, w, map[string]any{"error": map[string]any{"code": "not_found", "message": "not found"}})
		}
	}))
	t.Cleanup(server.Close)

	st := memstore.New()
	for _, placementID := range []string{"p1", "p2"} {
		_, err := st.Nodes().Create(context.Background(), store.NodeCreate{
			WorkspaceKey: "WS", NodeID: placementID, OwnerActor: "lead",
			Placement: &domain.NodePlacement{SandboxID: "sandbox-" + placementID, Generation: 7, State: domain.PlacementStateActive},
		})
		if err != nil {
			t.Fatalf("create placement %s: %v", placementID, err)
		}
	}
	key := bytes.Repeat([]byte{0x71}, 32)
	factory := cli.WorkspaceAwareIssueBackendForURL(server.URL, "serve-actor")
	mux := http.NewServeMux()
	modbuilder.NewLeadAPIModule(modbuilder.LeadAPIDeps{Store: st, TokenKey: key, IssueBackendFn: factory}).Register(mux)

	request := func(method, path, placementID, body string) *httptest.ResponseRecorder {
		t.Helper()
		token, err := leadtoken.MintOccupantToken(leadtoken.OccupantClaims{
			WorkspaceKey: "WS", PlacementID: placementID, Generation: 7, Caps: []string{leadtoken.CapLeadData},
		}, key, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req = req.WithContext(middleware.WithWorkspace(req.Context(), "WS"))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	createBody := `{"title":"occupant issue","issue_type":"task","priority":2,"created_by":"attacker","assignee":"client-assignee","owner":"client-owner"}`
	if rec := request(http.MethodPost, "/api/workspaces/WS/lead/data/issues", "p1", createBody); rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d; body = %s", rec.Code, rec.Body.String())
	}
	patchBody := `{"title":"updated","assignee":"kept-assignee","owner":"client-owner"}`
	if rec := request(http.MethodPatch, "/api/workspaces/WS/lead/data/issues/issue-1", "p1", patchBody); rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d; body = %s", rec.Code, rec.Body.String())
	}
	if rec := request(http.MethodPost, "/api/workspaces/WS/lead/data/issues/issue-1/comments", "p1", `{"text":"occupant comment"}`); rec.Code != http.StatusCreated {
		t.Fatalf("comment status = %d; body = %s", rec.Code, rec.Body.String())
	}
	if rec := request(http.MethodPost, "/api/workspaces/WS/lead/data/issues", "p2", createBody); rec.Code != http.StatusCreated {
		t.Fatalf("second placement create status = %d; body = %s", rec.Code, rec.Body.String())
	}

	p1Create := recorder.findByActor(t, http.MethodPost, "/api/v1/WS/issues", "lead-occupant:p1")
	assertCreateIssueWireBody(t, p1Create.body, "lead-occupant:p1", "lead-occupant:p1")
	p2Create := recorder.findByActor(t, http.MethodPost, "/api/v1/WS/issues", "lead-occupant:p2")
	assertCreateIssueWireBody(t, p2Create.body, "lead-occupant:p2", "lead-occupant:p2")
	patch := recorder.findByActor(t, http.MethodPatch, "/api/v1/WS/issues/issue-1", "lead-occupant:p1")
	assertJSONField(t, patch.body, "owner", "lead-occupant:p1")
	assign := recorder.findByActor(t, http.MethodPost, "/api/v1/WS/issues/issue-1/assign", "lead-occupant:p1")
	assertJSONField(t, assign.body, "assignee", "kept-assignee")
	comment := recorder.findByActor(t, http.MethodPost, "/api/v1/WS/issues/issue-1/comments", "lead-occupant:p1")
	if got, want := string(comment.body), `{"body":"occupant comment"}`; got != want {
		t.Fatalf("serve-to-fleet comment body = %s, want %s", got, want)
	}

	p1Actor, _ := middleware.OccupantActorFor("lead-occupant:p1")
	p2Actor, _ := middleware.OccupantActorFor("lead-occupant:p2")
	p1Backend := factory(middleware.WithActor(context.Background(), p1Actor))
	p2Backend := factory(middleware.WithActor(context.Background(), p2Actor))
	if p1Backend == p2Backend {
		t.Fatal("two placements resolved to the same actor-keyed backend instance")
	}
}

func TestIssueBackendWire_OccupantEpicRunDispatchAttribution(t *testing.T) {
	fixture := newIssueDispatchWireFixture(t)
	first := fixture.request(t, http.MethodPost, "/api/workspaces/WS/lead/dispatch/epic-run", "p1", `{"epicId":"epic-1"}`)
	second := fixture.request(t, http.MethodPost, "/api/workspaces/WS/lead/dispatch/epic-run", "p2", `{"epicId":"epic-2"}`)
	if first.Code != http.StatusAccepted || second.Code != http.StatusAccepted {
		t.Fatalf("dispatch statuses = %d/%d; bodies = %s / %s", first.Code, second.Code, first.Body.String(), second.Body.String())
	}
	assertIssueWireActor(t, fixture.recorder.findByActor(t, http.MethodGet,
		"/api/v1/WS/issues/epic-1", "lead-occupant:p1"), "lead-occupant:p1")
	assertIssueWireActor(t, fixture.recorder.findByActor(t, http.MethodGet,
		"/api/v1/WS/issues/epic-2", "lead-occupant:p2"), "lead-occupant:p2")

	runs, err := fixture.store.DriverRuns().List(context.Background(), "WS", store.DriverRunFilter{})
	if err != nil || len(runs) != 2 {
		t.Fatalf("runs = %+v, err = %v", runs, err)
	}
	runBySource := make(map[string]*domain.DriverRun, len(runs))
	for _, run := range runs {
		runBySource[run.SourceRef] = run
	}
	for _, placement := range []string{"p1", "p2"} {
		source := leadtoken.OccupantActor(placement)
		if runBySource[source] == nil {
			t.Fatalf("missing run for source %q: %+v", source, runs)
		}
	}
	for placement, ownSource := range map[string]string{"p1": "lead-occupant:p1", "p2": "lead-occupant:p2"} {
		own := runBySource[ownSource]
		foreign := runBySource[map[string]string{"p1": "lead-occupant:p2", "p2": "lead-occupant:p1"}[placement]]
		ownResult := fixture.request(t, http.MethodGet,
			"/api/workspaces/WS/lead/dispatch/runs/"+own.RunID, placement, "")
		foreignResult := fixture.request(t, http.MethodGet,
			"/api/workspaces/WS/lead/dispatch/runs/"+foreign.RunID, placement, "")
		if ownResult.Code != http.StatusOK || foreignResult.Code != http.StatusNotFound {
			t.Fatalf("placement %s own/foreign statuses = %d/%d", placement, ownResult.Code, foreignResult.Code)
		}
	}
}

func TestEpicRunStatus_MissingAndForeignRunsAreByteIdentical(t *testing.T) {
	fixture := newIssueDispatchWireFixture(t)
	dispatched := fixture.request(t, http.MethodPost,
		"/api/workspaces/WS/lead/dispatch/epic-run", "p1", `{"epicId":"epic-1"}`)
	if dispatched.Code != http.StatusAccepted {
		t.Fatalf("dispatch status = %d; body = %s", dispatched.Code, dispatched.Body.String())
	}
	var response struct {
		Data struct {
			RunID string `json:"runId"`
		} `json:"data"`
	}
	if err := json.Unmarshal(dispatched.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	foreign := fixture.request(t, http.MethodGet,
		"/api/workspaces/WS/lead/dispatch/runs/"+response.Data.RunID, "p2", "")
	missing := fixture.request(t, http.MethodGet,
		"/api/workspaces/WS/lead/dispatch/runs/run-does-not-exist", "p2", "")
	if foreign.Code != http.StatusNotFound || missing.Code != http.StatusNotFound {
		t.Fatalf("foreign/missing statuses = %d/%d", foreign.Code, missing.Code)
	}
	if !bytes.Equal(foreign.Body.Bytes(), missing.Body.Bytes()) {
		t.Fatalf("foreign body %q != missing body %q", foreign.Body.Bytes(), missing.Body.Bytes())
	}
}

type issueDispatchWireFixture struct {
	mux      *http.ServeMux
	store    store.Store
	key      []byte
	recorder *issueWireRecorder
}

func newIssueDispatchWireFixture(t *testing.T) *issueDispatchWireFixture {
	t.Helper()
	recorder := &issueWireRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		recorder.requests = append(recorder.requests, issueWireRequest{
			method: r.Method, path: r.URL.Path, actor: r.Header.Get("X-Actor"), body: body,
		})
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet &&
			(r.URL.Path == "/api/v1/WS/issues/epic-1" || r.URL.Path == "/api/v1/WS/issues/epic-2") {
			id := strings.TrimPrefix(r.URL.Path, "/api/v1/WS/issues/")
			writeIssueWireResponse(t, w, map[string]any{
				"id": id, "title": "Epic", "type": "epic", "repo": "repo-1",
			})
			return
		}
		writeIssueWireResponse(t, w, []any{})
	}))
	t.Cleanup(server.Close)

	st := memstore.New()
	seedIssueDispatchWireStore(t, st)
	key := bytes.Repeat([]byte{0x79}, 32)
	factory := cli.WorkspaceAwareIssueBackendForURL(server.URL, "serve-actor")
	mux := http.NewServeMux()
	modbuilder.NewLeadAPIModule(modbuilder.LeadAPIDeps{
		Store: st, TokenKey: key, IssueBackendFn: factory,
	}).Register(mux)
	return &issueDispatchWireFixture{mux: mux, store: st, key: key, recorder: recorder}
}

func seedIssueDispatchWireStore(t *testing.T, st store.Store) {
	t.Helper()
	ctx := context.Background()
	for _, placement := range []struct{ id, agent string }{{"p1", "lead-one"}, {"p2", "lead-two"}} {
		if _, err := st.Nodes().Create(ctx, store.NodeCreate{
			WorkspaceKey: "WS", NodeID: placement.id, OwnerActor: "agent:" + placement.agent,
			Labels:    []string{"loom-agent=" + placement.agent},
			Placement: &domain.NodePlacement{SandboxID: "sandbox-" + placement.id, Generation: 7, State: domain.PlacementStateActive},
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
			WorkspaceKey: "WS", SessionID: "session-" + placement.id, AgentID: placement.agent,
			NodeID: placement.id, Kind: domain.AgentSessionKindOrchestration, Status: domain.AgentSessionRunning,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.Repos().Create(ctx, store.RepoCreate{
		WorkspaceKey: "WS", Name: "repo-1", RemoteURL: "https://github.com/octocat/hello", DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS", DriverID: workflows.BuiltinEpicRunnerWorkflowName,
		Name: workflows.BuiltinEpicRunnerWorkflowName, OwnerType: domain.DriverOwnerSystem,
		ActiveVersionID: "epic-version", Status: domain.DriverStatusActive, TrustLevel: domain.DriverTrustTrusted,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: "WS", VersionID: "epic-version", DriverID: workflows.BuiltinEpicRunnerWorkflowName,
		Version: 1, SourceDigest: "sha256:source", BundleDigest: "sha256:bundle",
		ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatal(err)
	}
}

func (f *issueDispatchWireFixture) request(t *testing.T, method, path, placement, body string) *httptest.ResponseRecorder {
	t.Helper()
	token, err := leadtoken.MintOccupantToken(leadtoken.OccupantClaims{
		WorkspaceKey: "WS", PlacementID: placement, Generation: 7,
		Caps: []string{leadtoken.CapLeadDispatch},
	}, f.key, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "WS"))
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
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

func (r *issueWireRecorder) findByActor(t *testing.T, method, path, actor string) issueWireRequest {
	t.Helper()
	var matches []issueWireRequest
	for _, request := range r.requests {
		if request.method == method && request.path == path && request.actor == actor {
			matches = append(matches, request)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("found %d requests for %s %s actor=%s, want 1; all requests = %#v", len(matches), method, path, actor, r.requests)
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

func assertJSONField(t *testing.T, raw []byte, field, want string) {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal body %q: %v", raw, err)
	}
	if got := body[field]; got != want {
		t.Fatalf("body %s = %#v, want %q; body = %s", field, got, want, raw)
	}
}

func writeIssueWireResponse(t *testing.T, w http.ResponseWriter, body any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Errorf("encode response: %v", err)
		return
	}
}
