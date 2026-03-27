package issuetabs

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

const testWorkspaceID = "test-ws-uuid"

func setupTest(t *testing.T) (*Store, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	return NewStore(rdb, nil), mr
}

func TestSaveAndGet_RoundTrip(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	state := &IssueTabState{
		IssueID: "PROJ-123",
		Tabs: []IssueTab{
			{ID: "details", Type: "details", Label: "Details", SortOrder: 0},
			{ID: "logs", Type: "logs", Label: "Logs", SortOrder: 1},
			{ID: "terminal-sess1", Type: "terminal", Label: "Terminal 1", SessionName: "sess1", SortOrder: 2},
		},
		ActiveTabID: "terminal-sess1",
	}

	if err := store.Save(ctx, testWorkspaceID, state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Get(ctx, testWorkspaceID, "PROJ-123")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("expected state, got nil")
	}
	if got.IssueID != "PROJ-123" {
		t.Errorf("IssueID = %q, want %q", got.IssueID, "PROJ-123")
	}
	if got.ActiveTabID != "terminal-sess1" {
		t.Errorf("ActiveTabID = %q, want %q", got.ActiveTabID, "terminal-sess1")
	}
	if len(got.Tabs) != 3 {
		t.Fatalf("len(Tabs) = %d, want 3", len(got.Tabs))
	}
	if got.Tabs[0].ID != "details" {
		t.Errorf("Tabs[0].ID = %q, want %q", got.Tabs[0].ID, "details")
	}
	if got.Tabs[0].Type != "details" {
		t.Errorf("Tabs[0].Type = %q, want %q", got.Tabs[0].Type, "details")
	}
	if got.Tabs[2].SessionName != "sess1" {
		t.Errorf("Tabs[2].SessionName = %q, want %q", got.Tabs[2].SessionName, "sess1")
	}
	if got.Tabs[2].SortOrder != 2 {
		t.Errorf("Tabs[2].SortOrder = %d, want 2", got.Tabs[2].SortOrder)
	}
	if got.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should be set after Save")
	}
}

func TestGet_NotFound(t *testing.T) {
	store, _ := setupTest(t)
	state, err := store.Get(context.Background(), testWorkspaceID, "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != nil {
		t.Fatalf("expected nil, got %+v", state)
	}
}

func TestGet_EmptyIssueID(t *testing.T) {
	store, _ := setupTest(t)
	_, err := store.Get(context.Background(), testWorkspaceID, "")
	if err == nil {
		t.Fatal("expected error for empty issue ID")
	}
}

func TestSave_EmptyIssueID(t *testing.T) {
	store, _ := setupTest(t)
	err := store.Save(context.Background(), testWorkspaceID, &IssueTabState{
		IssueID: "",
		Tabs:    []IssueTab{},
	})
	if err == nil {
		t.Fatal("expected error for empty issue ID")
	}
}

