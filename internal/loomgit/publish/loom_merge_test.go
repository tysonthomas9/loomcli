package publish

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

type allowedLoomMerge struct{}

func (allowedLoomMerge) AuthorizeMerge(context.Context, StackRequest, string) error { return nil }

type mergeForgeFake struct {
	*fakeForge
	merged        int
	deleted       []string
	checks        string
	queued        bool
	pending       bool
	putStatus     string
	pollStatus    string
	unknownOnce   bool
	unknownAlways bool
	rejectPut     bool
	retargetErr   error
}

func (forge *mergeForgeFake) PullByNumber(_ context.Context, _, _ string, number int) (stackpublish.PR, error) {
	for _, pr := range forge.prs {
		if pr.Number == number {
			return pr, nil
		}
	}
	return stackpublish.PR{}, journal.ErrNotFound
}

func (forge *mergeForgeFake) PRStatuses(_ context.Context, _, _, prefix string) (map[string]stackpublish.PRStatus, error) {
	statuses := map[string]stackpublish.PRStatus{}
	for _, pr := range forge.prs {
		if pr.Head == prefix {
			statuses[pr.Head] = stackpublish.PRStatus{Number: pr.Number, Checks: forge.checks, Mergeable: "mergeable"}
		}
	}
	return statuses, nil
}

func (forge *mergeForgeFake) QueuedPRNumbers(context.Context, string, string) (map[int]bool, error) {
	queued := map[int]bool{}
	if forge.queued {
		queued[forge.prs[0].Number] = true
	}
	return queued, nil
}

func (forge *mergeForgeFake) FailedLoomChecks(context.Context, string, string, string) ([]string, error) {
	return []string{"ci-build"}, nil
}

func (forge *mergeForgeFake) MergeLoomPull(_ context.Context, _, _ string, number int, head string) (stackpublish.LoomMergeResult, error) {
	for index := range forge.prs {
		if forge.prs[index].Number == number && forge.prs[index].HeadSHA == head {
			forge.merged++
			if forge.rejectPut {
				return stackpublish.LoomMergeResult{}, &stackpublish.LoomMergeRejectedError{Cause: errors.New("unshaped 409 rejected")}
			}
			if (forge.unknownOnce && forge.merged == 1) || forge.unknownAlways {
				return stackpublish.LoomMergeResult{}, errors.New("connection lost after submission")
			}
			if !forge.pending && forge.putStatus != "failed" {
				forge.prs[index].Merged = true
				forge.prs[index].State = "closed"
			}
			result := stackpublish.LoomMergeResult{Status: "pending"}
			if forge.putStatus != "" {
				result.Status = forge.putStatus
				result.Details.Message = "checks rejected"
			}
			result.Details.UUID, result.Details.ExpectedHeadSHA = "request-1", head
			return result, nil
		}
	}
	return stackpublish.LoomMergeResult{}, journal.ErrStale
}

func (forge *mergeForgeFake) LoomMergeStatus(_ context.Context, _, _ string, _ int, uuid string) (stackpublish.LoomMergeResult, error) {
	if uuid != "request-1" {
		return stackpublish.LoomMergeResult{}, journal.ErrStale
	}
	result := stackpublish.LoomMergeResult{Status: "pending"}
	if !forge.pending {
		result.Status = "merged"
	}
	if forge.pollStatus != "" {
		result.Status = forge.pollStatus
		result.Details.Message = "merge queue rejected"
	}
	result.Details.UUID = uuid
	return result, nil
}

func (forge *mergeForgeFake) DeleteLoomBranch(_ context.Context, _, _, branch string) error {
	for _, pr := range forge.prs {
		if pr.State == "open" && pr.Base == branch {
			return errors.New("open PR still targets deleted branch")
		}
	}
	forge.deleted = append(forge.deleted, branch)
	return nil
}

func (forge *mergeForgeFake) UpdatePRBase(ctx context.Context, owner, repo string, number int, base string) error {
	if forge.retargetErr != nil {
		return forge.retargetErr
	}
	return forge.fakeForge.UpdatePRBase(ctx, owner, repo, number, base)
}

