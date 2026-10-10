package publish

import (
	"context"
	"testing"
)

// Both the merge queue and the when-green lead merge go through the same
// cross-repo gate as MergeUpTo.
func TestQueuedMergeWaitsForCrossRepoPredecessor(t *testing.T) {
	item, _ := leadMergeFixture(t, "loom")
	var unused StackRequest
	dependOnOtherRepo(t, item.store, "A", &unused)
	_, err := QueueMergeUpToLocal(context.Background(), "W", "C", tyson)
	blockedOn(t, err, "owner/api#7")
	requireNoMerge(t, item)
}

func TestWhenGreenLeadMergeWaitsForCrossRepoPredecessor(t *testing.T) {
	item, forge := whenGreenFixture(t, "approved", "approved", "approved", "approved")
	var unused StackRequest
	dependOnOtherRepo(t, item.store, "A", &unused)
	ctx := context.Background()
	setLeadMayMerge(t, item, "when_green")
	_ = ReconcileLeadMergesAt(ctx, item.storePath, forge)
	if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
		t.Fatal(err)
	}
	requireNoMerge(t, item)
	if forge.merged != 0 {
		t.Fatalf("when_green merged %d layers before the predecessor landed", forge.merged)
	}
	if err := item.store.MarkMerged(ctx, "W", "P1"); err != nil {
		t.Fatal(err)
	}
	if err := item.store.MarkLanded(ctx, "W", "P1", "merge_commit"); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileLeadMergesAt(ctx, item.storePath, forge); err != nil {
		t.Fatal(err)
	}
	if merge := leadMerge(t, item); merge.Authority != leadMergeAuthority {
		t.Fatalf("lead merge after predecessor landed = %+v", merge)
	}
}
