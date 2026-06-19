package stacklineage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Ported from git-town lineage_test.go (Ancestor/Ancestors/Root over deep chains)
// and spr's deeper-stack flows. Our model exposes the chain via Ordered + the
// per-node base via BaseBranch, so we assert those over a 4-deep stack.

func TestOrdered_DeepChainAndBases(t *testing.T) {
	nodes := linear("T1", "T2", "T3", "T4")
	ordered, err := Ordered(nodes)
	require.NoError(t, err)
	require.Len(t, ordered, 4)
	assert.Equal(t, []string{"T1", "T2", "T3", "T4"},
		[]string{ordered[0].TaskID, ordered[1].TaskID, ordered[2].TaskID, ordered[3].TaskID})

	stack := Stack{ID: "epic:E", RootBase: "main"}
	byTask := ByTask(ordered)

	// Each unit bases on its immediate predecessor; the root bases on RootBase.
	root, err := BaseBranch(stack, byTask["T1"], byTask)
	require.NoError(t, err)
	assert.Equal(t, "main", root)
	for _, pair := range [][2]string{{"T2", "T1"}, {"T3", "T2"}, {"T4", "T3"}} {
		base, berr := BaseBranch(stack, byTask[pair[0]], byTask)
		require.NoError(t, berr)
		assert.Equalf(t, OutputBranchName("epic:E", pair[1]), base, "%s should base on %s", pair[0], pair[1])
	}
}

func TestNextToMerge(t *testing.T) {
	ordered, err := Ordered(linear("T1", "T2", "T3"))
	require.NoError(t, err)

	id, ok := NextToMerge(ordered)
	assert.True(t, ok)
	assert.Equal(t, "T1", id, "the bottom unit is next to merge")

	ordered[0].State = NodeStateMerged
	id, ok = NextToMerge(ordered)
	assert.True(t, ok)
	assert.Equal(t, "T2", id, "skips the merged bottom unit")

	for i := range ordered {
		ordered[i].State = NodeStateMerged
	}
	_, ok = NextToMerge(ordered)
	assert.False(t, ok, "nothing left to merge")
}

func TestWouldCycle_DeepChain(t *testing.T) {
	nodes := linear("T1", "T2", "T3", "T4")
	// Repointing the root onto the leaf closes a 4-node cycle.
	assert.Error(t, WouldCycle(nodes, "T1", "T4"))
	// Repointing the leaf onto an interior node is a valid reorder on its own
	// (no cycle), but T4's old parent slot is unaffected here so it must branch
	// only if it duplicates a child — moving T4 under T2 makes T2 have children
	// T3 and T4 → branching.
	assert.ErrorIs(t, WouldCycle(nodes, "T4", "T2"), ErrBranching)
	// Appending a fresh leaf after the current tail is always fine.
	assert.NoError(t, WouldCycle(nodes, "T5", "T4"))
}
