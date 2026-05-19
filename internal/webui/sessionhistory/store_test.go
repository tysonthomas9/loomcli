package sessionhistory

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

func TestStoreClose(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := NewStore(rdb, nil)
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestAddAndList_RoundTrip(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	record := SessionRecord{
		ID:          "issue-proj-abc-1:1700000000",
		SessionName: "issue-proj-abc-1",
		IssueID:     "proj-abc.1",
		Backend:     "claude",
		Status:      "active",
		Launcher:    "user",
		StartedAt:   time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC),
	}

	if err := store.Add(ctx, testWorkspaceID, record); err != nil {
		t.Fatalf("Add: %v", err)
	}

	records, err := store.List(ctx, testWorkspaceID, "proj-abc.1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("len(records) = %d, want 1", len(records))
	}
	if records[0].ID != "issue-proj-abc-1:1700000000" {
		t.Errorf("ID = %q, want %q", records[0].ID, "issue-proj-abc-1:1700000000")
	}
	if records[0].SessionName != "issue-proj-abc-1" {
		t.Errorf("SessionName = %q, want %q", records[0].SessionName, "issue-proj-abc-1")
	}
	if records[0].IssueID != "proj-abc.1" {
		t.Errorf("IssueID = %q, want %q", records[0].IssueID, "proj-abc.1")
	}
	if records[0].Backend != "claude" {
		t.Errorf("Backend = %q, want %q", records[0].Backend, "claude")
	}
	if records[0].Status != "active" {
		t.Errorf("Status = %q, want %q", records[0].Status, "active")
	}
	if records[0].Launcher != "user" {
		t.Errorf("Launcher = %q, want %q", records[0].Launcher, "user")
	}
}

func TestList_EmptySliceForUnknownIssue(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	records, err := store.List(ctx, testWorkspaceID, "unknown-issue.99")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if records == nil {
		t.Fatal("expected non-nil empty slice, got nil")
	}
	if len(records) != 0 {
		t.Fatalf("len(records) = %d, want 0", len(records))
	}
}

func TestList_SortedByMostRecentFirst(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	// Add three records with different StartedAt times.
	records := []SessionRecord{
		{
			ID:          "sess-1:100",
			SessionName: "issue-proj-1",
			IssueID:     "proj.1",
			Backend:     "claude",
			Status:      "completed",
			Launcher:    "user",
			StartedAt:   time.Date(2025, 1, 10, 10, 0, 0, 0, time.UTC), // oldest
		},
		{
			ID:          "sess-2:200",
			SessionName: "issue-proj-1",
			IssueID:     "proj.1",
			Backend:     "claude",
			Status:      "active",
			Launcher:    "user",
			StartedAt:   time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC), // newest
		},
		{
			ID:          "sess-3:150",
			SessionName: "issue-proj-1",
			IssueID:     "proj.1",
			Backend:     "shell",
			Status:      "completed",
			Launcher:    "start-work",
			StartedAt:   time.Date(2025, 1, 12, 10, 0, 0, 0, time.UTC), // middle
		},
	}

	for _, r := range records {
		if err := store.Add(ctx, testWorkspaceID, r); err != nil {
			t.Fatalf("Add(%s): %v", r.ID, err)
		}
	}

	got, err := store.List(ctx, testWorkspaceID, "proj.1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len(records) = %d, want 3", len(got))
	}

	// Most recent first: sess-2 (Jan 15), sess-3 (Jan 12), sess-1 (Jan 10).
	if got[0].ID != "sess-2:200" {
		t.Errorf("records[0].ID = %q, want %q (most recent)", got[0].ID, "sess-2:200")
	}
	if got[1].ID != "sess-3:150" {
		t.Errorf("records[1].ID = %q, want %q (middle)", got[1].ID, "sess-3:150")
	}
	if got[2].ID != "sess-1:100" {
		t.Errorf("records[2].ID = %q, want %q (oldest)", got[2].ID, "sess-1:100")
	}
}

func TestComplete_MarksActiveSession(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	record := SessionRecord{
		ID:          "issue-proj-1:1700000000",
		SessionName: "issue-proj-1",
		IssueID:     "proj.1",
		Backend:     "claude",
		Status:      "active",
		Launcher:    "user",
		StartedAt:   time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC),
	}

	if err := store.Add(ctx, testWorkspaceID, record); err != nil {
		t.Fatalf("Add: %v", err)
	}

	scrollbackPath := "/home/user/.loom/session-scrollback/issue-proj-1.log"
	if err := store.Complete(ctx, testWorkspaceID, "proj.1", "issue-proj-1", scrollbackPath); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	records, err := store.List(ctx, testWorkspaceID, "proj.1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("len(records) = %d, want 1", len(records))
	}
	if records[0].Status != "completed" {
		t.Errorf("Status = %q, want %q", records[0].Status, "completed")
	}
	if records[0].EndedAt == nil {
		t.Fatal("EndedAt should not be nil after Complete")
	}
	if records[0].EndedAt.IsZero() {
		t.Error("EndedAt should not be zero after Complete")
	}
	if records[0].ScrollbackPath != scrollbackPath {
		t.Errorf("ScrollbackPath = %q, want %q", records[0].ScrollbackPath, scrollbackPath)
	}
}

