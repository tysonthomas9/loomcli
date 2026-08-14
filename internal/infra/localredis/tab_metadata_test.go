package localredis

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"
)

const tabMetadataTestWorkspace = "default"

type TabMetadata = interaction.TabMetadata
type LaunchSpec = interaction.LaunchSpec

func setupTabMetadataStoreTest(t *testing.T) (*TabMetadataStore, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	return NewTabMetadataStore(rdb, nil), mr
}

func TestTabMetadataGetNotFound(t *testing.T) {
	store, _ := setupTabMetadataStoreTest(t)
	meta, err := store.Get(context.Background(), tabMetadataTestWorkspace, "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta != nil {
		t.Fatalf("expected nil, got %+v", meta)
	}
}

func TestTabMetadataGetInvalidName(t *testing.T) {
	store, _ := setupTabMetadataStoreTest(t)
	_, err := store.Get(context.Background(), tabMetadataTestWorkspace, "invalid name!")
	if err == nil {
		t.Fatal("expected error for invalid session name")
	}
}

func TestTabMetadataSetAndGet(t *testing.T) {
	store, _ := setupTabMetadataStoreTest(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	meta := &TabMetadata{
		SessionName:                  "test-session",
		Workspace:                    tabMetadataTestWorkspace,
		Label:                        "My Session",
		Notes:                        "Some notes",
		SortOrder:                    5,
		InteractionSessionID:         "interaction-session-1",
		InteractionTerminalID:        "interaction-terminal-1",
		InteractionLeaseID:           "interaction-lease-1",
		InteractionLeaseFencingToken: 17,
		CreatedAt:                    now,
		UpdatedAt:                    now,
	}

	if err := store.Set(ctx, meta); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := store.Get(ctx, tabMetadataTestWorkspace, "test-session")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("expected metadata, got nil")
	}
	if got.SessionName != "test-session" {
		t.Errorf("SessionName = %q, want %q", got.SessionName, "test-session")
	}
	if got.Workspace != tabMetadataTestWorkspace {
		t.Errorf("Workspace = %q, want %q", got.Workspace, tabMetadataTestWorkspace)
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
	if got.InteractionSessionID != "interaction-session-1" ||
		got.InteractionTerminalID != "interaction-terminal-1" ||
		got.InteractionLeaseID != "interaction-lease-1" ||
		got.InteractionLeaseFencingToken != 17 {
		t.Errorf("Interaction lifecycle identity = %+v", got)
	}
}

func TestTabMetadataListEmpty(t *testing.T) {
	store, _ := setupTabMetadataStoreTest(t)
	tabs, err := store.List(context.Background(), tabMetadataTestWorkspace)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tabs) != 0 {
		t.Fatalf("expected empty list, got %d items", len(tabs))
	}
}

