package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// writeTestBackendConfig writes a loomConfigForBackend to config.yaml inside dir.
func writeTestBackendConfig(t *testing.T, dir string, cfg *loomConfigForBackend) {
	t.Helper()
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), data, 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// readTestBackendConfig reads and parses config.yaml from dir into loomConfigForBackend.
func readTestBackendConfig(t *testing.T, dir string) *loomConfigForBackend {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg loomConfigForBackend
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	return &cfg
}

func TestHandleWorkspaceBackendPatch_Success(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)

	writeTestBackendConfig(t, dir, &loomConfigForBackend{
		Version: 1,
		Backend: "claude",
		Workspaces: map[string]loomWorkspaceForBackend{
			"my-ws": {Path: "/home/user/projects/myws", Backend: "claude"},
		},
	})

	handler := handleWorkspaceBackendPatch(mockWorkspaceConfigFn)

	body := strings.NewReader(`{"backend":"codex"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/workspace/my-ws/config/backend", body)
	req.SetPathValue("name", "my-ws")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
	if resp.Data == nil {
		t.Fatal("expected Data to be non-nil when workspaceConfigFn provided")
	}
	if resp.Data.Name != "test-ws" {
		t.Errorf("expected data name 'test-ws', got %q", resp.Data.Name)
	}

	// Verify config was updated on disk
	cfg := readTestBackendConfig(t, dir)
	ws, ok := cfg.Workspaces["my-ws"]
	if !ok {
		t.Fatal("workspace 'my-ws' should still exist")
	}
	if ws.Backend != "codex" {
		t.Errorf("expected backend 'codex', got %q", ws.Backend)
	}
}

func TestHandleWorkspaceBackendPatch_InvalidBackend(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)

	writeTestBackendConfig(t, dir, &loomConfigForBackend{
		Version: 1,
		Workspaces: map[string]loomWorkspaceForBackend{
			"my-ws": {Path: "/home/user/projects/myws"},
		},
	})

	handler := handleWorkspaceBackendPatch(nil)

	body := strings.NewReader(`{"backend":"invalid-backend"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/workspace/my-ws/config/backend", body)
	req.SetPathValue("name", "my-ws")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Success {
		t.Fatal("expected failure")
	}
	if !strings.Contains(resp.Error, "invalid backend") {
		t.Errorf("expected 'invalid backend' in error, got: %s", resp.Error)
	}
}

func TestHandleWorkspaceBackendPatch_EmptyBackend(t *testing.T) {
	handler := handleWorkspaceBackendPatch(nil)

	body := strings.NewReader(`{"backend":""}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/workspace/my-ws/config/backend", body)
	req.SetPathValue("name", "my-ws")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Success {
		t.Fatal("expected failure")
	}
	if !strings.Contains(resp.Error, "backend is required") {
		t.Errorf("expected 'backend is required' in error, got: %s", resp.Error)
	}
}

func TestHandleWorkspaceBackendPatch_WorkspaceNotFound(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)

	writeTestBackendConfig(t, dir, &loomConfigForBackend{
		Version: 1,
		Workspaces: map[string]loomWorkspaceForBackend{
			"existing-ws": {Path: "/home/user/projects/existing"},
		},
	})

	handler := handleWorkspaceBackendPatch(nil)

	body := strings.NewReader(`{"backend":"claude"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/workspace/nonexistent/config/backend", body)
	req.SetPathValue("name", "nonexistent")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Success {
		t.Fatal("expected failure")
	}
	if !strings.Contains(resp.Error, "not found") {
		t.Errorf("expected 'not found' in error, got: %s", resp.Error)
	}
}

func TestHandleWorkspaceBackendPatch_NoConfigFile(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)
	// No config.yaml written — loadLoomConfigForBackend returns nil, nil

	handler := handleWorkspaceBackendPatch(nil)

	body := strings.NewReader(`{"backend":"claude"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/workspace/my-ws/config/backend", body)
	req.SetPathValue("name", "my-ws")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Success {
		t.Fatal("expected failure")
	}
	if !strings.Contains(resp.Error, "no config found") {
		t.Errorf("expected 'no config found' in error, got: %s", resp.Error)
	}
}

func TestHandleWorkspaceBackendPatch_MalformedJSON(t *testing.T) {
	handler := handleWorkspaceBackendPatch(nil)

	body := strings.NewReader(`{invalid json}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/workspace/my-ws/config/backend", body)
	req.SetPathValue("name", "my-ws")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Success {
		t.Fatal("expected failure")
	}
	if !strings.Contains(resp.Error, "invalid request body") {
		t.Errorf("expected 'invalid request body' in error, got: %s", resp.Error)
	}
}

func TestHandleWorkspaceBackendPatch_RequestBodyTooLarge(t *testing.T) {
	handler := handleWorkspaceBackendPatch(nil)

	// Create a JSON body larger than 1MB (maxRequestBody)
	largeBody := `{"backend":"` + strings.Repeat("a", 1<<20+1) + `"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/workspace/my-ws/config/backend", strings.NewReader(largeBody))
	req.SetPathValue("name", "my-ws")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Success {
		t.Fatal("expected failure")
	}
	if !strings.Contains(resp.Error, "request body too large") {
		t.Errorf("expected 'request body too large' in error, got: %s", resp.Error)
	}
}

func TestHandleWorkspaceBackendPatch_RoundTripPreservation(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)

	// Write a config with many fields to verify round-trip preservation
	writeTestBackendConfig(t, dir, &loomConfigForBackend{
		Version:          1,
		DefaultWorkspace: "my-ws",
		WorkspaceOrder:   []string{"my-ws", "other-ws"},
		Backend:          "claude",
		Workspaces: map[string]loomWorkspaceForBackend{
			"my-ws":    {Path: "/home/user/projects/myws", Backend: "claude"},
			"other-ws": {Path: "/home/user/projects/other", Backend: "opencode"},
		},
	})

	handler := handleWorkspaceBackendPatch(mockWorkspaceConfigFn)

	body := strings.NewReader(`{"backend":"gemini"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/workspace/my-ws/config/backend", body)
	req.SetPathValue("name", "my-ws")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}

	// Read back the config and verify all fields preserved
	cfg := readTestBackendConfig(t, dir)

	// Backend for target workspace should be updated
	ws := cfg.Workspaces["my-ws"]
	if ws.Backend != "gemini" {
		t.Errorf("expected backend 'gemini' for my-ws, got %q", ws.Backend)
	}

	// Other workspace should be unaffected
	otherWs := cfg.Workspaces["other-ws"]
	if otherWs.Backend != "opencode" {
		t.Errorf("expected backend 'opencode' for other-ws, got %q", otherWs.Backend)
	}

	// Top-level fields should be preserved
	if cfg.Version != 1 {
		t.Errorf("expected version 1, got %d", cfg.Version)
	}
	if cfg.DefaultWorkspace != "my-ws" {
		t.Errorf("expected default_workspace 'my-ws', got %q", cfg.DefaultWorkspace)
	}
	if cfg.Backend != "claude" {
		t.Errorf("expected global backend 'claude', got %q", cfg.Backend)
	}
	if len(cfg.WorkspaceOrder) != 2 || cfg.WorkspaceOrder[0] != "my-ws" || cfg.WorkspaceOrder[1] != "other-ws" {
		t.Errorf("expected workspace_order [my-ws, other-ws], got %v", cfg.WorkspaceOrder)
	}

	// Path should be preserved
	if ws.Path != "/home/user/projects/myws" {
		t.Errorf("expected path '/home/user/projects/myws', got %q", ws.Path)
	}
	if otherWs.Path != "/home/user/projects/other" {
		t.Errorf("expected path '/home/user/projects/other', got %q", otherWs.Path)
	}
}

func TestHandleWorkspaceBackendPatch_MultipleWorkspaces(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)

	writeTestBackendConfig(t, dir, &loomConfigForBackend{
		Version: 1,
		Workspaces: map[string]loomWorkspaceForBackend{
			"ws-a": {Path: "/a", Backend: "claude"},
			"ws-b": {Path: "/b", Backend: "codex"},
			"ws-c": {Path: "/c", Backend: "opencode"},
		},
	})

	handler := handleWorkspaceBackendPatch(mockWorkspaceConfigFn)

	// Update ws-b's backend to cursor
	body := strings.NewReader(`{"backend":"cursor"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/workspace/ws-b/config/backend", body)
	req.SetPathValue("name", "ws-b")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}

	// Verify ws-b was updated
	cfg := readTestBackendConfig(t, dir)
	if cfg.Workspaces["ws-b"].Backend != "cursor" {
		t.Errorf("expected ws-b backend 'cursor', got %q", cfg.Workspaces["ws-b"].Backend)
	}

	// Verify ws-a and ws-c are unaffected
	if cfg.Workspaces["ws-a"].Backend != "claude" {
		t.Errorf("expected ws-a backend 'claude', got %q", cfg.Workspaces["ws-a"].Backend)
	}
	if cfg.Workspaces["ws-c"].Backend != "opencode" {
		t.Errorf("expected ws-c backend 'opencode', got %q", cfg.Workspaces["ws-c"].Backend)
	}
}
