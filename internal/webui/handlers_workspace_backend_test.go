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

// backendPatchRequest creates a PATCH request with workspace UUID in context.
func backendPatchRequest(wsID, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/"+wsID+"/config/backend", strings.NewReader(body))
	ctx := WithWorkspace(req.Context(), wsID)
	return req.WithContext(ctx)
}

func TestHandleWorkspaceBackendPatch_Success(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)

	writeTestBackendConfig(t, dir, &loomConfigForBackend{
		Version: 1,
		Backend: "claude",
		Workspaces: map[string]loomWorkspaceForBackend{
			"my-ws": {ID: "ws-uuid-1", Path: "/home/user/projects/myws", Backend: "claude"},
		},
	})

	handler := handleWorkspaceBackendPatch(mockWorkspaceConfigFn)

	req := backendPatchRequest("ws-uuid-1", `{"backend":"codex"}`)
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
			"my-ws": {ID: "ws-uuid-1", Path: "/home/user/projects/myws"},
		},
	})

	handler := handleWorkspaceBackendPatch(nil)

	req := backendPatchRequest("ws-uuid-1", `{"backend":"invalid-backend"}`)
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

	req := backendPatchRequest("ws-uuid-1", `{"backend":""}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleWorkspaceBackendPatch_MissingWorkspaceID(t *testing.T) {
	handler := handleWorkspaceBackendPatch(nil)

	// No workspace ID in context
	body := strings.NewReader(`{"backend":"claude"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/config/backend", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleWorkspaceBackendPatch_UnknownUUID(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)

	writeTestBackendConfig(t, dir, &loomConfigForBackend{
		Version: 1,
		Workspaces: map[string]loomWorkspaceForBackend{
			"existing-ws": {ID: "uuid-existing", Path: "/home/user/projects/existing"},
		},
	})

	handler := handleWorkspaceBackendPatch(nil)

	req := backendPatchRequest("nonexistent-uuid", `{"backend":"claude"}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !strings.Contains(resp.Error, "not found") {
		t.Errorf("expected 'not found' in error, got: %s", resp.Error)
	}
}

func TestHandleWorkspaceBackendPatch_RoundTripPreservation(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)

	writeTestBackendConfig(t, dir, &loomConfigForBackend{
		Version:          1,
		DefaultWorkspace: "my-ws",
		WorkspaceOrder:   []string{"my-ws", "other-ws"},
		Backend:          "claude",
		Workspaces: map[string]loomWorkspaceForBackend{
			"my-ws":    {ID: "uuid-myws", Path: "/home/user/projects/myws", Backend: "claude"},
			"other-ws": {ID: "uuid-other", Path: "/home/user/projects/other", Backend: "opencode"},
		},
	})

	handler := handleWorkspaceBackendPatch(mockWorkspaceConfigFn)

	req := backendPatchRequest("uuid-myws", `{"backend":"gemini"}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

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
}

func TestHandleWorkspaceBackendPatch_MalformedJSON(t *testing.T) {
	handler := handleWorkspaceBackendPatch(nil)

	req := backendPatchRequest("ws-uuid", `{invalid json}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}
