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
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

type allowedMerge struct{}

func (allowedMerge) AuthorizeMerge(context.Context, StackRequest, string) error { return nil }

type mergeForgeFake struct {
	*fakeForge
	merged      int
	deleted     []string
	checks      string
	queued      bool
	retargetErr error
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

func (forge *mergeForgeFake) MergeLoomPull(_ context.Context, _, _ string, number int, head string) error {
	for index := range forge.prs {
		if forge.prs[index].Number == number && forge.prs[index].HeadSHA == head {
			forge.prs[index].Merged = true
			forge.prs[index].State = "closed"
			forge.merged++
			return nil
		}
	}
	return journal.ErrStale
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
	stack := StackRequest{Request: request, StackID: "feature", Changes: []string{"A"}, MergeAuthority: allowedMerge{}}
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
	stack := StackRequest{Request: request, StackID: "feature", Changes: []string{"A", "B", "C"}, MergeAuthority: allowedMerge{}}
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
