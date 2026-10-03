package apply

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
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
