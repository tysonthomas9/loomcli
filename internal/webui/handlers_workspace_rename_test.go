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

// writeTestLoomConfig writes a loomConfigForRename to config.yaml inside dir.
func writeTestLoomConfig(t *testing.T, dir string, cfg *loomConfigForRename) {
	t.Helper()
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), data, 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// readTestLoomConfig reads and parses config.yaml from dir.
func readTestLoomConfig(t *testing.T, dir string) *loomConfigForRename {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg loomConfigForRename
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	return &cfg
}

// setLoomConfigDir sets LOOM_CONFIG_DIR to dir and returns a cleanup function.
func setLoomConfigDir(t *testing.T, dir string) {
	t.Helper()
	old := os.Getenv("LOOM_CONFIG_DIR")
	os.Setenv("LOOM_CONFIG_DIR", dir)
	t.Cleanup(func() {
		if old == "" {
			os.Unsetenv("LOOM_CONFIG_DIR")
		} else {
			os.Setenv("LOOM_CONFIG_DIR", old)
		}
	})
}

// mockWorkspaceConfigFn returns a simple workspaceConfigFn for testing.
func mockWorkspaceConfigFn() (*WorkspaceData, error) {
	return &WorkspaceData{
		Name: "test-ws",
		Path: "/tmp/test",
	}, nil
}

