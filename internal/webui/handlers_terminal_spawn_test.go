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
	// Captured on the last call, for workspace-cwd assertions.
	lastWSID    string
	lastName    string
	lastCommand string
	lastWorkDir string
}

func (m *mockSpawner) SpawnInDir(wsID, name, command string, cols, rows uint16, workDir string) (bool, error) {
	m.lastWSID = wsID
	m.lastName = name
	m.lastCommand = command
	m.lastWorkDir = workDir
	if m.spawnErr != nil {
		return false, m.spawnErr
	}
	return m.spawnCreated, nil
}

func TestHandleTerminalSpawn_HappyPath(t *testing.T) {
	mock := &mockSpawner{spawnCreated: true}
	handler := handleTerminalSpawnImpl(mock)

	body := `{"session_name":"my-session","backend":"claude"}`
	req := withWorkspaceCtx(
		httptest.NewRequest(http.MethodPost, "/api/terminal/spawn", strings.NewReader(body)),
		"ws-test",
	)
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
	req := withWorkspaceCtx(
		httptest.NewRequest(http.MethodPost, "/api/terminal/spawn", strings.NewReader(body)),
		"ws-test",
	)
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
	req := withWorkspaceCtx(
		httptest.NewRequest(http.MethodPost, "/api/terminal/spawn", strings.NewReader(body)),
		"ws-test",
	)
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
	handler := handleTerminalSpawn(nil, nil, nil)

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
	req := withWorkspaceCtx(
		httptest.NewRequest(http.MethodPost, "/api/terminal/spawn", strings.NewReader(body)),
		"ws-test",
	)
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
	req := withWorkspaceCtx(
		httptest.NewRequest(http.MethodPost, "/api/terminal/spawn", strings.NewReader(body)),
		"ws-test",
	)
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

func TestExtractIssueID(t *testing.T) {
	tests := []struct {
		sessionName string
		want        string
	}{
		{"issue-loomcli-fghge-1", "loomcli-fghge.1"},
		{"talk-to-lead", ""},
		{"issue-proj-42", "proj.42"},
		{"issue-my-project-name-99", "my-project-name.99"},
		{"issue-a-0", "a.0"},
		{"not-an-issue", ""},
		{"", ""},
		{"issue-", ""},
		{"issue--1", ""},
	}

	for _, tt := range tests {
		got := extractIssueID(tt.sessionName)
		if got != tt.want {
			t.Errorf("extractIssueID(%q) = %q, want %q", tt.sessionName, got, tt.want)
		}
	}
}

func TestHandleTerminalSpawn_WorkspaceCwd(t *testing.T) {
	// When the request is routed through WorkspaceMiddleware, the handler
	// reads the workspace ID from the context and resolves it to an on-disk
	// path, which flows through to SpawnInDir as the tmux session's cwd.
	mock := &mockSpawner{spawnCreated: true}
	cfg := func(id string) (*WorkspaceData, error) {
		if id == "ws-paperclip" {
			return &WorkspaceData{ID: id, Path: "/home/loom/.loom/workspaces/paperclip"}, nil
		}
		return nil, fmt.Errorf("unknown workspace %q", id)
	}
	handler := handleTerminalSpawnImplWithHistory(mock, nil, cfg)

	body := `{"session_name":"my-session","backend":"claude"}`
	req := withWorkspaceCtx(
		httptest.NewRequest(http.MethodPost, "/api/workspaces/ws-paperclip/terminal/spawn", strings.NewReader(body)),
		"ws-paperclip",
	)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
	if mock.lastWorkDir != "/home/loom/.loom/workspaces/paperclip" {
		t.Errorf("workDir = %q, want %q", mock.lastWorkDir, "/home/loom/.loom/workspaces/paperclip")
	}
	if mock.lastCommand != "claude" {
		t.Errorf("command = %q, want %q", mock.lastCommand, "claude")
	}
}

func TestHandleTerminalSpawn_NoWorkspaceContext(t *testing.T) {
	// When the handler is called without a workspace context (e.g., tests
	// that bypass WorkspaceMiddleware), the new workspace-aware
	// TerminalManager API requires a non-empty wsID, so the handler rejects
	// the request with 400 before touching the spawner or the workspace
	// config lookup.
	mock := &mockSpawner{spawnCreated: true}
	cfg := func(id string) (*WorkspaceData, error) {
		t.Errorf("workspace lookup should not be called when context has no wsID; got %q", id)
		return nil, nil
	}
	handler := handleTerminalSpawnImplWithHistory(mock, nil, cfg)

	body := `{"session_name":"my-session","backend":"claude"}`
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
	if !strings.Contains(resp.Error, "workspace context required") {
		t.Errorf("error = %q, want to contain %q", resp.Error, "workspace context required")
	}
	if mock.lastName != "" {
		t.Errorf("spawner should not have been called; lastName = %q", mock.lastName)
	}
}

func TestHandleTerminalSpawn_ShellBackendUsesShellCommand(t *testing.T) {
	mock := &mockSpawner{spawnCreated: true}
	handler := handleTerminalSpawnImpl(mock)

	body := `{"session_name":"lead-shell-2","backend":"shell"}`
	req := withWorkspaceCtx(
		httptest.NewRequest(http.MethodPost, "/api/terminal/spawn", strings.NewReader(body)),
		"ws-test",
	)
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
