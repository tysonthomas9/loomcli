package sourcecontrolstacklineage_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	. "github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
)

func TestOutputBranchName_Sanitization(t *testing.T) {
	cases := []struct {
		stack StackID
		task  string
		want  string
	}{
		{"epic:EPIC-1", "TASK-1", "loom/stack/epic-EPIC-1/TASK-1"},
		{"manual:parser-followups", "BUG-2", "loom/stack/manual-parser-followups/BUG-2"},
		{"epic:E1", "feature/web ui", "loom/stack/epic-E1/feature-web-ui"},
		{"epic:E1", "a..b", "loom/stack/epic-E1/a-b"},         // no ".."
		{"epic:E1", "-leading", "loom/stack/epic-E1/leading"}, // no leading '-'
		{"epic:E1", "trail.lock", "loom/stack/epic-E1/trail"}, // no trailing ".lock"
		{"epic:E1", "@{weird}", "loom/stack/epic-E1/weird"},
		{"epic:E1", "///", "loom/stack/epic-E1/unit"}, // degenerate → "unit"
	}
	for _, c := range cases {
		assert.Equal(t, c.want, OutputBranchName(c.stack, c.task), "stack=%q task=%q", c.stack, c.task)
	}
}

func TestAssignBranch_CollisionSuffix(t *testing.T) {
	taken := map[string]struct{}{}
	// "a/b" and "a-b" both sanitize to .../a-b — the second must get a suffix.
	b1 := AssignBranch("epic:E1", "a/b", taken)
	taken[b1] = struct{}{}
	b2 := AssignBranch("epic:E1", "a-b", taken)
	taken[b2] = struct{}{}

	assert.Equal(t, "loom/stack/epic-E1/a-b", b1)
	assert.NotEqual(t, b1, b2, "colliding task IDs must get distinct branches")
	assert.Contains(t, b2, "loom/stack/epic-E1/a-b-", "collision must append a hash suffix")
	assert.Len(t, taken, 2)

	// No collision → readable name, no suffix.
	b3 := AssignBranch("epic:E1", "TASK-3", taken)
	assert.Equal(t, "loom/stack/epic-E1/TASK-3", b3)
}

// linear builds a simple linear chain T1->T2->...->Tn with assigned branches.
func linear(ids ...string) []StackNode {
	nodes := make([]StackNode, len(ids))
	for i, id := range ids {
		base := ""
		if i > 0 {
			base = ids[i-1]
		}
		nodes[i] = StackNode{TaskID: id, BaseTaskID: base, OutputBranch: OutputBranchName("epic:E", id)}
	}
	return nodes
}

func TestOrdered_Linear(t *testing.T) {
	got, err := Ordered(linear("T1", "T2", "T3"))
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, []string{"T1", "T2", "T3"}, []string{got[0].TaskID, got[1].TaskID, got[2].TaskID})
}

func TestOrdered_EmptyAndSingle(t *testing.T) {
	got, err := Ordered(nil)
	require.NoError(t, err)
	assert.Empty(t, got)

	got, err = Ordered(linear("only"))
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "only", got[0].TaskID)
}

func TestOrdered_Forest(t *testing.T) {
	// Two parallel linear chains off the same base: A1->A2 and B1->B2->B3.
	nodes := []StackNode{
		{TaskID: "B1"}, {TaskID: "B2", BaseTaskID: "B1"}, {TaskID: "B3", BaseTaskID: "B2"},
		{TaskID: "A1"}, {TaskID: "A2", BaseTaskID: "A1"},
	}
	ordered, err := Ordered(nodes)
	require.NoError(t, err)
	require.Len(t, ordered, 5)
	// Roots are walked in deterministic (task-ID) order; each chain stays contiguous.
	got := make([]string, len(ordered))
	for i, n := range ordered {
		got[i] = n.TaskID
	}
	assert.Equal(t, []string{"A1", "A2", "B1", "B2", "B3"}, got)

	// Branching within a chain is still rejected (a unit can't have two successors).
	branched := []StackNode{{TaskID: "A1"}, {TaskID: "A2", BaseTaskID: "A1"}, {TaskID: "A3", BaseTaskID: "A1"}}
	_, err = Ordered(branched)
	assert.ErrorIs(t, err, ErrBranching)
}