func TestSave_SetsTTL(t *testing.T) {
	store, mr := setupTest(t)
	ctx := context.Background()

	state := &IssueTabState{
		IssueID:     "TTL-TEST",
		Tabs:        []IssueTab{{ID: "details", Type: "details", Label: "Details", SortOrder: 0}},
		ActiveTabID: "details",
	}

	if err := store.Save(ctx, testWorkspaceID, state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	redisTTL := mr.TTL(issueKey(testWorkspaceID, "TTL-TEST"))
	// TTL should be approximately 24 hours (allow some margin)
	expected := 24 * time.Hour
	if redisTTL < expected-time.Minute || redisTTL > expected+time.Minute {
		t.Errorf("TTL = %v, want ~%v", redisTTL, expected)
	}
}

func TestDelete(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	state := &IssueTabState{
		IssueID:     "DEL-TEST",
		Tabs:        []IssueTab{{ID: "details", Type: "details", Label: "Details", SortOrder: 0}},
		ActiveTabID: "details",
	}

	if err := store.Save(ctx, testWorkspaceID, state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := store.Delete(ctx, testWorkspaceID, "DEL-TEST"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err := store.Get(ctx, testWorkspaceID, "DEL-TEST")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil after delete, got %+v", got)
	}
}

func TestDelete_EmptyIssueID(t *testing.T) {
	store, _ := setupTest(t)
	err := store.Delete(context.Background(), testWorkspaceID, "")
	if err == nil {
		t.Fatal("expected error for empty issue ID")
	}
}

func TestValidateAndFilter_NilState(t *testing.T) {
	result := ValidateAndFilter(nil, []string{"sess1"})
	if result != nil {
		t.Fatalf("expected nil, got %+v", result)
	}
}

func TestValidateAndFilter_RemovesDeadSessions(t *testing.T) {
	state := &IssueTabState{
		IssueID: "FILTER-1",
		Tabs: []IssueTab{
			{ID: "details", Type: "details", Label: "Details", SortOrder: 0},
			{ID: "terminal-alive", Type: "terminal", Label: "Alive", SessionName: "alive", SortOrder: 1},
			{ID: "terminal-dead", Type: "terminal", Label: "Dead", SessionName: "dead", SortOrder: 2},
		},
		ActiveTabID: "details",
	}

	result := ValidateAndFilter(state, []string{"alive"})

	if len(result.Tabs) != 2 {
		t.Fatalf("len(Tabs) = %d, want 2", len(result.Tabs))
	}
	for _, tab := range result.Tabs {
		if tab.SessionName == "dead" {
			t.Error("dead session should have been filtered out")
		}
	}
}

func TestValidateAndFilter_PreservesNonTerminalTabs(t *testing.T) {
	state := &IssueTabState{
		IssueID: "FILTER-2",
		Tabs: []IssueTab{
			{ID: "details", Type: "details", Label: "Details", SortOrder: 0},
			{ID: "logs", Type: "logs", Label: "Logs", SortOrder: 1},
			{ID: "terminal-dead", Type: "terminal", Label: "Dead", SessionName: "dead", SortOrder: 2},
		},
		ActiveTabID: "details",
	}

	// No active sessions at all
	result := ValidateAndFilter(state, nil)

	if len(result.Tabs) != 2 {
		t.Fatalf("len(Tabs) = %d, want 2", len(result.Tabs))
	}
	if result.Tabs[0].ID != "details" {
		t.Errorf("Tabs[0].ID = %q, want %q", result.Tabs[0].ID, "details")
	}
	if result.Tabs[1].ID != "logs" {
		t.Errorf("Tabs[1].ID = %q, want %q", result.Tabs[1].ID, "logs")
	}
}

func TestValidateAndFilter_AllTerminalsDead_FallsBackToPermanentTabs(t *testing.T) {
	state := &IssueTabState{
		IssueID: "FILTER-3",
		Tabs: []IssueTab{
			{ID: "details", Type: "details", Label: "Details", SortOrder: 0},
			{ID: "terminal-a", Type: "terminal", Label: "A", SessionName: "a", SortOrder: 1},
			{ID: "terminal-b", Type: "terminal", Label: "B", SessionName: "b", SortOrder: 2},
		},
		ActiveTabID: "terminal-a",
	}

	result := ValidateAndFilter(state, nil)

	if len(result.Tabs) != 1 {
		t.Fatalf("len(Tabs) = %d, want 1 (only details)", len(result.Tabs))
	}
	if result.Tabs[0].ID != "details" {
		t.Errorf("Tabs[0].ID = %q, want %q", result.Tabs[0].ID, "details")
	}
}

func TestValidateAndFilter_FallsBackActiveTabToDetails(t *testing.T) {
	state := &IssueTabState{
		IssueID: "FILTER-4",
		Tabs: []IssueTab{
			{ID: "details", Type: "details", Label: "Details", SortOrder: 0},
			{ID: "terminal-dead", Type: "terminal", Label: "Dead", SessionName: "dead", SortOrder: 1},
		},
		ActiveTabID: "terminal-dead",
	}

	result := ValidateAndFilter(state, nil)

	if result.ActiveTabID != "details" {
		t.Errorf("ActiveTabID = %q, want %q (should fall back when active removed)", result.ActiveTabID, "details")
	}
}

func TestValidateAndFilter_KeepsActiveTabWhenStillAlive(t *testing.T) {
	state := &IssueTabState{
		IssueID: "FILTER-5",
		Tabs: []IssueTab{
			{ID: "details", Type: "details", Label: "Details", SortOrder: 0},
			{ID: "terminal-alive", Type: "terminal", Label: "Alive", SessionName: "alive", SortOrder: 1},
		},
		ActiveTabID: "terminal-alive",
	}

	result := ValidateAndFilter(state, []string{"alive"})

	if result.ActiveTabID != "terminal-alive" {
		t.Errorf("ActiveTabID = %q, want %q (active tab still alive)", result.ActiveTabID, "terminal-alive")
	}
}

func TestValidateAndFilter_RemovesTerminalWithEmptySessionName(t *testing.T) {
	state := &IssueTabState{
		IssueID: "FILTER-6",
		Tabs: []IssueTab{
			{ID: "details", Type: "details", Label: "Details", SortOrder: 0},
			{ID: "terminal-empty", Type: "terminal", Label: "Bad Tab", SessionName: "", SortOrder: 1},
		},
		ActiveTabID: "details",
	}

	result := ValidateAndFilter(state, []string{"anything"})

	if len(result.Tabs) != 1 {
		t.Fatalf("len(Tabs) = %d, want 1 (terminal with empty session_name removed)", len(result.Tabs))
	}
}

func TestIsolation_DifferentWorkspaces(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })

	store := NewStore(rdb, nil)
	ctx := context.Background()

	stateA := &IssueTabState{
		IssueID:     "PROJ-1",
		Tabs:        []IssueTab{{ID: "details", Type: "details", Label: "Details A", SortOrder: 0}},
		ActiveTabID: "details",
	}
	stateB := &IssueTabState{
		IssueID:     "PROJ-1",
		Tabs:        []IssueTab{{ID: "logs", Type: "logs", Label: "Logs B", SortOrder: 0}},
		ActiveTabID: "logs",
	}

	if err := store.Save(ctx, "ws-A", stateA); err != nil {
		t.Fatalf("Save ws-A: %v", err)
	}
	if err := store.Save(ctx, "ws-B", stateB); err != nil {
		t.Fatalf("Save ws-B: %v", err)
	}

	gotA, err := store.Get(ctx, "ws-A", "PROJ-1")
	if err != nil {
		t.Fatalf("Get ws-A: %v", err)
	}
	gotB, err := store.Get(ctx, "ws-B", "PROJ-1")
	if err != nil {
		t.Fatalf("Get ws-B: %v", err)
	}

	if gotA == nil {
		t.Fatal("expected state from ws-A, got nil")
	}
	if gotB == nil {
		t.Fatal("expected state from ws-B, got nil")
	}

	if len(gotA.Tabs) != 1 || gotA.Tabs[0].Label != "Details A" {
		t.Errorf("ws-A: Tabs[0].Label = %q, want %q", gotA.Tabs[0].Label, "Details A")
	}
	if gotA.ActiveTabID != "details" {
		t.Errorf("ws-A: ActiveTabID = %q, want %q", gotA.ActiveTabID, "details")
	}

	if len(gotB.Tabs) != 1 || gotB.Tabs[0].Label != "Logs B" {
		t.Errorf("ws-B: Tabs[0].Label = %q, want %q", gotB.Tabs[0].Label, "Logs B")
	}
	if gotB.ActiveTabID != "logs" {
		t.Errorf("ws-B: ActiveTabID = %q, want %q", gotB.ActiveTabID, "logs")
	}
}

