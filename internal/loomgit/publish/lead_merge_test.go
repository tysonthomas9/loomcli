package publish

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

var human = review.Actor{Kind: "human", ID: "tyson"}

func leadMergeFixture(t *testing.T, reviews ...string) (fixture, *mergeForgeFake) {
	t.Helper()
	item, forge, _ := fourLayerMergeEntryFixture(t, "loom")
	forge.reviews = map[int]string{}
	for index, state := range reviews {
		forge.reviews[forge.prs[index].Number] = state
	}
	return item, forge
}

func setLeadMayMerge(t *testing.T, item fixture, value string) {
	t.Helper()
	if _, err := SetWorkspacePolicy(context.Background(), item.store, "W", value, human, nil); err != nil {
		t.Fatal(err)
	}
}

func leadMerge(t *testing.T, item fixture) journal.LoomMerge {
	t.Helper()
	merge, err := item.store.LoomMerge(context.Background(), "W", "feature")
	if err != nil {
		t.Fatal(err)
	}
	return merge
}

func TestWhenGreenMergesGreenLayersAndLeavesPendingLayer(t *testing.T) {
	item, forge := leadMergeFixture(t, "approved", "approved", "approved", "approved")
	forge.prChecks = map[int]string{forge.prs[2].Number: "pending"}
	ctx := context.Background()
	setLeadMayMerge(t, item, "when_green")
	if err := ReconcileLeadMergesAt(ctx, item.storePath, forge); err != nil {
		t.Fatal(err)
	}
	merge := leadMerge(t, item)
	if merge.Target != "B" || merge.Authority != leadMergeAuthority || merge.PolicySetBy != "tyson" {
		t.Fatalf("lead merge = %+v, want target B under tyson's setting", merge)
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
		restackAfterMerge(t, item, forge, change, []string{"B", "C"}[index], landed)
		if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
			t.Fatal(err)
		}
	}
	if err := ReconcileLeadMergesAt(ctx, item.storePath, forge); err != nil {
		t.Fatal(err)
	}
	merge = leadMerge(t, item)
	if merge.Phase != "restacking" || merge.Target != "B" || forge.merged != 2 || !forge.prs[1].Merged || forge.prs[2].Merged || forge.prs[3].Merged {
		t.Fatalf("merge = %+v, merged = %d, C = %+v", merge, forge.merged, forge.prs[2])
	}
	forge.prChecks[forge.prs[2].Number] = "passing"
	if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
		t.Fatal(err)
	}
	merge = leadMerge(t, item)
	if merge.Phase != "done" || forge.merged != 2 || forge.prs[2].Merged {
		t.Fatalf("finished merge = %+v, merged = %d", merge, forge.merged)
	}
	for _, layer := range merge.Layers[:2] {
		if layer.MergedBy != "lead under setting set by tyson" {
			t.Fatalf("layer %s audit = %q", layer.Change, layer.MergedBy)
		}
	}
}

func TestLeadMayMergeOffNeverMergesGreenStack(t *testing.T) {
	item, forge := leadMergeFixture(t, "approved", "approved", "approved", "approved")
	ctx := context.Background()
	for _, explicit := range []bool{false, true} {
		if explicit {
			setLeadMayMerge(t, item, "off")
		}
		if err := ReconcileLeadMergesAt(ctx, item.storePath, forge); err != nil {
			t.Fatal(err)
		}
		if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
			t.Fatal(err)
		}
		if _, err := item.store.LoomMerge(ctx, "W", "feature"); !errors.Is(err, sql.ErrNoRows) || forge.merged != 0 {
			t.Fatalf("off policy (explicit=%v) started a merge: %v, merged = %d", explicit, err, forge.merged)
		}
	}
	view, err := appliedMergeView(ctx, item.store, "W", "L", "feature", "B", journal.Publication{Repo: item.repo})
	if err != nil {
		t.Fatal(err)
	}
	request, err := mergeEntryRequest(ctx, item.store, "W", "L", "feature", view, forge)
	if err != nil {
		t.Fatal(err)
	}
	request.MergeAuthority = whenGreenMerge{Store: item.store, SetBy: "tyson"}
	err = beginLoomMerge(ctx, item.store, request, "B")
	var coded *loomgit.Error
	if !errors.As(err, &coded) || coded.Kind != loomgit.MergeNotAuthorized {
		t.Fatalf("policy merge while off = %v", err)
	}
}

