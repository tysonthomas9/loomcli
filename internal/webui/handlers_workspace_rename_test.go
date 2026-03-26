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

func TestWorkspaceRename_Success(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)

	writeTestLoomConfig(t, dir, &loomConfigForRename{
		Version:          1,
		DefaultWorkspace: "other",
		Workspaces: map[string]loomWorkspaceForRename{
			"old-name": {Path: "/home/user/projects/old"},
			"other":    {Path: "/home/user/projects/other"},
		},
	})

	handler := handleWorkspaceRename(mockWorkspaceConfigFn)

	body := strings.NewReader(`{"old_name":"old-name","new_name":"new-name"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/workspace/rename", body)
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
			"my-ws": {Path: "/home/user/projects/myws"},
		},
	})

	handler := handleWorkspaceRename(mockWorkspaceConfigFn)

	body := strings.NewReader(`{"old_name":"my-ws","new_name":"renamed-ws"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/workspace/rename", body)
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

	cfg := readTestLoomConfig(t, dir)
	if cfg.DefaultWorkspace != "renamed-ws" {
		t.Errorf("expected default_workspace 'renamed-ws', got %q", cfg.DefaultWorkspace)
	}
	if _, ok := cfg.Workspaces["renamed-ws"]; !ok {
		t.Error("workspace 'renamed-ws' should exist in config")
	}
	if _, ok := cfg.Workspaces["my-ws"]; ok {
		t.Error("workspace 'my-ws' should no longer exist in config")
	}
}

func TestWorkspaceRename_DuplicateName(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)

	writeTestLoomConfig(t, dir, &loomConfigForRename{
		Version: 1,
		Workspaces: map[string]loomWorkspaceForRename{
			"alpha": {Path: "/a"},
			"beta":  {Path: "/b"},
		},
	})

	handler := handleWorkspaceRename(nil)

	body := strings.NewReader(`{"old_name":"alpha","new_name":"beta"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/workspace/rename", body)
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
			"ws": {Path: "/a"},
		},
	})

	handler := handleWorkspaceRename(nil)

	body := strings.NewReader(`{"old_name":"ws","new_name":""}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/workspace/rename", body)
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
	if !strings.Contains(resp.Error, "cannot be empty") {
		t.Errorf("expected 'cannot be empty' in error, got: %s", resp.Error)
	}
}

func TestWorkspaceRename_MaxLengthExceeded(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)

	writeTestLoomConfig(t, dir, &loomConfigForRename{
		Version: 1,
		Workspaces: map[string]loomWorkspaceForRename{
			"ws": {Path: "/a"},
		},
	})

	handler := handleWorkspaceRename(nil)

	longName := strings.Repeat("a", 65)
	body := strings.NewReader(`{"old_name":"ws","new_name":"` + longName + `"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/workspace/rename", body)
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
	if !strings.Contains(resp.Error, "too long") {
		t.Errorf("expected 'too long' in error, got: %s", resp.Error)
	}
}

func TestWorkspaceRename_InvalidCharacters(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)

	writeTestLoomConfig(t, dir, &loomConfigForRename{
		Version: 1,
		Workspaces: map[string]loomWorkspaceForRename{
			"ws": {Path: "/a"},
		},
	})

	handler := handleWorkspaceRename(nil)

	tests := []struct {
		name    string
		newName string
	}{
		{"space", "my workspace"},
		{"dot", "my.ws"},
		{"slash", "my/ws"},
		{"at", "my@ws"},
		{"bang", "my!ws"},
		{"hash", "my#ws"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := strings.NewReader(`{"old_name":"ws","new_name":"` + tt.newName + `"}`)
			req := httptest.NewRequest(http.MethodPatch, "/api/workspace/rename", body)
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
			if !strings.Contains(resp.Error, "alphanumeric") {
				t.Errorf("expected alphanumeric error, got: %s", resp.Error)
			}
		})
	}
}

func TestWorkspaceRename_WorkspaceNotFound(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)

	writeTestLoomConfig(t, dir, &loomConfigForRename{
		Version: 1,
		Workspaces: map[string]loomWorkspaceForRename{
			"existing": {Path: "/a"},
		},
	})

	handler := handleWorkspaceRename(nil)

	body := strings.NewReader(`{"old_name":"nonexistent","new_name":"new-name"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/workspace/rename", body)
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
			"my-ws": {Path: "/a"},
		},
	})

	handler := handleWorkspaceRename(nil)

	body := strings.NewReader(`{"old_name":"my-ws","new_name":"my-ws"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/workspace/rename", body)
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

	// Verify config is untouched
	cfg := readTestLoomConfig(t, dir)
	if _, ok := cfg.Workspaces["my-ws"]; !ok {
		t.Error("workspace 'my-ws' should still exist")
	}
}

func TestWorkspaceRename_NoConfigFound(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)
	// No config.yaml written — loadLoomConfig returns nil, nil

	handler := handleWorkspaceRename(nil)

	body := strings.NewReader(`{"old_name":"ws","new_name":"new-ws"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/workspace/rename", body)
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

func TestWorkspaceRename_InvalidRequestBody(t *testing.T) {
	handler := handleWorkspaceRename(nil)

	body := strings.NewReader(`{invalid json}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/workspace/rename", body)
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

func TestWorkspaceRename_RequestBodyTooLarge(t *testing.T) {
	handler := handleWorkspaceRename(nil)

	// Create a JSON body larger than 1MB (maxRequestBody)
	largeBody := `{"old_name":"` + strings.Repeat("a", 1<<20+1) + `"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/workspace/rename", strings.NewReader(largeBody))
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

func TestWorkspaceRename_SuccessWithWorkspaceConfigFn(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)

	writeTestLoomConfig(t, dir, &loomConfigForRename{
		Version: 1,
		Workspaces: map[string]loomWorkspaceForRename{
			"ws": {Path: "/home/user/projects/ws"},
		},
	})

	handler := handleWorkspaceRename(mockWorkspaceConfigFn)

	body := strings.NewReader(`{"old_name":"ws","new_name":"renamed"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/workspace/rename", body)
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
	if resp.Data == nil {
		t.Fatal("expected Data to be non-nil when workspaceConfigFn provided")
	}
	if resp.Data.Name != "test-ws" {
		t.Errorf("expected data name 'test-ws', got %q", resp.Data.Name)
	}
}

func TestWorkspaceRename_SuccessNilWorkspaceConfigFn(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)

	writeTestLoomConfig(t, dir, &loomConfigForRename{
		Version: 1,
		Workspaces: map[string]loomWorkspaceForRename{
			"ws": {Path: "/home/user/projects/ws"},
		},
	})

	handler := handleWorkspaceRename(nil)

	body := strings.NewReader(`{"old_name":"ws","new_name":"renamed"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/workspace/rename", body)
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
	if resp.Data != nil {
		t.Error("expected Data to be nil when workspaceConfigFn is nil")
	}
}

func TestWorkspaceRename_MaxLengthExactly64(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)

	writeTestLoomConfig(t, dir, &loomConfigForRename{
		Version: 1,
		Workspaces: map[string]loomWorkspaceForRename{
			"ws": {Path: "/a"},
		},
	})

	handler := handleWorkspaceRename(nil)

	// Exactly 64 characters should be accepted
	name64 := strings.Repeat("a", 64)
	body := strings.NewReader(`{"old_name":"ws","new_name":"` + name64 + `"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/workspace/rename", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for exactly 64 char name, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
}

func TestLoomWorkspaceForRename_RoundTrip(t *testing.T) {
	ws := loomWorkspaceForRename{
		ID:   "test-uuid-abc-123",
		Path: "/home/user/projects/myws",
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
}

func TestLoomWorkspaceForRename_EmptyID(t *testing.T) {
	ws := loomWorkspaceForRename{
		ID:   "",
		Path: "/home/user/projects/myws",
	}

	data, err := yaml.Marshal(ws)
	if err != nil {
		t.Fatalf("yaml.Marshal() error = %v", err)
	}

	var got loomWorkspaceForRename
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}

	if got.ID != "" {
		t.Errorf("ID = %q, want empty", got.ID)
	}
	if got.Path != ws.Path {
		t.Errorf("Path = %q, want %q", got.Path, ws.Path)
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
	if renamed.Path != "/home/user/projects/foo" {
		t.Errorf("Path = %q, want %q", renamed.Path, "/home/user/projects/foo")
	}
	if cfg.DefaultWorkspace != "bar" {
		t.Errorf("DefaultWorkspace = %q, want %q", cfg.DefaultWorkspace, "bar")
	}
	if len(cfg.WorkspaceOrder) != 1 || cfg.WorkspaceOrder[0] != "bar" {
		t.Errorf("WorkspaceOrder = %v, want [bar]", cfg.WorkspaceOrder)
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

	body := strings.NewReader(`{"old_name":"myws","new_name":"renamed"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/workspace/rename", body)
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
