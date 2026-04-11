package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

// ── handleListSessionsByIssue tests ─────────────────────────────────────────────

func TestHandleListSessionsByIssue_NilStore(t *testing.T) {
	handler := handleListSessionsByIssue(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/terminal/sessions/by-issue", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["success"] != false {
		t.Error("expected success=false")
	}
	if resp["error"] != "tab metadata not available (no Redis)" {
		t.Errorf("unexpected error message: %v", resp["error"])
	}
}

func TestHandleListSessionsByIssue_EmptyStore(t *testing.T) {
	store, _ := setupTabMetaTest(t)
	handler := handleListSessionsByIssue(store)

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/sessions/by-issue", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["success"] != true {
		t.Error("expected success=true")
	}
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data to be a map, got %T", resp["data"])
	}
	if len(data) != 0 {
		t.Errorf("expected empty map, got %v", data)
	}
}

func TestHandleListSessionsByIssue_ReturnsGroupedSessions(t *testing.T) {
	store, _ := setupTabMetaTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	sessions := []tabmeta.TabMetadata{
		{SessionName: "s1", Workspace: "default", Label: "s1", SortOrder: 1, IssueID: "PROJ-1", CreatedAt: now, UpdatedAt: now},
		{SessionName: "s2", Workspace: "default", Label: "s2", SortOrder: 2, IssueID: "PROJ-1", CreatedAt: now, UpdatedAt: now},
		{SessionName: "s3", Workspace: "default", Label: "s3", SortOrder: 3, IssueID: "PROJ-2", CreatedAt: now, UpdatedAt: now},
		{SessionName: "s4", Workspace: "default", Label: "s4", SortOrder: 4, IssueID: "", CreatedAt: now, UpdatedAt: now},
	}
	for _, s := range sessions {
		s := s
		if err := store.Set(ctx, &s); err != nil {
			t.Fatalf("Set %s: %v", s.SessionName, err)
		}
	}

	handler := handleListSessionsByIssue(store)
	req := httptest.NewRequest(http.MethodGet, "/api/terminal/sessions/by-issue", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["success"] != true {
		t.Errorf("expected success=true, got %v", resp["success"])
	}

	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data to be a map, got %T", resp["data"])
	}

	// PROJ-1 should have 2 sessions
	proj1, ok := data["PROJ-1"].([]interface{})
	if !ok {
		t.Fatalf("PROJ-1 not found or wrong type: %v", data["PROJ-1"])
	}
	if len(proj1) != 2 {
		t.Errorf("PROJ-1: expected 2 sessions, got %d", len(proj1))
	}

	// PROJ-2 should have 1 session
	proj2, ok := data["PROJ-2"].([]interface{})
	if !ok {
		t.Fatalf("PROJ-2 not found or wrong type: %v", data["PROJ-2"])
	}
	if len(proj2) != 1 {
		t.Errorf("PROJ-2: expected 1 session, got %d", len(proj2))
	}

	// Empty issue ID should not appear
	if _, exists := data[""]; exists {
		t.Error("empty issue ID key should not appear in response")
	}
}

// ── handleCloseAllSessions tests ────────────────────────────────────────────────

func TestHandleCloseAllSessions_NilManager(t *testing.T) {
	handler := handleCloseAllSessions(nil, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/sessions/close-all", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["success"] != false {
		t.Error("expected success=false")
	}
	if resp["error"] != "terminal manager not initialized" {
		t.Errorf("unexpected error message: %v", resp["error"])
	}
}

func TestHandleCloseAllSessions_DeletesTabMetadata(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", testRunPrefix+"-testcloseall", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager: %v", err)
	}
	t.Cleanup(func() { mgr.Shutdown() })

	store, hub := setupTabMetaTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	// Pre-create some tab metadata
	for _, name := range []string{"session-a", "session-b"} {
		if err := store.Set(ctx, &tabmeta.TabMetadata{
			SessionName: name,
			Workspace:   "default",
			Label:       name,
			SortOrder:   1,
			IssueID:     "PROJ-1",
			CreatedAt:   now,
			UpdatedAt:   now,
		}); err != nil {
			t.Fatalf("Set %s: %v", name, err)
		}
	}

	// Verify metadata exists before close-all
	before, err := store.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll before: %v", err)
	}
	if len(before) != 2 {
		t.Fatalf("expected 2 tabs before close-all, got %d", len(before))
	}

	handler := handleCloseAllSessions(mgr, store, hub)
	// Inject workspace context as WorkspaceMiddleware would on the real route.
	req := withWorkspaceCtx(
		httptest.NewRequest(http.MethodPost, "/api/workspaces/default/terminal/sessions/close-all", nil),
		"default",
	)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["success"] != true {
		t.Errorf("expected success=true, got %v", resp["success"])
	}

	// Verify the workspace's metadata was deleted.
	after, err := store.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll after: %v", err)
	}
	if len(after) != 0 {
		t.Errorf("expected 0 tabs after close-all, got %d", len(after))
	}
}

func TestHandleCloseAllSessions_WorksWithNilStore(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", testRunPrefix+"-testclosenil", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager: %v", err)
	}
	t.Cleanup(func() { mgr.Shutdown() })

	// Pass nil store and nil hub — should still succeed because the manager
	// itself is workspace-aware and KillWorkspaceSessions doesn't touch Redis.
	handler := handleCloseAllSessions(mgr, nil, nil)
	req := withWorkspaceCtx(
		httptest.NewRequest(http.MethodPost, "/api/workspaces/default/terminal/sessions/close-all", nil),
		"default",
	)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["success"] != true {
		t.Errorf("expected success=true")
	}
}
