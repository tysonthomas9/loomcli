package localredis

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

const issueTabTestWorkspaceID = "test-ws-uuid"

type IssueTab = interaction.IssueTab
type IssueTabState = interaction.IssueTabState

func setupIssueTabStoreTest(t *testing.T) (*IssueTabStore, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	return NewIssueTabStore(rdb, nil), mr
}

func TestIssueTabSaveAndGetRoundTrip(t *testing.T) {
	store, _ := setupIssueTabStoreTest(t)
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

	if err := store.ReplaceIssueTabs(ctx, issueTabTestWorkspaceID, state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Get(ctx, issueTabTestWorkspaceID, "PROJ-123")
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

func TestIssueTabSaveAndGetBackendRoundTrip(t *testing.T) {
	store, _ := setupIssueTabStoreTest(t)
	ctx := context.Background()

	state := &IssueTabState{
		IssueID: "BACKEND-1",
		Tabs: []IssueTab{
			{ID: "details", Type: "details", Label: "Details", SortOrder: 0},
			{ID: "terminal-sess1", Type: "terminal", Label: "Terminal 1", SessionName: "lead-claude-1", Backend: "claude", SortOrder: 1},
		},
		ActiveTabID: "terminal-sess1",
	}

	if err := store.ReplaceIssueTabs(ctx, issueTabTestWorkspaceID, state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Get(ctx, issueTabTestWorkspaceID, "BACKEND-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("expected state, got nil")
	}
	if got.Tabs[1].Backend != "claude" {
		t.Errorf("Tabs[1].Backend = %q, want %q", got.Tabs[1].Backend, "claude")
	}
	if got.Tabs[1].SessionName != "lead-claude-1" {
		t.Errorf("Tabs[1].SessionName = %q, want %q", got.Tabs[1].SessionName, "lead-claude-1")
	}
	// Non-terminal tab should have empty backend
	if got.Tabs[0].Backend != "" {
		t.Errorf("Tabs[0].Backend = %q, want empty for non-terminal tab", got.Tabs[0].Backend)
	}
}

func TestIssueTabGetNotFound(t *testing.T) {
	store, _ := setupIssueTabStoreTest(t)
	state, err := store.Get(context.Background(), issueTabTestWorkspaceID, "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != nil {
		t.Fatalf("expected nil, got %+v", state)
	}
}

func TestIssueTabGetEmptyIssueID(t *testing.T) {
	store, _ := setupIssueTabStoreTest(t)
	_, err := store.Get(context.Background(), issueTabTestWorkspaceID, "")
	if err == nil {
		t.Fatal("expected error for empty issue ID")
	}
}

func TestIssueTabSaveEmptyIssueID(t *testing.T) {
	store, _ := setupIssueTabStoreTest(t)
	err := store.ReplaceIssueTabs(context.Background(), issueTabTestWorkspaceID, &IssueTabState{
		IssueID: "",
		Tabs:    []IssueTab{},
	})
	if err == nil {
		t.Fatal("expected error for empty issue ID")
	}
}

func TestIssueTabSaveSetsTTL(t *testing.T) {
	store, mr := setupIssueTabStoreTest(t)
	ctx := context.Background()

	state := &IssueTabState{
		IssueID:     "TTL-TEST",
		Tabs:        []IssueTab{{ID: "details", Type: "details", Label: "Details", SortOrder: 0}},
		ActiveTabID: "details",
	}

	if err := store.ReplaceIssueTabs(ctx, issueTabTestWorkspaceID, state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	redisTTL := mr.TTL(issueTabKey(issueTabTestWorkspaceID, "TTL-TEST"))
	// TTL should be approximately 24 hours (allow some margin)
	expected := 24 * time.Hour
	if redisTTL < expected-time.Minute || redisTTL > expected+time.Minute {
		t.Errorf("TTL = %v, want ~%v", redisTTL, expected)
	}
}

func TestIssueTabDelete(t *testing.T) {
	store, _ := setupIssueTabStoreTest(t)
	ctx := context.Background()

	state := &IssueTabState{
		IssueID:     "DEL-TEST",
		Tabs:        []IssueTab{{ID: "details", Type: "details", Label: "Details", SortOrder: 0}},
		ActiveTabID: "details",
	}

	if err := store.ReplaceIssueTabs(ctx, issueTabTestWorkspaceID, state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := store.ClearIssueTabs(ctx, issueTabTestWorkspaceID, "DEL-TEST"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err := store.Get(ctx, issueTabTestWorkspaceID, "DEL-TEST")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil after delete, got %+v", got)
	}
}

func TestIssueTabDeleteEmptyIssueID(t *testing.T) {
	store, _ := setupIssueTabStoreTest(t)
	err := store.ClearIssueTabs(context.Background(), issueTabTestWorkspaceID, "")
	if err == nil {
		t.Fatal("expected error for empty issue ID")
	}
}

func TestValidateAndFilterIssueTabsNilState(t *testing.T) {
	result := interaction.ValidateAndFilterIssueTabs(nil, []string{"sess1"})
	if result != nil {
		t.Fatalf("expected nil, got %+v", result)
	}
}

func TestValidateAndFilterIssueTabsRemovesDeadSessions(t *testing.T) {
	state := &IssueTabState{
		IssueID: "FILTER-1",
		Tabs: []IssueTab{
			{ID: "details", Type: "details", Label: "Details", SortOrder: 0},
			{ID: "terminal-alive", Type: "terminal", Label: "Alive", SessionName: "alive", SortOrder: 1},
			{ID: "terminal-dead", Type: "terminal", Label: "Dead", SessionName: "dead", SortOrder: 2},
		},
		ActiveTabID: "details",
	}

	result := interaction.ValidateAndFilterIssueTabs(state, []string{"alive"})

	if len(result.Tabs) != 2 {
		t.Fatalf("len(Tabs) = %d, want 2", len(result.Tabs))
	}
	for _, tab := range result.Tabs {
		if tab.SessionName == "dead" {
			t.Error("dead session should have been filtered out")
		}
	}
}

func TestValidateAndFilterIssueTabsPreservesNonTerminalTabs(t *testing.T) {
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
	result := interaction.ValidateAndFilterIssueTabs(state, nil)

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

func TestValidateAndFilterIssueTabsAllTerminalsDeadFallsBack(t *testing.T) {
	state := &IssueTabState{
		IssueID: "FILTER-3",
		Tabs: []IssueTab{
			{ID: "details", Type: "details", Label: "Details", SortOrder: 0},
			{ID: "terminal-a", Type: "terminal", Label: "A", SessionName: "a", SortOrder: 1},
			{ID: "terminal-b", Type: "terminal", Label: "B", SessionName: "b", SortOrder: 2},
		},
		ActiveTabID: "terminal-a",
	}

	result := interaction.ValidateAndFilterIssueTabs(state, nil)

	if len(result.Tabs) != 1 {
		t.Fatalf("len(Tabs) = %d, want 1 (only details)", len(result.Tabs))
	}
	if result.Tabs[0].ID != "details" {
		t.Errorf("Tabs[0].ID = %q, want %q", result.Tabs[0].ID, "details")
	}
}

func TestValidateAndFilterIssueTabsFallsBackActiveTab(t *testing.T) {
	state := &IssueTabState{
		IssueID: "FILTER-4",
		Tabs: []IssueTab{
			{ID: "details", Type: "details", Label: "Details", SortOrder: 0},
			{ID: "terminal-dead", Type: "terminal", Label: "Dead", SessionName: "dead", SortOrder: 1},
		},
		ActiveTabID: "terminal-dead",
	}

	result := interaction.ValidateAndFilterIssueTabs(state, nil)

	if result.ActiveTabID != "details" {
		t.Errorf("ActiveTabID = %q, want %q (should fall back when active removed)", result.ActiveTabID, "details")
	}
}

func TestValidateAndFilterIssueTabsKeepsActiveTab(t *testing.T) {
	state := &IssueTabState{
		IssueID: "FILTER-5",
		Tabs: []IssueTab{
			{ID: "details", Type: "details", Label: "Details", SortOrder: 0},
			{ID: "terminal-alive", Type: "terminal", Label: "Alive", SessionName: "alive", SortOrder: 1},
		},
		ActiveTabID: "terminal-alive",
	}

	result := interaction.ValidateAndFilterIssueTabs(state, []string{"alive"})

	if result.ActiveTabID != "terminal-alive" {
		t.Errorf("ActiveTabID = %q, want %q (active tab still alive)", result.ActiveTabID, "terminal-alive")
	}
}

func TestValidateAndFilterIssueTabsRemovesEmptySession(t *testing.T) {
	state := &IssueTabState{
		IssueID: "FILTER-6",
		Tabs: []IssueTab{
			{ID: "details", Type: "details", Label: "Details", SortOrder: 0},
			{ID: "terminal-empty", Type: "terminal", Label: "Bad Tab", SessionName: "", SortOrder: 1},
		},
		ActiveTabID: "details",
	}

	result := interaction.ValidateAndFilterIssueTabs(state, []string{"anything"})

	if len(result.Tabs) != 1 {
		t.Fatalf("len(Tabs) = %d, want 1 (terminal with empty session_name removed)", len(result.Tabs))
	}
}

func TestIssueTabIsolationDifferentWorkspaces(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })

	store := NewIssueTabStore(rdb, nil)
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

	if err := store.ReplaceIssueTabs(ctx, "ws-A", stateA); err != nil {
		t.Fatalf("Save ws-A: %v", err)
	}
	if err := store.ReplaceIssueTabs(ctx, "ws-B", stateB); err != nil {
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
