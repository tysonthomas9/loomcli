package publish

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

// dependOnOtherRepo makes change's task wait for task T1, whose change P1 is
// owner/api PR 7 in another repository.
func dependOnOtherRepo(t *testing.T, store *journal.SQLite, change string, request *StackRequest) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.DriverChange(ctx, "W", "T2", "app", change); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DriverChange(ctx, "W", "T1", "api", "P1"); err != nil {
		t.Fatal(err)
	}
	publication := journal.Publication{Workspace: "W", Change: "P1", Repo: "/api", Branch: "loom/ws/W/change/P1",
		Trunk: "main", Slug: "owner/api", Head: strings.Repeat("a", 40)}
	if err := store.BeginPublication(ctx, publication); err != nil {
		t.Fatal(err)
	}
	publication.Phase, publication.PRNumber = "done", 7
	if err := store.AdvancePublication(ctx, publication); err != nil {
		t.Fatal(err)
	}
	request.Predecessors = func(_ context.Context, workspace, task string) ([]string, error) {
		if workspace == "W" && task == "T2" {
			return []string{"T1"}, nil
		}
		return nil, nil
	}
	previous := mergePredecessors
	mergePredecessors = request.Predecessors
	t.Cleanup(func() { mergePredecessors = previous })
}

func blockedOn(t *testing.T, err error, predecessor string) {
	t.Helper()
	codeIs(t, err, loomgit.MergeBlocked)
	if !strings.Contains(err.Error(), predecessor) {
		t.Fatalf("merge refusal %q does not name %s", err, predecessor)
	}
}

func TestLoomMergeWaitsForCrossRepoPredecessorToLand(t *testing.T) {
	item, forge, request := loomMergeFixture(t)
	dependOnOtherRepo(t, item.store, "A", &request)
	ctx := context.Background()
	blockedOn(t, (LoomStackBackend{Store: item.store}).MergeUpTo(ctx, request, "A"), "owner/api#7")
	if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
		t.Fatal(err)
	}
	if _, err := item.store.LoomMerge(ctx, "W", "feature"); !errors.Is(err, sql.ErrNoRows) || forge.merged != 0 {
		t.Fatalf("dependent merge recorded before its predecessor landed: %v, merged = %d", err, forge.merged)
	}
	if err := item.store.MarkMerged(ctx, "W", "P1"); err != nil {
		t.Fatal(err)
	}
	blockedOn(t, (LoomStackBackend{Store: item.store}).MergeUpTo(ctx, request, "A"), "owner/api#7")
	if err := item.store.MarkLanded(ctx, "W", "P1", "merge_commit"); err != nil {
		t.Fatal(err)
	}
	if err := (LoomStackBackend{Store: item.store}).MergeUpTo(ctx, request, "A"); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
		t.Fatal(err)
	}
	if forge.merged != 1 {
		t.Fatalf("merges after predecessor landed = %d", forge.merged)
	}
}

func TestNativeMergeWaitsForCrossRepoPredecessorOfAnyMergedLayer(t *testing.T) {
	caseFixture, forge, request := nativeMergeFixture(t)
	request.forge = forge
	dependOnOtherRepo(t, caseFixture.store, "B", &request)
	ctx := context.Background()
	blockedOn(t, (GitHubStackBackend{Store: caseFixture.store}).MergeUpTo(ctx, request, "B"), "owner/api#7")
	if len(forge.submitted) != 0 {
		t.Fatalf("submitted before predecessor landed: %v", forge.submitted)
	}
	if err := (GitHubStackBackend{Store: caseFixture.store}).MergeUpTo(ctx, request, "A"); err != nil {
		t.Fatalf("layer below the dependent was held: %v", err)
	}
}

func TestMergeWaitsForPredecessorTaskWithNoRecordedChange(t *testing.T) {
	item, forge, request := loomMergeFixture(t)
	if _, err := item.store.DriverChange(context.Background(), "W", "T2", "app", "A"); err != nil {
		t.Fatal(err)
	}
	request.Predecessors = func(_ context.Context, _, task string) ([]string, error) {
		if task == "T2" {
			return []string{"T9"}, nil
		}
		return nil, nil
	}
	blockedOn(t, (LoomStackBackend{Store: item.store}).MergeUpTo(context.Background(), request, "A"), "task T9")
	if forge.merged != 0 {
		t.Fatal("merged before an unmapped predecessor recorded a change")
	}
}

func TestLoomMergeDispatchRechecksDependenciesAddedAfterRequest(t *testing.T) {
	item, forge, request := loomMergeFixture(t)
	ctx := context.Background()
	if err := (LoomStackBackend{Store: item.store}).MergeUpTo(ctx, request, "A"); err != nil {
		t.Fatal(err)
	}
	dependOnOtherRepo(t, item.store, "A", &request)
	_ = ReconcileLoomMergesAt(ctx, item.storePath, forge)
	merge, err := item.store.LoomMerge(ctx, "W", "feature")
	if err != nil || forge.merged != 0 || merge.Phase != "blocked" || !strings.Contains(merge.Reason, "owner/api#7") {
		t.Fatalf("dispatch ignored a dependency added after the request: merge = %+v, %v, merged = %d", merge, err, forge.merged)
	}
}

func TestNativeMergeDispatchRechecksDependenciesAddedAfterRequest(t *testing.T) {
	caseFixture, forge, request := nativeMergeFixture(t)
	request.forge = forge
	ctx := context.Background()
	if err := beginNativeMerge(ctx, caseFixture.store, request, "B"); err != nil {
		t.Fatal(err)
	}
	dependOnOtherRepo(t, caseFixture.store, "B", &request)
	blockedOn(t, ReconcileNativeMerges(ctx, caseFixture.store, forge), "owner/api#7")
	if len(forge.submitted) != 0 {
		t.Fatalf("dispatched before predecessor landed: %v", forge.submitted)
	}
}
