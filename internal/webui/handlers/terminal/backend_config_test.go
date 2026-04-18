package terminal

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

	"github.com/tysonthomas9/loomcli/internal/rpc"
)

// mockConfigClient implements configClient for testing.
type mockConfigClient struct {
	statusFunc func() (*rpc.StatusResponse, error)
}

func (m *mockConfigClient) Status() (*rpc.StatusResponse, error) {
	if m.statusFunc != nil {
		return m.statusFunc()
	}
	return nil, errors.New("statusFunc not implemented")
}

// mockConfigPool implements configConnectionGetter for testing.
type mockConfigPool struct {
	getFunc func(ctx context.Context) (configClient, error)
	putFunc func(client configClient)
}

func (m *mockConfigPool) Get(ctx context.Context) (configClient, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx)
	}
	return nil, errors.New("getFunc not implemented")
}

func (m *mockConfigPool) Put(client configClient) {
	if m.putFunc != nil {
		m.putFunc(client)
	}
}

func (m *mockConfigPool) Discard(client configClient) {
	// no-op for tests
}

// newMockConfigPool creates a pool that returns a client pointing to the given workspace path.
func newMockConfigPool(wsPath string) *mockConfigPool {
	return &mockConfigPool{
		getFunc: func(ctx context.Context) (configClient, error) {
			return &mockConfigClient{
				statusFunc: func() (*rpc.StatusResponse, error) {
					return &rpc.StatusResponse{WorkspacePath: wsPath}, nil
				},
			}, nil
		},
		putFunc: func(client configClient) {},
	}
}

func TestHandleGetBackendConfig_WithProjectBackend(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "loom.yaml"), []byte("backend: codex\n"), 0644)

	pool := newMockConfigPool(dir)
	handler := handleGetBackendConfigWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/config/backend", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp BackendConfigResponse
	json.NewDecoder(rec.Body).Decode(&resp)

	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
	if resp.Data.Backend != "codex" {
		t.Errorf("expected backend codex, got %s", resp.Data.Backend)
	}
	if resp.Data.Source != "project" {
		t.Errorf("expected source project, got %s", resp.Data.Source)
	}
	if len(resp.Data.Available) != 6 {
		t.Errorf("expected 6 available backends (including shell), got %d", len(resp.Data.Available))
	}
}

func TestHandleGetBackendConfig_NoBackendField(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "loom.yaml"), []byte("agents:\n  - worktree: nova\n    role: task\n"), 0644)

	pool := newMockConfigPool(dir)
	handler := handleGetBackendConfigWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/config/backend", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp BackendConfigResponse
	json.NewDecoder(rec.Body).Decode(&resp)

	if resp.Data.Backend != "claude" {
		t.Errorf("expected default backend claude, got %s", resp.Data.Backend)
	}
	if resp.Data.Source != "default" {
		t.Errorf("expected source default, got %s", resp.Data.Source)
	}
}

func TestHandleGetBackendConfig_NoFile(t *testing.T) {
	dir := t.TempDir()

	pool := newMockConfigPool(dir)
	handler := handleGetBackendConfigWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/config/backend", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp BackendConfigResponse
	json.NewDecoder(rec.Body).Decode(&resp)

	if resp.Data.Backend != "claude" {
		t.Errorf("expected default backend claude, got %s", resp.Data.Backend)
	}
	if resp.Data.Source != "default" {
		t.Errorf("expected source default, got %s", resp.Data.Source)
	}
	if len(resp.Data.Agents) != 0 {
		t.Errorf("expected 0 agents, got %d", len(resp.Data.Agents))
	}
}

func TestHandleGetBackendConfig_WithAgentOverrides(t *testing.T) {
	dir := t.TempDir()
	yaml := `backend: claude
agents:
  - worktree: falcon
    role: plan
    backend: codex
  - worktree: nova
    role: reviewer
`
	os.WriteFile(filepath.Join(dir, "loom.yaml"), []byte(yaml), 0644)

	pool := newMockConfigPool(dir)
	handler := handleGetBackendConfigWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/config/backend", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp BackendConfigResponse
	json.NewDecoder(rec.Body).Decode(&resp)

	if len(resp.Data.Agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(resp.Data.Agents))
	}
	if resp.Data.Agents[0].Worktree != "falcon" || resp.Data.Agents[0].Backend != "codex" {
		t.Errorf("unexpected agent[0]: %+v", resp.Data.Agents[0])
	}
	if resp.Data.Agents[1].Worktree != "nova" || resp.Data.Agents[1].Backend != "" {
		t.Errorf("unexpected agent[1]: %+v", resp.Data.Agents[1])
	}
}

