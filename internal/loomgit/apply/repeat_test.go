package apply

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

func requireNoRevision(t *testing.T, f *fixture, number int) {
	t.Helper()
	if got, err := f.store.GetRevision(context.Background(), "W", "C1", number); err == nil {
		t.Fatalf("unexpected revision %d: base %s head %s operation %s", number, got.BaseSHA, got.HeadSHA, got.Operation)
	}
}

// Trunk-mode sequence from the P2.18 AFT: a first task is already on the lead,
// the second task's approval is followed by the verdict request and by a
// background reconcile pass that both read it as not yet applied.
func TestApplyRacingFollowPassRecordsNoEmptyDerivedRevision(t *testing.T) {
	f := newFixture(t)
	f.commit(t, "first-task", "first\n", "first task layer")
	request := Request{Workspace: "W", Lead: "L", Change: "C1", Revision: 1, RequestID: "approval:33"}
	first, err := f.service.Apply(context.Background(), request)
	if err != nil || first.Derived.Number != 2 {
		t.Fatalf("first pass: %+v, %v", first, err)
	}
	second, err := f.service.Apply(context.Background(), request)
	if err != nil {
		t.Fatalf("losing pass failed instead of settling: %v", err)
	}
	if second.HeadSHA != first.HeadSHA || second.Derived.Number != 0 || f.git(t, "rev-parse", "HEAD") != first.HeadSHA {
		t.Fatalf("losing pass: %+v", second)
	}
	requireNoRevision(t, f, 3)
	layers, err := f.store.AppliedLog(context.Background(), "W", "L")
	if err != nil || len(layers) != 1 || layers[0].Revision != 2 {
		t.Fatalf("applied log: %+v, %v", layers, err)
	}
}

func TestApplyRepeatVerdictForAppliedChangeRecordsNoEmptyDerivedRevision(t *testing.T) {
	f := newFixture(t)
	f.commit(t, "first-task", "first\n", "first task layer")
	ctx := context.Background()
	first, err := f.service.Apply(ctx, Request{Workspace: "W", Lead: "L", Change: "C1", Revision: 1, RequestID: "approval:33"})
	if err != nil || first.Derived.Number != 2 {
		t.Fatalf("first apply: %+v, %v", first, err)
	}
	again, err := f.service.Apply(ctx, Request{Workspace: "W", Lead: "L", Change: "C1", Revision: 1, RequestID: "approval:34"})
	if err != nil || again.HeadSHA != first.HeadSHA || again.Derived.Number != 0 {
		t.Fatalf("repeat verdict: %+v, %v", again, err)
	}
	requireNoRevision(t, f, 3)
}

