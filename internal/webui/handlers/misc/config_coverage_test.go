package misc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- handlePatchBackendConfigWithPool coverage ---

func TestHandlePatchBackendConfig_PoolGetError(t *testing.T) {
	pool := &mockConfigPool{
		getFunc: func(ctx context.Context) (configClient, error) {
			return nil, errors.New("connection refused")
		},
	}
	handler := handlePatchBackendConfigWithPool(pool)

	body := strings.NewReader(`{"backend":"codex"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/config/backend", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp BackendConfigResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Success {
		t.Fatal("expected failure")
	}
	if !strings.Contains(resp.Error, "daemon not available") {
		t.Errorf("expected daemon not available error, got: %s", resp.Error)
	}
}

func TestHandlePatchBackendConfig_PoolGetDeadlineExceeded(t *testing.T) {
	pool := &mockConfigPool{
		getFunc: func(ctx context.Context) (configClient, error) {
			return nil, context.DeadlineExceeded
		},
	}
	handler := handlePatchBackendConfigWithPool(pool)

	body := strings.NewReader(`{"backend":"codex"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/config/backend", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected 504 for deadline exceeded, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlePatchBackendConfig_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	pool := newMockConfigPool(dir)
	handler := handlePatchBackendConfigWithPool(pool)

	body := strings.NewReader(`{bad json}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/config/backend", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed JSON, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp BackendConfigResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Success {
		t.Fatal("expected failure")
	}
	if !strings.Contains(resp.Error, "invalid request body") {
		t.Errorf("expected invalid request body error, got: %s", resp.Error)
	}
}

func TestHandlePatchBackendConfig_MissingBackendField(t *testing.T) {
	dir := t.TempDir()
	pool := newMockConfigPool(dir)
	handler := handlePatchBackendConfigWithPool(pool)

	// JSON with no "backend" field decodes to empty string, which is invalid.
	body := strings.NewReader(`{"other":"field"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/config/backend", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing backend, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp BackendConfigResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !strings.Contains(resp.Error, "invalid backend") {
		t.Errorf("expected invalid backend error, got: %s", resp.Error)
	}
}

func TestHandlePatchBackendConfig_InvalidYAMLFile(t *testing.T) {
	dir := t.TempDir()
	// Write invalid YAML content to trigger loadProjectFile parse error.
	os.WriteFile(filepath.Join(dir, "loom.yaml"), []byte(":\t:\ninvalid:\n  - [[["), 0644)

	pool := newMockConfigPool(dir)
	handler := handlePatchBackendConfigWithPool(pool)

	body := strings.NewReader(`{"backend":"codex"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/config/backend", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for invalid YAML, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp BackendConfigResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !strings.Contains(resp.Error, "failed to parse config") {
		t.Errorf("expected parse config error, got: %s", resp.Error)
	}
}

func TestHandlePatchBackendConfig_SaveError(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "loom.yaml")
	os.WriteFile(yamlPath, []byte("backend: claude\n"), 0644)

	pool := newMockConfigPool(dir)
	handler := handlePatchBackendConfigWithPool(pool)

	// Make both the file and directory read-only to force a save error.
	os.Chmod(yamlPath, 0400)
	os.Chmod(dir, 0500)
	defer func() {
		os.Chmod(dir, 0700)
		os.Chmod(yamlPath, 0600)
	}()

	body := strings.NewReader(`{"backend":"codex"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/config/backend", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for save error, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp BackendConfigResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !strings.Contains(resp.Error, "failed to save config") {
		t.Errorf("expected save config error, got: %s", resp.Error)
	}
}

func TestHandlePatchBackendConfig_RequestBodyTooLarge(t *testing.T) {
	dir := t.TempDir()
	pool := newMockConfigPool(dir)
	handler := handlePatchBackendConfigWithPool(pool)

	// Create a body larger than maxRequestBody (1MB).
	// Use valid-looking JSON prefix so the decoder reads enough to hit the limit.
	largeData := make([]byte, maxRequestBody+1)
	largeData[0] = '{'
	largeData[1] = '"'
	largeData[2] = 'b'
	largeData[3] = '"'
	largeData[4] = ':'
	largeData[5] = '"'
	for i := 6; i < len(largeData); i++ {
		largeData[i] = 'a'
	}
	body := strings.NewReader(string(largeData))
	req := httptest.NewRequest(http.MethodPatch, "/api/config/backend", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// MaxBytesReader triggers either 413 (if MaxBytesError is detected) or
	// 400 (generic decode error). Both are acceptable error responses that
	// exercise the body-limit code path.
	var resp BackendConfigResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Success {
		t.Fatal("expected failure for oversized body")
	}
	if rec.Code != http.StatusRequestEntityTooLarge && rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 413 or 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlePatchBackendConfig_AllValidBackends(t *testing.T) {
	for _, backend := range validBackends {
		t.Run(backend, func(t *testing.T) {
			dir := t.TempDir()
			pool := newMockConfigPool(dir)
			handler := handlePatchBackendConfigWithPool(pool)

			body := strings.NewReader(`{"backend":"` + backend + `"}`)
			req := httptest.NewRequest(http.MethodPatch, "/api/config/backend", body)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200 for backend %s, got %d: %s", backend, rec.Code, rec.Body.String())
			}
			var resp BackendConfigResponse
			json.NewDecoder(rec.Body).Decode(&resp)
			if resp.Data.Backend != backend {
				t.Errorf("expected backend %s, got %s", backend, resp.Data.Backend)
			}
		})
	}
}

// --- shellCommand coverage ---

func TestShellCommand_EnvSet(t *testing.T) {
	original := os.Getenv("SHELL")
	defer os.Setenv("SHELL", original)

	os.Setenv("SHELL", "/bin/zsh")
	got := shellCommand()
	if got != "/bin/zsh" {
		t.Errorf("shellCommand() = %q, want %q", got, "/bin/zsh")
	}
}

func TestShellCommand_EnvUnset(t *testing.T) {
	original := os.Getenv("SHELL")
	defer os.Setenv("SHELL", original)

	os.Unsetenv("SHELL")
	got := shellCommand()
	if got != "/bin/bash" {
		t.Errorf("shellCommand() = %q when SHELL unset, want %q", got, "/bin/bash")
	}
}

func TestShellCommand_EnvEmpty(t *testing.T) {
	original := os.Getenv("SHELL")
	defer os.Setenv("SHELL", original)

	os.Setenv("SHELL", "")
	got := shellCommand()
	if got != "/bin/bash" {
		t.Errorf("shellCommand() = %q when SHELL empty, want %q", got, "/bin/bash")
	}
}

// --- attachCommandForSession coverage ---

func TestAttachCommandForSession_LeadShell(t *testing.T) {
	original := os.Getenv("SHELL")
	defer os.Setenv("SHELL", original)
	os.Setenv("SHELL", "/bin/zsh")

	tests := []struct {
		name    string
		session string
		want    string
	}{
		{"lead-shell prefix", "lead-shell-1", "/bin/zsh"},
		{"lead-shell prefix with id", "lead-shell-abc-123", "/bin/zsh"},
		{"lead-shell exact prefix", "lead-shell-", "/bin/zsh"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := attachCommandForSession(tt.session)
			if got != tt.want {
				t.Errorf("attachCommandForSession(%q) = %q, want %q", tt.session, got, tt.want)
			}
		})
	}
}

func TestAttachCommandForSession_NonLeadShell(t *testing.T) {
	tests := []struct {
		name    string
		session string
	}{
		{"agent session", "agent-worker-1"},
		{"claude session", "claude-session-abc"},
		{"empty string", ""},
		{"lead-shell without dash", "lead-shell"},
		{"partial prefix", "lead-shel-1"},
		{"different prefix", "other-shell-1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := attachCommandForSession(tt.session)
			if got != "" {
				t.Errorf("attachCommandForSession(%q) = %q, want empty string", tt.session, got)
			}
		})
	}
}

// --- loadProjectFile / saveProjectFile coverage ---

func TestLoadProjectFile_ReadError(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "loom.yaml")

	// Create loom.yaml as a directory to provoke a read error.
	os.Mkdir(yamlPath, 0755)

	_, err := loadProjectFile(dir)
	if err == nil {
		t.Fatal("expected error when loom.yaml is a directory")
	}
	if !strings.Contains(err.Error(), "reading") {
		t.Errorf("expected 'reading' in error message, got: %v", err)
	}
}

func TestLoadProjectFile_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "loom.yaml"), []byte(":\t:\n  [[[invalid"), 0644)

	_, err := loadProjectFile(dir)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
	if !strings.Contains(err.Error(), "parsing") {
		t.Errorf("expected 'parsing' in error message, got: %v", err)
	}
}