func TestHandleGetBackendConfig_NilPool(t *testing.T) {
	handler := handleGetBackendConfigWithPool(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/config/backend", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestHandleGetBackendConfig_DaemonUnavailable(t *testing.T) {
	pool := &mockConfigPool{
		getFunc: func(ctx context.Context) (configClient, error) {
			return nil, errors.New("connection refused")
		},
	}
	handler := handleGetBackendConfigWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/config/backend", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestHandlePatchBackendConfig_ValidBackend(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "loom.yaml"), []byte("backend: claude\n"), 0644)

	pool := newMockConfigPool(dir)
	handler := handlePatchBackendConfigWithPool(pool)

	body := strings.NewReader(`{"backend":"codex"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/config/backend", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp BackendConfigResponse
	json.NewDecoder(rec.Body).Decode(&resp)

	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
	if resp.Data.Backend != "codex" {
		t.Errorf("expected backend codex, got %s", resp.Data.Backend)
	}
	if resp.Data.Source != "project" {
		t.Errorf("expected source project, got %s", resp.Data.Source)
	}

	// Verify file was updated
	data, _ := os.ReadFile(filepath.Join(dir, "loom.yaml"))
	if !strings.Contains(string(data), "backend: codex") {
		t.Errorf("loom.yaml not updated: %s", string(data))
	}
}

func TestHandlePatchBackendConfig_InvalidBackend(t *testing.T) {
	dir := t.TempDir()
	pool := newMockConfigPool(dir)
	handler := handlePatchBackendConfigWithPool(pool)

	body := strings.NewReader(`{"backend":"invalid"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/config/backend", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}

	var resp BackendConfigResponse
	json.NewDecoder(rec.Body).Decode(&resp)

	if resp.Success {
		t.Fatal("expected failure")
	}
	if !strings.Contains(resp.Error, "invalid backend") {
		t.Errorf("expected invalid backend error, got: %s", resp.Error)
	}
}

func TestHandlePatchBackendConfig_EmptyBody(t *testing.T) {
	dir := t.TempDir()
	pool := newMockConfigPool(dir)
	handler := handlePatchBackendConfigWithPool(pool)

	req := httptest.NewRequest(http.MethodPatch, "/api/config/backend", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandlePatchBackendConfig_PreservesOtherFields(t *testing.T) {
	dir := t.TempDir()
	original := `backend: claude
agents:
    - worktree: falcon
      role: plan
      backend: codex
    - worktree: nova
      role: task
`
	os.WriteFile(filepath.Join(dir, "loom.yaml"), []byte(original), 0644)

	pool := newMockConfigPool(dir)
	handler := handlePatchBackendConfigWithPool(pool)

	body := strings.NewReader(`{"backend":"opencode"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/config/backend", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify agents are preserved
	data, _ := os.ReadFile(filepath.Join(dir, "loom.yaml"))
	content := string(data)
	if !strings.Contains(content, "backend: opencode") {
		t.Errorf("backend not updated: %s", content)
	}
	if !strings.Contains(content, "falcon") {
		t.Errorf("agents not preserved: %s", content)
	}
	if !strings.Contains(content, "nova") {
		t.Errorf("agents not preserved: %s", content)
	}
}

func TestHandlePatchBackendConfig_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	// No loom.yaml exists

	pool := newMockConfigPool(dir)
	handler := handlePatchBackendConfigWithPool(pool)

	body := strings.NewReader(`{"backend":"codex"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/config/backend", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify file was created
	data, err := os.ReadFile(filepath.Join(dir, "loom.yaml"))
	if err != nil {
		t.Fatalf("loom.yaml not created: %v", err)
	}
	if !strings.Contains(string(data), "backend: codex") {
		t.Errorf("unexpected content: %s", string(data))
	}
}

func TestHandlePatchBackendConfig_NilPool(t *testing.T) {
	handler := handlePatchBackendConfigWithPool(nil)

	body := strings.NewReader(`{"backend":"codex"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/config/backend", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestHandlePatchBackendConfig_ShellRejected(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "loom.yaml"), []byte("backend: claude\n"), 0644)

	pool := newMockConfigPool(dir)
	handler := handlePatchBackendConfigWithPool(pool)

	body := strings.NewReader(`{"backend":"shell"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/config/backend", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp BackendConfigResponse
	json.NewDecoder(rec.Body).Decode(&resp)

	if resp.Success {
		t.Fatal("expected failure when setting shell as project default")
	}
	if !strings.Contains(resp.Error, "invalid backend") {
		t.Errorf("expected invalid backend error, got: %s", resp.Error)
	}

	// Verify loom.yaml was NOT updated
	data, _ := os.ReadFile(filepath.Join(dir, "loom.yaml"))
	if strings.Contains(string(data), "backend: shell") {
		t.Errorf("loom.yaml should not have been updated to shell: %s", string(data))
	}
}

func TestHandleGetBackendConfig_AvailableIncludesShell(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "loom.yaml"), []byte("backend: claude\n"), 0644)

	pool := newMockConfigPool(dir)
	handler := handleGetBackendConfigWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/config/backend", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp BackendConfigResponse
	json.NewDecoder(rec.Body).Decode(&resp)

	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}

	// Available should include shell as the last item
	if len(resp.Data.Available) != 6 {
		t.Errorf("expected 6 available backends, got %d: %v", len(resp.Data.Available), resp.Data.Available)
	}

	found := false
	for _, b := range resp.Data.Available {
		if b == "shell" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'shell' in available list, got: %v", resp.Data.Available)
	}
}
