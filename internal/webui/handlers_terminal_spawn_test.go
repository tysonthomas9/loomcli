package webui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockSpawner implements the terminalSpawner interface for testing.
type mockSpawner struct {
	spawnCreated bool
	spawnErr     error
}

func (m *mockSpawner) Spawn(name, command string, cols, rows uint16) (bool, error) {
	if m.spawnErr != nil {
		return false, m.spawnErr
	}
	return m.spawnCreated, nil
}

func TestHandleTerminalSpawn_HappyPath(t *testing.T) {
	mock := &mockSpawner{spawnCreated: true}
	handler := handleTerminalSpawnImpl(mock)

	body := `{"session_name":"my-session","backend":"claude"}`
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/spawn", strings.NewReader(body))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp terminalSpawnResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true, got false; error=%s", resp.Error)
	}
	if resp.Data == nil {
		t.Fatal("expected non-nil data")
	}
	if !resp.Data.Created {
		t.Error("expected created=true")
	}
	if resp.Data.SessionName != "my-session" {
		t.Errorf("session_name = %q, want %q", resp.Data.SessionName, "my-session")
	}
	if resp.Data.Backend != "claude" {
		t.Errorf("backend = %q, want %q", resp.Data.Backend, "claude")
	}
	if resp.Data.Command != "claude" {
		t.Errorf("command = %q, want %q", resp.Data.Command, "claude")
	}
}

func TestHandleTerminalSpawn_Idempotent(t *testing.T) {
	mock := &mockSpawner{spawnCreated: false}
	handler := handleTerminalSpawnImpl(mock)

	body := `{"session_name":"existing-session","backend":"claude"}`
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/spawn", strings.NewReader(body))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp terminalSpawnResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true, got false; error=%s", resp.Error)
	}
	if resp.Data == nil {
		t.Fatal("expected non-nil data")
	}
	if resp.Data.Created {
		t.Error("expected created=false for idempotent call")
	}
}

func TestHandleTerminalSpawn_DotSanitization(t *testing.T) {
	mock := &mockSpawner{spawnCreated: true}
	handler := handleTerminalSpawnImpl(mock)

	body := `{"session_name":"issue-abc.5-claude-1","backend":"claude"}`
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/spawn", strings.NewReader(body))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp terminalSpawnResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true, got false; error=%s", resp.Error)
	}
	if resp.Data == nil {
		t.Fatal("expected non-nil data")
	}
	if resp.Data.SessionName != "issue-abc-5-claude-1" {
		t.Errorf("session_name = %q, want %q (dots replaced with dashes)", resp.Data.SessionName, "issue-abc-5-claude-1")
	}
}

func TestHandleTerminalSpawn_MissingSessionName(t *testing.T) {
	mock := &mockSpawner{spawnCreated: true}
	handler := handleTerminalSpawnImpl(mock)

	body := `{"backend":"claude"}`
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/spawn", strings.NewReader(body))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}

	var resp terminalSpawnResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Success {
		t.Error("expected success=false")
	}
	if !strings.Contains(resp.Error, "missing required field: session_name") {
		t.Errorf("error = %q, want it to contain %q", resp.Error, "missing required field: session_name")
	}
}

func TestHandleTerminalSpawn_MissingBackend(t *testing.T) {
	mock := &mockSpawner{spawnCreated: true}
	handler := handleTerminalSpawnImpl(mock)

	body := `{"session_name":"my-session"}`
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/spawn", strings.NewReader(body))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}

	var resp terminalSpawnResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Success {
		t.Error("expected success=false")
	}
	if !strings.Contains(resp.Error, "missing required field: backend") {
		t.Errorf("error = %q, want it to contain %q", resp.Error, "missing required field: backend")
	}
}

func TestHandleTerminalSpawn_InvalidSessionNameAfterSanitization(t *testing.T) {
	mock := &mockSpawner{spawnCreated: true}
	handler := handleTerminalSpawnImpl(mock)

	body := `{"session_name":"bad name!","backend":"claude"}`
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/spawn", strings.NewReader(body))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}

	var resp terminalSpawnResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Success {
		t.Error("expected success=false")
	}
	if !strings.Contains(resp.Error, "invalid session name") {
		t.Errorf("error = %q, want it to contain %q", resp.Error, "invalid session name")
	}
}

func TestHandleTerminalSpawn_InvalidBackend(t *testing.T) {
	mock := &mockSpawner{spawnCreated: true}
	handler := handleTerminalSpawnImpl(mock)

	body := `{"session_name":"my-session","backend":"invalid-backend"}`
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/spawn", strings.NewReader(body))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}

	var resp terminalSpawnResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Success {
		t.Error("expected success=false")
	}
	if !strings.Contains(resp.Error, "invalid backend") {
		t.Errorf("error = %q, want it to contain %q", resp.Error, "invalid backend")
	}
	// Check that valid options are listed in the error message.
	for _, backend := range validBackends {
		if !strings.Contains(resp.Error, backend) {
			t.Errorf("error = %q, want it to list valid backend %q", resp.Error, backend)
		}
	}
}

