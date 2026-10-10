package publish

import (
	"context"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

// landLoomMergeThroughB drives the recorded Loom merge up to B until it is done.
// pending, when set, turns C green so the machine can finish after B lands.
func landLoomMergeThroughB(t *testing.T, item fixture, forge *mergeForgeFake, pending func()) {
	t.Helper()
	ctx := context.Background()
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
		restackAfterMerge(t, item, forge, change, []string{"B", "C"}[index], landed)
		for range 2 {
			if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
				t.Fatal(err)
			}
		}
	}
	if pending != nil {
		pending()
		if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
			t.Fatal(err)
		}
	}
	if merge := leadMerge(t, item); merge.Phase != "done" || merge.Target != "B" {
		t.Fatalf("merge up to B = %+v", merge)
	}
	// The fake provider follows the restacked D head the way GitHub would.
	publication, _, err := item.store.Publication(ctx, "W", "D")
	if err != nil {
		t.Fatal(err)
	}
	forge.prs[3].HeadSHA = publication.Head
}

func loomMergeLandedThroughB(t *testing.T) (fixture, *mergeForgeFake, []string) {
	t.Helper()
	item, forge, heads := fourLayerMergeEntryFixture(t, "loom")
	oldProvider := localPublishProvider
	localPublishProvider = func() (Forge, string, string) { return forge, "fixture-token", "owner/repo" }
	t.Cleanup(func() { localPublishProvider = oldProvider })
	if _, err := QueueMergeUpToLocal(context.Background(), "W", "B", tyson); err != nil {
		t.Fatal(err)
	}
	landLoomMergeThroughB(t, item, forge, nil)
	return item, forge, heads
}

func TestLoomMergeUpToHigherLayerAfterEarlierMergeLanded(t *testing.T) {
	item, forge, heads := loomMergeLandedThroughB(t)
	ctx := context.Background()
	current, _, err := item.store.Publication(ctx, "W", "C")
	if err != nil || current.Head == heads[2] {
		t.Fatalf("C was not restacked: %+v, %v", current, err)
	}
	view, err := MergeUpToViewLocal(ctx, "W", "C")
	if err != nil || view.Phase != "" || len(view.Layers) != 2 || view.Layers[0].Change != "C" ||
		view.Layers[0].Head != current.Head || view.Layers[0].State != "pending" {
		t.Fatalf("view after B landed = %+v, %v", view, err)
	}
	if _, err := QueueMergeUpToLocal(ctx, "W", "C", tyson); err != nil {
		t.Fatal(err)
	}
	merge := leadMerge(t, item)
	if merge.Target != "C" || merge.Phase != "ready" || merge.Index != 0 || merge.Layers[0].Change != "C" || merge.Layers[0].Head != current.Head {
		t.Fatalf("merge up to C = %+v", merge)
	}
	if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
		t.Fatal(err)
	}
	if forge.merged != 3 || !forge.prs[2].Merged || forge.prs[3].Merged {
		t.Fatalf("merged = %d, C = %+v, D = %+v", forge.merged, forge.prs[2], forge.prs[3])
	}
}

func TestLoomMergeUpToHigherLayerRefusedWhenHeadMoves(t *testing.T) {
	item, _, _ := loomMergeLandedThroughB(t)
	ctx := context.Background()
	moved, _, err := item.store.Publication(ctx, "W", "C")
	if err != nil {
		t.Fatal(err)
	}
	if err := item.store.AdoptStackPublications(ctx, []journal.Publication{moved},
		map[string]string{"C": strings.Repeat("c", 40)}); err != nil {
		t.Fatal(err)
	}
	_, err = QueueMergeUpToLocal(ctx, "W", "C", tyson)
	if err == nil {
		t.Fatal("queued a merge of C after its head moved away from the provider's")
	}
	if merge := leadMerge(t, item); merge.Target != "B" || merge.Phase != "done" {
		t.Fatalf("moved head started a merge: %+v", merge)
	}
}

func TestWhenGreenContinuesToNextGreenLayerAfterLoomMergeLanded(t *testing.T) {
	item, forge := whenGreenFixture(t, "approved", "approved", "approved", "approved")
	forge.prChecks = map[int]string{forge.prs[2].Number: "pending"}
	oldProvider := localPublishProvider
	localPublishProvider = func() (Forge, string, string) { return forge, "fixture-token", "owner/repo" }
	t.Cleanup(func() { localPublishProvider = oldProvider })
	ctx := context.Background()
	setLeadMayMerge(t, item, "when_green")
	if err := ReconcileLeadMergesAt(ctx, item.storePath, forge); err != nil {
		t.Fatal(err)
	}
	landLoomMergeThroughB(t, item, forge, func() { forge.prChecks[forge.prs[2].Number] = "passing" })
	if err := ReconcileLeadMergesAt(ctx, item.storePath, forge); err != nil {
		t.Fatal(err)
	}
	merge := leadMerge(t, item)
	if merge.Target != "D" || merge.Authority != leadMergeAuthority || merge.Phase != "ready" ||
		len(merge.Layers) != 2 || merge.Layers[0].Change != "C" || merge.Layers[0].Head != forge.prs[2].HeadSHA {
		t.Fatalf("next lead merge = %+v", merge)
	}
	if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
		t.Fatal(err)
	}
	if forge.merged != 3 || !forge.prs[2].Merged {
		t.Fatalf("merged = %d, C = %+v", forge.merged, forge.prs[2])
	}
}
