package misc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// newTestSessionStore creates a sessions.Store rooted in a temporary directory.
func newTestSessionStore(t *testing.T) *sessions.Store {
	t.Helper()
	dir := t.TempDir()
	store, err := sessions.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

// createTestSession creates a session via the store and finalizes it with the
// given taskID so it appears in SessionsByTask queries.
func createTestSession(t *testing.T, store *sessions.Store, taskID string) *sessions.Session {
	t.Helper()
	sess, err := store.CreateSession(sessions.CreateOptions{
		AgentName:  "testagent",
		Backend:    "claude",
		AttemptNum: 1,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	err = sess.Finalize(sessions.FinalizeOptions{
		TaskID:   taskID,
		ExitCode: 0,
		DiffStats: sessions.DiffStats{
			FilesChanged: 2,
			LinesAdded:   10,
			LinesRemoved: 3,
		},
		DiffPatch: "diff --git a/foo.go b/foo.go\n+hello\n",
	})
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	return sess
}

// --- Tests ---

func TestListTaskSessions_Empty(t *testing.T) {
	store := newTestSessionStore(t)
	handler := handleListTaskSessions(NewSessionService(store, nil))

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/loom-abc123/sessions", nil)
	req.SetPathValue("taskId", "loom-abc123")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp SessionListResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Fatalf("success = false, error = %q", resp.Error)
	}
	if resp.Data == nil {
		t.Fatal("data is nil")
	}
	if resp.Data.TaskID != "loom-abc123" {
		t.Errorf("task_id = %q, want %q", resp.Data.TaskID, "loom-abc123")
	}
	if len(resp.Data.Sessions) != 0 {
		t.Errorf("sessions length = %d, want 0", len(resp.Data.Sessions))
	}
}

func TestListTaskSessions_WithData(t *testing.T) {
	store := newTestSessionStore(t)
	sess := createTestSession(t, store, "loom-xyz789")

	handler := handleListTaskSessions(NewSessionService(store, nil))

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/loom-xyz789/sessions", nil)
	req.SetPathValue("taskId", "loom-xyz789")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp SessionListResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Fatalf("success = false, error = %q", resp.Error)
	}
	if resp.Data == nil {
		t.Fatal("data is nil")
	}
	if len(resp.Data.Sessions) != 1 {
		t.Fatalf("sessions length = %d, want 1", len(resp.Data.Sessions))
	}
	if resp.Data.Sessions[0].SessionID != sess.SessionID() {
		t.Errorf("session_id = %q, want %q", resp.Data.Sessions[0].SessionID, sess.SessionID())
	}
	if resp.Data.Sessions[0].TaskID != "loom-xyz789" {
		t.Errorf("task_id = %q, want %q", resp.Data.Sessions[0].TaskID, "loom-xyz789")
	}
}

type workspaceListStub struct {
	service.SessionService
	items   []service.SessionListItem
	total   int
	err     error
	gotWS   string
	gotOpts service.WorkspaceSessionListOptions
}

func (s *workspaceListStub) ListWorkspaceSessions(_ context.Context, wsID string, opts service.WorkspaceSessionListOptions) ([]service.SessionListItem, int, error) {
	s.gotWS = wsID
	s.gotOpts = opts
	if s.err != nil {
		return nil, 0, s.err
	}
	return s.items, s.total, nil
}

func TestListWorkspaceSessions_DefaultsClampAndResponseShape(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	oldNow := workspaceSessionsNow
	workspaceSessionsNow = func() time.Time { return now }
	t.Cleanup(func() { workspaceSessionsNow = oldNow })

	stub := &workspaceListStub{
		items: []service.SessionListItem{{
			SessionRecord: sessions.SessionRecord{SessionID: "sess-1", AgentName: "nova", Status: sessions.StatusCompleted, StartedAt: now},
			Kind:          domain.AgentSessionKindTask,
		}},
		total: 3,
	}
	handler := handleListWorkspaceSessions(stub)
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/sessions?status=completed&agent_id=nova&kind=task&limit=5000", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if stub.gotOpts.Limit != workspaceSessionsMaxLimit {
		t.Fatalf("limit = %d, want clamp %d", stub.gotOpts.Limit, workspaceSessionsMaxLimit)
	}
	if !stub.gotOpts.Since.Equal(now.Add(-7*24*time.Hour)) || !stub.gotOpts.Until.IsZero() {
		t.Fatalf("since/until = %s/%s, want default since and empty until", stub.gotOpts.Since, stub.gotOpts.Until)
	}
	if stub.gotOpts.Status != domain.AgentSessionCompleted || stub.gotOpts.AgentID != "nova" || stub.gotOpts.Kind != domain.AgentSessionKindTask {
		t.Fatalf("opts = %+v", stub.gotOpts)
	}
	var resp WorkspaceSessionListResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success || resp.Data == nil {
		t.Fatalf("response success/data = %v/%#v error=%q", resp.Success, resp.Data, resp.Error)
	}
	if resp.Data.Total != 3 || resp.Data.Limit != workspaceSessionsMaxLimit || len(resp.Data.Sessions) != 1 {
		t.Fatalf("data = %+v", resp.Data)
	}
}