func TestComplete_NoOpWhenNoMatchingActiveSession(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	// Add a completed session (not active).
	record := SessionRecord{
		ID:          "issue-proj-1:1700000000",
		SessionName: "issue-proj-1",
		IssueID:     "proj.1",
		Backend:     "claude",
		Status:      "completed",
		Launcher:    "user",
		StartedAt:   time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC),
	}

	if err := store.Add(ctx, testWorkspaceID, record); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Complete for a session name that doesn't match any active session.
	err := store.Complete(ctx, testWorkspaceID, "proj.1", "nonexistent-session", "/tmp/scrollback.log")
	if err != nil {
		t.Fatalf("Complete should be no-op, got error: %v", err)
	}

	// Verify the existing record was not modified.
	records, err := store.List(ctx, testWorkspaceID, "proj.1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("len(records) = %d, want 1", len(records))
	}
	if records[0].Status != "completed" {
		t.Errorf("Status = %q, want %q (should not have changed)", records[0].Status, "completed")
	}
}

func TestComplete_NoOpForEmptyHistory(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	// Complete for an issue with no history at all.
	err := store.Complete(ctx, testWorkspaceID, "proj.1", "issue-proj-1", "/tmp/scrollback.log")
	if err != nil {
		t.Fatalf("Complete on empty history should be no-op, got error: %v", err)
	}
}

func TestAdd_InvalidIssueID(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	record := SessionRecord{
		ID:          "test:1",
		SessionName: "test",
		IssueID:     "", // empty
		Backend:     "claude",
		Status:      "active",
	}

	err := store.Add(ctx, testWorkspaceID, record)
	if err == nil {
		t.Fatal("expected error for empty issue ID")
	}
}

func TestList_InvalidIssueID(t *testing.T) {
	store, _ := setupTest(t)
	_, err := store.List(context.Background(), testWorkspaceID, "")
	if err == nil {
		t.Fatal("expected error for empty issue ID")
	}
}

func TestComplete_InvalidIssueID(t *testing.T) {
	store, _ := setupTest(t)
	err := store.Complete(context.Background(), testWorkspaceID, "", "session", "/tmp/scrollback.log")
	if err == nil {
		t.Fatal("expected error for empty issue ID")
	}
}

func TestValidateIssueID(t *testing.T) {
	tests := []struct {
		id      string
		wantErr bool
	}{
		{"proj.1", false},
		{"loomcli-fghge.1", false},
		{"PROJ-123", false},
		{"a_b.c-d", false},
		{"", true},
		{"bad id!", true},
		{"has space", true},
	}

	for _, tt := range tests {
		err := ValidateIssueID(tt.id)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateIssueID(%q) error = %v, wantErr = %v", tt.id, err, tt.wantErr)
		}
	}
}

func TestIsolation_DifferentWorkspaces(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })

	store := NewStore(rdb, nil)
	ctx := context.Background()

	recA := SessionRecord{
		ID:          "sess-a:100",
		SessionName: "issue-proj-1",
		IssueID:     "proj.1",
		Backend:     "claude",
		Status:      "active",
		Launcher:    "user",
		StartedAt:   time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC),
	}
	recB := SessionRecord{
		ID:          "sess-b:200",
		SessionName: "issue-proj-1",
		IssueID:     "proj.1",
		Backend:     "shell",
		Status:      "completed",
		Launcher:    "start-work",
		StartedAt:   time.Date(2025, 2, 20, 14, 0, 0, 0, time.UTC),
	}

	if err := store.Add(ctx, "ws-A", recA); err != nil {
		t.Fatalf("Add ws-A: %v", err)
	}
	if err := store.Add(ctx, "ws-B", recB); err != nil {
		t.Fatalf("Add ws-B: %v", err)
	}

	gotA, err := store.List(ctx, "ws-A", "proj.1")
	if err != nil {
		t.Fatalf("List ws-A: %v", err)
	}
	gotB, err := store.List(ctx, "ws-B", "proj.1")
	if err != nil {
		t.Fatalf("List ws-B: %v", err)
	}

	if len(gotA) != 1 {
		t.Fatalf("ws-A: len(records) = %d, want 1", len(gotA))
	}
	if gotA[0].ID != "sess-a:100" {
		t.Errorf("ws-A: records[0].ID = %q, want %q", gotA[0].ID, "sess-a:100")
	}
	if gotA[0].Backend != "claude" {
		t.Errorf("ws-A: records[0].Backend = %q, want %q", gotA[0].Backend, "claude")
	}

	if len(gotB) != 1 {
		t.Fatalf("ws-B: len(records) = %d, want 1", len(gotB))
	}
	if gotB[0].ID != "sess-b:200" {
		t.Errorf("ws-B: records[0].ID = %q, want %q", gotB[0].ID, "sess-b:200")
	}
	if gotB[0].Backend != "shell" {
		t.Errorf("ws-B: records[0].Backend = %q, want %q", gotB[0].Backend, "shell")
	}
}