func loomMergeFixture(t *testing.T) (fixture, *mergeForgeFake, StackRequest) {
	t.Helper()
	item := newFixture(t)
	first := stackRevision(t, item, "A", 1, item.base)
	forge := &mergeForgeFake{fakeForge: &fakeForge{}, checks: "passing"}
	request := item.request()
	request.forge = forge
	stack := StackRequest{Request: request, StackID: "feature", Changes: []string{"A"}, MergeAuthority: allowedLoomMerge{}}
	if _, err := (LoomStackBackend{Store: item.store}).Publish(context.Background(), stack); err != nil {
		t.Fatal(err)
	}
	if err := item.store.RecordStackBackend(context.Background(), "W", "feature", "loom"); err != nil {
		t.Fatal(err)
	}
	forge.prs[0].HeadSHA = first.HeadSHA
	return item, forge, stack
}

func TestLoomMergeRestartsAtEachPhaseAndDeletesAfterLanding(t *testing.T) {
	item, forge, request := loomMergeFixture(t)
	ctx := context.Background()
	if err := (LoomStackBackend{Store: item.store}).MergeUpTo(ctx, request, "A"); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"merging", "landing"} {
		if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
			t.Fatal(err)
		}
		merge, err := item.store.LoomMerge(ctx, "W", "feature")
		if err != nil || merge.Phase != phase || forge.merged != 1 || len(forge.deleted) != 0 {
			t.Fatalf("merge phase = %+v, merged = %d, deleted = %v, err = %v", merge, forge.merged, forge.deleted, err)
		}
	}
	if err := (LoomStackBackend{Store: item.store}).MergeUpTo(ctx, request, "A"); err != nil {
		t.Fatalf("repeated confirmed request: %v", err)
	}
	if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
		t.Fatal(err)
	}
	if err := item.store.MarkLanded(ctx, "W", "A", "merge_commit"); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"restacking", "done"} {
		if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
			t.Fatal(err)
		}
		merge, err := item.store.LoomMerge(ctx, "W", "feature")
		if err != nil || merge.Phase != phase {
			t.Fatalf("merge phase = %+v, err = %v", merge, err)
		}
	}
	if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil || forge.merged != 1 || len(forge.deleted) != 1 {
		t.Fatalf("replayed merge = %d, deleted = %v, err = %v", forge.merged, forge.deleted, err)
	}
}

func TestLoomMergeTerminalReplayDoesNotRearm(t *testing.T) {
	for _, phase := range []string{"blocked", "done"} {
		t.Run(phase, func(t *testing.T) {
			item, _, request := loomMergeFixture(t)
			ctx := context.Background()
			backend := LoomStackBackend{Store: item.store}
			if err := backend.MergeUpTo(ctx, request, "A"); err != nil {
				t.Fatal(err)
			}
			before, err := item.store.LoomMerge(ctx, "W", "feature")
			if err != nil {
				t.Fatal(err)
			}
			after := before
			after.Phase = phase
			if err := item.store.AdvanceLoomMerge(ctx, before, after); err != nil {
				t.Fatal(err)
			}
			if err := backend.MergeUpTo(ctx, request, "A"); err != nil {
				t.Fatal(err)
			}
			replayed, err := item.store.LoomMerge(ctx, "W", "feature")
			if err != nil || replayed.Phase != phase || replayed.Version != before.Version+1 {
				t.Fatalf("terminal replay = %+v, %v", replayed, err)
			}
		})
	}
}

func TestLoomMergePendingRequestSurvivesTicksAndRestart(t *testing.T) {
	for _, mergedAfterDispatch := range []bool{false, true} {
		name := "pending"
		if mergedAfterDispatch {
			name = "merged"
		}
		t.Run(name, func(t *testing.T) {
			item, forge, request := loomMergeFixture(t)
			forge.pending = true
			ctx := context.Background()
			if err := (LoomStackBackend{Store: item.store}).MergeUpTo(ctx, request, "A"); err != nil {
				t.Fatal(err)
			}
			if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
				t.Fatal(err)
			}
			merge, err := item.store.LoomMerge(ctx, "W", "feature")
			if err != nil || merge.Phase != "merging" || merge.ProviderRequestID != "request-1" ||
				merge.PRNumber != forge.prs[0].Number || merge.DispatchHead != forge.prs[0].HeadSHA {
				t.Fatalf("dispatch ack = %+v, %v", merge, err)
			}
			if mergedAfterDispatch {
				forge.prs[0].Merged, forge.prs[0].State = true, "closed"
			}
			for range 2 {
				if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
					t.Fatal(err)
				}
			}
			merge, err = item.store.LoomMerge(ctx, "W", "feature")
			wantPhase := "merging"
			if mergedAfterDispatch {
				wantPhase = "landing"
			}
			if err != nil || merge.Phase != wantPhase || forge.merged != 1 {
				t.Fatalf("replayed dispatch = %+v, calls = %d, err = %v", merge, forge.merged, err)
			}
		})
	}
}

