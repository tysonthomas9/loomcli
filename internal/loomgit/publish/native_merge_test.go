package publish

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

type allowedMerge struct{}

func (allowedMerge) AuthorizeMerge(context.Context, StackRequest, string) error { return nil }

type fakeMergeForge struct {
	*fakeForge
	prs          []stackpublish.PR
	submitted    []int
	mergeErr     error
	resultStatus string
	statusCalls  int
}

func (forge *fakeMergeForge) PullByNumber(_ context.Context, _, _ string, number int) (stackpublish.PR, error) {
	for _, pr := range forge.prs {
		if pr.Number == number {
			return pr, nil
		}
	}
	return stackpublish.PR{}, journal.ErrNotFound
}

func (forge *fakeMergeForge) MergeNativePull(_ context.Context, _, _ string, number int, head string) (stackpublish.NativeMergeResult, error) {
	if forge.mergeErr != nil {
		return stackpublish.NativeMergeResult{}, forge.mergeErr
	}
	pr, err := forge.PullByNumber(context.Background(), "", "", number)
	if err != nil || pr.HeadSHA != head {
		return stackpublish.NativeMergeResult{}, journal.ErrStale
	}
	forge.submitted = append(forge.submitted, number)
	result := stackpublish.NativeMergeResult{Status: "pending"}
	result.Details.UUID = "request-uuid"
	return result, nil
}

func (forge *fakeMergeForge) NativeMergeStatus(_ context.Context, _, _ string, _ int, uuid string) (stackpublish.NativeMergeResult, error) {
	if uuid != "request-uuid" {
		return stackpublish.NativeMergeResult{}, journal.ErrStale
	}
	forge.statusCalls++
	status := forge.resultStatus
	if status == "" {
		status = "pending"
	}
	result := stackpublish.NativeMergeResult{Status: status}
	result.Details.UUID = uuid
	return result, nil
}

func nativeMergeFixture(t *testing.T) (fixture, *fakeMergeForge, StackRequest) {
	t.Helper()
	caseFixture := newFixture(t)
	first := stackRevision(t, caseFixture, "A", 1, caseFixture.base)
	second := stackRevision(t, caseFixture, "B", 1, first.HeadSHA)
	ctx := context.Background()
	if err := caseFixture.store.RecordStackBackend(ctx, "W", "feature", "native"); err != nil {
		t.Fatal(err)
	}
	forge := &fakeMergeForge{fakeForge: &fakeForge{}}
	for index, revision := range []loomgit.Revision{first, second} {
		publication := journal.Publication{Workspace: "W", Change: revision.Change, Repo: caseFixture.repo,
			Branch: revision.Change, Trunk: "main", Slug: "owner/repo", Head: revision.HeadSHA, StackID: "feature"}
		if err := caseFixture.store.BeginPublication(ctx, publication); err != nil {
			t.Fatal(err)
		}
		publication.Phase, publication.PRNumber = "done", index+1
		if err := caseFixture.store.AdvancePublication(ctx, publication); err != nil {
			t.Fatal(err)
		}
		forge.prs = append(forge.prs, stackpublish.PR{Number: index + 1, Head: revision.Change,
			HeadSHA: revision.HeadSHA, State: "open"})
	}
	request := StackRequest{Request: caseFixture.request(), StackID: "feature", Changes: []string{"A", "B"}}
	request.MergeAuthority = allowedMerge{}
	return caseFixture, forge, request
}

func TestNativeMergeRequiresAuthorityAndStackMode(t *testing.T) {
	caseFixture, forge, request := nativeMergeFixture(t)
	ctx := context.Background()
	request.forge = forge
	request.MergeAuthority = nil
	codeIs(t, (GitHubStackBackend{Store: caseFixture.store}).MergeUpTo(ctx, request, "B"), loomgit.MergeNotAuthorized)
	request.MergeAuthority = allowedMerge{}
	if err := caseFixture.store.SetDeliveryMode(ctx, "W", "trunk"); err != nil {
		t.Fatal(err)
	}
	codeIs(t, beginNativeMerge(ctx, caseFixture.store, request, "B"), loomgit.ModeMismatch)
}

