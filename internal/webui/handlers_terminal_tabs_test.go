package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

func setupTabMetaTest(t *testing.T) (*tabmeta.Store, *SSEHub) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })

	hub := NewSSEHub()
	go hub.Run()
	t.Cleanup(func() { hub.Stop() })

	return tabmeta.NewStore(rdb, nil), hub
}

// withWorkspaceCtx injects a workspace ID into the request context,
// simulating what WorkspaceMiddleware does for workspace-scoped routes.
func withWorkspaceCtx(r *http.Request, ws string) *http.Request {
	return r.WithContext(WithWorkspace(r.Context(), ws))
}

// wrapWithWorkspace wraps an http.Handler so every request passing through
// it has the given workspace injected into its context. Used by tests that
// spin up httptest.NewServer — those bypass WorkspaceMiddleware entirely,
// so the handler would otherwise see an empty workspace context.
func wrapWithWorkspace(handler http.Handler, ws string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r.WithContext(WithWorkspace(r.Context(), ws)))
	})
}

func TestHandleListTerminalTabs_NilStore(t *testing.T) {
	handler := handleListTerminalTabs(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/terminal/tabs", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleListTerminalTabs_Empty(t *testing.T) {
	store, _ := setupTabMetaTest(t)
	handler := handleListTerminalTabs(store, nil)

	req := withWorkspaceCtx(httptest.NewRequest(http.MethodGet, "/api/terminal/tabs", nil), "default")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp tabMetadataResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true, got false: %s", resp.Error)
	}
}