func TestApplyUnfinishedRequestLeavesNoPartialDerivedRevision(t *testing.T) {
	f := newFixture(t)
	tip := f.commit(t, "first-task", "first\n", "first task layer")
	ctx := context.Background()
	if err := f.store.SaveApplied(ctx, loomgit.AppliedLayer{RequestID: "approval:9", Workspace: "W", Lead: "L",
		Change: "C1", Revision: 1, OldTip: tip, NewTip: tip}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.AdvanceApplied(ctx, "approval:9", "prepared", "installing"); err != nil {
		t.Fatal(err)
	}
	_, err := f.service.Apply(ctx, Request{Workspace: "W", Lead: "L", Change: "C1", Revision: 1, RequestID: "approval:9"})
	if !errors.Is(err, loomgit.NewError(loomgit.Stale, "", nil)) {
		t.Fatalf("unfinished request: %v", err)
	}
	requireNoRevision(t, f, 2)
	if f.git(t, "rev-parse", "HEAD") != tip {
		t.Fatal("checkout moved")
	}
}

// The flaky first-approval 409 in loomgit-merge-up-to: the verdict request lost
// the same race on a fast-forward layer and surfaced "stale" to the caller.
func TestApplyRacingFollowPassOnFastForwardSettles(t *testing.T) {
	f := newFixture(t)
	request := Request{Workspace: "W", Lead: "L", Change: "C1", Revision: 1, RequestID: "approval:1"}
	if _, err := f.service.Apply(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	second, err := f.service.Apply(context.Background(), request)
	if err != nil || second.HeadSHA != f.source || second.Derived.Number != 0 {
		t.Fatalf("losing verdict pass: %+v, %v", second, err)
	}
	requireNoRevision(t, f, 2)
}

// newSourceRevision records and approves another source revision of C1 whose
// head is a commit on top of parent writing body to the file "change".
func newSourceRevision(t *testing.T, f *fixture, parent, body, message string) loomgit.Revision {
	t.Helper()
	ctx := context.Background()
	lead := f.git(t, "symbolic-ref", "--short", "HEAD")
	f.git(t, "checkout", "-q", "--detach", parent)
	head := f.commit(t, "change", body, message+"\n\nLoom-Change-Id: C1")
	f.git(t, "checkout", "-q", lead)
	r, err := f.store.ReserveRevision(ctx, loomgit.Revision{
		Workspace: "W", Change: "C1", RequestID: "source:" + head, Kind: "source", Operation: "snapshot",
		Outcome: "completed", BaseSHA: f.base, TreeHash: f.git(t, "rev-parse", head+"^{tree}"), SourceHeadSHA: head,
	})
	if err != nil {
		t.Fatal(err)
	}
	r.HeadSHA = head
	if err := f.store.FinishRevision(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := review.Submit(ctx, f.store, "W", "C1", r.Number, head, "approve", "", review.Actor{Kind: "human", ID: "reviewer"}); err != nil {
		t.Fatal(err)
	}
	return r
}

// Verifier probe: a request that applied r1 must not report r2 as applied.
func TestApplyReusedRequestForNewerRevisionIsNotSkipped(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.service.Apply(ctx, Request{Workspace: "W", Lead: "L", Change: "C1", Revision: 1, RequestID: "approval:1"}); err != nil {
		t.Fatal(err)
	}
	r2 := newSourceRevision(t, f, f.source, "change v2\n", "fix-up")
	_, err := f.service.Apply(ctx, Request{Workspace: "W", Lead: "L", Change: "C1", Revision: r2.Number, RequestID: "approval:1"})
	if !errors.Is(err, loomgit.NewError(loomgit.Stale, "", nil)) {
		t.Fatalf("reused request for r%d: %v", r2.Number, err)
	}
	if f.git(t, "rev-parse", "HEAD") != f.source {
		t.Fatal("checkout moved on a refused request")
	}
	requireNoRevision(t, f, r2.Number+1)
	got, err := f.service.Apply(ctx, Request{Workspace: "W", Lead: "L", Change: "C1", Revision: r2.Number, RequestID: "approval:2"})
	if err != nil || f.git(t, "show", "HEAD:change") != "change v2" {
		t.Fatalf("fix-up revision was not applied: %+v, %v", got, err)
	}
}

// A fix-up revision whose replay adds nothing is still a different revision:
// it is applied (as a dropped-commit layer), not reported as already applied.
func TestApplyEmptyReplayOfAnotherRevisionIsNotSkipped(t *testing.T) {
	f := newFixture(t)
	f.commit(t, "first-task", "first\n", "first task layer")
	ctx := context.Background()
	first, err := f.service.Apply(ctx, Request{Workspace: "W", Lead: "L", Change: "C1", Revision: 1, RequestID: "approval:1"})
	if err != nil || first.Derived.Number != 2 {
		t.Fatalf("first apply: %+v, %v", first, err)
	}
	recapture := newSourceRevision(t, f, f.base, "change\n", "re-capture")
	got, err := f.service.Apply(ctx, Request{Workspace: "W", Lead: "L", Change: "C1", Revision: recapture.Number, RequestID: "approval:3"})
	if err != nil || got.Derived.Number == 0 || got.Derived.DerivedFromNumber != recapture.Number {
		t.Fatalf("re-captured revision skipped: %+v, %v", got, err)
	}
	applied, err := f.store.RevisionApplied(ctx, "W", "L", "C1", got.Derived.Number)
	if err != nil || !applied {
		t.Fatalf("re-captured revision has no applied layer: %v, %v", applied, err)
	}
}

// After Unapply, a new approval of the same revision applies it again.
func TestApplyReapprovalAfterUnapplyApplies(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.service.Apply(ctx, Request{Workspace: "W", Lead: "L", Change: "C1", Revision: 1, RequestID: "approval:1"}); err != nil {
		t.Fatal(err)
	}
	f.git(t, "reset", "-q", "--hard", f.base)
	db, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, `UPDATE applied_layers SET phase='unapplied' WHERE request_id='approval:1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Apply(ctx, Request{Workspace: "W", Lead: "L", Change: "C1", Revision: 1, RequestID: "approval:1"}); !errors.Is(err, loomgit.NewError(loomgit.Stale, "", nil)) {
		t.Fatalf("unapplied request reported as applied: %v", err)
	}
	got, err := f.service.Apply(ctx, Request{Workspace: "W", Lead: "L", Change: "C1", Revision: 1, RequestID: "approval:2"})
	if err != nil || got.HeadSHA != f.source || f.git(t, "rev-parse", "HEAD") != f.source {
		t.Fatalf("re-approval after Unapply: %+v, %v", got, err)
	}
	if applied, err := f.store.RevisionApplied(ctx, "W", "L", "C1", 1); err != nil || !applied {
		t.Fatalf("re-approved layer not done: %v, %v", applied, err)
	}
}

// A refused layer write (a concurrent writer won the request) must not leave a
// ready derived revision without its layer; the next pass finishes both.
func TestApplyRefusedLayerLeavesNoReadyDerivedRevision(t *testing.T) {
	f := newFixture(t)
	tip := f.commit(t, "first-task", "first\n", "first task layer")
	ctx := context.Background()
	db, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	f.service.beforeSaveLayer = func() {
		f.service.beforeSaveLayer = nil
		if _, err := db.ExecContext(ctx, `INSERT INTO applied_layers
			(request_id,workspace,lead,change_id,revision,old_tip,new_tip,commits,dropped,commit_details,phase)
			VALUES ('approval:7','W','L','C1',1,?,?,'[]','[]','[]','done')`, tip, tip); err != nil {
			t.Error(err)
		}
	}
	request := Request{Workspace: "W", Lead: "L", Change: "C1", Revision: 1, RequestID: "approval:7"}
	if _, err := f.service.Apply(ctx, request); !errors.Is(err, loomgit.NewError(loomgit.Stale, "", nil)) {
		t.Fatalf("refused layer: %v", err)
	}
	if r, err := f.store.GetRevision(ctx, "W", "C1", 2); err == nil && r.Ready {
		t.Fatalf("ready derived revision left without a layer: %+v", r)
	}
	if f.git(t, "rev-parse", "HEAD") != tip {
		t.Fatal("checkout moved")
	}
	if _, err := db.ExecContext(ctx, `UPDATE applied_layers SET phase='not_applied' WHERE request_id='approval:7'`); err != nil {
		t.Fatal(err)
	}
	// Cross a commit-timestamp second so a fresh replay gets a different SHA
	// than the one the first pass reserved.
	time.Sleep(1100 * time.Millisecond)
	got, err := f.service.Apply(ctx, request)
	if err != nil || got.Derived.Number != 2 || !got.Derived.Ready {
		t.Fatalf("next pass: %+v, %v", got, err)
	}
	if applied, err := f.store.RevisionApplied(ctx, "W", "L", "C1", 2); err != nil || !applied {
		t.Fatalf("derived revision has no done layer: %v, %v", applied, err)
	}
	if r, err := f.store.GetRevision(ctx, "W", "C1", 2); err != nil || !r.Ready || r.HeadSHA != got.HeadSHA {
		t.Fatalf("derived revision: %+v, %v", r, err)
	}
}
