package webui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockArgvSpawner implements argvSpawner for testing handleCreateLeadSession
// without touching a real tmux binary.
type mockArgvSpawner struct {
	spawnCreated bool
	spawnErr     error
	// Captured on the last call, for argv assertions.
	lastName string
	lastArgv []string
	lastCols uint16
	lastRows uint16
}

func (m *mockArgvSpawner) SpawnArgv(name string, argv []string, cols, rows uint16) (bool, error) {
	m.lastName = name
	m.lastArgv = argv
	m.lastCols = cols
	m.lastRows = rows
	if m.spawnErr != nil {
		return false, m.spawnErr
	}
	return m.spawnCreated, nil
}

func postLeadSession(handler http.HandlerFunc, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/lead-session", strings.NewReader(body))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func TestHandleCreateLeadSession_HappyPath(t *testing.T) {
	mock := &mockArgvSpawner{spawnCreated: true}
	handler := handleCreateLeadSessionImpl(mock)

	rr := postLeadSession(handler, `{"message":"add dark mode toggle","backend":"claude"}`)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp leadSessionResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true, got false; error=%s", resp.Error)
	}
	if resp.Data == nil {
		t.Fatal("expected non-nil data")
	}
	if !strings.HasPrefix(resp.Data.SessionName, "lead-claude-") {
		t.Errorf("session_name = %q, want prefix %q", resp.Data.SessionName, "lead-claude-")
	}
	if resp.Data.Backend != "claude" {
		t.Errorf("backend = %q, want %q", resp.Data.Backend, "claude")
	}

	// Verify the spawner received the argv we built, with the message passed
	// as a separate argv element (not shell-interpolated).
	wantArgv := []string{"loom", "lead", "--backend", "claude", "--message", "add dark mode toggle"}
	if fmt.Sprint(mock.lastArgv) != fmt.Sprint(wantArgv) {
		t.Errorf("argv = %v, want %v", mock.lastArgv, wantArgv)
	}
	if mock.lastName != resp.Data.SessionName {
		t.Errorf("spawned name = %q, response session_name = %q", mock.lastName, resp.Data.SessionName)
	}
}

func TestHandleCreateLeadSession_NilManager(t *testing.T) {
	handler := handleCreateLeadSession(nil)

	rr := postLeadSession(handler, `{"message":"hi","backend":"claude"}`)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}
	var resp leadSessionResponse
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if resp.Error == "" {
		t.Error("expected non-empty error")
	}
}