func TestOrdered_Errors(t *testing.T) {
	t.Run("self-parent", func(t *testing.T) {
		_, err := Ordered([]StackNode{{TaskID: "A", BaseTaskID: "A"}})
		assert.ErrorIs(t, err, ErrCycle)
	})
	t.Run("missing predecessor", func(t *testing.T) {
		_, err := Ordered([]StackNode{{TaskID: "B", BaseTaskID: "ghost"}, {TaskID: "A"}})
		assert.ErrorIs(t, err, ErrMissingPredecessor)
	})
	t.Run("no root", func(t *testing.T) {
		// A->B, B->A : no node with empty base.
		_, err := Ordered([]StackNode{{TaskID: "A", BaseTaskID: "B"}, {TaskID: "B", BaseTaskID: "A"}})
		assert.ErrorIs(t, err, ErrNoRoot)
	})
	t.Run("branching (multiple successors)", func(t *testing.T) {
		// A is root; both B and C base on A.
		_, err := Ordered([]StackNode{{TaskID: "A"}, {TaskID: "B", BaseTaskID: "A"}, {TaskID: "C", BaseTaskID: "A"}})
		assert.ErrorIs(t, err, ErrBranching)
	})
	t.Run("cycle below root", func(t *testing.T) {
		// A root; B->C, C->B (unreachable from A).
		_, err := Ordered([]StackNode{{TaskID: "A"}, {TaskID: "B", BaseTaskID: "C"}, {TaskID: "C", BaseTaskID: "B"}})
		assert.ErrorIs(t, err, ErrCycle)
	})
}

func TestBaseBranch(t *testing.T) {
	stack := Stack{ID: "epic:E", RootBase: "main"}
	nodes := linear("T1", "T2", "T3")
	byTask := ByTask(nodes)

	// Root unit → RootBase.
	base, err := BaseBranch(stack, byTask["T1"], byTask)
	require.NoError(t, err)
	assert.Equal(t, "main", base)

	// Non-root → predecessor's output branch.
	base, err = BaseBranch(stack, byTask["T2"], byTask)
	require.NoError(t, err)
	assert.Equal(t, OutputBranchName("epic:E", "T1"), base)
}

func TestBaseBranch_FailClosed(t *testing.T) {
	stack := Stack{ID: "epic:E", RootBase: "main"}

	// Predecessor missing → error, never RootBase.
	n := StackNode{TaskID: "T2", BaseTaskID: "T1"}
	_, err := BaseBranch(stack, n, map[string]StackNode{"T2": n})
	assert.ErrorIs(t, err, ErrMissingPredecessor)

	// Predecessor present but no assigned branch → error, never RootBase.
	byTask := map[string]StackNode{
		"T1": {TaskID: "T1"}, // OutputBranch empty
		"T2": {TaskID: "T2", BaseTaskID: "T1"},
	}
	_, err = BaseBranch(stack, byTask["T2"], byTask)
	assert.ErrorIs(t, err, ErrNoOutputBranch)
}

func TestWouldCycle(t *testing.T) {
	nodes := linear("T1", "T2", "T3")
	// Repointing T1 onto T3 closes a cycle.
	assert.Error(t, WouldCycle(nodes, "T1", "T3"))
	// Self-parent.
	assert.ErrorIs(t, WouldCycle(nodes, "T2", "T2"), ErrCycle)
	// A valid move (reorder T3 under T1) is fine on its own... but T2 still points
	// at T1 too → branching. Verify WouldCycle surfaces that.
	assert.ErrorIs(t, WouldCycle(nodes, "T3", "T1"), ErrBranching)
	// Adding a brand-new leaf is fine.
	assert.NoError(t, WouldCycle(nodes, "T4", "T3"))
}
