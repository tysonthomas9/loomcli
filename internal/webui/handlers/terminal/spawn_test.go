package terminal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

// mockSpawnService implements TerminalService for spawn handler tests.
// Only SpawnSession is used by handleTerminalSpawn; all other methods panic.
type mockSpawnService struct {
	spawnFunc func(ctx context.Context, wsID string, params *SpawnParams) (*SpawnResult, error)
}

func (m *mockSpawnService) SpawnSession(ctx context.Context, wsID string, params *SpawnParams) (*SpawnResult, error) {
	if m.spawnFunc != nil {
		return m.spawnFunc(ctx, wsID, params)
	}
	return nil, service.ErrUnavailable("not configured")
}

// --- Stub methods required by TerminalService interface ---
func (m *mockSpawnService) CreateLeadSession(_ context.Context, _ string, _ *LeadSessionParams) (*LeadSessionResult, error) {
	panic("not implemented")
}
func (m *mockSpawnService) GenerateToken(_ context.Context, _, _ string) (string, error) {
	panic("not implemented")
}
func (m *mockSpawnService) RestartSession(_ context.Context, _, _ string) (*TerminalRestartResult, error) {
	panic("not implemented")
}
func (m *mockSpawnService) KillSession(_ context.Context, _ string) error {
	panic("not implemented")
}
func (m *mockSpawnService) GetSessionStatus(_ context.Context, _ string) (*TerminalStatusResult, error) {
	panic("not implemented")
}
func (m *mockSpawnService) ListSessions(_ context.Context, _ string) ([]TerminalSessionInfo, error) {
	panic("not implemented")
}
func (m *mockSpawnService) SeedSession(_ context.Context, _ string, _ *SeedParams) error {
	panic("not implemented")
}
func (m *mockSpawnService) ScheduleKill(_ context.Context, _ string) error {
	panic("not implemented")
}
func (m *mockSpawnService) CloseAllSessions(_ context.Context) (*CloseAllResult, error) {
	panic("not implemented")
}
func (m *mockSpawnService) ExportSession(_ context.Context, _ string) (string, error) {
	panic("not implemented")
}
func (m *mockSpawnService) GetScrollbackInfo(_ context.Context, _ string) (*ScrollbackInfoResult, error) {
	panic("not implemented")
}
func (m *mockSpawnService) GetScrollback(_ context.Context, _ string) (*ScrollbackResult, error) {
	panic("not implemented")
}
func (m *mockSpawnService) ListTabs(_ context.Context, _ string) ([]tabmeta.TabMetadata, error) {
	panic("not implemented")
}
func (m *mockSpawnService) GetTab(_ context.Context, _, _ string) (*tabmeta.TabMetadata, error) {
	panic("not implemented")
}
func (m *mockSpawnService) PatchTab(_ context.Context, _, _ string, _ map[string]string) (*PatchTabResult, error) {
	panic("not implemented")
}
func (m *mockSpawnService) PutTab(_ context.Context, _ string, _ *tabmeta.TabMetadata) error {
	panic("not implemented")
}
func (m *mockSpawnService) DeleteTab(_ context.Context, _, _ string) error {
	panic("not implemented")
}
func (m *mockSpawnService) ListSessionsByIssue(_ context.Context) (map[string][]string, error) {
	panic("not implemented")
}
func (m *mockSpawnService) GetTerminalState(_ context.Context, _ string) (string, error) {
	panic("not implemented")
}
func (m *mockSpawnService) PatchTerminalState(_ context.Context, _, _ string) error {
	panic("not implemented")
}

// newMockSpawnService creates a mock that delegates spawn to the service impl
// (which handles validation) but uses a custom spawner function for the actual
// tmux spawn step. This simulates the old mockSpawner pattern.
func newMockSpawnService(spawnCreated bool, spawnErr error) TerminalService {
	return &mockSpawnService{
		spawnFunc: func(_ context.Context, _ string, params *SpawnParams) (*SpawnResult, error) {
			// Replicate the service's validation logic
			if params.SessionName == "" {
				return nil, service.ErrValidation("missing required field: session_name")
			}
			if params.Backend == "" {
				return nil, service.ErrValidation("missing required field: backend")
			}

			sanitizedName := strings.ReplaceAll(params.SessionName, ".", "-")
			if !validSessionName.MatchString(sanitizedName) {
				return nil, service.ErrValidation(fmt.Sprintf("invalid session name %q after sanitization: must match [a-zA-Z0-9_-]+", sanitizedName))
			}

			var command string
			if params.Backend == "shell" {
				command = shellCommand()
			} else if !isValidBackend(params.Backend) {
				return nil, service.ErrValidation(fmt.Sprintf("invalid backend %q; valid: %s", params.Backend, strings.Join(validBackends, ", ")))
			} else {
				command = params.Backend
			}

			if spawnErr != nil {
				return nil, service.ErrInternal("failed to spawn terminal session", spawnErr)
			}

			return &SpawnResult{
				SessionName: sanitizedName,
				Backend:     params.Backend,
				Command:     command,
				Created:     spawnCreated,
			}, nil
		},
	}
}