func TestLoomMergeDispatchingRecoveryKeepsExactHead(t *testing.T) {
	item, forge, request := loomMergeFixture(t)
	forge.pending = true
	ctx := context.Background()
	if err := (LoomStackBackend{Store: item.store}).MergeUpTo(ctx, request, "A"); err != nil {
		t.Fatal(err)
	}
	before, err := item.store.LoomMerge(ctx, "W", "feature")
	if err != nil {
		t.Fatal(err)
	}
	after := before
	after.Phase, after.PRNumber, after.DispatchHead, after.DispatchAttempts = "dispatching", forge.prs[0].Number, forge.prs[0].HeadSHA, 1
	if err := item.store.AdvanceLoomMerge(ctx, before, after); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
			t.Fatal(err)
		}
	}
	recovered, err := item.store.LoomMerge(ctx, "W", "feature")
	if err != nil || recovered.Phase != "merging" || recovered.ProviderRequestID == "" || recovered.DispatchAttempts != 2 || forge.merged != 1 {
		t.Fatalf("recovered dispatch = %+v, calls = %d, err = %v", recovered, forge.merged, err)
	}
}

func TestLoomMergeDefinitiveFailureNeverRetries(t *testing.T) {
	for _, failureAt := range []string{"put", "poll", "rejected"} {
		t.Run(failureAt, func(t *testing.T) {
			item, forge, request := loomMergeFixture(t)
			forge.pending = true
			if failureAt == "put" {
				forge.putStatus = "failed"
			} else if failureAt == "poll" {
				forge.pollStatus = "failed"
			} else {
				forge.rejectPut = true
			}
			ctx := context.Background()
			if err := (LoomStackBackend{Store: item.store}).MergeUpTo(ctx, request, "A"); err != nil {
				t.Fatal(err)
			}
			if failureAt == "poll" {
				if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
					t.Fatal(err)
				}
			}
			codeIs(t, ReconcileLoomMergesAt(ctx, item.storePath, forge), loomgit.MergeBlocked)
			for range 2 {
				if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
					t.Fatal(err)
				}
			}
			merge, err := item.store.LoomMerge(ctx, "W", "feature")
			if err != nil || merge.Phase != "blocked" || !strings.Contains(merge.Reason, "rejected") || forge.merged != 1 {
				t.Fatalf("failed request replay = %+v, calls = %d, err = %v", merge, forge.merged, err)
			}
		})
	}
}

func TestLoomMergeUnknownSubmissionRetriesOnce(t *testing.T) {
	item, forge, request := loomMergeFixture(t)
	forge.pending, forge.unknownOnce = true, true
	ctx := context.Background()
	if err := (LoomStackBackend{Store: item.store}).MergeUpTo(ctx, request, "A"); err != nil {
		t.Fatal(err)
	}
	codeIs(t, ReconcileLoomMergesAt(ctx, item.storePath, forge), loomgit.AttentionRequired)
	if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
		t.Fatal(err)
	}
	merge, err := item.store.LoomMerge(ctx, "W", "feature")
	if err != nil || merge.Phase != "merging" || merge.DispatchAttempts != 2 || forge.merged != 2 {
		t.Fatalf("unknown recovery = %+v, calls = %d, err = %v", merge, forge.merged, err)
	}
}

