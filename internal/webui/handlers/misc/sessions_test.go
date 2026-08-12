package misc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/artifacts/transcript"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
	"github.com/tysonthomas9/loomcli/internal/webui/sessioncoord"
)

// newTestSessionStore creates a sessions.Store rooted in a temporary directory.
func newTestSessionStore(t *testing.T) *sessions.Store {
	t.Helper()
	dir := t.TempDir()
	store, err := sessions.NewStore(t.Context(), dir)
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

func TestSessionContentHandlersPreserveUnavailable(t *testing.T) {
	base := NewSessionService(nil, nil)
	svc := &sessionContentErrorService{
		SessionService: base,
		err:            apperrors.ErrUnavailable("managed content temporarily unavailable"),
	}
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		path    string
	}{
		{
			name:    "transcript",
			handler: handleGetSessionTranscript(svc),
			path:    "/api/tasks/TASK-1/sessions/session-1/transcript",
		},
		{
			name:    "diff",
			handler: handleGetSessionDiff(svc),
			path:    "/api/tasks/TASK-1/sessions/session-1/diff",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.SetPathValue("taskId", "TASK-1")
			req.SetPathValue("sessionId", "session-1")
			rr := httptest.NewRecorder()
			tc.handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d body=%s, want 503", rr.Code, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), "managed content temporarily unavailable") {
				t.Fatalf("body = %s", rr.Body.String())
			}
		})
	}
}

type sessionContentErrorService struct {
	sessioncoord.SessionService
	err error
}

func (s *sessionContentErrorService) GetSessionTranscript(
	context.Context,
	string,
	string,
	string,
) ([]transcript.Event, error) {
	return nil, s.err
}

func (s *sessionContentErrorService) GetSessionDiff(
	context.Context,
	string,
	string,
	string,
) (string, error) {
	return "", s.err
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
