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

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/tabs", nil)
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

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/tabs/nonexistent", nil)
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

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/tabs/invalid%20name", nil)
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
		Label:       "Test Label",
		Notes:       "Test Notes",
		SortOrder:   1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	handler := handleGetTerminalTab(store)
	req := httptest.NewRequest(http.MethodGet, "/api/terminal/tabs/test-session", nil)
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
		Label:       "Original",
		SortOrder:   1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	handler := handlePatchTerminalTab(store, hub)
	body := `{"label": "Updated Label"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/terminal/tabs/patch-test", strings.NewReader(body))
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
	req := httptest.NewRequest(http.MethodPatch, "/api/terminal/tabs/nonexistent", strings.NewReader(body))
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
		Label:       "Label",
		SortOrder:   1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	handler := handlePatchTerminalTab(store, hub)
	body := `{}`
	req := httptest.NewRequest(http.MethodPatch, "/api/terminal/tabs/empty-patch", strings.NewReader(body))
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
		Label:       "To Delete",
		SortOrder:   1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	handler := handleDeleteTerminalTab(store, hub)
	req := httptest.NewRequest(http.MethodDelete, "/api/terminal/tabs/delete-test", nil)
	req.SetPathValue("session", "delete-test")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	// Verify it's gone
	got, err := store.Get(ctx, "delete-test")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if got != nil {
		t.Error("expected nil after delete")
	}
}

func TestHandleDeleteTerminalTab_InvalidName(t *testing.T) {
	store, hub := setupTabMetaTest(t)
	handler := handleDeleteTerminalTab(store, hub)

	req := httptest.NewRequest(http.MethodDelete, "/api/terminal/tabs/bad%20name", nil)
	req.SetPathValue("session", "bad name")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestWorkspaceFromRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		url  string
		want string
	}{
		{"no workspace param uses default", "/api/terminal/ws?session=test", DefaultWorkspace},
		{"empty workspace param uses default", "/api/terminal/ws?session=test&workspace=", DefaultWorkspace},
		{"explicit workspace", "/api/terminal/ws?session=test&workspace=myproject", "myproject"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			got := workspaceFromRequest(req)
			if got != tt.want {
				t.Errorf("workspaceFromRequest() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetInWorkspace_ReturnsMetadata(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { client.Close() })

	store := tabmeta.NewStore(client, nil)

	ctx := context.Background()
	now := time.Now().UTC()

	// Set metadata in "myproject" workspace using raw Redis commands
	// (store.Set only writes to the non-workspace key path)
	key := "terminal:meta:myproject:test-session"
	client.HSet(ctx, key, map[string]interface{}{
		"label":      "My Session",
		"notes":      "",
		"issue_id":   "PROJ-42",
		"sort_order": "1",
		"created_at": now.Format(time.RFC3339),
		"updated_at": now.Format(time.RFC3339),
	})

	// GetInWorkspace with correct workspace should find it
	meta, err := store.GetInWorkspace(ctx, "myproject", "test-session")
	if err != nil {
		t.Fatalf("GetInWorkspace error: %v", err)
	}
	if meta == nil {
		t.Fatal("expected metadata, got nil")
	}
	if meta.IssueID != "PROJ-42" {
		t.Errorf("IssueID = %q, want %q", meta.IssueID, "PROJ-42")
	}

	// GetInWorkspace with wrong workspace should not find it
	meta2, err := store.GetInWorkspace(ctx, "other-workspace", "test-session")
	if err != nil {
		t.Fatalf("GetInWorkspace error: %v", err)
	}
	if meta2 != nil {
		t.Errorf("expected nil for wrong workspace, got %+v", meta2)
	}

	// GetInWorkspace with default workspace should use default key
	defaultKey := "terminal:meta:default:test-session"
	client.HSet(ctx, defaultKey, map[string]interface{}{
		"label":      "Default Session",
		"notes":      "",
		"issue_id":   "DEF-1",
		"sort_order": "1",
		"created_at": now.Format(time.RFC3339),
		"updated_at": now.Format(time.RFC3339),
	})

	meta3, err := store.GetInWorkspace(ctx, "default", "test-session")
	if err != nil {
		t.Fatalf("GetInWorkspace error: %v", err)
	}
	if meta3 == nil {
		t.Fatal("expected metadata for default workspace, got nil")
	}
	if meta3.IssueID != "DEF-1" {
		t.Errorf("IssueID = %q, want %q", meta3.IssueID, "DEF-1")
	}
}
