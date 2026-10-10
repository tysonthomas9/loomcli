package review

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

// P2.24: the agent's Changes tab says where the task started: its blocker's
// revision, the lead's working area, or trunk when the lead has none.
func TestStartedFromBlockerLeadOrTrunk(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	local := &Local{store: s}
	check := func(task, wantKind, wantTask string) {
		t.Helper()
		kind, blocker, err := local.StartedFrom(ctx, "W", task, "L")
		if err != nil || kind != wantKind || blocker != wantTask {
			t.Fatalf("%s: got %q %q %v, want %q %q", task, kind, blocker, err, wantKind, wantTask)
		}
	}
	check("B", "trunk", "")
	if err := s.SaveWorkingAreas(ctx, []journal.WorkingArea{{Workspace: "W", Lead: "L", Repo: "repo",
		Path: t.TempDir(), Branch: "loom/lead", BaseSHA: "abc", Mode: "worktree"}}); err != nil {
		t.Fatal(err)
	}
	check("B", "lead", "")
	if _, err := s.DriverChange(ctx, "W", "A", "repo", "CA"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordLocalLineage(ctx, journal.LocalLineage{Workspace: "W", Task: "B", Repo: "repo",
		PredecessorChange: "CA", PredecessorRevision: 1, BaseSHA: "def"}); err != nil {
		t.Fatal(err)
	}
	check("B", "blocker", "A")
}