func TestSetWorkspacePolicyRefusesAgents(t *testing.T) {
	item, _ := leadMergeFixture(t)
	ctx := context.Background()
	for name, call := range map[string]struct {
		actor review.Actor
		env   []string
	}{
		"agent":          {review.Actor{Kind: "agent", ID: "task-1"}, nil},
		"lead":           {review.Actor{Kind: "lead", ID: "L"}, nil},
		"agent name env": {human, []string{"HOME=/home/tyson", "LOOM_AGENT_NAME=lead"}},
		"task run env":   {human, []string{"LOOM_TASK_RUN_LEASE_TOKEN=secret"}},
		"orchestrator":   {human, []string{"LOOM_ORCHESTRATOR_SESSION_ID=s1"}},
	} {
		_, err := SetWorkspacePolicy(ctx, item.store, "W", "when_green", call.actor, call.env)
		var coded *loomgit.Error
		if !errors.As(err, &coded) || coded.Kind != loomgit.MergeNotAuthorized {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	policy, err := item.store.LeadMayMerge(ctx, "W")
	if err != nil || policy.Value != "off" {
		t.Fatalf("refused call changed policy: %+v, %v", policy, err)
	}
	warning, err := SetWorkspacePolicy(ctx, item.store, "W", "when_green", human, []string{"HOME=/home/tyson"})
	if err != nil || warning != journal.LeadMayMergeWarning {
		t.Fatalf("human policy change: %q, %v", warning, err)
	}
}

func TestWhenGreenMergesWithoutRequiredReview(t *testing.T) {
	item, forge := leadMergeFixture(t, "none", "review_required")
	ctx := context.Background()
	warning, err := SetWorkspacePolicy(ctx, item.store, "W", "when_green", human, nil)
	if err != nil || warning == "" {
		t.Fatalf("no warning when turning on: %q, %v", warning, err)
	}
	if err := ReconcileLeadMergesAt(ctx, item.storePath, forge); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
		t.Fatal(err)
	}
	merge := leadMerge(t, item)
	if merge.Target != "A" || forge.merged != 1 || !forge.prs[0].Merged || forge.prs[1].Merged {
		t.Fatalf("merge = %+v, merged = %d", merge, forge.merged)
	}
}

func TestWhenGreenWaitsForRequiredReviewAtDispatch(t *testing.T) {
	item, forge := leadMergeFixture(t, "approved")
	ctx := context.Background()
	setLeadMayMerge(t, item, "when_green")
	if err := ReconcileLeadMergesAt(ctx, item.storePath, forge); err != nil {
		t.Fatal(err)
	}
	forge.reviews[forge.prs[0].Number] = "review_required"
	if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
		t.Fatal(err)
	}
	if merge := leadMerge(t, item); merge.Phase != "ready" || forge.merged != 0 {
		t.Fatalf("merged without required review: %+v, merged = %d", merge, forge.merged)
	}
}

func TestWhenGreenStopsBelowChangesRequested(t *testing.T) {
	item, forge := leadMergeFixture(t, "approved", "changes_requested", "approved", "approved")
	ctx := context.Background()
	setLeadMayMerge(t, item, "when_green")
	if err := ReconcileLeadMergesAt(ctx, item.storePath, forge); err != nil {
		t.Fatal(err)
	}
	merge := leadMerge(t, item)
	if merge.Target != "A" {
		t.Fatalf("merge target = %q, want A below changes requested", merge.Target)
	}
	forge.reviews[forge.prs[0].Number] = "changes_requested"
	if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err == nil {
		t.Fatal("expected changes requested to block the lead merge")
	}
	if merge := leadMerge(t, item); merge.Phase != "blocked" || forge.merged != 0 {
		t.Fatalf("changes requested merge = %+v, merged = %d", merge, forge.merged)
	}
}

func TestTurningPolicyOffCancelsQueuedLeadMerge(t *testing.T) {
	item, forge := leadMergeFixture(t, "approved", "approved")
	ctx := context.Background()
	setLeadMayMerge(t, item, "when_green")
	if err := ReconcileLeadMergesAt(ctx, item.storePath, forge); err != nil {
		t.Fatal(err)
	}
	if merge := leadMerge(t, item); merge.Phase != "ready" || merge.Target != "B" {
		t.Fatalf("queued merge = %+v", merge)
	}
	setLeadMayMerge(t, item, "off")
	if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
		t.Fatal(err)
	}
	merge := leadMerge(t, item)
	if merge.Phase != "blocked" || merge.Reason != "cancelled: lead_may_merge is off" || forge.merged != 0 {
		t.Fatalf("cancelled merge = %+v, merged = %d", merge, forge.merged)
	}
}