func TestHandleGetTerminalTab_NotFound(t *testing.T) {
	store, _ := setupTabMetaTest(t)
	handler := handleGetTerminalTab(store)

	req := withWorkspaceCtx(httptest.NewRequest(http.MethodGet, "/api/terminal/tabs/nonexistent", nil), "default")
	req.SetPathValue("session", "nonexistent")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestHandleGetTerminalTab_InvalidName(t *testing.T) {
	store, _ := setupTabMetaTest(t)
	handler := handleGetTerminalTab(store)

	req := withWorkspaceCtx(httptest.NewRequest(http.MethodGet, "/api/terminal/tabs/invalid%20name", nil), "default")
	req.SetPathValue("session", "invalid name")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestHandleGetTerminalTab_Found(t *testing.T) {
	store, _ := setupTabMetaTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	if err := store.Set(ctx, &tabmeta.TabMetadata{
		SessionName: "test-session",
		Workspace:   "default",
		Label:       "Test Label",
		Notes:       "Test Notes",
		SortOrder:   1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	handler := handleGetTerminalTab(store)
	req := withWorkspaceCtx(httptest.NewRequest(http.MethodGet, "/api/terminal/tabs/test-session", nil), "default")
	req.SetPathValue("session", "test-session")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp struct {
		Success bool                `json:"success"`
		Data    tabmeta.TabMetadata `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.Label != "Test Label" {
		t.Errorf("Label = %q, want %q", resp.Data.Label, "Test Label")
	}
}

func TestHandlePatchTerminalTab(t *testing.T) {
	store, hub := setupTabMetaTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	if err := store.Set(ctx, &tabmeta.TabMetadata{
		SessionName: "patch-test",
		Workspace:   "default",
		Label:       "Original",
		SortOrder:   1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	handler := handlePatchTerminalTab(store, hub)
	body := `{"label": "Updated Label"}`
	req := withWorkspaceCtx(httptest.NewRequest(http.MethodPatch, "/api/terminal/tabs/patch-test", strings.NewReader(body)), "default")
	req.SetPathValue("session", "patch-test")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp struct {
		Success bool                `json:"success"`
		Data    tabmeta.TabMetadata `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.Label != "Updated Label" {
		t.Errorf("Label = %q, want %q", resp.Data.Label, "Updated Label")
	}
}

func TestHandlePatchTerminalTab_NotFound(t *testing.T) {
	store, hub := setupTabMetaTest(t)
	handler := handlePatchTerminalTab(store, hub)

	body := `{"label": "New Label"}`
	req := withWorkspaceCtx(httptest.NewRequest(http.MethodPatch, "/api/terminal/tabs/nonexistent", strings.NewReader(body)), "default")
	req.SetPathValue("session", "nonexistent")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestHandlePatchTerminalTab_EmptyBody(t *testing.T) {
	store, hub := setupTabMetaTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	if err := store.Set(ctx, &tabmeta.TabMetadata{
		SessionName: "empty-patch",
		Workspace:   "default",
		Label:       "Label",
		SortOrder:   1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	handler := handlePatchTerminalTab(store, hub)
	body := `{}`
	req := withWorkspaceCtx(httptest.NewRequest(http.MethodPatch, "/api/terminal/tabs/empty-patch", strings.NewReader(body)), "default")
	req.SetPathValue("session", "empty-patch")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestHandleDeleteTerminalTab(t *testing.T) {
	store, hub := setupTabMetaTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	if err := store.Set(ctx, &tabmeta.TabMetadata{
		SessionName: "delete-test",
		Workspace:   "default",
		Label:       "To Delete",
		SortOrder:   1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	handler := handleDeleteTerminalTab(store, hub)
	req := withWorkspaceCtx(httptest.NewRequest(http.MethodDelete, "/api/terminal/tabs/delete-test", nil), "default")
	req.SetPathValue("session", "delete-test")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	// Verify it's gone
	got, err := store.Get(ctx, "default", "delete-test")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if got != nil {
		t.Error("expected nil after delete")
	}
}

// ── PUT handler tests ─────────────────────────────────────────────────────────

func TestHandlePutTerminalTab_CreatesNew(t *testing.T) {
	store, hub := setupTabMetaTest(t)
	handler := handlePutTerminalTab(store, hub)

	body := `{"label": "lead-claude-1", "sort_order": 0}`
	req := withWorkspaceCtx(httptest.NewRequest(http.MethodPut, "/api/terminal/tabs/lead-claude-1", strings.NewReader(body)), "default")
	req.SetPathValue("session", "lead-claude-1")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp struct {
		Success bool                `json:"success"`
		Data    tabmeta.TabMetadata `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Fatal("expected success=true")
	}
	if resp.Data.Label != "lead-claude-1" {
		t.Errorf("Label = %q, want %q", resp.Data.Label, "lead-claude-1")
	}

	// Verify stored in Redis
	meta, err := store.Get(context.Background(), "default", "lead-claude-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if meta == nil {
		t.Fatal("expected metadata to be stored")
	}
	if meta.Label != "lead-claude-1" {
		t.Errorf("stored Label = %q, want %q", meta.Label, "lead-claude-1")
	}
}

func TestHandlePutTerminalTab_InvalidSession(t *testing.T) {
	store, hub := setupTabMetaTest(t)
	handler := handlePutTerminalTab(store, hub)

	body := `{"label": "test"}`
	req := withWorkspaceCtx(httptest.NewRequest(http.MethodPut, "/api/terminal/tabs/invalid%20name", strings.NewReader(body)), "default")
	req.SetPathValue("session", "invalid name")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestHandlePutTerminalTab_EmptyLabel(t *testing.T) {
	store, hub := setupTabMetaTest(t)
	handler := handlePutTerminalTab(store, hub)

	body := `{"label": "", "sort_order": 0}`
	req := withWorkspaceCtx(httptest.NewRequest(http.MethodPut, "/api/terminal/tabs/test-session", strings.NewReader(body)), "default")
	req.SetPathValue("session", "test-session")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestHandlePutTerminalTab_Idempotent(t *testing.T) {
	store, hub := setupTabMetaTest(t)
	handler := handlePutTerminalTab(store, hub)

	body := `{"label": "lead-claude-1", "sort_order": 0}`

	// First PUT
	req1 := withWorkspaceCtx(httptest.NewRequest(http.MethodPut, "/api/terminal/tabs/lead-claude-1", strings.NewReader(body)), "default")
	req1.SetPathValue("session", "lead-claude-1")
	rr1 := httptest.NewRecorder()
	handler(rr1, req1)

	if rr1.Code != http.StatusOK {
		t.Fatalf("first PUT: status = %d, want %d", rr1.Code, http.StatusOK)
	}

	// Second PUT (same data)
	req2 := withWorkspaceCtx(httptest.NewRequest(http.MethodPut, "/api/terminal/tabs/lead-claude-1", strings.NewReader(body)), "default")
	req2.SetPathValue("session", "lead-claude-1")
	rr2 := httptest.NewRecorder()
	handler(rr2, req2)

	if rr2.Code != http.StatusOK {
		t.Fatalf("second PUT: status = %d, want %d", rr2.Code, http.StatusOK)
	}
}

func TestHandlePutTerminalTab_NilStore(t *testing.T) {
	handler := handlePutTerminalTab(nil, nil)

	body := `{"label": "test"}`
	req := httptest.NewRequest(http.MethodPut, "/api/terminal/tabs/test-session", strings.NewReader(body))
	req.SetPathValue("session", "test-session")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}
}

func TestHandlePutTerminalTab_WithNotes(t *testing.T) {
	store, hub := setupTabMetaTest(t)
	handler := handlePutTerminalTab(store, hub)

	body := `{"label": "lead-claude-1", "sort_order": 2, "notes": "some notes"}`
	req := withWorkspaceCtx(httptest.NewRequest(http.MethodPut, "/api/terminal/tabs/lead-claude-1", strings.NewReader(body)), "default")
	req.SetPathValue("session", "lead-claude-1")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	meta, err := store.Get(context.Background(), "default", "lead-claude-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if meta.Notes != "some notes" {
		t.Errorf("Notes = %q, want %q", meta.Notes, "some notes")
	}
	if meta.SortOrder != 2 {
		t.Errorf("SortOrder = %d, want %d", meta.SortOrder, 2)
	}
}

func TestHandleDeleteTerminalTab_InvalidName(t *testing.T) {
	store, hub := setupTabMetaTest(t)
	handler := handleDeleteTerminalTab(store, hub)

	req := withWorkspaceCtx(httptest.NewRequest(http.MethodDelete, "/api/terminal/tabs/bad%20name", nil), "default")
	req.SetPathValue("session", "bad name")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestHandleListTerminalTabs_WithWorkspaceContext(t *testing.T) {
	store, _ := setupTabMetaTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	// Create tab in "ws-a"
	if err := store.Set(ctx, &tabmeta.TabMetadata{
		SessionName: "tab-a",
		Workspace:   "ws-a",
		Label:       "Tab A",
		SortOrder:   1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Create tab in "ws-b"
	if err := store.Set(ctx, &tabmeta.TabMetadata{
		SessionName: "tab-b",
		Workspace:   "ws-b",
		Label:       "Tab B",
		SortOrder:   1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	handler := handleListTerminalTabs(store, nil)

	// List tabs for ws-a via context — should only see tab-a
	req := withWorkspaceCtx(httptest.NewRequest(http.MethodGet, "/api/workspaces/ws-a/terminal/tabs", nil), "ws-a")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp struct {
		Success bool                  `json:"success"`
		Data    []tabmeta.TabMetadata `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 tab for ws-a, got %d", len(resp.Data))
	}
	if resp.Data[0].SessionName != "tab-a" {
		t.Errorf("expected tab-a, got %s", resp.Data[0].SessionName)
	}
}
