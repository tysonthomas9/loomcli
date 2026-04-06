package misc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/sessions"
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

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/bd-abc123/sessions", nil)
	req.SetPathValue("taskId", "bd-abc123")
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
	if resp.Data.TaskID != "bd-abc123" {
		t.Errorf("task_id = %q, want %q", resp.Data.TaskID, "bd-abc123")
	}
	if len(resp.Data.Sessions) != 0 {
		t.Errorf("sessions length = %d, want 0", len(resp.Data.Sessions))
	}
}

func TestListTaskSessions_WithData(t *testing.T) {
	store := newTestSessionStore(t)
	sess := createTestSession(t, store, "bd-xyz789")

	handler := handleListTaskSessions(NewSessionService(store, nil))

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/bd-xyz789/sessions", nil)
	req.SetPathValue("taskId", "bd-xyz789")
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
	if resp.Data.Sessions[0].TaskID != "bd-xyz789" {
		t.Errorf("task_id = %q, want %q", resp.Data.Sessions[0].TaskID, "bd-xyz789")
	}
}

func TestGetSession_NotFound(t *testing.T) {
	store := newTestSessionStore(t)
	handler := handleGetSession(NewSessionService(store, nil))

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/bd-abc/sessions/nonexistent-session", nil)
	req.SetPathValue("taskId", "bd-abc")
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
	sess := createTestSession(t, store, "bd-task1")

	handler := handleGetSession(NewSessionService(store, nil))

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/bd-task1/sessions/"+sess.SessionID(), nil)
	req.SetPathValue("taskId", "bd-task1")
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
	store := newTestSessionStore(t)
	sess := createTestSession(t, store, "bd-task2")

	// Append a transcript entry.
	err := store.AppendTranscript(sess.SessionID(), sessions.TranscriptEntry{
		Seq:       1,
		Timestamp: time.Now().UTC(),
		Role:      "user",
		Type:      "text",
		Content:   "Hello agent",
	})
	if err != nil {
		t.Fatalf("AppendTranscript: %v", err)
	}

	handler := handleGetSessionTranscript(NewSessionService(store, nil))

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/bd-task2/sessions/"+sess.SessionID()+"/transcript", nil)
	req.SetPathValue("taskId", "bd-task2")
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
	if resp.Data.Entries[0].Content != "Hello agent" {
		t.Errorf("content = %q, want %q", resp.Data.Entries[0].Content, "Hello agent")
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
		TaskID:   "bd-nodiff",
		ExitCode: 0,
	})
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	handler := handleGetSessionDiff(NewSessionService(store, nil))

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/bd-nodiff/sessions/"+sess.SessionID()+"/diff", nil)
	req.SetPathValue("taskId", "bd-nodiff")
	req.SetPathValue("sessionId", sess.SessionID())
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestGetSessionDiff_WithDiff(t *testing.T) {
	store := newTestSessionStore(t)
	sess := createTestSession(t, store, "bd-withdiff")

	handler := handleGetSessionDiff(NewSessionService(store, nil))

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/bd-withdiff/sessions/"+sess.SessionID()+"/diff", nil)
	req.SetPathValue("taskId", "bd-withdiff")
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
		{"ListTaskSessions", handleListTaskSessions(NewSessionService(nil, nil)), "/api/tasks/bd-abc/sessions"},
		{"GetSession", handleGetSession(NewSessionService(nil, nil)), "/api/tasks/bd-abc/sessions/some-session"},
		{"GetSessionTranscript", handleGetSessionTranscript(NewSessionService(nil, nil)), "/api/tasks/bd-abc/sessions/some-session/transcript"},
		{"GetSessionDiff", handleGetSessionDiff(NewSessionService(nil, nil)), "/api/tasks/bd-abc/sessions/some-session/diff"},
	}

	for _, h := range handlers {
		t.Run(h.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, h.path, nil)
			req.SetPathValue("taskId", "bd-abc")
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

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/bd-unknown/sessions", nil)
	req.SetPathValue("taskId", "bd-unknown")
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
	sess := createTestSession(t, store, "bd-noentries")

	handler := handleGetSessionTranscript(NewSessionService(store, nil))

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/bd-noentries/sessions/"+sess.SessionID()+"/transcript", nil)
	req.SetPathValue("taskId", "bd-noentries")
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
	sess.Meta.TaskID = "bd-active"
	metaJSON, _ := json.Marshal(sess.Meta)
	metaPath := filepath.Join(store.Dir(), sess.SessionID(), "metadata.json")
	if err := os.WriteFile(metaPath, metaJSON, 0o600); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	handler := handleGetSession(NewSessionService(store, nil))

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/bd-active/sessions/"+sess.SessionID(), nil)
	req.SetPathValue("taskId", "bd-active")
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
