package tabmeta

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

const testWorkspace = "default"

func setupTest(t *testing.T) (*Store, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	return NewStore(rdb, nil), mr
}

func TestGet_NotFound(t *testing.T) {
	store, _ := setupTest(t)
	meta, err := store.Get(context.Background(), testWorkspace, "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta != nil {
		t.Fatalf("expected nil, got %+v", meta)
	}
}

func TestGet_InvalidName(t *testing.T) {
	store, _ := setupTest(t)
	_, err := store.Get(context.Background(), testWorkspace, "invalid name!")
	if err == nil {
		t.Fatal("expected error for invalid session name")
	}
}

func TestSetAndGet(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	meta := &TabMetadata{
		SessionName: "test-session",
		Workspace:   testWorkspace,
		Label:       "My Session",
		Notes:       "Some notes",
		SortOrder:   5,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := store.Set(ctx, meta); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := store.Get(ctx, testWorkspace, "test-session")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("expected metadata, got nil")
	}
	if got.SessionName != "test-session" {
		t.Errorf("SessionName = %q, want %q", got.SessionName, "test-session")
	}
	if got.Workspace != testWorkspace {
		t.Errorf("Workspace = %q, want %q", got.Workspace, testWorkspace)
	}
	if got.Label != "My Session" {
		t.Errorf("Label = %q, want %q", got.Label, "My Session")
	}
	if got.Notes != "Some notes" {
		t.Errorf("Notes = %q, want %q", got.Notes, "Some notes")
	}
	if got.SortOrder != 5 {
		t.Errorf("SortOrder = %d, want %d", got.SortOrder, 5)
	}
}

func TestList_Empty(t *testing.T) {
	store, _ := setupTest(t)
	tabs, err := store.List(context.Background(), testWorkspace)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tabs) != 0 {
		t.Fatalf("expected empty list, got %d items", len(tabs))
	}
}

func TestList_Multiple(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	for i, name := range []string{"session-c", "session-a", "session-b"} {
		if err := store.Set(ctx, &TabMetadata{
			SessionName: name,
			Workspace:   testWorkspace,
			Label:       name,
			SortOrder:   3 - i, // c=3, a=2, b=1
			CreatedAt:   now,
			UpdatedAt:   now,
		}); err != nil {
			t.Fatalf("Set %s: %v", name, err)
		}
	}

	tabs, err := store.List(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tabs) != 3 {
		t.Fatalf("expected 3 tabs, got %d", len(tabs))
	}
	// Should be sorted by sort_order ascending: b(1), a(2), c(3)
	if tabs[0].SessionName != "session-b" {
		t.Errorf("tabs[0] = %q, want session-b", tabs[0].SessionName)
	}
	if tabs[1].SessionName != "session-a" {
		t.Errorf("tabs[1] = %q, want session-a", tabs[1].SessionName)
	}
	if tabs[2].SessionName != "session-c" {
		t.Errorf("tabs[2] = %q, want session-c", tabs[2].SessionName)
	}
}