func TestListWorkspaceSessions_InvalidSinceReturns400(t *testing.T) {
	handler := handleListWorkspaceSessions(&workspaceListStub{})
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/sessions?since=not-a-date", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
	var resp WorkspaceSessionListResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Success || resp.Error != "invalid since: must be RFC3339" {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestListWorkspaceSessions_MissingTotalSkewReturns502(t *testing.T) {
	stub := &workspaceListStub{err: service.ErrBadGateway("fleet-db must be upgraded: agent-sessions list response is missing total for server-side session time filtering")}
	handler := handleListWorkspaceSessions(stub)
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/sessions?since=2026-07-16T00:00:00Z", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadGateway)
	}
	var resp WorkspaceSessionListResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Success || resp.Error != "fleet-db must be upgraded: agent-sessions list response is missing total for server-side session time filtering" {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestWorkspaceSessionHandlers_LocalStore(t *testing.T) {
	t.Setenv("LOOM_REDACT_TRANSCRIPTS", "off")
	store := newTestSessionStore(t)
	sess := createTestSession(t, store, "loom-workspace")
	native := filepath.Join(t.TempDir(), "native.jsonl")
	payload := []byte(`{"type":"user","uuid":"u1","message":{"content":"Hello workspace"}}` + "\n")
	if err := os.WriteFile(native, payload, 0o600); err != nil {
		t.Fatalf("write native: %v", err)
	}
	if err := store.SyncNativeTranscript(sess.SessionID(), native, sessions.TranscriptFormatRaw); err != nil {
		t.Fatalf("SyncNativeTranscript: %v", err)
	}
	svc := NewSessionService(store, nil)

	t.Run("detail", func(t *testing.T) {
		handler := handleGetWorkspaceSession(svc)
		req := httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/sessions/"+sess.SessionID(), nil)
		req.SetPathValue("sessionId", sess.SessionID())
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
	})

	t.Run("transcript", func(t *testing.T) {
		handler := handleGetWorkspaceSessionTranscript(svc)
		req := httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/sessions/"+sess.SessionID()+"/transcript", nil)
		req.SetPathValue("sessionId", sess.SessionID())
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		var resp TranscriptResponse
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !resp.Success || resp.Data == nil || len(resp.Data.Entries) != 1 {
			t.Fatalf("resp = %+v", resp)
		}
	})

	t.Run("diff", func(t *testing.T) {
		handler := handleGetWorkspaceSessionDiff(svc)
		req := httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/sessions/"+sess.SessionID()+"/diff", nil)
		req.SetPathValue("sessionId", sess.SessionID())
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		if rr.Body.String() == "" {
			t.Fatal("diff body is empty")
		}
	})

	t.Run("subagents", func(t *testing.T) {
		handler := handleListWorkspaceSessionSubagents(svc)
		req := httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/sessions/"+sess.SessionID()+"/subagents", nil)
		req.SetPathValue("sessionId", sess.SessionID())
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		var resp SubagentListResponse
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !resp.Success || resp.Data == nil || len(resp.Data.SubagentIDs) != 0 {
			t.Fatalf("resp = %+v", resp)
		}
	})
}

func TestGetSession_NotFound(t *testing.T) {
	store := newTestSessionStore(t)
	handler := handleGetSession(NewSessionService(store, nil))

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/loom-abc/sessions/nonexistent-session", nil)
	req.SetPathValue("taskId", "loom-abc")
	req.SetPathValue("sessionId", "nonexistent-session")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}

	var resp SessionDetailResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Success {
		t.Error("expected success = false")
	}
}

func TestGetSession_Found(t *testing.T) {
	store := newTestSessionStore(t)
	sess := createTestSession(t, store, "loom-task1")

	handler := handleGetSession(NewSessionService(store, nil))

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/loom-task1/sessions/"+sess.SessionID(), nil)
	req.SetPathValue("taskId", "loom-task1")
	req.SetPathValue("sessionId", sess.SessionID())
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp SessionDetailResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Fatalf("success = false, error = %q", resp.Error)
	}
	if resp.Data == nil {
		t.Fatal("data is nil")
	}
	// Finalized sessions are completed, not active.
	if resp.Data.IsActive {
		t.Error("is_active = true, want false for completed session")
	}
	if resp.Data.SessionID != sess.SessionID() {
		t.Errorf("session_id = %q, want %q", resp.Data.SessionID, sess.SessionID())
	}
}

func TestGetSessionTranscript(t *testing.T) {
	t.Setenv("LOOM_REDACT_TRANSCRIPTS", "off")

	store := newTestSessionStore(t)
	sess := createTestSession(t, store, "loom-task2")

	// Seed a Claude Code native transcript and sync it into the session.
	native := filepath.Join(t.TempDir(), "native.jsonl")
	payload := []byte(`{"type":"user","uuid":"u1","message":{"content":"Hello agent"}}` + "\n")
	if err := os.WriteFile(native, payload, 0o600); err != nil {
		t.Fatalf("write native: %v", err)
	}
	if err := store.SyncNativeTranscript(sess.SessionID(), native, sessions.TranscriptFormatRaw); err != nil {
		t.Fatalf("SyncNativeTranscript: %v", err)
	}

	handler := handleGetSessionTranscript(NewSessionService(store, nil))

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/loom-task2/sessions/"+sess.SessionID()+"/transcript", nil)
	req.SetPathValue("taskId", "loom-task2")
	req.SetPathValue("sessionId", sess.SessionID())
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp TranscriptResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Fatalf("success = false, error = %q", resp.Error)
	}
	if resp.Data == nil {
		t.Fatal("data is nil")
	}
	if len(resp.Data.Entries) != 1 {
		t.Fatalf("entries length = %d, want 1", len(resp.Data.Entries))
	}
	if resp.Data.Entries[0].Text != "Hello agent" {
		t.Errorf("text = %q, want %q", resp.Data.Entries[0].Text, "Hello agent")
	}
}

