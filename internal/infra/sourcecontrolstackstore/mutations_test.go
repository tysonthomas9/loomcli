package stackstore

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sl "github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
)

// Mutation-coverage gaps ported from git-town's lineage/delete/swap/set-parent
// tests (the portable, linear ones).

func seedChain(t *testing.T, s *LocalStore, ids ...string) {
	t.Helper()
	seedStack(t, s)
	for i, id := range ids {
		base := ""
		if i > 0 {
			base = ids[i-1]
		}
		_, err := s.AddStackNodeRecord(context.Background(), ws, "epic:E1", id, base, "")
		require.NoError(t, err)
	}
}

func taskOrder(t *testing.T, s *LocalStore) []string {
	t.Helper()
	nodes, err := s.ListStackNodeRecords(context.Background(), ws, "epic:E1")
	require.NoError(t, err)
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.TaskID
	}
	return out
}

// git-town: sync ancestor_shipped/multiple_ancestors_shipped — sequential removal
// of leading units cascades the root forward.
func TestRemoveNode_SequentialCascade(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seedChain(t, s, "T1", "T2", "T3", "T4")

	require.NoError(t, s.RemoveStackNodeRecord(ctx, ws, "epic:E1", "T1"))
	assert.Equal(t, []string{"T2", "T3", "T4"}, taskOrder(t, s))

	require.NoError(t, s.RemoveStackNodeRecord(ctx, ws, "epic:E1", "T2"))
	got := taskOrder(t, s)
	assert.Equal(t, []string{"T3", "T4"}, got)
	byTask := sl.ByTask(mustNodes(t, s))
	assert.Equal(t, "", byTask["T3"].BaseTaskID, "T3 is the new root")
	assert.Equal(t, "T3", byTask["T4"].BaseTaskID)
}

// git-town: lineage RemoveBranch (leaf) — removing the tail leaves the rest intact.
func TestRemoveNode_Leaf(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seedChain(t, s, "T1", "T2", "T3")
	require.NoError(t, s.RemoveStackNodeRecord(ctx, ws, "epic:E1", "T3"))
	assert.Equal(t, []string{"T1", "T2"}, taskOrder(t, s))
	byTask := sl.ByTask(mustNodes(t, s))
	assert.Equal(t, "T1", byTask["T2"].BaseTaskID, "T2 base unchanged after leaf removal")
}

// git-town: lineage RemoveBranch (not in lineage) — removing an unknown unit is rejected.
func TestRemoveNode_NonExistent(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seedChain(t, s, "T1", "T2", "T3")
	assert.ErrorIs(t, s.RemoveStackNodeRecord(ctx, ws, "epic:E1", "ghost"), ErrNodeNotFound)
	assert.Equal(t, []string{"T1", "T2", "T3"}, taskOrder(t, s), "stack unchanged")
}

// git-town: set-parent dialog/no_change — re-setting the same parent is a no-op.
func TestSetBase_Idempotent(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seedChain(t, s, "T1", "T2", "T3")
	require.NoError(t, s.SetStackNodeBaseRecord(ctx, ws, "epic:E1", "T2", "T1")) // already T1
	assert.Equal(t, []string{"T1", "T2", "T3"}, taskOrder(t, s))
}

// git-town: swap non-adjacent — splice the tail to sit just after the root.
func TestMoveNode_NonAdjacent(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seedChain(t, s, "T1", "T2", "T3", "T4")
	require.NoError(t, s.MoveStackNodeRecord(ctx, ws, "epic:E1", "T4", "T1"))
	assert.Equal(t, []string{"T1", "T4", "T2", "T3"}, taskOrder(t, s))
}

// Moving the current root forward re-roots the stack onto its old child.
func TestMoveNode_RootReRoots(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seedChain(t, s, "T1", "T2", "T3")
	require.NoError(t, s.MoveStackNodeRecord(ctx, ws, "epic:E1", "T1", "T3"))
	assert.Equal(t, []string{"T2", "T3", "T1"}, taskOrder(t, s))
	byTask := sl.ByTask(mustNodes(t, s))
	assert.Equal(t, "", byTask["T2"].BaseTaskID, "T2 is the new root")
}

func TestMoveNode_UnknownTarget(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seedChain(t, s, "T1", "T2")
	assert.ErrorIs(t, s.MoveStackNodeRecord(ctx, ws, "epic:E1", "T2", "ghost"), ErrNodeNotFound)
}

// Linear invariant: add --after an already-occupied unit would branch → rejected.
// The supported "insert in the middle" path is add-at-tail then move.
func TestAddNode_AfterOccupiedBranches(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seedChain(t, s, "T1", "T2")
	_, err := s.AddStackNodeRecord(ctx, ws, "epic:E1", "TX", "T1", "")
	assert.ErrorIs(t, err, sl.ErrBranching)

	// Insert TX between T1 and T2 via the supported add-then-move.
	_, err = s.AddStackNodeRecord(ctx, ws, "epic:E1", "TX", "T2", "") // append at tail
	require.NoError(t, err)
	require.NoError(t, s.MoveStackNodeRecord(ctx, ws, "epic:E1", "TX", "T1"))
	assert.Equal(t, []string{"T1", "TX", "T2"}, taskOrder(t, s))
}

func mustNodes(t *testing.T, s *LocalStore) []sl.StackNode {
	t.Helper()
	nodes, err := s.ListStackNodeRecords(context.Background(), ws, "epic:E1")
	require.NoError(t, err)
	return nodes
}