// renameRequest creates a rename request with workspace UUID in context.
func renameRequest(wsID, newName string) *http.Request {
	body := strings.NewReader(`{"new_name":"` + newName + `"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/"+wsID+"/name", body)
	ctx := WithWorkspace(req.Context(), wsID)
	return req.WithContext(ctx)
}

func TestWorkspaceRename_Success(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)

	writeTestLoomConfig(t, dir, &loomConfigForRename{
		Version:          1,
		DefaultWorkspace: "other",
		Workspaces: map[string]loomWorkspaceForRename{
			"old-name": {ID: "uuid-old", Path: "/home/user/projects/old"},
			"other":    {ID: "uuid-other", Path: "/home/user/projects/other"},
		},
	})

	handler := handleWorkspaceRename(mockWorkspaceConfigFn)

	req := renameRequest("uuid-old", "new-name")
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

	// Verify config was updated
	cfg := readTestLoomConfig(t, dir)
	if _, ok := cfg.Workspaces["old-name"]; ok {
		t.Error("old key 'old-name' should have been removed")
	}
	ws, ok := cfg.Workspaces["new-name"]
	if !ok {
		t.Fatal("new key 'new-name' should be present")
	}
	if ws.Path != "/home/user/projects/old" {
		t.Errorf("expected path /home/user/projects/old, got %s", ws.Path)
	}
	if ws.ID != "uuid-old" {
		t.Errorf("expected ID 'uuid-old', got %s", ws.ID)
	}

	// DefaultWorkspace should remain unchanged since it was "other"
	if cfg.DefaultWorkspace != "other" {
		t.Errorf("expected default_workspace 'other', got %q", cfg.DefaultWorkspace)
	}
}

func TestWorkspaceRename_DefaultWorkspaceUpdated(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)

	writeTestLoomConfig(t, dir, &loomConfigForRename{
		Version:          1,
		DefaultWorkspace: "my-ws",
		Workspaces: map[string]loomWorkspaceForRename{
			"my-ws": {ID: "uuid-myws", Path: "/home/user/projects/myws"},
		},
	})

	handler := handleWorkspaceRename(mockWorkspaceConfigFn)

	req := renameRequest("uuid-myws", "renamed-ws")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	cfg := readTestLoomConfig(t, dir)
	if cfg.DefaultWorkspace != "renamed-ws" {
		t.Errorf("expected default_workspace 'renamed-ws', got %q", cfg.DefaultWorkspace)
	}
}

func TestWorkspaceRename_DuplicateName(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)

	writeTestLoomConfig(t, dir, &loomConfigForRename{
		Version: 1,
		Workspaces: map[string]loomWorkspaceForRename{
			"alpha": {ID: "uuid-alpha", Path: "/a"},
			"beta":  {ID: "uuid-beta", Path: "/b"},
		},
	})

	handler := handleWorkspaceRename(nil)

	req := renameRequest("uuid-alpha", "beta")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Success {
		t.Fatal("expected failure")
	}
	if !strings.Contains(resp.Error, "already exists") {
		t.Errorf("expected 'already exists' in error, got: %s", resp.Error)
	}
}

func TestWorkspaceRename_EmptyName(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)

	writeTestLoomConfig(t, dir, &loomConfigForRename{
		Version: 1,
		Workspaces: map[string]loomWorkspaceForRename{
			"ws": {ID: "uuid-ws", Path: "/a"},
		},
	})

	handler := handleWorkspaceRename(nil)

	req := renameRequest("uuid-ws", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWorkspaceRename_UnknownUUID(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)

	writeTestLoomConfig(t, dir, &loomConfigForRename{
		Version: 1,
		Workspaces: map[string]loomWorkspaceForRename{
			"existing": {ID: "uuid-existing", Path: "/a"},
		},
	})

	handler := handleWorkspaceRename(nil)

	req := renameRequest("nonexistent-uuid", "new-name")
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

func TestWorkspaceRename_SameNameNoOp(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)

	writeTestLoomConfig(t, dir, &loomConfigForRename{
		Version: 1,
		Workspaces: map[string]loomWorkspaceForRename{
			"my-ws": {ID: "uuid-myws", Path: "/a"},
		},
	})

	handler := handleWorkspaceRename(nil)

	req := renameRequest("uuid-myws", "my-ws")
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
}

func TestWorkspaceRename_NoConfigFound(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)
	// No config.yaml written — loadLoomConfig returns nil, nil

	handler := handleWorkspaceRename(nil)

	req := renameRequest("some-uuid", "new-ws")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWorkspaceRename_MissingWorkspaceID(t *testing.T) {
	handler := handleWorkspaceRename(nil)

	// Request without workspace ID in context
	body := strings.NewReader(`{"new_name":"new-ws"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/name", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWorkspaceRename_InvalidRequestBody(t *testing.T) {
	handler := handleWorkspaceRename(nil)

	body := strings.NewReader(`{invalid json}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/uuid/name", body)
	ctx := WithWorkspace(req.Context(), "some-uuid")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWorkspaceRename_PreservesUUID_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)

	writeTestLoomConfig(t, dir, &loomConfigForRename{
		Version: 1,
		Workspaces: map[string]loomWorkspaceForRename{
			"myws": {ID: "stable-uuid-456", Path: "/home/user/projects/myws"},
		},
	})

	handler := handleWorkspaceRename(nil)

	req := renameRequest("stable-uuid-456", "renamed")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	cfg := readTestLoomConfig(t, dir)
	ws, ok := cfg.Workspaces["renamed"]
	if !ok {
		t.Fatal("workspace 'renamed' should exist in config")
	}
	if ws.ID != "stable-uuid-456" {
		t.Errorf("ID = %q, want %q; UUID was not preserved through rename", ws.ID, "stable-uuid-456")
	}
}

func TestValidWorkspaceName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"alphanumeric", "abc123", true},
		{"hyphens", "my-workspace", true},
		{"underscores", "my_workspace", true},
		{"mixed", "My-Workspace_01", true},
		{"space", "my workspace", false},
		{"dot", "my.ws", false},
		{"slash", "my/ws", false},
		{"empty", "", true}, // empty string has no invalid chars
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validWorkspaceName(tt.input)
			if got != tt.want {
				t.Errorf("validWorkspaceName(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestLoomWorkspaceForRename_RoundTrip(t *testing.T) {
	ws := loomWorkspaceForRename{
		ID:      "test-uuid-abc-123",
		Path:    "/home/user/projects/myws",
		Backend: "codex",
	}

	data, err := yaml.Marshal(ws)
	if err != nil {
		t.Fatalf("yaml.Marshal() error = %v", err)
	}

	var got loomWorkspaceForRename
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}

	if got.ID != ws.ID {
		t.Errorf("ID = %q, want %q", got.ID, ws.ID)
	}
	if got.Path != ws.Path {
		t.Errorf("Path = %q, want %q", got.Path, ws.Path)
	}
	if got.Backend != ws.Backend {
		t.Errorf("Backend = %q, want %q", got.Backend, ws.Backend)
	}
}

func TestApplyWorkspaceRename_PreservesUUID(t *testing.T) {
	cfg := &loomConfigForRename{
		Version:          1,
		DefaultWorkspace: "foo",
		WorkspaceOrder:   []string{"foo"},
		Workspaces: map[string]loomWorkspaceForRename{
			"foo": {ID: "test-uuid-123", Path: "/home/user/projects/foo"},
		},
	}

	ws := cfg.Workspaces["foo"]
	applyWorkspaceRename(cfg, "foo", "bar", ws)

	if _, ok := cfg.Workspaces["foo"]; ok {
		t.Error("old key 'foo' should have been removed")
	}
	renamed, ok := cfg.Workspaces["bar"]
	if !ok {
		t.Fatal("new key 'bar' should be present")
	}
	if renamed.ID != "test-uuid-123" {
		t.Errorf("ID = %q, want %q", renamed.ID, "test-uuid-123")
	}
	if cfg.DefaultWorkspace != "bar" {
		t.Errorf("DefaultWorkspace = %q, want %q", cfg.DefaultWorkspace, "bar")
	}
}

func TestResolveWorkspaceNameByID(t *testing.T) {
	cfg := &loomConfigForRename{
		Workspaces: map[string]loomWorkspaceForRename{
			"ws-one": {ID: "uuid-1", Path: "/one"},
			"ws-two": {ID: "uuid-2", Path: "/two"},
		},
	}

	// Found case
	name, ws, found := resolveWorkspaceNameByID(cfg, "uuid-2")
	if !found {
		t.Fatal("expected to find workspace by ID")
	}
	if name != "ws-two" {
		t.Errorf("name = %q, want %q", name, "ws-two")
	}
	if ws.Path != "/two" {
		t.Errorf("path = %q, want %q", ws.Path, "/two")
	}

	// Not found case
	_, _, found = resolveWorkspaceNameByID(cfg, "nonexistent")
	if found {
		t.Error("expected not found for unknown UUID")
	}
}

func TestRenamePreservesWorkspaceBackend(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)

	writeTestLoomConfig(t, dir, &loomConfigForRename{
		Version: 1,
		Workspaces: map[string]loomWorkspaceForRename{
			"my-ws": {ID: "uuid-backend", Path: "/home/user/projects/myws", Backend: "codex"},
		},
	})

	handler := handleWorkspaceRename(mockWorkspaceConfigFn)

	req := renameRequest("uuid-backend", "renamed-ws")
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

	cfg := readTestLoomConfig(t, dir)
	ws, ok := cfg.Workspaces["renamed-ws"]
	if !ok {
		t.Fatal("workspace 'renamed-ws' should exist in config")
	}
	if ws.Backend != "codex" {
		t.Errorf("Backend = %q, want %q; workspace backend was not preserved through rename", ws.Backend, "codex")
	}
	if ws.ID != "uuid-backend" {
		t.Errorf("ID = %q, want %q", ws.ID, "uuid-backend")
	}
	if ws.Path != "/home/user/projects/myws" {
		t.Errorf("Path = %q, want %q", ws.Path, "/home/user/projects/myws")
	}
}