func TestGetSessionDiff_NoDiff(t *testing.T) {
	store := newTestSessionStore(t)
	// Create a session without a diff.
	sess, err := store.CreateSession(sessions.CreateOptions{
		AgentName:  "testagent",
		Backend:    "claude",
		AttemptNum: 1,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// Finalize without DiffPatch.
	err = sess.Finalize(sessions.FinalizeOptions{
		TaskID:   "loom-nodiff",
		ExitCode: 0,
	})
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	handler := handleGetSessionDiff(NewSessionService(store, nil))

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/loom-nodiff/sessions/"+sess.SessionID()+"/diff", nil)
	req.SetPathValue("taskId", "loom-nodiff")
	req.SetPathValue("sessionId", sess.SessionID())
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestGetSessionDiff_WithDiff(t *testing.T) {
	store := newTestSessionStore(t)
	sess := createTestSession(t, store, "loom-withdiff")

	handler := handleGetSessionDiff(NewSessionService(store, nil))

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/loom-withdiff/sessions/"+sess.SessionID()+"/diff", nil)
	req.SetPathValue("taskId", "loom-withdiff")
	req.SetPathValue("sessionId", sess.SessionID())
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "text/plain" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/plain")
	}

	body := rr.Body.String()
	if body == "" {
		t.Error("body is empty, expected diff content")
	}
}

func TestNilStore_503(t *testing.T) {
	handlers := []struct {
		name    string
		handler http.HandlerFunc
		path    string
	}{
		{"ListTaskSessions", handleListTaskSessions(NewSessionService(nil, nil)), "/api/tasks/loom-abc/sessions"},
		{"GetSession", handleGetSession(NewSessionService(nil, nil)), "/api/tasks/loom-abc/sessions/some-session"},
		{"GetSessionTranscript", handleGetSessionTranscript(NewSessionService(nil, nil)), "/api/tasks/loom-abc/sessions/some-session/transcript"},
		{"GetSessionDiff", handleGetSessionDiff(NewSessionService(nil, nil)), "/api/tasks/loom-abc/sessions/some-session/diff"},
	}

	for _, h := range handlers {
		t.Run(h.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, h.path, nil)
			req.SetPathValue("taskId", "loom-abc")
			req.SetPathValue("sessionId", "some-session")
			rr := httptest.NewRecorder()

			h.handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusServiceUnavailable {
				t.Errorf("status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
			}
		})
	}
}

func TestInvalidTaskId_400(t *testing.T) {
	store := newTestSessionStore(t)
	handlers := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"ListTaskSessions", handleListTaskSessions(NewSessionService(store, nil))},
		{"GetSession", handleGetSession(NewSessionService(store, nil))},
		{"GetSessionTranscript", handleGetSessionTranscript(NewSessionService(store, nil))},
		{"GetSessionDiff", handleGetSessionDiff(NewSessionService(store, nil))},
	}

	for _, h := range handlers {
		t.Run(h.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/tasks/../etc/sessions", nil)
			req.SetPathValue("taskId", "../etc")
			req.SetPathValue("sessionId", "valid-session")
			rr := httptest.NewRecorder()

			h.handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestListTaskSessions_EmptyArrayNotNull(t *testing.T) {
	store := newTestSessionStore(t)
	handler := handleListTaskSessions(NewSessionService(store, nil))

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/loom-unknown/sessions", nil)
	req.SetPathValue("taskId", "loom-unknown")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	// Verify JSON contains [] not null for sessions.
	body := rr.Body.String()
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	data, ok := raw["data"].(map[string]interface{})
	if !ok {
		t.Fatal("data is not an object")
	}
	sessionsVal := data["sessions"]
	arr, ok := sessionsVal.([]interface{})
	if !ok {
		t.Fatalf("sessions is %T, want []interface{}", sessionsVal)
	}
	if len(arr) != 0 {
		t.Errorf("sessions length = %d, want 0", len(arr))
	}
}

func TestGetSessionTranscript_EmptyEntries(t *testing.T) {
	store := newTestSessionStore(t)
	sess := createTestSession(t, store, "loom-noentries")

	handler := handleGetSessionTranscript(NewSessionService(store, nil))

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/loom-noentries/sessions/"+sess.SessionID()+"/transcript", nil)
	req.SetPathValue("taskId", "loom-noentries")
	req.SetPathValue("sessionId", sess.SessionID())
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp TranscriptResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Fatalf("success = false, error = %q", resp.Error)
	}
	if resp.Data == nil {
		t.Fatal("data is nil")
	}
	// Should be empty array, not null.
	if len(resp.Data.Entries) != 0 {
		t.Errorf("entries length = %d, want 0", len(resp.Data.Entries))
	}
}

func TestGetSession_IsActive(t *testing.T) {
	store := newTestSessionStore(t)
	// Create a session but do NOT finalize it — it stays in "running" status.
	sess, err := store.CreateSession(sessions.CreateOptions{
		AgentName:  "testagent",
		Backend:    "claude",
		AttemptNum: 1,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Set TaskID on metadata so ownership check passes.
	sess.Meta.TaskID = "loom-active"
	metaJSON, _ := json.Marshal(sess.Meta)
	metaPath := filepath.Join(store.Dir(), sess.SessionID(), "metadata.json")
	if err := os.WriteFile(metaPath, metaJSON, 0o600); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	handler := handleGetSession(NewSessionService(store, nil))

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/loom-active/sessions/"+sess.SessionID(), nil)
	req.SetPathValue("taskId", "loom-active")
	req.SetPathValue("sessionId", sess.SessionID())
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp SessionDetailResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Fatalf("success = false, error = %q", resp.Error)
	}
	if resp.Data == nil {
		t.Fatal("data is nil")
	}
	if !resp.Data.IsActive {
		t.Error("is_active = false, want true for running session")
	}
}
