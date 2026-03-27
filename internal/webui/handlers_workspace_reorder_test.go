package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestWorkspaceReorder_ValidNames(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)

	writeTestLoomConfig(t, dir, &loomConfigForRename{
		Version: 1,
		Workspaces: map[string]loomWorkspaceForRename{
			"alpha": {Path: "/a"},
			"beta":  {Path: "/b"},
			"gamma": {Path: "/c"},
		},
	})

	handler := handleWorkspaceReorder(mockWorkspaceConfigFn)

	body := strings.NewReader(`{"order":["gamma","alpha","beta"]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/workspaces/order", body)
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

	// Verify config was updated with the order
	cfg := readTestLoomConfig(t, dir)
	if len(cfg.WorkspaceOrder) != 3 {
		t.Fatalf("expected 3 items in workspace_order, got %d", len(cfg.WorkspaceOrder))
	}
	expected := []string{"gamma", "alpha", "beta"}
	for i, name := range expected {
		if cfg.WorkspaceOrder[i] != name {
			t.Errorf("workspace_order[%d] = %q, want %q", i, cfg.WorkspaceOrder[i], name)
		}
	}
}

func TestWorkspaceReorder_FiltersUnknownNames(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)

	writeTestLoomConfig(t, dir, &loomConfigForRename{
		Version: 1,
		Workspaces: map[string]loomWorkspaceForRename{
			"alpha": {Path: "/a"},
			"beta":  {Path: "/b"},
		},
	})

	handler := handleWorkspaceReorder(nil)

	// Include unknown names that should be filtered out
	body := strings.NewReader(`{"order":["beta","nonexistent","alpha","also-unknown"]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/workspaces/order", body)
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

	// Verify only valid names were saved
	cfg := readTestLoomConfig(t, dir)
	if len(cfg.WorkspaceOrder) != 2 {
		t.Fatalf("expected 2 items in workspace_order, got %d: %v", len(cfg.WorkspaceOrder), cfg.WorkspaceOrder)
	}
	if cfg.WorkspaceOrder[0] != "beta" {
		t.Errorf("workspace_order[0] = %q, want %q", cfg.WorkspaceOrder[0], "beta")
	}
	if cfg.WorkspaceOrder[1] != "alpha" {
		t.Errorf("workspace_order[1] = %q, want %q", cfg.WorkspaceOrder[1], "alpha")
	}
}

func TestWorkspaceReorder_EmptyBody(t *testing.T) {
	handler := handleWorkspaceReorder(nil)

	req := httptest.NewRequest(http.MethodPut, "/api/workspaces/order", strings.NewReader(""))
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

func TestWorkspaceReorder_EmptyOrderArray(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)

	writeTestLoomConfig(t, dir, &loomConfigForRename{
		Version:        1,
		WorkspaceOrder: []string{"beta", "alpha"},
		Workspaces: map[string]loomWorkspaceForRename{
			"alpha": {Path: "/a"},
			"beta":  {Path: "/b"},
		},
	})

	handler := handleWorkspaceReorder(nil)

	body := strings.NewReader(`{"order":[]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/workspaces/order", body)
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

	// Verify custom ordering was cleared
	cfg := readTestLoomConfig(t, dir)
	if len(cfg.WorkspaceOrder) != 0 {
		t.Errorf("expected empty workspace_order, got %v", cfg.WorkspaceOrder)
	}
}

func TestWorkspaceReorder_NoConfigFound(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)
	// No config.yaml written — loadLoomConfig returns nil, nil

	handler := handleWorkspaceReorder(nil)

	body := strings.NewReader(`{"order":["alpha"]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/workspaces/order", body)
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

func TestWorkspaceReorder_ConfigSaveFails(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)

	writeTestLoomConfig(t, dir, &loomConfigForRename{
		Version: 1,
		Workspaces: map[string]loomWorkspaceForRename{
			"alpha": {Path: "/a"},
		},
	})

	handler := handleWorkspaceReorder(nil)

	// Remove the directory's write permission to prevent creating temp files
	// (saveLoomConfig uses os.CreateTemp in the config dir)
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() {
		os.Chmod(dir, 0755)
	})

	body := strings.NewReader(`{"order":["alpha"]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/workspaces/order", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp workspaceResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Success {
		t.Fatal("expected failure")
	}
	if !strings.Contains(resp.Error, "failed to save config") {
		t.Errorf("expected 'failed to save config' in error, got: %s", resp.Error)
	}
}

func TestWorkspaceReorder_RequestBodyTooLarge(t *testing.T) {
	handler := handleWorkspaceReorder(nil)

	// Create a JSON body larger than 1MB (maxRequestBody)
	largeBody := `{"order":["` + strings.Repeat("a", 1<<20+1) + `"]}`
	req := httptest.NewRequest(http.MethodPut, "/api/workspaces/order", strings.NewReader(largeBody))
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

func TestWorkspaceReorder_NilWorkspaceConfigFn(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)

	writeTestLoomConfig(t, dir, &loomConfigForRename{
		Version: 1,
		Workspaces: map[string]loomWorkspaceForRename{
			"alpha": {Path: "/a"},
			"beta":  {Path: "/b"},
		},
	})

	handler := handleWorkspaceReorder(nil)

	body := strings.NewReader(`{"order":["beta","alpha"]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/workspaces/order", body)
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

	// Verify config was still updated
	cfg := readTestLoomConfig(t, dir)
	if len(cfg.WorkspaceOrder) != 2 {
		t.Fatalf("expected 2 items in workspace_order, got %d", len(cfg.WorkspaceOrder))
	}
}

func TestWorkspaceReorder_InvalidJSON(t *testing.T) {
	handler := handleWorkspaceReorder(nil)

	body := strings.NewReader(`{invalid json}`)
	req := httptest.NewRequest(http.MethodPut, "/api/workspaces/order", body)
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

func TestWorkspaceReorder_ResolvesUUIDs(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)

	writeTestLoomConfig(t, dir, &loomConfigForRename{
		Version: 1,
		Workspaces: map[string]loomWorkspaceForRename{
			"alpha": {ID: "uuid-1", Path: "/a"},
			"beta":  {ID: "uuid-2", Path: "/b"},
		},
	})

	handler := handleWorkspaceReorder(nil)

	body := strings.NewReader(`{"order":["uuid-2","uuid-1"]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/workspaces/order", body)
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
	if len(cfg.WorkspaceOrder) != 2 {
		t.Fatalf("expected 2 items in workspace_order, got %d: %v", len(cfg.WorkspaceOrder), cfg.WorkspaceOrder)
	}
	if cfg.WorkspaceOrder[0] != "beta" {
		t.Errorf("workspace_order[0] = %q, want %q", cfg.WorkspaceOrder[0], "beta")
	}
	if cfg.WorkspaceOrder[1] != "alpha" {
		t.Errorf("workspace_order[1] = %q, want %q", cfg.WorkspaceOrder[1], "alpha")
	}
}

func TestWorkspaceReorder_MixedNamesAndUUIDs(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)

	writeTestLoomConfig(t, dir, &loomConfigForRename{
		Version: 1,
		Workspaces: map[string]loomWorkspaceForRename{
			"alpha": {ID: "uuid-1", Path: "/a"},
			"beta":  {ID: "uuid-2", Path: "/b"},
			"gamma": {ID: "uuid-3", Path: "/c"},
		},
	})

	handler := handleWorkspaceReorder(nil)

	body := strings.NewReader(`{"order":["gamma","uuid-1","beta"]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/workspaces/order", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	cfg := readTestLoomConfig(t, dir)
	if len(cfg.WorkspaceOrder) != 3 {
		t.Fatalf("expected 3 items in workspace_order, got %d: %v", len(cfg.WorkspaceOrder), cfg.WorkspaceOrder)
	}
	expected := []string{"gamma", "alpha", "beta"}
	for i, name := range expected {
		if cfg.WorkspaceOrder[i] != name {
			t.Errorf("workspace_order[%d] = %q, want %q", i, cfg.WorkspaceOrder[i], name)
		}
	}
}

func TestWorkspaceReorder_DeduplicatesUUIDAndName(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)

	writeTestLoomConfig(t, dir, &loomConfigForRename{
		Version: 1,
		Workspaces: map[string]loomWorkspaceForRename{
			"alpha": {ID: "uuid-1", Path: "/a"},
			"beta":  {ID: "uuid-2", Path: "/b"},
		},
	})

	handler := handleWorkspaceReorder(nil)

	// uuid-1 resolves to "alpha", which also appears later — should be deduped
	body := strings.NewReader(`{"order":["uuid-1","beta","alpha"]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/workspaces/order", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	cfg := readTestLoomConfig(t, dir)
	if len(cfg.WorkspaceOrder) != 2 {
		t.Fatalf("expected 2 items in workspace_order, got %d: %v", len(cfg.WorkspaceOrder), cfg.WorkspaceOrder)
	}
	if cfg.WorkspaceOrder[0] != "alpha" {
		t.Errorf("workspace_order[0] = %q, want %q", cfg.WorkspaceOrder[0], "alpha")
	}
	if cfg.WorkspaceOrder[1] != "beta" {
		t.Errorf("workspace_order[1] = %q, want %q", cfg.WorkspaceOrder[1], "beta")
	}
}

func TestWorkspaceReorder_AllUUIDsUnknown(t *testing.T) {
	dir := t.TempDir()
	setLoomConfigDir(t, dir)

	writeTestLoomConfig(t, dir, &loomConfigForRename{
		Version: 1,
		Workspaces: map[string]loomWorkspaceForRename{
			"alpha": {ID: "uuid-1", Path: "/a"},
		},
	})

	handler := handleWorkspaceReorder(nil)

	body := strings.NewReader(`{"order":["unknown-uuid-1","unknown-uuid-2"]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/workspaces/order", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	cfg := readTestLoomConfig(t, dir)
	if len(cfg.WorkspaceOrder) != 0 {
		t.Errorf("expected empty workspace_order, got %v", cfg.WorkspaceOrder)
	}
}
