package stackpublish

import (
	"testing"

	"github.com/stretchr/testify/assert"

	sl "github.com/tysonthomas9/loomcli/internal/stacklineage"
)

// computePlan gaps ported from spr's TestMatchPullRequestStack / TestSPRReorder /
// TestSPRDelete matrix (the portable, linear cases). These harden the reconcile
// decision table that the real-GitHub e2e only spot-checks.

func openPR(num int, task, base string) PR {
	return PR{Number: num, Head: br(task), Base: base, State: "open"}
}

// spr T18 — drop the ROOT: close its PR, reparent the new root to RootBase.
func TestComputePlan_DropRoot(t *testing.T) {
	stack := sl.Stack{ID: sid, RootBase: "main"}
	ordered := []sl.Node{node("T2", ""), node("T3", "T2")} // T1 removed; T2 is new root
	prs := map[string]PR{
		br("T1"): openPR(1, "T1", "main"),
		br("T2"): openPR(2, "T2", br("T1")),
		br("T3"): openPR(3, "T3", br("T2")),
	}
	got := byKind(computePlan(stack, ordered, prs, nil))
	assert.Equal(t, actClose, got["orphan:"+br("T1")].Kind)
	assert.Equal(t, actReparent, got["T2"].Kind)
	assert.Equal(t, "main", got["T2"].DesiredBase, "new root rebased onto RootBase")
	assert.Equal(t, actSkip, got["T3"].Kind)
}

// spr T16 — drop the TOP: close its PR, nothing else moves.
func TestComputePlan_DropTop(t *testing.T) {
	stack := sl.Stack{ID: sid, RootBase: "main"}
	ordered := []sl.Node{node("T1", ""), node("T2", "T1")} // T3 removed
	prs := map[string]PR{
		br("T1"): openPR(1, "T1", "main"),
		br("T2"): openPR(2, "T2", br("T1")),
		br("T3"): openPR(3, "T3", br("T2")),
	}
	got := byKind(computePlan(stack, ordered, prs, nil))
	assert.Equal(t, actSkip, got["T1"].Kind)
	assert.Equal(t, actSkip, got["T2"].Kind)
	assert.Equal(t, actClose, got["orphan:"+br("T3")].Kind)
}

// spr T25 — drop MULTIPLE consecutive middle units: close both orphans, reparent the tail to the survivor.
func TestComputePlan_DropMultipleMiddle(t *testing.T) {
	stack := sl.Stack{ID: sid, RootBase: "main"}
	ordered := []sl.Node{node("T1", ""), node("T4", "T1")} // T2,T3 removed; T4 onto T1
	prs := map[string]PR{
		br("T1"): openPR(1, "T1", "main"),
		br("T2"): openPR(2, "T2", br("T1")),
		br("T3"): openPR(3, "T3", br("T2")),
		br("T4"): openPR(4, "T4", br("T3")),
	}
	got := byKind(computePlan(stack, ordered, prs, nil))
	assert.Equal(t, actClose, got["orphan:"+br("T2")].Kind)
	assert.Equal(t, actClose, got["orphan:"+br("T3")].Kind)
	assert.Equal(t, actReparent, got["T4"].Kind)
	assert.Equal(t, br("T1"), got["T4"].DesiredBase)
	assert.Equal(t, actSkip, got["T1"].Kind)
}

// spr T12 — partial chain: bottom units already have PRs, only the top is new.
func TestComputePlan_PartialChainCreatesTop(t *testing.T) {
	stack := sl.Stack{ID: sid, RootBase: "main"}
	ordered := []sl.Node{node("T1", ""), node("T2", "T1"), node("T3", "T2")}
	prs := map[string]PR{
		br("T1"): openPR(1, "T1", "main"),
		br("T2"): openPR(2, "T2", br("T1")),
		// T3 has no PR yet
	}
	got := byKind(computePlan(stack, ordered, prs, nil))
	assert.Equal(t, actSkip, got["T1"].Kind)
	assert.Equal(t, actSkip, got["T2"].Kind)
	assert.Equal(t, actCreate, got["T3"].Kind)
	assert.Equal(t, br("T2"), got["T3"].DesiredBase)
}

// spr T24 — full non-adjacent permutation: every unit's base changes → all reparent.
func TestComputePlan_FullPermutation(t *testing.T) {
	stack := sl.Stack{ID: sid, RootBase: "main"}
	// Desired order T2 -> T4 -> T1 -> T3.
	ordered := []sl.Node{node("T2", ""), node("T4", "T2"), node("T1", "T4"), node("T3", "T1")}
	prs := map[string]PR{ // current PRs reflect old order T1->T2->T3->T4
		br("T1"): openPR(1, "T1", "main"),
		br("T2"): openPR(2, "T2", br("T1")),
		br("T3"): openPR(3, "T3", br("T2")),
		br("T4"): openPR(4, "T4", br("T3")),
	}
	got := byKind(computePlan(stack, ordered, prs, nil))
	assert.Equal(t, actReparent, got["T2"].Kind)
	assert.Equal(t, "main", got["T2"].DesiredBase)
	assert.Equal(t, br("T2"), got["T4"].DesiredBase)
	assert.Equal(t, br("T4"), got["T1"].DesiredBase)
	assert.Equal(t, br("T1"), got["T3"].DesiredBase)
	for _, task := range []string{"T1", "T2", "T3", "T4"} {
		assert.Equalf(t, actReparent, got[task].Kind, "%s reparents in a full permutation", task)
	}
}

// spr T15 — every unit removed but an orphan PR remains → close it (the empty-stack sweep).
func TestComputePlan_EmptyStackClosesOrphan(t *testing.T) {
	stack := sl.Stack{ID: sid, RootBase: "main"}
	prs := map[string]PR{br("T1"): openPR(1, "T1", "main")}
	actions := computePlan(stack, nil, prs, nil)
	got := byKind(actions)
	assert.Len(t, actions, 1)
	assert.Equal(t, actClose, got["orphan:"+br("T1")].Kind)
}

// Merged MIDDLE unit is terminal; its descendant slides past it to the live predecessor.
func TestComputePlan_MergedMiddleSlide(t *testing.T) {
	stack := sl.Stack{ID: sid, RootBase: "main"}
	ordered := []sl.Node{node("T1", ""), node("T2", "T1"), node("T3", "T2")}
	prs := map[string]PR{
		br("T1"): openPR(1, "T1", "main"),
		br("T2"): {Number: 2, Head: br("T2"), Base: br("T1"), State: "closed", Merged: true},
		br("T3"): openPR(3, "T3", br("T2")),
	}
	got := byKind(computePlan(stack, ordered, prs, nil))
	assert.Equal(t, actSkip, got["T1"].Kind)
	assert.Equal(t, actMerged, got["T2"].Kind)
	assert.Equal(t, actReparent, got["T3"].Kind)
	assert.Equal(t, br("T1"), got["T3"].DesiredBase, "T3 slides past the merged T2 onto T1")
}

// A closed-but-not-merged PR is replaced (create), not skipped.
func TestComputePlan_ClosedNotMergedRecreates(t *testing.T) {
	stack := sl.Stack{ID: sid, RootBase: "main"}
	ordered := []sl.Node{node("T1", "")}
	prs := map[string]PR{
		br("T1"): {Number: 1, Head: br("T1"), Base: "main", State: "closed", Merged: false},
	}
	got := byKind(computePlan(stack, ordered, prs, nil))
	assert.Equal(t, actCreate, got["T1"].Kind, "a closed (not merged) PR is recreated")
}
