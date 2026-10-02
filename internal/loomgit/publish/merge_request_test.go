package publish

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

var tyson = MergeActor{Kind: "human", ID: "Tyson"}
var leadL = MergeActor{Kind: "lead", ID: "L"}

type cardStatusForge struct {
	Forge
	statuses map[string]stackpublish.PRStatus
}

func (forge cardStatusForge) PRStatuses(context.Context, string, string, string) (map[string]stackpublish.PRStatus, error) {
	return forge.statuses, nil
}

func leadMergeFixture(t *testing.T, backend string) (fixture, []string) {
	t.Helper()
	item, forge, heads := fourLayerMergeEntryFixture(t, backend)
	var mergeForge Forge = forge
	if backend == "native" {
		mergeForge = &fakeMergeForge{fakeForge: forge.fakeForge, prs: forge.prs}
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

func TestLeadMergeRequestMergesOnlyAfterHumanConfirms(t *testing.T) {
	for _, backend := range []string{"loom", "native"} {
		t.Run(backend, func(t *testing.T) {
			item, heads := leadMergeFixture(t, backend)
			ctx := context.Background()
			request, err := RequestMergeLocal(ctx, "W", "L", "feature", "C", leadL)
			if err != nil || request.Status != "pending" || request.RequestedBy != "L" || len(request.Layers) != 3 {
				t.Fatalf("request=%+v err=%v", request, err)
			}
			for index, layer := range request.Layers {
				if layer.Head != heads[index] || layer.PRURL == "" {
					t.Fatalf("card layer %d=%+v", index, layer)
				}
			}
			if got := request.ExpiresAt.Sub(mergeRequestNow()); got < 29*time.Minute || got > MergeRequestTTL {
				t.Fatalf("expires in %s", got)
			}
			requireNoMerge(t, item)
			for _, agent := range []MergeActor{leadL, {Kind: "agent", ID: "impl-1"}, {Kind: "human", ID: "L"}, {Kind: "human"}} {
				_, err := ConfirmMergeRequestLocal(ctx, "W", "L", request.ID, agent)
				requireCode(t, err, loomgit.MergeNotAuthorized, "confirmed by a human")
			}
			requireNoMerge(t, item)
			view, err := ConfirmMergeRequestLocal(ctx, "W", "L", request.ID, tyson)
			if err != nil || (view.Phase != "ready" && view.Phase != "sent") {
				t.Fatalf("confirmed view=%+v err=%v", view, err)
			}
			assertMergedThroughC(t, item, backend)
			listed, err := MergeRequestsLocal(ctx, "W", "L")
			if err != nil || len(listed) != 1 || listed[0].Status != "confirmed" || listed[0].ConfirmedBy != "Tyson" {
				t.Fatalf("listed=%+v err=%v", listed, err)
			}
			want := "requested by lead L, confirmed by Tyson: A@" + heads[0] + " B@" + heads[1] + " C@" + heads[2]
			if listed[0].Audit != want {
				t.Fatalf("audit=%q want %q", listed[0].Audit, want)
			}
		})
	}
}

func assertMergedThroughC(t *testing.T, item fixture, backend string) {
	t.Helper()
	ctx := context.Background()
	if backend == "native" {
		merge, err := item.store.NativeMerge(ctx, "W", "feature")
		if err != nil || merge.Target != "C" || strings.Join(merge.Changes, ",") != "A,B,C" {
			t.Fatalf("native merge=%+v err=%v", merge, err)
		}
		return
	}
	merge, err := item.store.LoomMerge(ctx, "W", "feature")
	if err != nil || merge.Target != "C" {
		t.Fatalf("loom merge=%+v err=%v", merge, err)
	}
}

func TestLeadMergeRequestGoesStaleWhenHeadMoves(t *testing.T) {
	item, _ := leadMergeFixture(t, "loom")
	ctx := context.Background()
	request, err := RequestMergeLocal(ctx, "W", "L", "feature", "C", leadL)
	if err != nil {
		t.Fatal(err)
	}
	moved, _, err := item.store.Publication(ctx, "W", "B")
	if err != nil {
		t.Fatal(err)
	}
	if err := item.store.AdoptStackPublications(ctx, []journal.Publication{moved},
		map[string]string{"B": strings.Repeat("b", 40)}); err != nil {
		t.Fatal(err)
	}
	_, err = ConfirmMergeRequestLocal(ctx, "W", "L", request.ID, tyson)
	requireCode(t, err, loomgit.Stale, "request the merge again")
	requireNoMerge(t, item)
	recorded, err := item.store.MergeRequest(ctx, "W", request.ID)
	if err != nil || recorded.Status != "stale" {
		t.Fatalf("recorded=%+v err=%v", recorded, err)
	}
	_, err = ConfirmMergeRequestLocal(ctx, "W", "L", request.ID, tyson)
	requireCode(t, err, loomgit.Stale, "request the merge again")
	requireNoMerge(t, item)
}

func TestLeadMergeRequestGoesStaleWhenOnlyProviderHeadMoves(t *testing.T) {
	for _, backend := range []string{"loom", "native"} {
		t.Run(backend, func(t *testing.T) {
			item, forge, _ := fourLayerMergeEntryFixture(t, backend)
			var mergeForge Forge = forge
			if backend == "native" {
				mergeForge = &fakeMergeForge{fakeForge: forge.fakeForge, prs: forge.prs}
			}
			oldProvider := localPublishProvider
			localPublishProvider = func() (Forge, string, string) { return mergeForge, "fixture-token", "owner/repo" }
			t.Cleanup(func() { localPublishProvider = oldProvider })
			ctx := context.Background()
			request, err := RequestMergeLocal(ctx, "W", "L", "feature", "C", leadL)
			if err != nil {
				t.Fatal(err)
			}
			forge.prs[1].HeadSHA = strings.Repeat("f", 40)
			_, err = ConfirmMergeRequestLocal(ctx, "W", "L", request.ID, tyson)
			requireCode(t, err, loomgit.Stale, "request the merge again")
			requireNoMerge(t, item)
			recorded, err := item.store.MergeRequest(ctx, "W", request.ID)
			if err != nil || recorded.Status != "stale" {
				t.Fatalf("recorded=%+v err=%v", recorded, err)
			}
		})
	}
}

func TestLeadMergeRequestExpiresAfterThirtyMinutes(t *testing.T) {
	item, _ := leadMergeFixture(t, "native")
	ctx := context.Background()
	request, err := RequestMergeLocal(ctx, "W", "L", "feature", "C", leadL)
	if err != nil {
		t.Fatal(err)
	}
	started := mergeRequestNow()
	mergeRequestNow = func() time.Time { return started.Add(MergeRequestTTL + time.Second) }
	t.Cleanup(func() { mergeRequestNow = time.Now })
	_, err = ConfirmMergeRequestLocal(ctx, "W", "L", request.ID, tyson)
	requireCode(t, err, loomgit.MergeNotAuthorized, "expired")
	requireNoMerge(t, item)
	listed, err := MergeRequestsLocal(ctx, "W", "L")
	if err != nil || len(listed) != 1 || listed[0].Status != "expired" {
		t.Fatalf("listed=%+v err=%v", listed, err)
	}
}

func TestMergeRequestRefusesNonLeadAgents(t *testing.T) {
	item, _ := leadMergeFixture(t, "loom")
	ctx := context.Background()
	for _, requester := range []MergeActor{{Kind: "agent", ID: "impl-1"}, {Kind: "lead", ID: "other"}, {Kind: "lead"}} {
		_, err := RequestMergeLocal(ctx, "W", "L", "feature", "C", requester)
		requireCode(t, err, loomgit.MergeNotAuthorized, "only the lead or a human")
	}
	_, err := MergeStackLocal(ctx, "W", "L", "feature", "C", nil, leadL)
	requireCode(t, err, loomgit.MergeNotAuthorized, "confirmed by a human")
	requireNoMerge(t, item)
}

func TestMergeRequestCardShowsChecksAndReviews(t *testing.T) {
	item, _ := leadMergeFixture(t, "loom")
	ctx := context.Background()
	statuses := map[string]stackpublish.PRStatus{}
	for _, change := range []string{"A", "B", "C", "D"} {
		publication, _, err := item.store.Publication(ctx, "W", change)
		if err != nil {
			t.Fatal(err)
		}
		statuses[publication.Branch] = stackpublish.PRStatus{Checks: "passing", Review: "approved"}
	}
	statuses[mustBranch(t, item, "B")] = stackpublish.PRStatus{Checks: "failing", Review: "review_required"}
	localPublishProvider = func() (Forge, string, string) {
		return cardStatusForge{Forge: &fakeForge{}, statuses: statuses}, "fixture-token", "owner/repo"
	}
	request, err := RequestMergeLocal(ctx, "W", "L", "feature", "C", leadL)
	if err != nil {
		t.Fatal(err)
	}
	if request.Layers[0].Checks != "passing" || request.Layers[0].Review != "approved" ||
		request.Layers[1].Checks != "failing" || request.Layers[1].Review != "review_required" {
		t.Fatalf("card=%+v", request.Layers)
	}
}

func mustBranch(t *testing.T, item fixture, change string) string {
	t.Helper()
	publication, _, err := item.store.Publication(context.Background(), "W", change)
	if err != nil {
		t.Fatal(err)
	}
	return publication.Branch
}

func TestMergeAuthorityRefusesUnconfirmedRequest(t *testing.T) {
	item, _ := leadMergeFixture(t, "loom")
	ctx := context.Background()
	request, err := requestMerge(ctx, item.store, "W", "L", "feature", "C", leadL)
	if err != nil {
		t.Fatal(err)
	}
	forge, _, _ := localPublishProvider()
	_, err = mergeStackRecorded(ctx, item.store, request, forge)
	requireCode(t, err, loomgit.MergeNotAuthorized, "merge authorization denied")
	requireNoMerge(t, item)
}
