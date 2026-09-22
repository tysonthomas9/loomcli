package issues

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	hwtranscript "github.com/olesho/harness-wrapper/pkg/transcript"

	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/sessions/eventstore"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/misc"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/svcimpl"
)

// Compile-time assertion: *SessionModule implements Module.
var _ Module = (*SessionModule)(nil)

// noopHandler is a placeholder handler for route registration tests.
var noopHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func TestSessionModule_RegisterRoutes(t *testing.T) {
	mod := NewSessionModule(&stubSessionService{}, SessionModuleOpts{
		ListTaskSessions:             noopHandler,
		GetSession:                   noopHandler,
		GetSessionTranscript:         noopHandler,
		GetSessionDiff:               noopHandler,
		ListSessionSubagents:         noopHandler,
		GetSessionSubagentTranscript: noopHandler,
	})

	mux := http.NewServeMux()
	mod.Register(mux)

	routes := []struct {
		method string
		path   string
	}{
		{"GET", "/api/workspaces/test-ws/issues/issue1/sessions"},
		{"GET", "/api/workspaces/test-ws/issues/issue1/sessions/rec1/scrollback"},
		{"GET", "/api/workspaces/test-ws/tasks/task1/sessions"},
		{"GET", "/api/workspaces/test-ws/tasks/task1/sessions/sess1"},
		{"GET", "/api/workspaces/test-ws/tasks/task1/sessions/sess1/transcript"},
		{"GET", "/api/workspaces/test-ws/tasks/task1/sessions/sess1/diff"},
		{"GET", "/api/workspaces/test-ws/tasks/task1/sessions/sess1/subagents"},
		{"GET", "/api/workspaces/test-ws/tasks/task1/sessions/sess1/subagents/agent-789/transcript"},
	}

	for _, rt := range routes {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(rt.method, rt.path, nil)
		mux.ServeHTTP(rec, req)

		if rec.Code == http.StatusNotFound {
			t.Errorf("%s %s: got 404, route not registered", rt.method, rt.path)
		}
		if rec.Code == http.StatusMethodNotAllowed {
			t.Errorf("%s %s: got 405, wrong method registered", rt.method, rt.path)
		}
	}
}

func TestSessionModule_AllRoutesUnconditional(t *testing.T) {
	// All 8 routes register regardless of whether the underlying stores are nil.
	// The SessionService handles nil stores internally.
	mod := NewSessionModule(&stubSessionService{}, SessionModuleOpts{})

	mux := http.NewServeMux()
	mod.Register(mux) // must not panic

	// Verify session history routes are always registered (not conditional)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/workspaces/test-ws/issues/issue1/sessions", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Error("session history route should be registered unconditionally")
	}
}

func TestSessionModule_WrongMethod_Returns405(t *testing.T) {
	mod := NewSessionModule(&stubSessionService{}, SessionModuleOpts{
		ListTaskSessions:             noopHandler,
		GetSession:                   noopHandler,
		GetSessionTranscript:         noopHandler,
		GetSessionDiff:               noopHandler,
		ListSessionSubagents:         noopHandler,
		GetSessionSubagentTranscript: noopHandler,
	})

	mux := http.NewServeMux()
	mod.Register(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/workspaces/test-ws/tasks/task1/sessions", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST .../tasks/task1/sessions: expected 405, got %d", rec.Code)
	}
}

func TestSessionModule_NilDeps(t *testing.T) {
	mod := NewSessionModule(nil, SessionModuleOpts{})

	mux := http.NewServeMux()
	mod.Register(mux) // must not panic during registration
}