func TestLoomMergeUnknownSubmissionHasRecoveryLimit(t *testing.T) {
	item, forge, request := loomMergeFixture(t)
	forge.unknownAlways = true
	ctx := context.Background()
	if err := (LoomStackBackend{Store: item.store}).MergeUpTo(ctx, request, "A"); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		codeIs(t, ReconcileLoomMergesAt(ctx, item.storePath, forge), loomgit.AttentionRequired)
	}
	merge, err := item.store.LoomMerge(ctx, "W", "feature")
	if err != nil || merge.Phase != "dispatching" || merge.DispatchAttempts != 2 || forge.merged != 2 {
		t.Fatalf("unknown recovery limit = %+v, calls = %d, err = %v", merge, forge.merged, err)
	}
}

func TestLoomMergeFailsClosedWithoutAuthorization(t *testing.T) {
	item, forge, request := loomMergeFixture(t)
	request.MergeAuthority = nil
	codeIs(t, (LoomStackBackend{Store: item.store}).MergeUpTo(context.Background(), request, "A"), loomgit.MergeNotAuthorized)
	if forge.merged != 0 {
		t.Fatal("unauthorized merge reached provider")
	}
}

func TestLoomMergeRejectsReorderedStack(t *testing.T) {
	item, forge, request := threeLayerMergeFixture(t)
	request.Changes = []string{"B", "A", "C"}
	codeIs(t, (LoomStackBackend{Store: item.store}).MergeUpTo(context.Background(), request, "C"), loomgit.StackNotLinear)
	if forge.merged != 0 {
		t.Fatal("reordered merge reached provider")
	}
}

func TestLoomMergeMarksMovedHeadDiverged(t *testing.T) {
	item, forge, request := loomMergeFixture(t)
	ctx := context.Background()
	if err := (LoomStackBackend{Store: item.store}).MergeUpTo(ctx, request, "A"); err != nil {
		t.Fatal(err)
	}
	forge.prs[0].HeadSHA = item.base
	codeIs(t, ReconcileLoomMergesAt(ctx, item.storePath, forge), loomgit.Stale)
	publication, _, err := item.store.Publication(ctx, "W", "A")
	if err != nil || publication.Phase != "drift" || publication.DriftSHA != item.base || forge.merged != 0 {
		t.Fatalf("drift = %+v, merge calls = %d, err = %v", publication, forge.merged, err)
	}
}

func TestLoomMergeWaitsWhenChecksPending(t *testing.T) {
	item, forge, request := loomMergeFixture(t)
	ctx := context.Background()
	if err := (LoomStackBackend{Store: item.store}).MergeUpTo(ctx, request, "A"); err != nil {
		t.Fatal(err)
	}
	forge.checks = "pending"
	if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
		t.Fatal(err)
	}
	merge, err := item.store.LoomMerge(ctx, "W", "feature")
	if err != nil || merge.Phase != "ready" || forge.merged != 0 {
		t.Fatalf("pending merge = %+v, merged = %d, err = %v", merge, forge.merged, err)
	}
}

func TestLoomMergeQueueRequiredWaitsWithoutDirectMerge(t *testing.T) {
	item, forge, request := loomMergeFixture(t)
	ctx := context.Background()
	if err := (LoomStackBackend{Store: item.store}).MergeUpTo(ctx, request, "A"); err != nil {
		t.Fatal(err)
	}
	forge.queued = true
	if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil || forge.merged != 0 {
		t.Fatalf("queued merge dispatched: calls = %d, err = %v", forge.merged, err)
	}
	forge.queued = false
	if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil || forge.merged != 1 {
		t.Fatalf("queue release did not dispatch: calls = %d, err = %v", forge.merged, err)
	}
}

func TestLoomMergeStopsWhenRestackNeedsVerdict(t *testing.T) {
	item, forge, request := threeLayerMergeFixture(t)
	ctx := context.Background()
	if err := (LoomStackBackend{Store: item.store}).MergeUpTo(ctx, request, "C"); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
		t.Fatal(err)
	}
	if err := item.store.MarkLanded(ctx, "W", "A", "merge_commit"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
			t.Fatal(err)
		}
	}
	offer := journal.RestackOffer{Workspace: "W", Change: "B", Predecessor: "A", Repo: "repo", TrunkSHA: item.base}
	if err := item.store.RecordRestackReviewRequired(ctx, offer, "feature", "L", "B", 2); err != nil {
		t.Fatal(err)
	}
	codeIs(t, ReconcileLoomMergesAt(ctx, item.storePath, forge), loomgit.ReviewRequired)
	merge, err := item.store.LoomMerge(ctx, "W", "feature")
	if err != nil || merge.Phase != "blocked" || forge.merged != 1 {
		t.Fatalf("review stop = %+v, merged = %d, err = %v", merge, forge.merged, err)
	}
}