func TestHandleTerminalSpawn_HappyPath(t *testing.T) {
	svc := newMockSpawnService(true, nil)
	handler := handleTerminalSpawn(svc)

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
	svc := newMockSpawnService(false, nil)
	handler := handleTerminalSpawn(svc)

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
	svc := newMockSpawnService(true, nil)
	handler := handleTerminalSpawn(svc)

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
	svc := newMockSpawnService(true, nil)
	handler := handleTerminalSpawn(svc)

	body := `{"backend":"claude"}`
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/spawn", strings.NewReader(body))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}

	body2 := rr.Body.String()
	if !strings.Contains(body2, "missing required field: session_name") {
		t.Errorf("error body = %q, want it to contain %q", body2, "missing required field: session_name")
	}
}

func TestHandleTerminalSpawn_MissingBackend(t *testing.T) {
	svc := newMockSpawnService(true, nil)
	handler := handleTerminalSpawn(svc)

	body := `{"session_name":"my-session"}`
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/spawn", strings.NewReader(body))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}

	body2 := rr.Body.String()
	if !strings.Contains(body2, "missing required field: backend") {
		t.Errorf("error body = %q, want it to contain %q", body2, "missing required field: backend")
	}
}

func TestHandleTerminalSpawn_InvalidSessionNameAfterSanitization(t *testing.T) {
	svc := newMockSpawnService(true, nil)
	handler := handleTerminalSpawn(svc)

	body := `{"session_name":"bad name!","backend":"claude"}`
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/spawn", strings.NewReader(body))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}

	body2 := rr.Body.String()
	if !strings.Contains(body2, "invalid session name") {
		t.Errorf("error body = %q, want it to contain %q", body2, "invalid session name")
	}
}

func TestHandleTerminalSpawn_InvalidBackend(t *testing.T) {
	svc := newMockSpawnService(true, nil)
	handler := handleTerminalSpawn(svc)

	body := `{"session_name":"my-session","backend":"invalid-backend"}`
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/spawn", strings.NewReader(body))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}

	body2 := rr.Body.String()
	if !strings.Contains(body2, "invalid backend") {
		t.Errorf("error body = %q, want it to contain %q", body2, "invalid backend")
	}
	// Check that valid options are listed in the error message.
	for _, backend := range validBackends {
		if !strings.Contains(body2, backend) {
			t.Errorf("error body = %q, want it to list valid backend %q", body2, backend)
		}
	}
}

func TestHandleTerminalSpawn_NilManager(t *testing.T) {
	handler := handleTerminalSpawn(NewTerminalService(nil, nil, nil, nil, nil, nil, nil))

	body := `{"session_name":"my-session","backend":"claude"}`
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/spawn", strings.NewReader(body))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusServiceUnavailable, rr.Body.String())
	}

	body2 := rr.Body.String()
	if !strings.Contains(body2, "terminal manager not initialized") {
		t.Errorf("error body = %q, want it to contain %q", body2, "terminal manager not initialized")
	}
}

func TestHandleTerminalSpawn_TmuxFailure(t *testing.T) {
	svc := newMockSpawnService(false, fmt.Errorf("tmux new-session: exit status 1"))
	handler := handleTerminalSpawn(svc)

	body := `{"session_name":"my-session","backend":"claude"}`
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/spawn", strings.NewReader(body))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusInternalServerError, rr.Body.String())
	}

	body2 := rr.Body.String()
	if !strings.Contains(body2, "failed to spawn terminal session") {
		t.Errorf("error body = %q, want it to contain %q", body2, "failed to spawn terminal session")
	}
}

func TestHandleTerminalSpawn_MalformedJSON(t *testing.T) {
	svc := newMockSpawnService(true, nil)
	handler := handleTerminalSpawn(svc)

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
	svc := newMockSpawnService(true, nil)
	handler := handleTerminalSpawn(svc)

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
	svc := newMockSpawnService(true, nil)
	handler := handleTerminalSpawn(svc)

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

func TestHandleTerminalSpawn_ShellBackendUsesShellCommand(t *testing.T) {
	svc := newMockSpawnService(true, nil)
	handler := handleTerminalSpawn(svc)

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
