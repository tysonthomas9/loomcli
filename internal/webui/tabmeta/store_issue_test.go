package tabmeta

import (
	"context"
	"testing"
	"time"
)

func TestSetAndGet_WithIssueID(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	meta := &TabMetadata{
		SessionName: "issue-session",
		Workspace:   testWorkspace,
		Label:       "Issue Session",
		Notes:       "",
		SortOrder:   1,
		IssueID:     "PROJ-42",
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := store.Set(ctx, meta); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := store.Get(ctx, testWorkspace, "issue-session")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("expected metadata, got nil")
	}
	if got.IssueID != "PROJ-42" {
		t.Errorf("IssueID = %q, want %q", got.IssueID, "PROJ-42")
	}
}

func TestListByIssue_Empty(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	tabs, err := store.ListByIssue(ctx, "no-such-issue")
	if err != nil {
		t.Fatalf("ListByIssue: %v", err)
	}
	if len(tabs) != 0 {
		t.Fatalf("expected empty list, got %d items", len(tabs))
	}
}

func TestListByIssue_FiltersCorrectly(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	// Create sessions with different issue IDs
	sessions := []TabMetadata{
		{SessionName: "s1", Workspace: testWorkspace, Label: "s1", SortOrder: 1, IssueID: "PROJ-1", CreatedAt: now, UpdatedAt: now},
		{SessionName: "s2", Workspace: testWorkspace, Label: "s2", SortOrder: 2, IssueID: "PROJ-1", CreatedAt: now, UpdatedAt: now},
		{SessionName: "s3", Workspace: testWorkspace, Label: "s3", SortOrder: 3, IssueID: "PROJ-2", CreatedAt: now, UpdatedAt: now},
		{SessionName: "s4", Workspace: testWorkspace, Label: "s4", SortOrder: 4, IssueID: "", CreatedAt: now, UpdatedAt: now},
	}
	for _, s := range sessions {
		s := s
		if err := store.Set(ctx, &s); err != nil {
			t.Fatalf("Set %s: %v", s.SessionName, err)
		}
	}

	// List for PROJ-1 should return 2 sessions
	tabs, err := store.ListByIssue(ctx, "PROJ-1")
	if err != nil {
		t.Fatalf("ListByIssue(PROJ-1): %v", err)
	}
	if len(tabs) != 2 {
		t.Fatalf("expected 2 tabs for PROJ-1, got %d", len(tabs))
	}
	names := map[string]bool{}
	for _, tab := range tabs {
		names[tab.SessionName] = true
	}
	if !names["s1"] || !names["s2"] {
		t.Errorf("expected s1 and s2, got %v", names)
	}

	// List for PROJ-2 should return 1 session
	tabs2, err := store.ListByIssue(ctx, "PROJ-2")
	if err != nil {
		t.Fatalf("ListByIssue(PROJ-2): %v", err)
	}
	if len(tabs2) != 1 {
		t.Fatalf("expected 1 tab for PROJ-2, got %d", len(tabs2))
	}
	if tabs2[0].SessionName != "s3" {
		t.Errorf("expected s3, got %s", tabs2[0].SessionName)
	}
}

func TestListIssueSessionMap_Empty(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	m, err := store.ListIssueSessionMap(ctx)
	if err != nil {
		t.Fatalf("ListIssueSessionMap: %v", err)
	}
	if len(m) != 0 {
		t.Fatalf("expected empty map, got %d entries", len(m))
	}
}

func TestListIssueSessionMap_GroupsByIssue(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	sessions := []TabMetadata{
		{SessionName: "s1", Workspace: testWorkspace, Label: "s1", SortOrder: 1, IssueID: "PROJ-1", CreatedAt: now, UpdatedAt: now},
		{SessionName: "s2", Workspace: testWorkspace, Label: "s2", SortOrder: 2, IssueID: "PROJ-1", CreatedAt: now, UpdatedAt: now},
		{SessionName: "s3", Workspace: testWorkspace, Label: "s3", SortOrder: 3, IssueID: "PROJ-2", CreatedAt: now, UpdatedAt: now},
		{SessionName: "s4", Workspace: testWorkspace, Label: "s4", SortOrder: 4, IssueID: "", CreatedAt: now, UpdatedAt: now},
	}
	for _, s := range sessions {
		s := s
		if err := store.Set(ctx, &s); err != nil {
			t.Fatalf("Set %s: %v", s.SessionName, err)
		}
	}

	m, err := store.ListIssueSessionMap(ctx)
	if err != nil {
		t.Fatalf("ListIssueSessionMap: %v", err)
	}

	// Should have 2 issue keys (PROJ-1 and PROJ-2), not the empty-IssueID session
	if len(m) != 2 {
		t.Fatalf("expected 2 issue keys, got %d: %v", len(m), m)
	}

	proj1 := m["PROJ-1"]
	if len(proj1) != 2 {
		t.Errorf("PROJ-1: expected 2 sessions, got %d", len(proj1))
	}

	proj2 := m["PROJ-2"]
	if len(proj2) != 1 {
		t.Errorf("PROJ-2: expected 1 session, got %d", len(proj2))
	}
	if proj2[0] != "s3" {
		t.Errorf("PROJ-2[0] = %q, want %q", proj2[0], "s3")
	}

	// Empty issue ID should not appear
	if _, ok := m[""]; ok {
		t.Error("empty issue ID should not appear in map")
	}
}

func TestListIssueSessionMap_ExcludesEmptyIssueID(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	// Only sessions without issue IDs
	if err := store.Set(ctx, &TabMetadata{
		SessionName: "orphan",
		Workspace:   testWorkspace,
		Label:       "Orphan",
		SortOrder:   1,
		IssueID:     "",
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	m, err := store.ListIssueSessionMap(ctx)
	if err != nil {
		t.Fatalf("ListIssueSessionMap: %v", err)
	}
	if len(m) != 0 {
		t.Fatalf("expected empty map when no sessions have issue IDs, got %v", m)
	}
}

func TestPatch_IssueID(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	if err := store.Set(ctx, &TabMetadata{
		SessionName: "patch-issue",
		Workspace:   testWorkspace,
		Label:       "test",
		SortOrder:   1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := store.Patch(ctx, testWorkspace, "patch-issue", map[string]string{"issue_id": "PROJ-99"})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if got.IssueID != "PROJ-99" {
		t.Errorf("IssueID = %q, want %q", got.IssueID, "PROJ-99")
	}
}
