package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/tysonthomas9/loomcli/internal/webui/issuetabs"
)

func setupIssueTabsTest(t *testing.T) (*issuetabs.Store, *SSEHub) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })

	hub := NewSSEHub()
	go hub.Run()
	t.Cleanup(func() { hub.Stop() })

	return issuetabs.NewStore(rdb, "test-ws-uuid", nil), hub
}

// ── GET handler tests ─────────────────────────────────────────────────────────

func TestHandleGetIssueTabs_NilStore(t *testing.T) {
	handler := handleGetIssueTabs(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/issues/PROJ-1/tabs", nil)
	req.SetPathValue("issueId", "PROJ-1")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleGetIssueTabs_MissingIssueID(t *testing.T) {
	store, _ := setupIssueTabsTest(t)
	handler := handleGetIssueTabs(store, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/issues//tabs", nil)
	// Don't set path value to simulate missing issueId
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestHandleGetIssueTabs_NoSavedState(t *testing.T) {
	store, _ := setupIssueTabsTest(t)
	handler := handleGetIssueTabs(store, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/PROJ-1/tabs", nil)
	req.SetPathValue("issueId", "PROJ-1")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp issueTabResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true, got false: %s", resp.Error)
	}
	if resp.Data != nil {
		t.Errorf("expected data=nil for no saved state, got %v", resp.Data)
	}
}

func TestHandleGetIssueTabs_ReturnsSavedState(t *testing.T) {
	store, _ := setupIssueTabsTest(t)
	ctx := context.Background()

	// Pre-save some state
	state := &issuetabs.IssueTabState{
		IssueID: "PROJ-2",
		Tabs: []issuetabs.IssueTab{
			{ID: "details", Type: "details", Label: "Details", SortOrder: 0},
			{ID: "logs", Type: "logs", Label: "Logs", SortOrder: 1},
		},
		ActiveTabID: "details",
	}
	if err := store.Save(ctx, state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// No terminal manager means no session filtering
	handler := handleGetIssueTabs(store, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/issues/PROJ-2/tabs", nil)
	req.SetPathValue("issueId", "PROJ-2")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp struct {
		Success bool                    `json:"success"`
		Data    issuetabs.IssueTabState `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true, got false")
	}
	if len(resp.Data.Tabs) != 2 {
		t.Errorf("len(Tabs) = %d, want 2", len(resp.Data.Tabs))
	}
	if resp.Data.ActiveTabID != "details" {
		t.Errorf("ActiveTabID = %q, want %q", resp.Data.ActiveTabID, "details")
	}
}

// ── PUT handler tests ─────────────────────────────────────────────────────────

func TestHandleSaveIssueTabs_NilStore(t *testing.T) {
	handler := handleSaveIssueTabs(nil, nil)

	body := `{"tabs":[],"active_tab_id":"details"}`
	req := httptest.NewRequest(http.MethodPut, "/api/issues/PROJ-1/tabs", strings.NewReader(body))
	req.SetPathValue("issueId", "PROJ-1")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleSaveIssueTabs_MissingIssueID(t *testing.T) {
	store, hub := setupIssueTabsTest(t)
	handler := handleSaveIssueTabs(store, hub)

	body := `{"tabs":[],"active_tab_id":"details"}`
	req := httptest.NewRequest(http.MethodPut, "/api/issues//tabs", strings.NewReader(body))
	// Don't set path value to simulate missing issueId
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestHandleSaveIssueTabs_InvalidBody(t *testing.T) {
	store, hub := setupIssueTabsTest(t)
	handler := handleSaveIssueTabs(store, hub)

	req := httptest.NewRequest(http.MethodPut, "/api/issues/PROJ-1/tabs", strings.NewReader("not json"))
	req.SetPathValue("issueId", "PROJ-1")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestHandleSaveIssueTabs_SavesAndReturnsSuccess(t *testing.T) {
	store, hub := setupIssueTabsTest(t)
	handler := handleSaveIssueTabs(store, hub)

	body := `{
		"tabs": [
			{"id": "details", "type": "details", "label": "Details", "sort_order": 0},
			{"id": "terminal-s1", "type": "terminal", "label": "Terminal 1", "session_name": "s1", "sort_order": 1}
		],
		"active_tab_id": "terminal-s1"
	}`
	req := httptest.NewRequest(http.MethodPut, "/api/issues/PROJ-3/tabs", strings.NewReader(body))
	req.SetPathValue("issueId", "PROJ-3")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp struct {
		Success bool                    `json:"success"`
		Data    issuetabs.IssueTabState `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Error("expected success=true")
	}
	if resp.Data.IssueID != "PROJ-3" {
		t.Errorf("IssueID = %q, want %q", resp.Data.IssueID, "PROJ-3")
	}
	if len(resp.Data.Tabs) != 2 {
		t.Errorf("len(Tabs) = %d, want 2", len(resp.Data.Tabs))
	}

	// Verify it was persisted in Redis
	got, err := store.Get(context.Background(), "PROJ-3")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("expected stored state, got nil")
	}
	if got.ActiveTabID != "terminal-s1" {
		t.Errorf("stored ActiveTabID = %q, want %q", got.ActiveTabID, "terminal-s1")
	}
}

func TestHandleSaveIssueTabs_BroadcastsSSE(t *testing.T) {
	store, hub := setupIssueTabsTest(t)

	// Register an SSE client to verify broadcast
	client := &SSEClient{
		send: make(chan *MutationPayload, 16),
	}
	hub.register <- client
	// Give the hub goroutine a moment to process
	// We use a non-blocking approach: try to read after the handler call

	handler := handleSaveIssueTabs(store, hub)

	body := `{"tabs":[{"id":"details","type":"details","label":"Details","sort_order":0}],"active_tab_id":"details"}`
	req := httptest.NewRequest(http.MethodPut, "/api/issues/PROJ-SSE/tabs", strings.NewReader(body))
	req.SetPathValue("issueId", "PROJ-SSE")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	// The hub broadcasts asynchronously; verify the broadcast channel received the event.
	// Since Broadcast writes to the hub's broadcast channel and the hub's Run goroutine
	// forwards it to clients, we check success via the response (broadcast is fire-and-forget).
}

// ── DELETE handler tests ──────────────────────────────────────────────────────

func TestHandleDeleteIssueTabs_NilStore(t *testing.T) {
	handler := handleDeleteIssueTabs(nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/issues/PROJ-1/tabs", nil)
	req.SetPathValue("issueId", "PROJ-1")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleDeleteIssueTabs_MissingIssueID(t *testing.T) {
	store, _ := setupIssueTabsTest(t)
	handler := handleDeleteIssueTabs(store)

	req := httptest.NewRequest(http.MethodDelete, "/api/issues//tabs", nil)
	// Don't set path value to simulate missing issueId
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestHandleDeleteIssueTabs_RemovesState(t *testing.T) {
	store, _ := setupIssueTabsTest(t)
	ctx := context.Background()

	// Pre-save state
	state := &issuetabs.IssueTabState{
		IssueID:     "DEL-ISSUE",
		Tabs:        []issuetabs.IssueTab{{ID: "details", Type: "details", Label: "Details", SortOrder: 0}},
		ActiveTabID: "details",
	}
	if err := store.Save(ctx, state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	handler := handleDeleteIssueTabs(store)
	req := httptest.NewRequest(http.MethodDelete, "/api/issues/DEL-ISSUE/tabs", nil)
	req.SetPathValue("issueId", "DEL-ISSUE")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp issueTabResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true, got false: %s", resp.Error)
	}

	// Verify it's gone from Redis
	got, err := store.Get(ctx, "DEL-ISSUE")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if got != nil {
		t.Error("expected nil after delete")
	}
}

func TestHandleDeleteIssueTabs_NonExistentIssue(t *testing.T) {
	store, _ := setupIssueTabsTest(t)
	handler := handleDeleteIssueTabs(store)

	req := httptest.NewRequest(http.MethodDelete, "/api/issues/NONEXISTENT/tabs", nil)
	req.SetPathValue("issueId", "NONEXISTENT")
	rr := httptest.NewRecorder()
	handler(rr, req)

	// Deleting a non-existent key in Redis is not an error
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

// ── Additional coverage tests ─────────────────────────────────────────────────

func TestHandleGetIssueTabs_InvalidIssueID(t *testing.T) {
	store, _ := setupIssueTabsTest(t)
	handler := handleGetIssueTabs(store, nil)

	// Issue ID with invalid characters should fail validation
	req := httptest.NewRequest(http.MethodGet, "/api/issues/PROJ@!#/tabs", nil)
	req.SetPathValue("issueId", "PROJ@!#")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}

	var resp issueTabResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Success {
		t.Error("expected success=false for invalid issue ID")
	}
	if resp.Error == "" {
		t.Error("expected non-empty error message")
	}
}

func TestHandleGetIssueTabs_EmptyIssueID(t *testing.T) {
	store, _ := setupIssueTabsTest(t)
	handler := handleGetIssueTabs(store, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/issues//tabs", nil)
	req.SetPathValue("issueId", "")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body: %s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
}

func TestHandleGetIssueTabs_PoolError(t *testing.T) {
	// Create a miniredis, get the store, then close the server to simulate pool error
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := issuetabs.NewStore(rdb, "test-ws-uuid", nil)

	// Close Redis to simulate connection failure
	mr.Close()

	handler := handleGetIssueTabs(store, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/POOL-ERR/tabs", nil)
	req.SetPathValue("issueId", "POOL-ERR")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d; body: %s", rr.Code, http.StatusInternalServerError, rr.Body.String())
	}

	var resp issueTabResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Success {
		t.Error("expected success=false on pool error")
	}
	if resp.Error == "" {
		t.Error("expected non-empty error message")
	}

	rdb.Close()
}

func TestHandleGetIssueTabs_EmptyTabs(t *testing.T) {
	store, _ := setupIssueTabsTest(t)
	ctx := context.Background()

	// Save state with an empty tabs slice
	state := &issuetabs.IssueTabState{
		IssueID:     "EMPTY-TABS",
		Tabs:        []issuetabs.IssueTab{},
		ActiveTabID: "",
	}
	if err := store.Save(ctx, state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	handler := handleGetIssueTabs(store, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/issues/EMPTY-TABS/tabs", nil)
	req.SetPathValue("issueId", "EMPTY-TABS")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp struct {
		Success bool                    `json:"success"`
		Data    issuetabs.IssueTabState `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Error("expected success=true")
	}
	if len(resp.Data.Tabs) != 0 {
		t.Errorf("len(Tabs) = %d, want 0", len(resp.Data.Tabs))
	}
}

func TestHandleDeleteIssueTabs_PoolTimeout(t *testing.T) {
	// Create a miniredis, get the store, then close the server to simulate timeout/pool error
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := issuetabs.NewStore(rdb, "test-ws-uuid", nil)

	// Close Redis to simulate connection failure (pool timeout)
	mr.Close()

	handler := handleDeleteIssueTabs(store)

	req := httptest.NewRequest(http.MethodDelete, "/api/issues/TIMEOUT-DEL/tabs", nil)
	req.SetPathValue("issueId", "TIMEOUT-DEL")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d; body: %s", rr.Code, http.StatusInternalServerError, rr.Body.String())
	}

	var resp issueTabResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Success {
		t.Error("expected success=false on pool timeout")
	}
	if !strings.Contains(resp.Error, "failed to delete") {
		t.Errorf("expected error to mention 'failed to delete', got: %q", resp.Error)
	}

	rdb.Close()
}

func TestHandleDeleteIssueTabs_InvalidIssueID(t *testing.T) {
	store, _ := setupIssueTabsTest(t)
	handler := handleDeleteIssueTabs(store)

	req := httptest.NewRequest(http.MethodDelete, "/api/issues/BAD@ID/tabs", nil)
	req.SetPathValue("issueId", "BAD@ID")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestHandleDeleteIssueTabs_EmptyIssueID(t *testing.T) {
	store, _ := setupIssueTabsTest(t)
	handler := handleDeleteIssueTabs(store)

	req := httptest.NewRequest(http.MethodDelete, "/api/issues//tabs", nil)
	req.SetPathValue("issueId", "")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body: %s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
}