func TestLoadProjectFile_ValidWithAllFields(t *testing.T) {
	dir := t.TempDir()
	content := `backend: gemini
agents:
  - worktree: hawk
    role: review
    auto: true
    backend: codex
  - worktree: eagle
    role: task
`
	os.WriteFile(filepath.Join(dir, "loom.yaml"), []byte(content), 0644)

	pf, err := loadProjectFile(dir)
	if err != nil {
		t.Fatalf("loadProjectFile() error = %v", err)
	}
	if pf.Backend != "gemini" {
		t.Errorf("Backend = %q, want %q", pf.Backend, "gemini")
	}
	if len(pf.Agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(pf.Agents))
	}
	if pf.Agents[0].Worktree != "hawk" || pf.Agents[0].Backend != "codex" || !pf.Agents[0].Auto {
		t.Errorf("unexpected agent[0]: %+v", pf.Agents[0])
	}
	if pf.Agents[1].Worktree != "eagle" || pf.Agents[1].Role != "task" {
		t.Errorf("unexpected agent[1]: %+v", pf.Agents[1])
	}
}

func TestSaveProjectFile_WriteError(t *testing.T) {
	// Use a non-existent nested directory path that cannot be written.
	dir := filepath.Join(t.TempDir(), "nonexistent", "path")
	// Do not create the directory; saveProjectFile does not create directories.

	pf := &projectFile{Backend: "claude"}
	err := saveProjectFile(dir, pf)
	if err == nil {
		t.Fatal("expected error when writing to non-existent directory")
	}
}

