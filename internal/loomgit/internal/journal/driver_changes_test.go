package journal

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestRepoForChangeUsesRecordedTaskRepo(t *testing.T) {
	store, err := OpenSQLite(filepath.Join(t.TempDir(), "journal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	if _, err := store.DriverChange(ctx, "workspace", "task", "repo", "change"); err != nil {
		t.Fatal(err)
	}
	repo, err := store.RepoForChange(ctx, "workspace", "change")
	if err != nil || repo != "repo" {
		t.Fatalf("repo = %q, err = %v", repo, err)
	}
	_, err = store.RepoForChange(ctx, "other-workspace", "change")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace lookup error = %v, want ErrNotFound", err)
	}
}