func TestSessionModule_SubagentRoutesUseInjectedHandlers(t *testing.T) {
	t.Setenv("LOOM_SERVE_FROM_EVENTSTORE", "1")

	runtimeDir := t.TempDir()
	sessStore, err := sessions.NewStore(runtimeDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sessionID := createModuleTestSession(t, sessStore, "TASK-3")
	emptySessionID := createModuleTestSession(t, sessStore, "TASK-EMPTY")

	es := eventstore.Open(sessStore.SessionDir(sessionID))
	if err := es.AppendEnvelope(hwtranscript.EventEnvelope{
		RunID:            "run-1",
		Harness:          "claude",
		HarnessSessionID: "agent-789",
		ParentSessionID:  "parent-native",
		Event: hwtranscript.Event{
			Seq:       0,
			Timestamp: time.Unix(2, 0),
			Role:      "assistant",
			Type:      "text",
			Text:      "subagent from event store",
		},
	}); err != nil {
		t.Fatalf("append eventstore subagent: %v", err)
	}

	sessSvc := svcimpl.NewSessionServiceWithRuntimeDir(nil, nil, runtimeDir)
	mod := NewSessionModule(sessSvc, SessionModuleOpts{
		ListSessionSubagents:         misc.HandleListSessionSubagents(sessSvc),
		GetSessionSubagentTranscript: misc.HandleGetSessionSubagentTranscript(sessSvc),
	})

	mux := http.NewServeMux()
	mod.Register(mux)

	rec := serveWorkspaceRoute(mux, http.MethodGet, "/api/workspaces/WS/tasks/TASK-3/sessions/"+sessionID+"/subagents")
	if rec.Code != http.StatusOK {
		t.Fatalf("subagents status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var listResp misc.SubagentListResponse
	if err := json.NewDecoder(rec.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode subagents: %v", err)
	}
	if !listResp.Success || listResp.Data == nil {
		t.Fatalf("subagents response = %+v", listResp)
	}
	if listResp.Data.SessionID != sessionID {
		t.Fatalf("subagents session_id = %q, want %q", listResp.Data.SessionID, sessionID)
	}
	if len(listResp.Data.SubagentIDs) != 1 || listResp.Data.SubagentIDs[0] != "agent-789" {
		t.Fatalf("subagent_ids = %v, want [agent-789]", listResp.Data.SubagentIDs)
	}

	rec = serveWorkspaceRoute(mux, http.MethodGet, "/api/workspaces/WS/tasks/TASK-EMPTY/sessions/"+emptySessionID+"/subagents")
	if rec.Code != http.StatusOK {
		t.Fatalf("empty subagents status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var emptyResp misc.SubagentListResponse
	if err := json.NewDecoder(rec.Body).Decode(&emptyResp); err != nil {
		t.Fatalf("decode empty subagents: %v", err)
	}
	if !emptyResp.Success || emptyResp.Data == nil {
		t.Fatalf("empty subagents response = %+v", emptyResp)
	}
	if len(emptyResp.Data.SubagentIDs) != 0 {
		t.Fatalf("empty subagent_ids = %v, want []", emptyResp.Data.SubagentIDs)
	}

	rec = serveWorkspaceRoute(mux, http.MethodGet, "/api/workspaces/WS/tasks/TASK-3/sessions/"+sessionID+"/subagents/agent-789/transcript")
	if rec.Code != http.StatusOK {
		t.Fatalf("subagent transcript status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var transcriptResp misc.TranscriptResponse
	if err := json.NewDecoder(rec.Body).Decode(&transcriptResp); err != nil {
		t.Fatalf("decode subagent transcript: %v", err)
	}
	if !transcriptResp.Success || transcriptResp.Data == nil {
		t.Fatalf("subagent transcript response = %+v", transcriptResp)
	}
	if transcriptResp.Data.SessionID != sessionID+"/agent-789" {
		t.Fatalf("transcript session_id = %q, want %q", transcriptResp.Data.SessionID, sessionID+"/agent-789")
	}
	if len(transcriptResp.Data.Entries) != 1 || transcriptResp.Data.Entries[0].Text != "subagent from event store" {
		t.Fatalf("transcript entries = %+v", transcriptResp.Data.Entries)
	}
}

func createModuleTestSession(t *testing.T, store *sessions.Store, taskID string) string {
	t.Helper()
	sess, err := store.CreateSession(sessions.CreateOptions{
		AgentName: "worker-1",
		Backend:   "claude",
		Phase:     "implementation",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := sess.Finalize(sessions.FinalizeOptions{TaskID: taskID, ExitCode: 0}); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	return sess.SessionID()
}

func serveWorkspaceRoute(mux *http.ServeMux, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "WS"))
	mux.ServeHTTP(rec, req)
	return rec
}