func TestNativeMergeResumesAtomicDownstackRequest(t *testing.T) {
	caseFixture, forge, request := nativeMergeFixture(t)
	ctx := context.Background()
	request.forge = forge
	if err := (GitHubStackBackend{Store: caseFixture.store}).MergeUpTo(ctx, request, "B"); err != nil {
		t.Fatal(err)
	}
	if len(forge.submitted) != 1 || forge.submitted[0] != 2 {
		t.Fatalf("submitted = %v", forge.submitted)
	}
	reopened, err := journal.OpenSQLite(caseFixture.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	openMerges, err := reopened.OpenNativeMerges(ctx)
	if err != nil || len(openMerges) != 1 || openMerges[0].UUID != "request-uuid" {
		t.Fatalf("reopened merge = %v, %v", openMerges, err)
	}
	if err := ReconcileNativeMerges(ctx, reopened, forge); err != nil {
		t.Fatal(err)
	}
	if len(forge.submitted) != 1 {
		t.Fatalf("resubmitted after restart: %v", forge.submitted)
	}
	forge.resultStatus = "merged"
	forge.prs[0].Merged, forge.prs[0].State = true, "closed"
	forge.prs[1].Merged, forge.prs[1].State = true, "closed"
	if err := ReconcileNativeMerges(ctx, reopened, forge); err != nil {
		t.Fatal(err)
	}
	if len(forge.submitted) != 1 {
		t.Fatalf("merged before landing: %v", forge.submitted)
	}
	if err := reopened.MarkLanded(ctx, "W", "A"); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileNativeMerges(ctx, reopened, forge); err != nil {
		t.Fatal(err)
	}
	if err := reopened.MarkLanded(ctx, "W", "B"); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileNativeMerges(ctx, reopened, forge); err != nil {
		t.Fatal(err)
	}
	merges, err := reopened.OpenNativeMerges(ctx)
	if err != nil || len(merges) != 0 || len(forge.submitted) != 1 || forge.statusCalls == 0 {
		t.Fatalf("merges=%v submitted=%v polls=%d err=%v", merges, forge.submitted, forge.statusCalls, err)
	}
}

func TestNativeMergeEnqueuedWaitsForLanding(t *testing.T) {
	caseFixture, forge, request := nativeMergeFixture(t)
	ctx := context.Background()
	request.forge = forge
	if err := (GitHubStackBackend{Store: caseFixture.store}).MergeUpTo(ctx, request, "B"); err != nil {
		t.Fatal(err)
	}
	forge.resultStatus = "enqueued"
	if err := ReconcileNativeMerges(ctx, caseFixture.store, forge); err != nil {
		t.Fatal(err)
	}
	merges, err := caseFixture.store.OpenNativeMerges(ctx)
	if err != nil || len(merges) != 1 || len(forge.submitted) != 1 {
		t.Fatalf("merge=%v submits=%v err=%v", merges, forge.submitted, err)
	}
	forge.prs[1].Merged, forge.prs[1].State = true, "closed"
	if err := ReconcileNativeMerges(ctx, caseFixture.store, forge); err != nil {
		t.Fatal(err)
	}
	merges, err = caseFixture.store.OpenNativeMerges(ctx)
	if err != nil || len(merges) != 1 {
		t.Fatalf("advanced before landing: %v, %v", merges, err)
	}
}

func TestNativeMergeRejectsChangedProviderHead(t *testing.T) {
	caseFixture, forge, request := nativeMergeFixture(t)
	request.forge = forge
	forge.prs[0].HeadSHA = "another-head"
	codeIs(t, (GitHubStackBackend{Store: caseFixture.store}).MergeUpTo(context.Background(), request, "B"), loomgit.Stale)
	if len(forge.submitted) != 0 {
		t.Fatalf("submitted = %v", forge.submitted)
	}
	merges, err := caseFixture.store.OpenNativeMerges(context.Background())
	if err != nil || len(merges) != 0 {
		t.Fatalf("open merges = %v, %v", merges, err)
	}
}

func TestNativeMergeUnknownSubmissionRequiresAttention(t *testing.T) {
	caseFixture, forge, request := nativeMergeFixture(t)
	ctx := context.Background()
	if err := beginNativeMerge(ctx, caseFixture.store, request, "A"); err != nil {
		t.Fatal(err)
	}
	merges, err := caseFixture.store.OpenNativeMerges(ctx)
	if err != nil || len(merges) != 1 {
		t.Fatalf("merges = %v, %v", merges, err)
	}
	if err := caseFixture.store.AdvanceNativeMerge(ctx, merges[0], "dispatching", forge.prs[0].HeadSHA, 0); err != nil {
		t.Fatal(err)
	}
	codeIs(t, ReconcileNativeMerges(ctx, caseFixture.store, forge), loomgit.AttentionRequired)
	if len(forge.submitted) != 0 {
		t.Fatalf("submitted = %v", forge.submitted)
	}
}

func TestNativeMergeStopsAfterHigherLayerMoves(t *testing.T) {
	caseFixture, forge, request := nativeMergeFixture(t)
	ctx := context.Background()
	request.forge = forge
	if err := (GitHubStackBackend{Store: caseFixture.store}).MergeUpTo(ctx, request, "B"); err != nil {
		t.Fatal(err)
	}
	forge.prs[1].HeadSHA = "human-pushed"
	forge.resultStatus = "merged"
	codeIs(t, ReconcileNativeMerges(ctx, caseFixture.store, forge), loomgit.Stale)
	if len(forge.submitted) != 1 || forge.submitted[0] != 2 {
		t.Fatalf("submitted = %v", forge.submitted)
	}
}

func TestNativeMergeQueueRequiredStopsWithoutRetry(t *testing.T) {
	caseFixture, forge, request := nativeMergeFixture(t)
	request.forge = forge
	forge.mergeErr = stackpublish.ErrMergeQueueRequired
	ctx := context.Background()
	codeIs(t, (GitHubStackBackend{Store: caseFixture.store}).MergeUpTo(ctx, request, "B"), loomgit.MergeQueueRequired)
	if err := ReconcileNativeMerges(ctx, caseFixture.store, forge); err != nil {
		t.Fatal(err)
	}
	if len(forge.submitted) != 0 {
		t.Fatalf("submitted = %v", forge.submitted)
	}
}

func TestNativeMergeFailedAsyncResultBlocks(t *testing.T) {
	caseFixture, forge, request := nativeMergeFixture(t)
	request.forge = forge
	ctx := context.Background()
	if err := (GitHubStackBackend{Store: caseFixture.store}).MergeUpTo(ctx, request, "B"); err != nil {
		t.Fatal(err)
	}
	forge.resultStatus = "failed"
	codeIs(t, ReconcileNativeMerges(ctx, caseFixture.store, forge), loomgit.MergeBlocked)
	merges, err := caseFixture.store.OpenNativeMerges(ctx)
	if err != nil || len(merges) != 0 || len(forge.submitted) != 1 {
		t.Fatalf("merges=%v submits=%v err=%v", merges, forge.submitted, err)
	}
}