func TestTabMetadataListMultiple(t *testing.T) {
	store, _ := setupTabMetadataStoreTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	for i, name := range []string{"session-c", "session-a", "session-b"} {
		if err := store.Set(ctx, &TabMetadata{
			SessionName: name,
			Workspace:   tabMetadataTestWorkspace,
			Label:       name,
			SortOrder:   3 - i, // c=3, a=2, b=1
			CreatedAt:   now,
			UpdatedAt:   now,
		}); err != nil {
			t.Fatalf("Set %s: %v", name, err)
		}
	}

	tabs, err := store.List(ctx, tabMetadataTestWorkspace)
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

func TestTabMetadataPatchPartialUpdate(t *testing.T) {
	store, _ := setupTabMetadataStoreTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	if err := store.Set(ctx, &TabMetadata{
		SessionName: "test",
		Workspace:   tabMetadataTestWorkspace,
		Label:       "original",
		Notes:       "original notes",
		SortOrder:   1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Patch only the label
	got, err := store.Patch(ctx, tabMetadataTestWorkspace, "test", map[string]string{"label": "updated"})
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

func TestTabMetadataPatchNotFound(t *testing.T) {
	store, _ := setupTabMetadataStoreTest(t)
	_, err := store.Patch(context.Background(), tabMetadataTestWorkspace, "nonexistent", map[string]string{"label": "x"})
	if err == nil {
		t.Fatal("expected error for patching nonexistent session")
	}
}

func TestTabMetadataDelete(t *testing.T) {
	store, _ := setupTabMetadataStoreTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	if err := store.Set(ctx, &TabMetadata{
		SessionName: "to-delete",
		Workspace:   tabMetadataTestWorkspace,
		Label:       "delete me",
		SortOrder:   1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if err := store.Delete(ctx, tabMetadataTestWorkspace, "to-delete"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err := store.Get(ctx, tabMetadataTestWorkspace, "to-delete")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil after delete, got %+v", got)
	}
}

func TestTabMetadataDeleteInvalidName(t *testing.T) {
	store, _ := setupTabMetadataStoreTest(t)
	err := store.Delete(context.Background(), tabMetadataTestWorkspace, "")
	if err == nil {
		t.Fatal("expected error for empty session name")
	}
}

func TestTabMetadataEnsureDefaultsCreatesNewSessions(t *testing.T) {
	store, _ := setupTabMetadataStoreTest(t)
	ctx := context.Background()

	tabs, err := store.EnsureDefaults(ctx, tabMetadataTestWorkspace, []string{"session-a", "session-b"})
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

func TestTabMetadataEnsureDefaultsPreservesExisting(t *testing.T) {
	store, _ := setupTabMetadataStoreTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	// Pre-create one session with custom label
	if err := store.Set(ctx, &TabMetadata{
		SessionName: "existing",
		Workspace:   tabMetadataTestWorkspace,
		Label:       "Custom Label",
		SortOrder:   5,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	tabs, err := store.EnsureDefaults(ctx, tabMetadataTestWorkspace, []string{"existing", "new-session"})
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

func TestTabMetadataEnsureDefaultsEmptyActiveSessions(t *testing.T) {
	store, _ := setupTabMetadataStoreTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	// Pre-create metadata for a dead session
	if err := store.Set(ctx, &TabMetadata{
		SessionName: "dead-session",
		Workspace:   tabMetadataTestWorkspace,
		Label:       "Dead",
		SortOrder:   1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// EnsureDefaults with no active sessions should still return stored metadata
	tabs, err := store.EnsureDefaults(ctx, tabMetadataTestWorkspace, nil)
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

func TestTabMetadataValidateSessionName(t *testing.T) {
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
		err := interaction.ValidateTerminalSessionName(tt.name)
		if tt.valid && err != nil {
			t.Errorf("ValidateSessionName(%q) = error %v, want nil", tt.name, err)
		}
		if !tt.valid && err == nil {
			t.Errorf("ValidateSessionName(%q) = nil, want error", tt.name)
		}
	}
}

func TestTabMetadataStoreWorkspaceScoping(t *testing.T) {
	store, _ := setupTabMetadataStoreTest(t)
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

func TestTabMetadataValidateWorkspaceName(t *testing.T) {
	tests := []struct {
		name    string
		valid   bool
		wantErr string
	}{
		{"default", true, ""},
		{"my-workspace", true, ""},
		{"workspace_1", true, ""},
		{strings.Repeat("a", workspacemodule.MaxNameLength), true, ""},
		{"", false, "workspace name is required"},
		{"invalid name", false, "must match [a-zA-Z0-9_-]+"},
		{"bad:name", false, "must match [a-zA-Z0-9_-]+"},
		{strings.Repeat("a", workspacemodule.MaxNameLength+1), false, "workspace name is too long (max 64 characters)"},
	}

	for _, tt := range tests {
		err := validateTabMetadataWorkspaceName(tt.name)
		if tt.valid && err != nil {
			t.Errorf("ValidateWorkspaceName(%q) = error %v, want nil", tt.name, err)
		}
		if !tt.valid && err == nil {
			t.Errorf("ValidateWorkspaceName(%q) = nil, want error", tt.name)
		}
		if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
			t.Errorf("ValidateWorkspaceName(%q) = %v, want containing %q", tt.name, err, tt.wantErr)
		}
	}
}