func TestLoomMergeStopsOnRestackedCheckFailure(t *testing.T) {
	item, forge, request := threeLayerMergeFixture(t)
	ctx := context.Background()
	if err := (LoomStackBackend{Store: item.store}).MergeUpTo(ctx, request, "C"); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
		t.Fatal(err)
	}
	landed := squashMergeLayer(t, item, "A")
	if err := item.store.MarkLanded(ctx, "W", "A", "merge_commit"); err != nil {
		t.Fatal(err)
	}
	restackAfterMerge(t, item, forge, "A", "B", landed)
	forge.checks = "failing"
	for range 2 {
		if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
			t.Fatal(err)
		}
	}
	codeIs(t, ReconcileLoomMergesAt(ctx, item.storePath, forge), loomgit.MergeBlocked)
	merge, err := item.store.LoomMerge(ctx, "W", "feature")
	if err != nil || merge.Phase != "blocked" || forge.merged != 1 || len(forge.deleted) != 0 || !strings.Contains(merge.Reason, "ci-build") {
		t.Fatalf("check failure = %+v, merged = %d, deleted = %v, err = %v", merge, forge.merged, forge.deleted, err)
	}
}

func TestLoomMergeRetargetsBeforeRestackPush(t *testing.T) {
	item, forge, request := threeLayerMergeFixture(t)
	ctx := context.Background()
	if err := (LoomStackBackend{Store: item.store}).MergeUpTo(ctx, request, "C"); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
		t.Fatal(err)
	}
	landed := squashMergeLayer(t, item, "A")
	if err := item.store.MarkLanded(ctx, "W", "A", "merge_commit"); err != nil {
		t.Fatal(err)
	}
	publication, _, err := item.store.Publication(ctx, "W", "B")
	if err != nil {
		t.Fatal(err)
	}
	before := git(t, item.remote, "rev-parse", "refs/heads/"+publication.Branch)
	offer := journal.RestackOffer{Workspace: "W", Change: "B", Predecessor: "A", Repo: "repo", Revision: 1, TrunkSHA: landed}
	if err := item.store.OfferRestack(ctx, offer); err != nil {
		t.Fatal(err)
	}
	forge.retargetErr = errors.New("retarget unavailable")
	if _, err := RestackOffer(ctx, offer, forge); !errors.Is(err, forge.retargetErr) {
		t.Fatalf("retarget error = %v", err)
	}
	if got := git(t, item.remote, "rev-parse", "refs/heads/"+publication.Branch); got != before {
		t.Fatalf("restack pushed before retarget: %s -> %s", before, got)
	}
}

func TestLoomMergeThreeLayersRestacksBetweenMerges(t *testing.T) {
	item, forge, request := threeLayerMergeFixture(t)
	ctx := context.Background()
	if err := (LoomStackBackend{Store: item.store}).MergeUpTo(ctx, request, "C"); err != nil {
		t.Fatal(err)
	}
	for index, change := range []string{"A", "B", "C"} {
		if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
			t.Fatal(err)
		}
		if forge.merged != index+1 {
			t.Fatalf("merged %d layers, want %d", forge.merged, index+1)
		}
		landedSHA := squashMergeLayer(t, item, change)
		forge.prs[index].MergeCommitSHA = landedSHA
		if err := item.store.MarkLanded(ctx, "W", change, "merge_commit"); err != nil {
			t.Fatal(err)
		}
		if index < 2 {
			restackAfterMerge(t, item, forge, change, []string{"B", "C"}[index], landedSHA)
		}
		for range 2 {
			if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
				t.Fatal(err)
			}
		}
		if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
			t.Fatal(err)
		}
	}
	merge, err := item.store.LoomMerge(ctx, "W", "feature")
	if err != nil || merge.Phase != "done" || forge.merged != 3 || len(forge.deleted) != 3 {
		t.Fatalf("merge = %+v, merged = %d, deleted = %v, err = %v", merge, forge.merged, forge.deleted, err)
	}
}