func TestHandleCreateLeadSession_MalformedJSON(t *testing.T) {
	mock := &mockArgvSpawner{spawnCreated: true}
	handler := handleCreateLeadSessionImpl(mock)

	rr := postLeadSession(handler, `{not json`)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestHandleCreateLeadSession_MissingMessage(t *testing.T) {
	mock := &mockArgvSpawner{spawnCreated: true}
	handler := handleCreateLeadSessionImpl(mock)

	rr := postLeadSession(handler, `{"backend":"claude"}`)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
	var resp leadSessionResponse
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if !strings.Contains(resp.Error, "message is required") {
		t.Errorf("error = %q, want to contain %q", resp.Error, "message is required")
	}
}

func TestHandleCreateLeadSession_WhitespaceMessage(t *testing.T) {
	mock := &mockArgvSpawner{spawnCreated: true}
	handler := handleCreateLeadSessionImpl(mock)

	rr := postLeadSession(handler, `{"message":"   \t  ","backend":"claude"}`)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestHandleCreateLeadSession_MissingBackend(t *testing.T) {
	mock := &mockArgvSpawner{spawnCreated: true}
	handler := handleCreateLeadSessionImpl(mock)

	rr := postLeadSession(handler, `{"message":"hello"}`)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
	var resp leadSessionResponse
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if !strings.Contains(resp.Error, "backend is required") {
		t.Errorf("error = %q, want to contain %q", resp.Error, "backend is required")
	}
}

func TestHandleCreateLeadSession_InvalidBackend(t *testing.T) {
	mock := &mockArgvSpawner{spawnCreated: true}
	handler := handleCreateLeadSessionImpl(mock)

	rr := postLeadSession(handler, `{"message":"hello","backend":"nonexistent"}`)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
	var resp leadSessionResponse
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if !strings.Contains(resp.Error, "invalid backend") {
		t.Errorf("error = %q, want to contain %q", resp.Error, "invalid backend")
	}
}

func TestHandleCreateLeadSession_MessageTooLong(t *testing.T) {
	mock := &mockArgvSpawner{spawnCreated: true}
	handler := handleCreateLeadSessionImpl(mock)

	// leadMessageMaxLen+1 bytes of 'a', JSON-encoded
	longMsg := strings.Repeat("a", leadMessageMaxLen+1)
	body := fmt.Sprintf(`{"message":%q,"backend":"claude"}`, longMsg)

	rr := postLeadSession(handler, body)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
	var resp leadSessionResponse
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if !strings.Contains(resp.Error, "too long") {
		t.Errorf("error = %q, want to contain %q", resp.Error, "too long")
	}
}

func TestHandleCreateLeadSession_BodyTooLarge(t *testing.T) {
	mock := &mockArgvSpawner{spawnCreated: true}
	handler := handleCreateLeadSessionImpl(mock)

	// Build a JSON body larger than maxRequestBody.
	payload := make([]byte, maxRequestBody+1024)
	for i := range payload {
		payload[i] = 'x'
	}
	body := fmt.Sprintf(`{"message":%q,"backend":"claude"}`, payload)

	req := httptest.NewRequest(http.MethodPost, "/api/terminal/lead-session", bytes.NewReader([]byte(body)))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestHandleCreateLeadSession_SpawnError(t *testing.T) {
	mock := &mockArgvSpawner{spawnErr: errors.New("tmux failed")}
	handler := handleCreateLeadSessionImpl(mock)

	rr := postLeadSession(handler, `{"message":"hi","backend":"claude"}`)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
	}
	var resp leadSessionResponse
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if !strings.Contains(resp.Error, "failed to spawn") {
		t.Errorf("error = %q, want to contain %q", resp.Error, "failed to spawn")
	}
}

func TestHandleCreateLeadSession_SessionAlreadyExists(t *testing.T) {
	// spawnCreated=false means Spawn reported an idempotent no-op. For a
	// timestamp-based session name this should be vanishingly rare, but the
	// handler treats it as a conflict rather than success.
	mock := &mockArgvSpawner{spawnCreated: false}
	handler := handleCreateLeadSessionImpl(mock)

	rr := postLeadSession(handler, `{"message":"hi","backend":"claude"}`)

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusConflict)
	}
}

func TestHandleCreateLeadSession_AllBackends(t *testing.T) {
	// Sanity check: every backend in validBackends is accepted by the handler
	// and flows through to SpawnArgv with the right argv shape.
	for _, backend := range validBackends {
		t.Run(backend, func(t *testing.T) {
			mock := &mockArgvSpawner{spawnCreated: true}
			handler := handleCreateLeadSessionImpl(mock)

			body := fmt.Sprintf(`{"message":"test","backend":%q}`, backend)
			rr := postLeadSession(handler, body)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusOK, rr.Body.String())
			}
			if len(mock.lastArgv) < 6 {
				t.Fatalf("argv too short: %v", mock.lastArgv)
			}
			if mock.lastArgv[0] != "loom" || mock.lastArgv[1] != "lead" {
				t.Errorf("argv[0:2] = %v, want [loom lead]", mock.lastArgv[0:2])
			}
			if mock.lastArgv[3] != backend {
				t.Errorf("argv[3] = %q, want %q", mock.lastArgv[3], backend)
			}
			if mock.lastArgv[5] != "test" {
				t.Errorf("argv[5] = %q, want %q", mock.lastArgv[5], "test")
			}
		})
	}
}