func TestPatch_PartialUpdate(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	if err := store.Set(ctx, &TabMetadata{
		SessionName: "test",
		Workspace:   testWorkspace,
		Label:       "original",
		Notes:       "original notes",
		SortOrder:   1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Patch only the label
	got, err := store.Patch(ctx, testWorkspace, "test", map[string]string{"label": "updated"})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if got.Label != "updated" {
		t.Errorf("Label = %q, want %q", got.Label, "updated")
	}
	if got.Notes != "original notes" {
		t.Errorf("Notes = %q, want %q (should be unchanged)", got.Notes, "original notes")
	}
}

func TestPatch_NotFound(t *testing.T) {
	store, _ := setupTest(t)
	_, err := store.Patch(context.Background(), testWorkspace, "nonexistent", map[string]string{"label": "x"})
	if err == nil {
		t.Fatal("expected error for patching nonexistent session")
	}
}

func TestDelete(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	if err := store.Set(ctx, &TabMetadata{
		SessionName: "to-delete",
		Workspace:   testWorkspace,
		Label:       "delete me",
		SortOrder:   1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if err := store.Delete(ctx, testWorkspace, "to-delete"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err := store.Get(ctx, testWorkspace, "to-delete")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil after delete, got %+v", got)
	}
}

func TestDelete_InvalidName(t *testing.T) {
	store, _ := setupTest(t)
	err := store.Delete(context.Background(), testWorkspace, "")
	if err == nil {
		t.Fatal("expected error for empty session name")
	}
}

func TestEnsureDefaults_CreatesNewSessions(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	tabs, err := store.EnsureDefaults(ctx, testWorkspace, []string{"session-a", "session-b"})
	if err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}
	if len(tabs) != 2 {
		t.Fatalf("expected 2 tabs, got %d", len(tabs))
	}
	// Defaults should use session name as label
	if tabs[0].Label != "session-a" {
		t.Errorf("tabs[0].Label = %q, want session-a", tabs[0].Label)
	}
	if tabs[1].Label != "session-b" {
		t.Errorf("tabs[1].Label = %q, want session-b", tabs[1].Label)
	}
	// Sort orders should be sequential
	if tabs[0].SortOrder != 1 {
		t.Errorf("tabs[0].SortOrder = %d, want 1", tabs[0].SortOrder)
	}
	if tabs[1].SortOrder != 2 {
		t.Errorf("tabs[1].SortOrder = %d, want 2", tabs[1].SortOrder)
	}
}

func TestEnsureDefaults_PreservesExisting(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	// Pre-create one session with custom label
	if err := store.Set(ctx, &TabMetadata{
		SessionName: "existing",
		Workspace:   testWorkspace,
		Label:       "Custom Label",
		SortOrder:   5,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	tabs, err := store.EnsureDefaults(ctx, testWorkspace, []string{"existing", "new-session"})
	if err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}
	if len(tabs) != 2 {
		t.Fatalf("expected 2 tabs, got %d", len(tabs))
	}

	// Existing session should retain custom label
	var existingTab *TabMetadata
	for i := range tabs {
		if tabs[i].SessionName == "existing" {
			existingTab = &tabs[i]
		}
	}
	if existingTab == nil {
		t.Fatal("existing session not found in results")
	}
	if existingTab.Label != "Custom Label" {
		t.Errorf("existing label = %q, want %q", existingTab.Label, "Custom Label")
	}

	// New session should have sort_order > existing max (5)
	var newTab *TabMetadata
	for i := range tabs {
		if tabs[i].SessionName == "new-session" {
			newTab = &tabs[i]
		}
	}
	if newTab == nil {
		t.Fatal("new-session not found in results")
	}
	if newTab.SortOrder <= 5 {
		t.Errorf("new session sort_order = %d, should be > 5", newTab.SortOrder)
	}
}

func TestEnsureDefaults_EmptyActiveSessions(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	// Pre-create metadata for a dead session
	if err := store.Set(ctx, &TabMetadata{
		SessionName: "dead-session",
		Workspace:   testWorkspace,
		Label:       "Dead",
		SortOrder:   1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// EnsureDefaults with no active sessions should still return stored metadata
	tabs, err := store.EnsureDefaults(ctx, testWorkspace, nil)
	if err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}
	if len(tabs) != 1 {
		t.Fatalf("expected 1 tab (dead session metadata), got %d", len(tabs))
	}
	if tabs[0].SessionName != "dead-session" {
		t.Errorf("expected dead-session, got %s", tabs[0].SessionName)
	}
}

func TestValidateSessionName(t *testing.T) {
	tests := []struct {
		name  string
		valid bool
	}{
		{"valid-name", true},
		{"valid_name", true},
		{"ValidName123", true},
		{"a", true},
		{"", false},
		{"invalid name", false},
		{"invalid/name", false},
		{"invalid.name", false},
		{"invalid@name", false},
	}

	for _, tt := range tests {
		err := ValidateSessionName(tt.name)
		if tt.valid && err != nil {
			t.Errorf("ValidateSessionName(%q) = error %v, want nil", tt.name, err)
		}
		if !tt.valid && err == nil {
			t.Errorf("ValidateSessionName(%q) = nil, want error", tt.name)
		}
	}
}

func TestStoreWorkspaceScoping(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	// Set metadata in workspace "ws-a"
	if err := store.Set(ctx, &TabMetadata{
		SessionName: "session-1",
		Workspace:   "ws-a",
		Label:       "A session",
		SortOrder:   1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("Set ws-a: %v", err)
	}

	// Set metadata in workspace "ws-b"
	if err := store.Set(ctx, &TabMetadata{
		SessionName: "session-1",
		Workspace:   "ws-b",
		Label:       "B session",
		SortOrder:   1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("Set ws-b: %v", err)
	}

	// Get from ws-a should return "A session"
	got, err := store.Get(ctx, "ws-a", "session-1")
	if err != nil {
		t.Fatalf("Get ws-a: %v", err)
	}
	if got.Label != "A session" {
		t.Errorf("ws-a Label = %q, want %q", got.Label, "A session")
	}

	// Get from ws-b should return "B session"
	got, err = store.Get(ctx, "ws-b", "session-1")
	if err != nil {
		t.Fatalf("Get ws-b: %v", err)
	}
	if got.Label != "B session" {
		t.Errorf("ws-b Label = %q, want %q", got.Label, "B session")
	}

	// List ws-a should return only 1 entry
	listA, err := store.List(ctx, "ws-a")
	if err != nil {
		t.Fatalf("List ws-a: %v", err)
	}
	if len(listA) != 1 {
		t.Fatalf("ws-a list: expected 1, got %d", len(listA))
	}

	// ListAll should return 2 entries
	all, err := store.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListAll: expected 2, got %d", len(all))
	}

	// Delete from ws-a should not affect ws-b
	if err := store.Delete(ctx, "ws-a", "session-1"); err != nil {
		t.Fatalf("Delete ws-a: %v", err)
	}
	got, err = store.Get(ctx, "ws-b", "session-1")
	if err != nil {
		t.Fatalf("Get ws-b after ws-a delete: %v", err)
	}
	if got == nil {
		t.Fatal("ws-b session should still exist after ws-a delete")
	}
}

func TestMigrateLegacyKeys(t *testing.T) {
	store, mr := setupTest(t)
	ctx := context.Background()

	// Manually create legacy keys (old format: terminal:meta:{session})
	mr.HSet("terminal:meta:legacy-session", "label", "Legacy", "sort_order", "1",
		"created_at", "2024-01-01T00:00:00Z", "updated_at", "2024-01-01T00:00:00Z")

	// Also create a new-format key that should NOT be migrated
	mr.HSet("terminal:meta:default:existing", "label", "Existing", "sort_order", "2",
		"created_at", "2024-01-01T00:00:00Z", "updated_at", "2024-01-01T00:00:00Z")

	if err := store.MigrateLegacyKeys(ctx, "default"); err != nil {
		t.Fatalf("MigrateLegacyKeys: %v", err)
	}

	// Legacy key should now be accessible under "default" workspace
	got, err := store.Get(ctx, "default", "legacy-session")
	if err != nil {
		t.Fatalf("Get migrated: %v", err)
	}
	if got == nil {
		t.Fatal("expected migrated metadata, got nil")
	}
	if got.Label != "Legacy" {
		t.Errorf("Label = %q, want %q", got.Label, "Legacy")
	}

	// Existing key should still be accessible
	got2, err := store.Get(ctx, "default", "existing")
	if err != nil {
		t.Fatalf("Get existing: %v", err)
	}
	if got2 == nil {
		t.Fatal("expected existing metadata, got nil")
	}

	// Old legacy key should no longer exist
	if mr.Exists("terminal:meta:legacy-session") {
		t.Error("legacy key should have been renamed")
	}

	// Re-running migration should be idempotent
	if err := store.MigrateLegacyKeys(ctx, "default"); err != nil {
		t.Fatalf("MigrateLegacyKeys (idempotent): %v", err)
	}
}

func TestValidateWorkspaceName(t *testing.T) {
	tests := []struct {
		name  string
		valid bool
	}{
		{"default", true},
		{"my-workspace", true},
		{"workspace_1", true},
		{"", false},
		{"invalid name", false},
		{"bad:name", false},
	}

	for _, tt := range tests {
		err := ValidateWorkspaceName(tt.name)
		if tt.valid && err != nil {
			t.Errorf("ValidateWorkspaceName(%q) = error %v, want nil", tt.name, err)
		}
		if !tt.valid && err == nil {
			t.Errorf("ValidateWorkspaceName(%q) = nil, want error", tt.name)
		}
	}
}