func TestHandleTerminalSpawn_NilManager(t *testing.T) {
	handler := handleTerminalSpawn(nil)

	body := `{"session_name":"my-session","backend":"claude"}`
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/spawn", strings.NewReader(body))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusServiceUnavailable, rr.Body.String())
	}

	var resp terminalSpawnResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Success {
		t.Error("expected success=false")
	}
	if !strings.Contains(resp.Error, "terminal manager not initialized") {
		t.Errorf("error = %q, want it to contain %q", resp.Error, "terminal manager not initialized")
	}
}

func TestHandleTerminalSpawn_TmuxFailure(t *testing.T) {
	mock := &mockSpawner{
		spawnErr: fmt.Errorf("tmux new-session: exit status 1"),
	}
	handler := handleTerminalSpawnImpl(mock)

	body := `{"session_name":"my-session","backend":"claude"}`
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/spawn", strings.NewReader(body))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusInternalServerError, rr.Body.String())
	}

	var resp terminalSpawnResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Success {
		t.Error("expected success=false")
	}
	if !strings.Contains(resp.Error, "failed to spawn terminal session") {
		t.Errorf("error = %q, want it to contain %q", resp.Error, "failed to spawn terminal session")
	}
}

func TestHandleTerminalSpawn_MalformedJSON(t *testing.T) {
	mock := &mockSpawner{spawnCreated: true}
	handler := handleTerminalSpawnImpl(mock)

	body := `{invalid json`
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/spawn", strings.NewReader(body))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}

	var resp terminalSpawnResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Success {
		t.Error("expected success=false")
	}
	if !strings.Contains(resp.Error, "invalid request body") {
		t.Errorf("error = %q, want it to contain %q", resp.Error, "invalid request body")
	}
}

func TestHandleTerminalSpawn_OversizedBody(t *testing.T) {
	mock := &mockSpawner{spawnCreated: true}
	handler := handleTerminalSpawnImpl(mock)

	// Create a body larger than 1MB (maxRequestBody)
	largeData := make([]byte, maxRequestBody+1)
	for i := range largeData {
		largeData[i] = 'a'
	}
	body := `{"session_name":"` + string(largeData) + `","backend":"claude"}`
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/spawn", strings.NewReader(body))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusRequestEntityTooLarge, rr.Body.String())
	}

	var resp terminalSpawnResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Success {
		t.Error("expected success=false")
	}
	if !strings.Contains(resp.Error, "request body too large") {
		t.Errorf("error = %q, want it to contain %q", resp.Error, "request body too large")
	}
}

func TestHandleTerminalSpawn_ShellBackend(t *testing.T) {
	mock := &mockSpawner{spawnCreated: true}
	handler := handleTerminalSpawnImpl(mock)

	body := `{"session_name":"lead-shell-1","backend":"shell"}`
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/spawn", strings.NewReader(body))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp terminalSpawnResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true, got false; error=%s", resp.Error)
	}
	if resp.Data == nil {
		t.Fatal("expected non-nil data")
	}
	if resp.Data.Backend != "shell" {
		t.Errorf("backend = %q, want %q", resp.Data.Backend, "shell")
	}
	// The command should be the shell path, NOT the literal string "shell"
	if resp.Data.Command == "shell" {
		t.Error("command should be the shell executable path, not the literal string \"shell\"")
	}
	// The command should be a non-empty shell path (e.g., /bin/bash or $SHELL)
	if resp.Data.Command == "" {
		t.Error("command should not be empty for shell backend")
	}
	if resp.Data.SessionName != "lead-shell-1" {
		t.Errorf("session_name = %q, want %q", resp.Data.SessionName, "lead-shell-1")
	}
	if !resp.Data.Created {
		t.Error("expected created=true")
	}
}

func TestHandleTerminalSpawn_ShellBackendUsesShellCommand(t *testing.T) {
	mock := &mockSpawner{spawnCreated: true}
	handler := handleTerminalSpawnImpl(mock)

	body := `{"session_name":"lead-shell-2","backend":"shell"}`
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/spawn", strings.NewReader(body))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp terminalSpawnResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true, got false; error=%s", resp.Error)
	}
	if resp.Data == nil {
		t.Fatal("expected non-nil data")
	}
	// The command should match shellCommand() output
	expected := shellCommand()
	if resp.Data.Command != expected {
		t.Errorf("command = %q, want shellCommand() = %q", resp.Data.Command, expected)
	}
}