func TestMigrateLegacyKeys(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })

	// Insert 3 keys in old "issue:tabs:{id}" format directly into miniredis.
	mr.Set("issue:tabs:PROJ-1", `{"issue_id":"PROJ-1","tabs":[{"id":"details","type":"details","label":"Details","sort_order":0}],"active_tab_id":"details","updated_at":"2025-01-15T10:00:00Z"}`)
	mr.Set("issue:tabs:PROJ-2", `{"issue_id":"PROJ-2","tabs":[{"id":"logs","type":"logs","label":"Logs","sort_order":0}],"active_tab_id":"logs","updated_at":"2025-01-16T10:00:00Z"}`)
	mr.Set("issue:tabs:PROJ-3", `{"issue_id":"PROJ-3","tabs":[{"id":"details","type":"details","label":"Details","sort_order":0},{"id":"terminal-s1","type":"terminal","label":"Term","session_name":"s1","sort_order":1}],"active_tab_id":"terminal-s1","updated_at":"2025-01-17T10:00:00Z"}`)

	store := NewStore(rdb, nil)
	ctx := context.Background()

	count, err := store.MigrateLegacyKeys(ctx, "my-ws")
	if err != nil {
		t.Fatalf("MigrateLegacyKeys: %v", err)
	}
	if count != 3 {
		t.Errorf("migrated count = %d, want 3", count)
	}

	// Verify all 3 now exist under the new namespaced key format via store.Get.
	for _, id := range []string{"PROJ-1", "PROJ-2", "PROJ-3"} {
		got, err := store.Get(ctx, "my-ws", id)
		if err != nil {
			t.Fatalf("Get(%s): %v", id, err)
		}
		if got == nil {
			t.Errorf("Get(%s): expected state, got nil", id)
		}
	}

	// Verify old keys are gone.
	for _, oldKey := range []string{"issue:tabs:PROJ-1", "issue:tabs:PROJ-2", "issue:tabs:PROJ-3"} {
		if mr.Exists(oldKey) {
			t.Errorf("old key %q should no longer exist", oldKey)
		}
	}
}

