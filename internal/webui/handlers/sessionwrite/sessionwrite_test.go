package sessionwrite

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

const (
	testWS      = "DEMO"
	testSession = "sess-1"
)

// seedStore returns a memstore with one session (DEMO/sess-1, task DEMO-1).
func seedStore(t *testing.T) *memstore.Store {
	t.Helper()
	st := memstore.New()
	if _, err := st.AgentSessions().Create(context.Background(), store.AgentSessionCreate{
		WorkspaceKey: testWS,
		SessionID:    testSession,
		AgentID:      "nova",
		TaskID:       "DEMO-1",
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return st
}

// newReq builds a request to a session endpoint with the workspace in context
// and the sessionId path value set.
func newReq(method, sessionID, body string) *http.Request {
	r := httptest.NewRequest(method, "/api/workspaces/"+testWS+"/sessions/"+sessionID+"/x", strings.NewReader(body))
	r.SetPathValue("sessionId", sessionID)
	return r.WithContext(middleware.WithWorkspace(r.Context(), testWS))
}

func TestPostSessionArtifact_PersistsAndPropagatesSessionFields(t *testing.T) {
	st := seedStore(t)
	h := HandlePostSessionArtifact(st.AgentSessions(), st.Artifacts())

	rec := httptest.NewRecorder()
	h(rec, newReq(http.MethodPost, testSession, `{"type":"patch","uri":"/tmp/x.patch","summary":"did X","files_changed":3}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	arts, err := st.Artifacts().List(context.Background(), testWS, store.ArtifactFilter{SessionID: testSession})
	if err != nil || len(arts) != 1 {
		t.Fatalf("expected 1 persisted artifact, got %d (err=%v)", len(arts), err)
	}
	a := arts[0]
	if a.Type != "patch" || a.URI != "/tmp/x.patch" || a.Summary != "did X" {
		t.Errorf("artifact fields = %+v", a)
	}
	// Session's task/agent are propagated onto the artifact (the runner only
	// names the session; the server fills the rest).
	if a.TaskID != "DEMO-1" || a.AgentID != "nova" {
		t.Errorf("artifact backrefs = task %q agent %q, want DEMO-1/nova", a.TaskID, a.AgentID)
	}
	if a.Metadata["files_changed"] != "3" {
		t.Errorf("files_changed metadata = %q, want 3", a.Metadata["files_changed"])
	}
}

func TestPostSessionArtifact_Validation(t *testing.T) {
	st := seedStore(t)
	h := HandlePostSessionArtifact(st.AgentSessions(), st.Artifacts())

	// Missing type/uri → 400.
	rec := httptest.NewRecorder()
	h(rec, newReq(http.MethodPost, testSession, `{"summary":"x"}`))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing fields: status = %d, want 400", rec.Code)
	}

	// Unknown session → 404.
	rec = httptest.NewRecorder()
	h(rec, newReq(http.MethodPost, "sess-unknown", `{"type":"patch","uri":"/tmp/x"}`))
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown session: status = %d, want 404", rec.Code)
	}

	// Malformed JSON → 400.
	rec = httptest.NewRecorder()
	h(rec, newReq(http.MethodPost, testSession, `{not json`))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad json: status = %d, want 400", rec.Code)
	}
}

func TestRecordSessionUsage_PersistsUsageArtifact(t *testing.T) {
	st := seedStore(t)
	h := HandleRecordSessionUsage(st.AgentSessions(), st.Artifacts())

	rec := httptest.NewRecorder()
	h(rec, newReq(http.MethodPost, testSession, `{"input_tokens":100,"output_tokens":20,"cache_read_tokens":5}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// Usage is stored as a typed "usage" artifact (not session metadata, which
	// loom's finalizer would clobber), with the counts in artifact metadata.
	arts, err := st.Artifacts().List(context.Background(), testWS, store.ArtifactFilter{SessionID: testSession, Type: "usage"})
	if err != nil || len(arts) != 1 {
		t.Fatalf("expected 1 usage artifact, got %d (err=%v)", len(arts), err)
	}
	a := arts[0]
	if a.Type != "usage" || a.TaskID != "DEMO-1" {
		t.Errorf("usage artifact = %+v", a)
	}
	if a.Metadata["input_tokens"] != "100" || a.Metadata["output_tokens"] != "20" || a.Metadata["cache_read_tokens"] != "5" {
		t.Errorf("usage artifact metadata = %+v", a.Metadata)
	}

	// Unknown session → 404.
	rec = httptest.NewRecorder()
	h(rec, newReq(http.MethodPost, "nope", `{"input_tokens":1,"output_tokens":1}`))
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown session: status = %d, want 404", rec.Code)
	}
}

func TestHeartbeatSession(t *testing.T) {
	st := seedStore(t)
	// A refresher re-mints the caller's token; its value must surface in the
	// response so the SDK can rotate onto it. nil refresh → no token.
	h := HandleHeartbeatSession(st.AgentSessions(), func(*http.Request) string { return "fresh-token" })

	rec := httptest.NewRecorder()
	h(rec, newReq(http.MethodPost, testSession, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"session_id":"sess-1"`) {
		t.Errorf("body missing session_id: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"token":"fresh-token"`) {
		t.Errorf("body missing refreshed token: %s", rec.Body.String())
	}

	// nil refresher (keyless dev-mode) → no token field in the response.
	rec = httptest.NewRecorder()
	HandleHeartbeatSession(st.AgentSessions(), nil)(rec, newReq(http.MethodPost, testSession, ""))
	if strings.Contains(rec.Body.String(), `"token"`) {
		t.Errorf("nil refresh should omit token: %s", rec.Body.String())
	}

	// Heartbeat actually bumped LastHeartbeat.
	sess, _ := st.AgentSessions().Get(context.Background(), testWS, testSession)
	if sess.LastHeartbeat.IsZero() {
		t.Error("LastHeartbeat was not set")
	}

	// Unknown session → 404.
	rec = httptest.NewRecorder()
	h(rec, newReq(http.MethodPost, "nope", ""))
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown session: status = %d, want 404", rec.Code)
	}
}

func TestAppendSessionLog(t *testing.T) {
	st := seedStore(t)
	h := HandleAppendSessionLog(st.AgentSessions())

	// Valid → 202.
	rec := httptest.NewRecorder()
	h(rec, newReq(http.MethodPost, testSession, `{"stream":"stdout","text":"hello"}`))
	if rec.Code != http.StatusAccepted {
		t.Errorf("valid log: status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}

	// Bad stream → 400.
	rec = httptest.NewRecorder()
	h(rec, newReq(http.MethodPost, testSession, `{"stream":"weird","text":"x"}`))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad stream: status = %d, want 400", rec.Code)
	}

	// Unknown session → 404.
	rec = httptest.NewRecorder()
	h(rec, newReq(http.MethodPost, "nope", `{"stream":"stdout","text":"x"}`))
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown session: status = %d, want 404", rec.Code)
	}
}

func TestPostSessionArtifact_IdempotencyKeyDedupes(t *testing.T) {
	st := seedStore(t)
	h := HandlePostSessionArtifact(st.AgentSessions(), st.Artifacts())
	post := func() int {
		r := newReq(http.MethodPost, testSession, `{"type":"commit","uri":"x#b@sha1","files_changed":1}`)
		r.Header.Set("Idempotency-Key", "sha1")
		rec := httptest.NewRecorder()
		h(rec, r)
		return rec.Code
	}
	if c1, c2 := post(), post(); c1 != http.StatusCreated || c2 != http.StatusCreated {
		t.Fatalf("statuses = %d,%d, want 201,201", c1, c2)
	}
	arts, err := st.Artifacts().List(context.Background(), testWS, store.ArtifactFilter{SessionID: testSession})
	if err != nil || len(arts) != 1 {
		t.Fatalf("expected exactly 1 artifact after a retried idempotent POST, got %d (err=%v)", len(arts), err)
	}
	// Without a key, a second POST creates a distinct artifact.
	noKey := newReq(http.MethodPost, testSession, `{"type":"commit","uri":"y"}`)
	rec := httptest.NewRecorder()
	h(rec, noKey)
	arts, _ = st.Artifacts().List(context.Background(), testWS, store.ArtifactFilter{SessionID: testSession})
	if len(arts) != 2 {
		t.Errorf("keyless POST should add a distinct artifact; total = %d, want 2", len(arts))
	}
}
