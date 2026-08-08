package stackpublish

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sl "github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol/stacklineage"
)

const sid sl.StackID = "epic:E1"

func node(id, base string) sl.Node {
	return sl.Node{StackID: sid, TaskID: id, BaseTaskID: base, OutputBranch: sl.OutputBranchName(sid, id)}
}

func br(id string) string { return sl.OutputBranchName(sid, id) }

func byKind(actions []action) map[string]action {
	m := map[string]action{}
	for _, a := range actions {
		key := a.TaskID
		if key == "" {
			key = "orphan:" + a.Branch
		}
		m[key] = a
	}
	return m
}

func TestComputePlan_FreshCreate(t *testing.T) {
	stack := sl.Stack{ID: sid, RootBase: "main"}
	ordered := []sl.Node{node("T1", ""), node("T2", "T1"), node("T3", "T2")}
	got := byKind(computePlan(stack, ordered, map[string]PR{}, nil))

	require.Equal(t, actCreate, got["T1"].Kind)
	assert.Equal(t, "main", got["T1"].DesiredBase)
	assert.Equal(t, br("T1"), got["T2"].DesiredBase)
	assert.Equal(t, br("T2"), got["T3"].DesiredBase)
}

func TestComputePlan_Idempotent(t *testing.T) {
	stack := sl.Stack{ID: sid, RootBase: "main"}
	ordered := []sl.Node{node("T1", ""), node("T2", "T1"), node("T3", "T2")}
	prs := map[string]PR{
		br("T1"): {Number: 1, Head: br("T1"), Base: "main", State: "open"},
		br("T2"): {Number: 2, Head: br("T2"), Base: br("T1"), State: "open"},
		br("T3"): {Number: 3, Head: br("T3"), Base: br("T2"), State: "open"},
	}
	for _, a := range computePlan(stack, ordered, prs, nil) {
		assert.Equal(t, actSkip, a.Kind, "task %s should be a no-op", a.TaskID)
	}
}

func TestComputePlan_ReorderSwap(t *testing.T) {
	stack := sl.Stack{ID: sid, RootBase: "main"}
	// Desired after swap: T1 -> T3 -> T2.
	ordered := []sl.Node{node("T1", ""), node("T3", "T1"), node("T2", "T3")}
	// Current PRs reflect the OLD order T1->T2->T3.
	prs := map[string]PR{
		br("T1"): {Number: 1, Head: br("T1"), Base: "main", State: "open"},
		br("T2"): {Number: 2, Head: br("T2"), Base: br("T1"), State: "open"},
		br("T3"): {Number: 3, Head: br("T3"), Base: br("T2"), State: "open"},
	}
	got := byKind(computePlan(stack, ordered, prs, nil))
	assert.Equal(t, actSkip, got["T1"].Kind)
	assert.Equal(t, actReparent, got["T3"].Kind)
	assert.Equal(t, br("T1"), got["T3"].DesiredBase)
	assert.Equal(t, actReparent, got["T2"].Kind)
	assert.Equal(t, br("T3"), got["T2"].DesiredBase)
}

func TestComputePlan_DropClosesOrphan(t *testing.T) {
	stack := sl.Stack{ID: sid, RootBase: "main"}
	// T2 dropped; desired is T1 -> T3 (reparented onto T1).
	ordered := []sl.Node{node("T1", ""), node("T3", "T1")}
	prs := map[string]PR{
		br("T1"): {Number: 1, Head: br("T1"), Base: "main", State: "open"},
		br("T2"): {Number: 2, Head: br("T2"), Base: br("T1"), State: "open"}, // orphan
		br("T3"): {Number: 3, Head: br("T3"), Base: br("T2"), State: "open"},
	}
	got := byKind(computePlan(stack, ordered, prs, nil))
	assert.Equal(t, actClose, got["orphan:"+br("T2")].Kind)
	assert.Equal(t, actReparent, got["T3"].Kind)
	assert.Equal(t, br("T1"), got["T3"].DesiredBase, "T3 slides onto T1 after T2 drop")
}

func TestComputePlan_MergedSlide(t *testing.T) {
	stack := sl.Stack{ID: sid, RootBase: "main"}
	ordered := []sl.Node{node("T1", ""), node("T2", "T1")}
	prs := map[string]PR{
		br("T1"): {Number: 1, Head: br("T1"), Base: "main", State: "closed", Merged: true},
		br("T2"): {Number: 2, Head: br("T2"), Base: br("T1"), State: "open"},
	}
	got := byKind(computePlan(stack, ordered, prs, nil))
	assert.Equal(t, actMerged, got["T1"].Kind, "merged unit is terminal")
	assert.Equal(t, actReparent, got["T2"].Kind)
	assert.Equal(t, "main", got["T2"].DesiredBase, "descendant slides to RootBase past the merged unit")
}

func TestComputePlan_Empty(t *testing.T) {
	stack := sl.Stack{ID: sid, RootBase: "main"}
	ordered := []sl.Node{node("T1", ""), node("T2", "T1"), node("T3", "T2")}
	empty := map[string]bool{br("T2"): true}
	got := byKind(computePlan(stack, ordered, map[string]PR{}, empty))
	assert.Equal(t, actEmpty, got["T2"].Kind, "empty unit gets no PR")
	assert.Equal(t, actCreate, got["T3"].Kind)
	assert.Equal(t, br("T1"), got["T3"].DesiredBase, "T3 slides past the empty T2 onto T1")
}