func TestSaveProjectFile_RoundTrip(t *testing.T) {
	dir := t.TempDir()

	original := &projectFile{
		Backend: "opencode",
		Agents: []agentEntry{
			{Worktree: "w1", Role: "plan", Backend: "codex", Auto: true},
			{Worktree: "w2", Role: "task"},
		},
	}

	if err := saveProjectFile(dir, original); err != nil {
		t.Fatalf("saveProjectFile() error = %v", err)
	}

	loaded, err := loadProjectFile(dir)
	if err != nil {
		t.Fatalf("loadProjectFile() error = %v", err)
	}

	if loaded.Backend != original.Backend {
		t.Errorf("Backend = %q, want %q", loaded.Backend, original.Backend)
	}
	if len(loaded.Agents) != len(original.Agents) {
		t.Fatalf("agents count = %d, want %d", len(loaded.Agents), len(original.Agents))
	}
	for i, a := range loaded.Agents {
		o := original.Agents[i]
		if a.Worktree != o.Worktree || a.Role != o.Role || a.Backend != o.Backend || a.Auto != o.Auto {
			t.Errorf("agent[%d] = %+v, want %+v", i, a, o)
		}
	}
}

// --- handleGetBackendConfig: deadline exceeded coverage ---

func TestHandleGetBackendConfig_DeadlineExceeded(t *testing.T) {
	pool := &mockConfigPool{
		getFunc: func(ctx context.Context) (configClient, error) {
			return nil, context.DeadlineExceeded
		},
	}
	handler := handleGetBackendConfigWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/config/backend", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected 504 for deadline exceeded, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleGetBackendConfig_InvalidYAMLFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "loom.yaml"), []byte(":\t:\n  [[[invalid"), 0644)

	pool := newMockConfigPool(dir)
	handler := handleGetBackendConfigWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/config/backend", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for invalid YAML, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp BackendConfigResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !strings.Contains(resp.Error, "failed to parse config") {
		t.Errorf("expected parse config error, got: %s", resp.Error)
	}
}
