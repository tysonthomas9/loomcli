package journal

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestApprovalLineageSyncsAndDefersToDeclaredLineage(t *testing.T) {
	store, err := OpenSQLite(filepath.Join(t.TempDir(), "journal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	for _, change := range []string{"A", "B", "C"} {
		if _, err := store.DriverChange(ctx, "W", "task-"+change, "repo", change); err != nil {
			t.Fatal(err)
		}
	}
	stack := []StackLayer{{"A", 1, "a1"}, {"B", 1, "b1"}, {"C", 2, "c2"}}
	for range 2 {
		if err := store.SyncApprovalLineage(ctx, "W", "L", "repo", stack); err != nil {
			t.Fatal(err)
		}
	}
	// task-C is also a declared dependent of B: it is listed once.
	if err := store.RecordLocalLineage(ctx, LocalLineage{Workspace: "W", Task: "task-C", Repo: "repo",
		PredecessorChange: "B", PredecessorRevision: 1, BaseSHA: "b1"}); err != nil {
		t.Fatal(err)
	}
	for change, want := range map[string]string{"A": "task-B", "B": "task-C"} {
		found, err := store.DependentsOf(ctx, "W", change)
		if err != nil || len(found) != 1 || found[0].Task != want {
			t.Fatalf("dependents of %s = %+v, %v", change, found, err)
		}
	}
	// A shorter stack drops the layers that left it.
	if err := store.SyncApprovalLineage(ctx, "W", "L", "repo", stack[1:2]); err != nil {
		t.Fatal(err)
	}
	for _, task := range []string{"task-B", "task-C"} {
		if _, err := store.ApprovalLineage(ctx, "W", task, "repo"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s lineage after shrink: %v", task, err)
		}
	}
	if found, err := store.DependentsOf(ctx, "W", "A"); err != nil || len(found) != 0 {
		t.Fatalf("dependents of A after shrink = %+v, %v", found, err)
	}
}
