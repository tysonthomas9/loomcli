package taskcopy

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

func TestRevisionBaseUsesExactNumberedHead(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	path := filepath.Join(os.Getenv("LOOM_CONFIG_DIR"), "loomgit")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	st, err := journal.OpenSQLite(filepath.Join(path, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	for i, head := range []string{"first-head", "second-head"} {
		rev, err := st.ReserveRevision(ctx, loomgit.Revision{Workspace: "W", Change: "C", RequestID: head, Kind: "source", Operation: "snapshot", Outcome: "completed", BaseSHA: "base", TreeHash: "tree", SourceHeadSHA: head})
		if err != nil || rev.Number != i+1 {
			t.Fatalf("reserve %d: %+v %v", i, rev, err)
		}
		rev.HeadSHA = head
		if err := st.FinishRevision(ctx, rev); err != nil {
			t.Fatal(err)
		}
	}
	got, err := RevisionBase(ctx, "W", "C", 2)
	if err != nil || got != "second-head" {
		t.Fatalf("revision C r2 = %q, %v", got, err)
	}
}
