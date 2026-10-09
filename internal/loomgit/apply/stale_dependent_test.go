package apply

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

// addRevision commits file on top of base in a scratch worktree and records
// it as change's next ready source revision.
func addRevision(t *testing.T, f *fixture, change, base, file string) (int, string) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), file)
	f.git(t, "worktree", "add", "-q", "--detach", path, base)
	if err := os.WriteFile(filepath.Join(path, file), []byte(file+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	scratch, err := gitexec.New(path, gitexec.Options{GlobalConfig: os.DevNull, SystemConfig: os.DevNull,
		FallbackIdentity: gitexec.Identity{Name: "Test", Email: "test@example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", file}, {"commit", "-qm", file}} {
		if _, err := scratch.Run(ctx, args...); err != nil {
			t.Fatal(err)
		}
	}
	out, err := scratch.Run(ctx, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(string(out))
	r, err := f.store.ReserveRevision(ctx, loomgit.Revision{Workspace: "W", Change: change,
		RequestID: change + ":" + file, Kind: "source", Operation: "snapshot", Outcome: "completed",
		BaseSHA: base, TreeHash: f.git(t, "rev-parse", head+"^{tree}"), SourceHeadSHA: head})
	if err != nil {
		t.Fatal(err)
	}
	r.HeadSHA = head
	if err := f.store.FinishRevision(ctx, r); err != nil {
		t.Fatal(err)
	}
	return r.Number, head
}

// dependentFixture is T2 (change C2) built on T1's revision 1 (change C1)
// before T1's code was reviewed, with a lead L that has a working area.
func dependentFixture(t *testing.T) (*fixture, *config.LoomConfig, int, string) {
	t.Helper()
	f, cfg := followFixture(t)
	ctx := context.Background()
	if _, err := f.store.DriverChange(ctx, "W", "T2", "repo", "C2"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.RecordLocalLineage(ctx, journal.LocalLineage{Workspace: "W", Task: "T2", Repo: "repo",
		PredecessorChange: "C1", PredecessorRevision: 1, BaseSHA: f.source}); err != nil {
		t.Fatal(err)
	}
	number, head := addRevision(t, f, "C2", f.source, "second")
	return f, cfg, number, head
}

func revisionOf(t *testing.T, path, task string) review.TaskRevision {
	t.Helper()
	local, err := review.OpenLocalAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = local.Close() }()
	revisions, err := local.TaskRevisionsForLead(context.Background(), "W", task, "L")
	if err != nil || len(revisions) == 0 {
		t.Fatalf("revisions of %s: %+v, %v", task, revisions, err)
	}
	return revisions[0]
}

var reviewer = review.Actor{Kind: "human", ID: "reviewer"}

func hasCode(err error, code loomgit.Code) bool {
	var loomError *loomgit.Error
	return errors.As(err, &loomError) && loomError.Code() == string(code)
}

func openDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// Approving B before A waits, and the revision says what it waits for.
func TestApprovedDependentSaysWhatItWaitsFor(t *testing.T) {
	f, cfg, number, head := dependentFixture(t)
	ctx := context.Background()
	if _, err := review.SubmitForLead(ctx, f.store, "W", "C2", number, head, "approve", "", reviewer, "L"); err != nil {
		t.Fatal(err)
	}
	if result, err := followWithStore(ctx, f.store, cfg, "W", "L"); err != nil || len(result.Pending) != 1 {
		t.Fatalf("dependent approval did not wait: %+v, %v", result, err)
	}
	// The fixture approved T1's revision without a lead, so T1 is approved
	// but not applied in L.
	got := revisionOf(t, f.dbPath, "T2")
	if got.DependsOn != "T1" || got.FollowStatus != "waiting_for_dependency" ||
		got.FollowReason != "waiting for T1's approved code to be applied" || got.LineageState != "" {
		t.Fatalf("waiting dependent = %+v", got)
	}
	if _, err := review.Submit(ctx, f.store, "W", "C1", 1, f.source, "reject", "not yet", reviewer); err == nil {
		// T1's code is no longer approved; the reject also makes T2 stale.
		got = revisionOf(t, f.dbPath, "T2")
		if got.LineageState != "stale" {
			t.Fatalf("dependent of a rejected revision = %+v", got)
		}
	} else {
		t.Fatal(err)
	}
}

// Rejecting A makes B stale: its waiting approval is spent with a reason,
// approving it again is refused, and Rebuild builds it on A's next revision.
func TestRejectedPredecessorMakesDependentStale(t *testing.T) {
	f, cfg, number, head := dependentFixture(t)
	ctx := context.Background()
	if _, err := review.SubmitForLead(ctx, f.store, "W", "C2", number, head, "approve", "", reviewer, "L"); err != nil {
		t.Fatal(err)
	}
	if _, err := review.Submit(ctx, f.store, "W", "C1", 1, f.source, "reject", "wrong approach", reviewer); err != nil {
		t.Fatal(err)
	}
	result, err := followWithStore(ctx, f.store, cfg, "W", "L")
	const waitReason = "built on T1's revision 1, which was rejected: rebuild it once T1 has a new revision"
	if err != nil || len(result.Spent) != 1 || result.Spent[0].Reason != waitReason || len(result.Applied) != 0 {
		t.Fatalf("stale dependent approval was not spent: %+v, %v", result, err)
	}
	got := revisionOf(t, f.dbPath, "T2")
	if got.LineageState != "stale" || got.LineageReason != waitReason || got.RebuildOn != 0 || got.FollowStatus != "spent" {
		t.Fatalf("stale dependent = %+v", got)
	}
	_, err = review.SubmitForLead(ctx, f.store, "W", "C2", number, head, "approve", "", reviewer, "L")
	if !hasCode(err, loomgit.Stale) || !strings.Contains(err.Error(), "approve is refused: "+waitReason) {
		t.Fatalf("approving a stale dependent = %v, want refused", err)
	}

	local, err := review.OpenLocalAt(f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = local.Close() }()
	if _, err := local.Rebuild(ctx, "W", "T2", reviewer); !hasCode(err, loomgit.LineageUnresolved) {
		t.Fatalf("rebuild with no new predecessor revision = %v, want refused", err)
	}
	next, _ := addRevision(t, f, "C1", f.base, "change-again")
	got = revisionOf(t, f.dbPath, "T2")
	if got.RebuildOn != next || !strings.HasSuffix(got.LineageReason, "rebuild it on revision 2") {
		t.Fatalf("dependent with a new predecessor revision = %+v", got)
	}
	rebuilt, err := local.Rebuild(ctx, "W", "T2", reviewer)
	if err != nil || rebuilt.Change != "C2" || rebuilt.RebuildOn != next || rebuilt.VerdictKind != "reject" {
		t.Fatalf("Rebuild = %+v, %v", rebuilt, err)
	}
	verdict, err := f.store.LatestVerdict(ctx, loomgit.Revision{Workspace: "W", Change: "C2", Number: number})
	if err != nil || verdict.Kind != "reject" || verdict.Reason != "rebuild on T1's revision 2" {
		t.Fatalf("rebuild verdict = %+v, %v", verdict, err)
	}
	if _, err := f.store.LocalLineage(ctx, "W", "T2", "repo"); !errors.Is(err, journal.ErrNotFound) {
		t.Fatalf("rebuild kept the stale pin: %v", err)
	}
	if _, err := local.Rebuild(ctx, "W", "T2", reviewer); !hasCode(err, loomgit.Conflict) {
		t.Fatalf("second rebuild = %v, want refused", err)
	}
}

// Override is a human's deliberate decision and is not refused on a stale
// dependent; a published dependent is rebuilt by its stack and is not stale.
func TestStaleDependentAllowsOverrideAndIgnoresPublishedStacks(t *testing.T) {
	f, _, number, head := dependentFixture(t)
	ctx := context.Background()
	if _, err := review.Submit(ctx, f.store, "W", "C1", 1, f.source, "reject", "no", reviewer); err != nil {
		t.Fatal(err)
	}
	if _, err := review.Submit(ctx, f.store, "W", "C2", number, head, "override", "ship it", reviewer); err != nil {
		t.Fatalf("override of a stale dependent: %v", err)
	}
	state, _, found, err := f.store.DependentLineage(ctx, "W", "C2")
	if err != nil || !found || state.State != "stale" {
		t.Fatalf("lineage = %+v, %v, %v", state, found, err)
	}
	db := openDB(t, f.dbPath)
	if _, err := db.ExecContext(ctx, `INSERT INTO change_publications(workspace,change_id,repo,branch,trunk,slug,head_sha,phase)
		VALUES('W','C2','repo','b','main','s',?,'done')`, head); err != nil {
		t.Fatal(err)
	}
	if state, _, _, err := f.store.DependentLineage(ctx, "W", "C2"); err != nil || state.State != "current" {
		t.Fatalf("published dependent lineage = %+v, %v", state, err)
	}
}
