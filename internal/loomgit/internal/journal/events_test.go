package journal_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

func TestFinishRevisionQueuesOneDurableEvent(t *testing.T) {
	store, err := journal.OpenSQLite(filepath.Join(t.TempDir(), "journal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	revision, err := store.ReserveRevision(ctx, loomgit.Revision{Workspace: "W", Change: "C", RequestID: "revision-1",
		Kind: "source", Operation: "snapshot", Outcome: "completed", BaseSHA: "base", TreeHash: "tree", SourceHeadSHA: "head"})
	if err != nil {
		t.Fatal(err)
	}
	if pending, err := store.PendingEvents(ctx); err != nil || len(pending) != 0 {
		t.Fatalf("event before revision finish: %+v, %v", pending, err)
	}
	revision.HeadSHA = "head"
	for range 2 {
		if err := store.FinishRevision(ctx, revision); err != nil {
			t.Fatal(err)
		}
	}
	events, err := store.PendingEvents(ctx)
	if err != nil || len(events) != 1 || events[0].Kind != "git.revision_created" ||
		!strings.Contains(string(events[0].Payload), `"head_sha":"head"`) {
		t.Fatalf("revision events: %+v, %v", events, err)
	}
}

func TestRecordVerdictQueuesEventOnlyForAcceptedReview(t *testing.T) {
	store, err := journal.OpenSQLite(filepath.Join(t.TempDir(), "journal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	revision, err := store.ReserveRevision(ctx, loomgit.Revision{Workspace: "W", Change: "C", RequestID: "revision-1",
		Kind: "source", Operation: "snapshot", Outcome: "completed", BaseSHA: "base", TreeHash: "tree", SourceHeadSHA: "head"})
	if err != nil {
		t.Fatal(err)
	}
	revision.HeadSHA = "head"
	if err := store.FinishRevision(ctx, revision); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordVerdict(ctx, loomgit.Verdict{Workspace: "W", Change: "C", Number: revision.Number,
		HeadSHA: "wrong", Kind: "approve", ActorKind: "human", ActorID: "reviewer"}); err == nil {
		t.Fatal("wrong-head review was accepted")
	}
	if _, err := store.RecordVerdict(ctx, loomgit.Verdict{Workspace: "W", Change: "C", Number: revision.Number,
		HeadSHA: "head", Kind: "approve", ActorKind: "human", ActorID: "reviewer"}); err != nil {
		t.Fatal(err)
	}
	events, err := store.PendingEvents(ctx)
	if err != nil || len(events) != 2 || events[1].Kind != "git.review_recorded" ||
		!strings.Contains(string(events[1].Payload), `"head_sha":"head"`) {
		t.Fatalf("review events: %+v, %v", events, err)
	}
}

func TestPublishEventWaitsForCompletedPublication(t *testing.T) {
	store, err := journal.OpenSQLite(filepath.Join(t.TempDir(), "journal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	publication := journal.Publication{Workspace: "W", Change: "C", Repo: "repo", Branch: "branch",
		Trunk: "main", Slug: "owner/repo", Head: "head"}
	if err := store.BeginPublication(ctx, publication); err != nil {
		t.Fatal(err)
	}
	publication.Phase = "pushed"
	if err := store.AdvancePublication(ctx, publication); err != nil {
		t.Fatal(err)
	}
	if pending, err := store.PendingEvents(ctx); err != nil || len(pending) != 0 {
		t.Fatalf("event before PR completion: %+v, %v", pending, err)
	}
	publication.Phase, publication.PRNumber, publication.PRURL = "done", 17, "https://example.test/pr/17"
	for range 2 {
		if err := store.AdvancePublication(ctx, publication); err != nil {
			t.Fatal(err)
		}
	}
	events, err := store.PendingEvents(ctx)
	if err != nil || len(events) != 1 || events[0].Kind != "git.published" ||
		!strings.Contains(string(events[0].Payload), `"pr_number":17`) {
		t.Fatalf("publication events: %+v, %v", events, err)
	}
}