func TestMigrateLegacyKeys_Idempotent(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })

	mr.Set("issue:tabs:PROJ-1", `{"issue_id":"PROJ-1","tabs":[{"id":"details","type":"details","label":"Details","sort_order":0}],"active_tab_id":"details","updated_at":"2025-01-15T10:00:00Z"}`)

	store := NewStore(rdb, nil)
	ctx := context.Background()

	count1, err := store.MigrateLegacyKeys(ctx, "my-ws")
	if err != nil {
		t.Fatalf("MigrateLegacyKeys (first run): %v", err)
	}
	if count1 != 1 {
		t.Errorf("first run: migrated count = %d, want 1", count1)
	}

	count2, err := store.MigrateLegacyKeys(ctx, "my-ws")
	if err != nil {
		t.Fatalf("MigrateLegacyKeys (second run): %v", err)
	}
	if count2 != 0 {
		t.Errorf("second run: migrated count = %d, want 0", count2)
	}
}

func TestMigrateLegacyKeys_SkipsNamespacedKeys(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })

	// Insert keys already in ws:... format directly into miniredis.
	mr.Set("ws:other-ws:issue:tabs:PROJ-1", `{"issue_id":"PROJ-1","tabs":[],"active_tab_id":"details","updated_at":"2025-01-15T10:00:00Z"}`)
	mr.Set("ws:other-ws:issue:tabs:PROJ-2", `{"issue_id":"PROJ-2","tabs":[],"active_tab_id":"details","updated_at":"2025-01-16T10:00:00Z"}`)

	store := NewStore(rdb, nil)
	ctx := context.Background()

	count, err := store.MigrateLegacyKeys(ctx, "my-ws")
	if err != nil {
		t.Fatalf("MigrateLegacyKeys: %v", err)
	}
	if count != 0 {
		t.Errorf("migrated count = %d, want 0 (namespaced keys should be skipped)", count)
	}

	// Verify original keys are untouched.
	if !mr.Exists("ws:other-ws:issue:tabs:PROJ-1") {
		t.Error("ws:other-ws:issue:tabs:PROJ-1 should still exist")
	}
	if !mr.Exists("ws:other-ws:issue:tabs:PROJ-2") {
		t.Error("ws:other-ws:issue:tabs:PROJ-2 should still exist")
	}
}

func TestMigrateLegacyKeys_EmptyDB(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })

	store := NewStore(rdb, nil)
	ctx := context.Background()

	count, err := store.MigrateLegacyKeys(ctx, "my-ws")
	if err != nil {
		t.Fatalf("MigrateLegacyKeys: %v", err)
	}
	if count != 0 {
		t.Errorf("migrated count = %d, want 0", count)
	}
}
