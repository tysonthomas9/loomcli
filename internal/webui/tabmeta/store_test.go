package tabmeta

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func setupTest(t *testing.T) (*Store, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	return NewStore(rdb, nil), mr
}

func TestGet_NotFound(t *testing.T) {
	store, _ := setupTest(t)
	meta, err := store.Get(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta != nil {
		t.Fatalf("expected nil, got %+v", meta)
	}
}

func TestGet_InvalidName(t *testing.T) {
	store, _ := setupTest(t)
	_, err := store.Get(context.Background(), "invalid name!")
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
		Label:       "My Session",
		Notes:       "Some notes",
		SortOrder:   5,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := store.Set(ctx, meta); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := store.Get(ctx, "test-session")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("expected metadata, got nil")
	}
	if got.SessionName != "test-session" {
		t.Errorf("SessionName = %q, want %q", got.SessionName, "test-session")
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
	tabs, err := store.List(context.Background())
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
			Label:       name,
			SortOrder:   3 - i, // c=3, a=2, b=1
			CreatedAt:   now,
			UpdatedAt:   now,
		}); err != nil {
			t.Fatalf("Set %s: %v", name, err)
		}
	}

	tabs, err := store.List(ctx)
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
		Label:       "original",
		Notes:       "original notes",
		SortOrder:   1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Patch only the label
	got, err := store.Patch(ctx, "test", map[string]string{"label": "updated"})
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
	_, err := store.Patch(context.Background(), "nonexistent", map[string]string{"label": "x"})
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
		Label:       "delete me",
		SortOrder:   1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if err := store.Delete(ctx, "to-delete"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err := store.Get(ctx, "to-delete")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil after delete, got %+v", got)
	}
}

func TestDelete_InvalidName(t *testing.T) {
	store, _ := setupTest(t)
	err := store.Delete(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty session name")
	}
}

func TestEnsureDefaults_CreatesNewSessions(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	tabs, err := store.EnsureDefaults(ctx, []string{"session-a", "session-b"})
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
		Label:       "Custom Label",
		SortOrder:   5,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	tabs, err := store.EnsureDefaults(ctx, []string{"existing", "new-session"})
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
		Label:       "Dead",
		SortOrder:   1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// EnsureDefaults with no active sessions should still return stored metadata
	tabs, err := store.EnsureDefaults(ctx, nil)
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