type leadNativeForge struct {
	*mergeForgeFake
	native *fakeMergeForge
}

func (forge leadNativeForge) MergeNativePull(ctx context.Context, owner, repo string, number int, head string) (stackpublish.NativeMergeResult, error) {
	return forge.native.MergeNativePull(ctx, owner, repo, number, head)
}

func (forge leadNativeForge) RecoverNativePull(ctx context.Context, owner, repo string, number int, head string) (stackpublish.NativeMergeResult, error) {
	return forge.native.RecoverNativePull(ctx, owner, repo, number, head)
}

func (forge leadNativeForge) NativeMergeStatus(ctx context.Context, owner, repo string, number int, uuid string) (stackpublish.NativeMergeResult, error) {
	return forge.native.NativeMergeStatus(ctx, owner, repo, number, uuid)
}

func TestWhenGreenMergesNativeGreenPrefix(t *testing.T) {
	item, loomForge, _ := fourLayerMergeEntryFixture(t, "native")
	loomForge.reviews = map[int]string{}
	for _, pr := range loomForge.prs {
		loomForge.reviews[pr.Number] = "approved"
	}
	loomForge.prChecks = map[int]string{loomForge.prs[2].Number: "pending"}
	forge := leadNativeForge{mergeForgeFake: loomForge, native: &fakeMergeForge{fakeForge: loomForge.fakeForge, prs: loomForge.prs}}
	ctx := context.Background()
	setLeadMayMerge(t, item, "when_green")
	if err := ReconcileLeadMergesAt(ctx, item.storePath, forge); err != nil {
		t.Fatal(err)
	}
	merge, err := item.store.NativeMerge(ctx, "W", "feature")
	if err != nil || merge.Target != "B" || len(merge.Changes) != 2 || merge.Authority != leadMergeAuthority {
		t.Fatalf("native lead merge = %+v, %v", merge, err)
	}
	if len(forge.native.submitted) != 1 || forge.native.submitted[0] != loomForge.prs[1].Number {
		t.Fatalf("submitted = %v, want only layer B's PR", forge.native.submitted)
	}
}

func TestTurningPolicyOffCancelsQueuedNativeLeadMerge(t *testing.T) {
	item, loomForge, _ := fourLayerMergeEntryFixture(t, "native")
	native := &fakeMergeForge{fakeForge: loomForge.fakeForge, prs: loomForge.prs}
	ctx := context.Background()
	if err := item.store.BeginNativeMerge(ctx, journal.NativeMerge{Workspace: "W", StackID: "feature", Target: "B",
		Changes: []string{"A", "B"}, Authority: leadMergeAuthority}); err != nil {
		t.Fatal(err)
	}
	setLeadMayMerge(t, item, "off")
	if err := ReconcileNativeMerges(ctx, item.store, native); err != nil {
		t.Fatal(err)
	}
	merge, err := item.store.NativeMerge(ctx, "W", "feature")
	if err != nil || merge.Phase != "blocked" || merge.Reason != "cancelled: lead_may_merge is off" || len(native.submitted) != 0 {
		t.Fatalf("native merge = %+v, submitted = %v, %v", merge, native.submitted, err)
	}
}

func TestTurningPolicyOffCancelsProviderQueuedLeadMerge(t *testing.T) {
	item, forge := leadMergeFixture(t, "approved", "approved")
	forge.queued = true
	ctx := context.Background()
	setLeadMayMerge(t, item, "when_green")
	if err := ReconcileLeadMergesAt(ctx, item.storePath, forge); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
		t.Fatal(err)
	}
	if merge := leadMerge(t, item); merge.Phase != "dispatching" || merge.DispatchAttempts != 0 {
		t.Fatalf("queued dispatch = %+v", merge)
	}
	setLeadMayMerge(t, item, "off")
	forge.queued = false
	for range 2 {
		if err := ReconcileLoomMergesAt(ctx, item.storePath, forge); err != nil {
			t.Fatal(err)
		}
	}
	merge := leadMerge(t, item)
	if merge.Phase != "blocked" || merge.Reason != "cancelled: lead_may_merge is off" || forge.merged != 0 {
		t.Fatalf("queued merge after policy off = %+v, merged = %d", merge, forge.merged)
	}
}
