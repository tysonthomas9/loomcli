package publish

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
)

var tyson = MergeActor{Kind: "human", ID: "Tyson"}
var leadL = MergeActor{Kind: "lead", ID: "L"}

// leadMergeFixture is a four-layer stack A-D applied by lead L and published
// on backend, with every layer approved.
func leadMergeFixture(t *testing.T, backend string) (fixture, []string) {
	t.Helper()
	item, forge, heads := fourLayerMergeEntryFixture(t, backend)
	var mergeForge Forge = forge
	if backend == "native" {
		// Every PR is approved and C's checks are still running, so a lead's
		// merge waits in the queue while a human's is sent straight away.
		forge.reviews = map[int]string{}
		for _, pr := range forge.prs {
			forge.reviews[pr.Number] = "approved"
		}
		forge.prChecks = map[int]string{forge.prs[2].Number: "pending"}
		mergeForge = leadNativeForge{mergeForgeFake: forge, native: &fakeMergeForge{fakeForge: forge.fakeForge, prs: forge.prs}}
	}
	oldProvider := localPublishProvider
	localPublishProvider = func() (Forge, string, string) { return mergeForge, "fixture-token", "owner/repo" }
	t.Cleanup(func() { localPublishProvider = oldProvider })
	return item, heads
}

func requireNoMerge(t *testing.T, item fixture) {
	t.Helper()
	ctx := context.Background()
	if _, err := item.store.LoomMerge(ctx, "W", "feature"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("loom merge recorded: %v", err)
	}
	if _, err := item.store.NativeMerge(ctx, "W", "feature"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("native merge recorded: %v", err)
	}
}

func requireCode(t *testing.T, err error, code loomgit.Code, message string) {
	t.Helper()
	var coded *loomgit.Error
	if !errors.As(err, &coded) || coded.Kind != code || !strings.Contains(err.Error(), message) {
		t.Fatalf("err=%v, want %s containing %q", err, code, message)
	}
}

// requireQueuedThroughC checks the stack's merge machine holds a merge up to C
// under authority, set by setBy.
func requireQueuedThroughC(t *testing.T, item fixture, backend, authority, setBy string) {
	t.Helper()
	ctx := context.Background()
	if backend == "native" {
		merge, err := item.store.NativeMerge(ctx, "W", "feature")
		if err != nil || merge.Target != "C" || strings.Join(merge.Changes, ",") != "A,B,C" ||
			merge.Authority != authority || merge.SetBy != setBy {
			t.Fatalf("native merge=%+v err=%v", merge, err)
		}
		return
	}
	merge, err := item.store.LoomMerge(ctx, "W", "feature")
	if err != nil || merge.Target != "C" || merge.Phase != "ready" || merge.Authority != authority || merge.PolicySetBy != setBy {
		t.Fatalf("loom merge=%+v err=%v", merge, err)
	}
}

func TestLeadMergeIsRefusedWhileLeadMayMergeIsOff(t *testing.T) {
	for _, backend := range []string{"loom", "native"} {
		t.Run(backend, func(t *testing.T) {
			item, _ := leadMergeFixture(t, backend)
			_, err := QueueMergeUpToLocal(context.Background(), "W", "C", leadL)
			requireCode(t, err, loomgit.MergeNotAuthorized, LeadMayMergeOff)
			requireNoMerge(t, item)
		})
	}
}

func TestLeadQueuesWholeStackMergeWithOneRequest(t *testing.T) {
	for _, backend := range []string{"loom", "native"} {
		t.Run(backend, func(t *testing.T) {
			item, _ := leadMergeFixture(t, backend)
			setLeadMayMerge(t, item, "when_green")
			ctx := context.Background()
			view, err := QueueMergeUpToLocal(ctx, "W", "C", leadL)
			if err != nil || view.StackID != "feature" || view.Phase == "" {
				t.Fatalf("view=%+v err=%v", view, err)
			}
			// The lead's merge carries the lead authority, so it waits for green
			// and stops if the human turns Lead may merge off.
			requireQueuedThroughC(t, item, backend, leadMergeAuthority, human.ID)
			shown, err := MergeUpToViewLocal(ctx, "W", "C")
			if err != nil || shown.Phase != view.Phase || shown.StackID != "feature" {
				t.Fatalf("shown=%+v err=%v", shown, err)
			}
			again, err := QueueMergeUpToLocal(ctx, "W", "C", leadL)
			if err != nil || again.Phase != view.Phase {
				t.Fatalf("asking again=%+v err=%v", again, err)
			}
			_, err = QueueMergeUpToLocal(ctx, "W", "D", tyson)
			requireCode(t, err, loomgit.MergeBlocked, "already running")
			requireQueuedThroughC(t, item, backend, leadMergeAuthority, human.ID)
		})
	}
}

