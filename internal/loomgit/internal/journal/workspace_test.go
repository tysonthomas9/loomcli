package journal_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/loomgittest"
)

func TestWorkspaceRecordsCommitWithJournalEntry(t *testing.T) {
	factories := map[string]func(*testing.T) loomgit.WorkspaceStore{
		"sqlite": func(t *testing.T) loomgit.WorkspaceStore {
			s, err := journal.OpenSQLite(filepath.Join(t.TempDir(), "store.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			return s
		},
		"memory": func(_ *testing.T) loomgit.WorkspaceStore { return loomgittest.NewStore() },
	}
	for name, makeStore := range factories {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			s := makeStore(t)
			entry, created, err := s.Begin(ctx, "workspace-create:W1", "ensure_workspace")
			if err != nil || !created {
				t.Fatalf("begin: created=%v err=%v", created, err)
			}
			record := loomgit.WorkspaceRepo{Workspace: "W1", Repo: "api", Trunk: "main", WorkspaceBranch: "loom/ws/W1/interactive/lead", BaseSHA: "abc"}
			if err := s.CommitWorkspace(ctx, entry, []loomgit.WorkspaceRepo{record, record}); err == nil {
				t.Fatal("duplicate record commit succeeded")
			}
			rows, err := s.WorkspaceRepos(ctx, "W1")
			if err != nil || len(rows) != 0 {
				t.Fatalf("partial rows=%v err=%v", rows, err)
			}
			if err := s.CommitWorkspace(ctx, entry, []loomgit.WorkspaceRepo{record}); err != nil {
				t.Fatal(err)
			}
			rows, err = s.WorkspaceRepos(ctx, "W1")
			if err != nil || len(rows) != 1 || rows[0] != record {
				t.Fatalf("committed rows=%v err=%v", rows, err)
			}
		})
	}
}