func TestLoomMergeBelowTopRetargetsBeforeDeletion(t *testing.T) {
	item, forge, request := threeLayerMergeFixture(t)
	ctx := context.Background()
	if err := (LoomStackBackend{Store: item.store}).MergeUpTo(ctx, request, "B"); err != nil {
		t.Fatal(err)
	}
	for index, change := range []string{"A", "B"} {
		if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
			t.Fatal(err)
		}
		landed := squashMergeLayer(t, item, change)
		if err := item.store.MarkLanded(ctx, "W", change, "merge_commit"); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
				t.Fatal(err)
			}
		}
		if len(forge.deleted) != index {
			t.Fatalf("deleted branch before next PR restacked: %v", forge.deleted)
		}
		restackAfterMerge(t, item, forge, change, []string{"B", "C"}[index], landed)
		if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
			t.Fatal(err)
		}
	}
	merge, err := item.store.LoomMerge(ctx, "W", "feature")
	if err != nil || merge.Phase != "done" || forge.merged != 2 || len(forge.deleted) != 2 || forge.prs[2].Base != "develop" {
		t.Fatalf("below-top merge = %+v, merged = %d, deleted = %v, next = %+v, err = %v", merge, forge.merged, forge.deleted, forge.prs[2], err)
	}
}

func threeLayerMergeFixture(t *testing.T) (fixture, *mergeForgeFake, StackRequest) {
	t.Helper()
	item := newFixture(t)
	parent := item.base
	for _, change := range []string{"A", "B", "C"} {
		parent = stackRevision(t, item, change, 1, parent).HeadSHA
	}
	git(t, item.repo, "branch", "-m", "loom/ws/W/interactive/L")
	ctx := context.Background()
	if err := item.store.SaveWorkingAreas(ctx, []journal.WorkingArea{{Workspace: "W", Lead: "L", Repo: "repo",
		Path: item.repo, Branch: "loom/ws/W/interactive/L", BaseSHA: item.base, Mode: "worktree"}}); err != nil {
		t.Fatal(err)
	}
	forge := &mergeForgeFake{fakeForge: &fakeForge{}, checks: "passing"}
	request := item.request()
	request.forge = forge
	stack := StackRequest{Request: request, StackID: "feature", Changes: []string{"A", "B", "C"}, MergeAuthority: allowedLoomMerge{}}
	if _, err := (LoomStackBackend{Store: item.store}).Publish(ctx, stack); err != nil {
		t.Fatal(err)
	}
	if err := item.store.RecordStackBackend(ctx, "W", "feature", "loom"); err != nil {
		t.Fatal(err)
	}
	for index, change := range stack.Changes {
		publication, _, err := item.store.Publication(ctx, "W", change)
		if err != nil {
			t.Fatal(err)
		}
		forge.prs[index].HeadSHA = publication.Head
	}
	configDir := t.TempDir()
	if err := os.Symlink(filepath.Dir(item.storePath), filepath.Join(configDir, "loomgit")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	t.Setenv("GITHUB_TOKEN", "fixture-token")
	return item, forge, stack
}

func squashMergeLayer(t *testing.T, item fixture, change string) string {
	t.Helper()
	base := item.base
	if change != "A" {
		base = git(t, item.remote, "rev-parse", "refs/heads/develop")
	}
	trunk := filepath.Join(t.TempDir(), "trunk")
	git(t, item.repo, "worktree", "add", "-q", "--detach", trunk, base)
	if err := os.WriteFile(filepath.Join(trunk, change), []byte(change+"1"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, trunk, "add", change)
	git(t, trunk, "commit", "-qm", "squash "+change)
	head := git(t, trunk, "rev-parse", "HEAD")
	git(t, trunk, "push", "-q", "origin", "HEAD:refs/heads/develop")
	return head
}

func restackAfterMerge(t *testing.T, item fixture, forge *mergeForgeFake, predecessor, next, trunkSHA string) {
	t.Helper()
	ctx := context.Background()
	revision, err := item.store.SourceRevision(ctx, "W", next)
	if err != nil {
		t.Fatal(err)
	}
	offer := journal.RestackOffer{Workspace: "W", Change: next, Predecessor: predecessor,
		Repo: "repo", Revision: revision, TrunkSHA: trunkSHA}
	if err := item.store.OfferRestack(ctx, offer); err != nil {
		t.Fatal(err)
	}
	derived, err := RestackOffer(ctx, offer, forge)
	if err != nil {
		t.Fatal(err)
	}
	if err := item.store.CompleteRestackOffer(ctx, offer, derived); err != nil {
		t.Fatal(err)
	}
	for index, change := range []string{"A", "B", "C"} {
		publication, found, err := item.store.Publication(ctx, "W", change)
		if err != nil || !found {
			t.Fatalf("publication %s: %v", change, err)
		}
		forge.prs[index].HeadSHA = publication.Head
	}
}

func TestLoomMergeAdvancesAfterNoOpRestack(t *testing.T) {
	item, forge, request := threeLayerMergeFixture(t)
	ctx := context.Background()
	reapproveSameHead(t, item, "B", forge.prs[1].HeadSHA)
	if err := (LoomStackBackend{Store: item.store}).MergeUpTo(ctx, request, "B"); err != nil {
		t.Fatal(err)
	}
	for index, change := range []string{"A", "B"} {
		if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
			t.Fatal(err)
		}
		head := forge.prs[index].HeadSHA
		git(t, item.repo, "push", "-q", "origin", head+":refs/heads/develop")
		if err := item.store.MarkLanded(ctx, "W", change, "merge_commit"); err != nil {
			t.Fatal(err)
		}
		next := forge.prs[index+1].HeadSHA
		restackAfterMerge(t, item, forge, change, []string{"B", "C"}[index], head)
		if forge.prs[index+1].HeadSHA != next {
			t.Fatalf("no-op restack moved %s", forge.prs[index+1].Head)
		}
		for range 3 {
			if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
				t.Fatal(err)
			}
		}
	}
	merge, err := item.store.LoomMerge(ctx, "W", "feature")
	if err != nil || merge.Phase != "done" || forge.merged != 2 || len(forge.deleted) != 2 {
		t.Fatalf("no-op restack merge = %+v, merged = %d, deleted = %v, err = %v", merge, forge.merged, forge.deleted, err)
	}
}