func TestHumanQueuesMergeUpToHereWithLeadMayMergeOff(t *testing.T) {
	for _, backend := range []string{"loom", "native"} {
		t.Run(backend, func(t *testing.T) {
			item, _ := leadMergeFixture(t, backend)
			if _, err := QueueMergeUpToLocal(context.Background(), "W", "C", tyson); err != nil {
				t.Fatal(err)
			}
			requireQueuedThroughC(t, item, backend, humanMergeAuthority, "Tyson")
		})
	}
}

func TestMergeQueueRefusesTaskAgentsAndUnnamedActors(t *testing.T) {
	item, _ := leadMergeFixture(t, "loom")
	setLeadMayMerge(t, item, "when_green")
	for _, actor := range []MergeActor{{Kind: "agent", ID: "impl-1"}, {Kind: "lead"}, {Kind: "human"}, {}} {
		_, err := QueueMergeUpToLocal(context.Background(), "W", "C", actor)
		requireCode(t, err, loomgit.MergeNotAuthorized, "only a human or the lead")
	}
	requireNoMerge(t, item)
}

func TestQueuedLeadMergeStopsWhenLeadMayMergeTurnsOff(t *testing.T) {
	item, forge := whenGreenFixture(t, "approved", "approved", "approved", "approved")
	oldProvider := localPublishProvider
	localPublishProvider = func() (Forge, string, string) { return forge, "fixture-token", "owner/repo" }
	t.Cleanup(func() { localPublishProvider = oldProvider })
	setLeadMayMerge(t, item, "when_green")
	ctx := context.Background()
	if _, err := QueueMergeUpToLocal(ctx, "W", "C", leadL); err != nil {
		t.Fatal(err)
	}
	setLeadMayMerge(t, item, "off")
	if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
		t.Fatal(err)
	}
	if merge := leadMerge(t, item); merge.Phase != "blocked" || !strings.Contains(merge.Reason, "lead_may_merge is off") {
		t.Fatalf("merge after Lead may merge off = %+v", merge)
	}
	if forge.merged != 0 {
		t.Fatalf("merged %d layers after Lead may merge was turned off", forge.merged)
	}
}

func TestMergeQueueStartsAgainAfterABlockedMerge(t *testing.T) {
	item, _ := leadMergeFixture(t, "loom")
	ctx := context.Background()
	if _, err := QueueMergeUpToLocal(ctx, "W", "C", tyson); err != nil {
		t.Fatal(err)
	}
	blocked := leadMerge(t, item)
	if err := setLoomPhase(ctx, item.store, blocked, "blocked", 0, "PR 1 checks or review failed"); err != nil {
		t.Fatal(err)
	}
	view, err := QueueMergeUpToLocal(ctx, "W", "C", tyson)
	if err != nil || view.Phase != "ready" {
		t.Fatalf("queued again=%+v err=%v", view, err)
	}
	if merge := leadMerge(t, item); merge.RequestID == blocked.RequestID || merge.Phase != "ready" {
		t.Fatalf("merge=%+v, blocked request %s", merge, blocked.RequestID)
	}
}

func TestHumanQueuedMergeLandsAndRecordsWhoMerged(t *testing.T) {
	item, forge, _ := fourLayerMergeEntryFixture(t, "loom")
	oldProvider := localPublishProvider
	localPublishProvider = func() (Forge, string, string) { return forge, "fixture-token", "owner/repo" }
	t.Cleanup(func() { localPublishProvider = oldProvider })
	if _, err := QueueMergeUpToLocal(context.Background(), "W", "B", tyson); err != nil {
		t.Fatal(err)
	}
	landLoomMergeThroughB(t, item, forge, nil)
	merge := leadMerge(t, item)
	if merge.Layers[0].MergedBy != "Merge up to here by Tyson" || merge.Layers[1].MergedBy != "Merge up to here by Tyson" {
		t.Fatalf("merged by = %+v", merge.Layers)
	}
}

func TestMergeQueueListsQueuedMergeForThePRPage(t *testing.T) {
	item, _ := leadMergeFixture(t, "native")
	setLeadMayMerge(t, item, "when_green")
	ctx := context.Background()
	queue, err := mergeQueue(ctx, item.store, "W")
	if err != nil || len(queue) != 0 {
		t.Fatalf("empty queue=%+v err=%v", queue, err)
	}
	if _, err := QueueMergeUpToLocal(ctx, "W", "C", leadL); err != nil {
		t.Fatal(err)
	}
	queue, err = mergeQueue(ctx, item.store, "W")
	publication, _, _ := item.store.Publication(ctx, "W", "C")
	if err != nil || len(queue) != 1 || queue[0].Target != "C" || queue[0].QueuedBy != "lead" ||
		queue[0].PRNumber != publication.PRNumber || queue[0].Backend != "native" || queue[0].Phase == "" {
		t.Fatalf("queue=%+v err=%v", queue, err)
	}
}
