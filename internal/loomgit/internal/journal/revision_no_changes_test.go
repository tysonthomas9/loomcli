package journal

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
)

func reserveFinished(t *testing.T, s *SQLite, r loomgit.Revision, head string) loomgit.Revision {
	t.Helper()
	got, err := s.ReserveRevision(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	got.HeadSHA = head
	if err := s.FinishRevision(context.Background(), got); err != nil {
		t.Fatal(err)
	}
	got.Ready = true
	return got
}

// An older journal has no no_changes column. Reopening adds it and marks only
// complete source revisions with no commits after their base.
func TestNoChangesColumnBackfillsOnlyEmptyCompleteSources(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	base, other := strings.Repeat("a", 40), strings.Repeat("e", 40)
	row := func(request, kind string) loomgit.Revision {
		return loomgit.Revision{Workspace: "W", Change: "C-" + request, RequestID: request, Kind: kind,
			Operation: "snapshot", Outcome: "completed", BaseSHA: base, TreeHash: strings.Repeat("b", 40), SourceHeadSHA: base}
	}
	empty := reserveFinished(t, s, row("empty", "source"), base)
	changed := reserveFinished(t, s, row("changed", "source"), other)
	derived := reserveFinished(t, s, row("derived", "derived"), base)
	incomplete := reserveFinished(t, s, row("incomplete", "source"), base)
	if err := s.SetRevisionIncomplete(ctx, incomplete); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`ALTER TABLE change_revisions DROP COLUMN no_changes`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	for _, tc := range []struct {
		r    loomgit.Revision
		want bool
	}{{empty, true}, {changed, false}, {derived, false}, {incomplete, false}} {
		got, err := s.GetRevision(ctx, "W", tc.r.Change, tc.r.Number)
		if err != nil || got.NoChanges != tc.want {
			t.Fatalf("%s: no_changes=%v err=%v, want %v", tc.r.RequestID, got.NoChanges, err, tc.want)
		}
	}
	// Reopening an upgraded journal never re-runs the backfill on new rows.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err = OpenSQLite(path); err != nil {
		t.Fatal(err)
	}
	later := reserveFinished(t, s, row("later", "source"), base)
	if later.NoChanges {
		t.Fatal("a reserved row without the flag was marked no_changes")
	}
}

func TestNoChangesIsSourceOnlyAndClearedWhenIncomplete(t *testing.T) {
	ctx := context.Background()
	s, err := OpenSQLite(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	r := loomgit.Revision{Workspace: "W", Change: "C", RequestID: "derived", Kind: "derived", Operation: "apply",
		Outcome: "completed", BaseSHA: strings.Repeat("a", 40), TreeHash: strings.Repeat("b", 40),
		SourceHeadSHA: strings.Repeat("a", 40), NoChanges: true}
	if _, err := s.ReserveRevision(ctx, r); err == nil {
		t.Fatal("a derived revision was recorded as no changes")
	}
	r.Kind, r.RequestID, r.Operation = "source", "source", "snapshot"
	got := reserveFinished(t, s, r, r.BaseSHA)
	if !got.NoChanges {
		t.Fatalf("source no_changes not stored: %+v", got)
	}
	if _, err := s.ReserveRevision(ctx, loomgit.Revision{Workspace: "W", Change: "C", RequestID: "source",
		Kind: "source", Operation: "snapshot", Outcome: "completed", BaseSHA: r.BaseSHA, TreeHash: r.TreeHash,
		SourceHeadSHA: r.SourceHeadSHA}); err == nil {
		t.Fatal("request reused with a different no_changes flag")
	}
	if err := s.SetRevisionIncomplete(ctx, got); err != nil {
		t.Fatal(err)
	}
	if got, err = s.GetRevision(ctx, "W", "C", got.Number); err != nil || got.NoChanges || !got.Incomplete {
		t.Fatalf("incomplete revision kept no_changes: %+v %v", got, err)
	}
}