func reapproveSameHead(t *testing.T, item fixture, change, head string) {
	t.Helper()
	ctx := context.Background()
	revision, err := item.store.ReserveRevision(ctx, loomgit.Revision{Workspace: "W", Change: change,
		RequestID: change + "-reapprove", Kind: "derived", Operation: "apply", Outcome: "completed",
		BaseSHA: head, TreeHash: head, SourceHeadSHA: head, DerivedFromChange: change, DerivedFromNumber: 1})
	if err != nil {
		t.Fatal(err)
	}
	revision.HeadSHA = head
	if err := item.store.FinishRevision(ctx, revision); err != nil {
		t.Fatal(err)
	}
	if _, err := review.Submit(ctx, item.store, "W", change, revision.Number, head, "approve", "", review.Actor{Kind: "human", ID: "reviewer"}); err != nil {
		t.Fatal(err)
	}
}

func TestLoomMergeNoOpRestackStopsOnMovedHead(t *testing.T) {
	item, forge, request := threeLayerMergeFixture(t)
	ctx := context.Background()
	if err := (LoomStackBackend{Store: item.store}).MergeUpTo(ctx, request, "B"); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
		t.Fatal(err)
	}
	head := forge.prs[0].HeadSHA
	git(t, item.repo, "push", "-q", "origin", head+":refs/heads/develop")
	if err := item.store.MarkLanded(ctx, "W", "A", "merge_commit"); err != nil {
		t.Fatal(err)
	}
	restackAfterMerge(t, item, forge, "A", "B", head)
	forge.prs[1].HeadSHA = head
	var err error
	for range 3 {
		if err = ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
			break
		}
	}
	codeIs(t, err, loomgit.Stale)
	merge, mergeErr := item.store.LoomMerge(ctx, "W", "feature")
	if mergeErr != nil || merge.Phase != "blocked" || forge.merged != 1 {
		t.Fatalf("moved no-op head merge = %+v, merged = %d, err = %v", merge, forge.merged, mergeErr)
	}
}
