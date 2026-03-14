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
