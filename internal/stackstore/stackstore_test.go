package stackstore

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sl "github.com/tysonthomas9/loomcli/internal/stacklineage"
)

func newStore(t *testing.T) *LocalStore {
	t.Helper()
	return New(t.TempDir())
}

const ws = "WS"

func seedStack(t *testing.T, s *LocalStore) {
	t.Helper()
	require.NoError(t, s.EnsureStack(context.Background(), sl.Stack{
		ID: "epic:E1", WorkspaceKey: ws, RepoName: "loomcli", RootBase: "main",
	}))
}

func TestAddNodeChainAndOrder(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seedStack(t, s)

	t1, err := s.AddNode(ctx, ws, "epic:E1", "T1", "", "")
	require.NoError(t, err)
	assert.Equal(t, sl.CommitModeLoom, t1.CommitMode) // default
	assert.Equal(t, "loom/stack/epic-E1/T1", t1.OutputBranch)
	assert.Equal(t, sl.NodeStatePending, t1.State)

	_, err = s.AddNode(ctx, ws, "epic:E1", "T2", "T1", "")
	require.NoError(t, err)
	_, err = s.AddNode(ctx, ws, "epic:E1", "T3", "T2", "")
	require.NoError(t, err)

	nodes, err := s.ListNodes(ctx, ws, "epic:E1")
	require.NoError(t, err)
	require.Len(t, nodes, 3)
	assert.Equal(t, []string{"T1", "T2", "T3"},
		[]string{nodes[0].TaskID, nodes[1].TaskID, nodes[2].TaskID})

	// Persists across a fresh store on the same dir.
	s2 := New(s.dir)
	nodes2, err := s2.ListNodes(ctx, ws, "epic:E1")
	require.NoError(t, err)
	assert.Len(t, nodes2, 3)
}

func TestAddNodeErrors(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	_, err := s.AddNode(ctx, ws, "epic:missing", "T1", "", "")
	assert.ErrorIs(t, err, ErrStackNotFound)

	seedStack(t, s)
	_, err = s.AddNode(ctx, ws, "epic:E1", "T1", "", "")
	require.NoError(t, err)
	_, err = s.AddNode(ctx, ws, "epic:E1", "T1", "", "")
	assert.ErrorIs(t, err, ErrNodeExists)

	// A second root is allowed — it starts a parallel chain off the same base.
	_, err = s.AddNode(ctx, ws, "epic:E1", "T2", "", "")
	require.NoError(t, err)
}

func TestForestParallelChains(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seedStack(t, s)
	// Chain A: A1 -> A2 ; parallel chain B: B1 -> B2, both rooted at the base.
	for _, e := range []struct{ id, base string }{
		{"A1", ""}, {"A2", "A1"}, {"B1", ""}, {"B2", "B1"},
	} {
		_, err := s.AddNode(ctx, ws, "epic:E1", e.id, e.base, "")
		require.NoError(t, err)
	}
	nodes, err := s.ListNodes(ctx, ws, "epic:E1")
	require.NoError(t, err)
	require.Len(t, nodes, 4)
	byTask := sl.ByTask(nodes)
	assert.Equal(t, "", byTask["A1"].BaseTaskID)
	assert.Equal(t, "A1", byTask["A2"].BaseTaskID)
	assert.Equal(t, "", byTask["B1"].BaseTaskID)
	assert.Equal(t, "B1", byTask["B2"].BaseTaskID)

	// Distinct branches per unit.
	assert.NotEqual(t, byTask["A2"].OutputBranch, byTask["B2"].OutputBranch)
}

func TestSetBaseRejectsCycle(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seedStack(t, s)
	for _, id := range []string{"T1", "T2", "T3"} {
		base := map[string]string{"T1": "", "T2": "T1", "T3": "T2"}[id]
		_, err := s.AddNode(ctx, ws, "epic:E1", id, base, "")
		require.NoError(t, err)
	}
	// Repoint T1 onto T3 → cycle, rejected.
	assert.Error(t, s.SetBase(ctx, ws, "epic:E1", "T1", "T3"))
	// Self-parent rejected.
	assert.ErrorIs(t, s.SetBase(ctx, ws, "epic:E1", "T2", "T2"), sl.ErrCycle)
	// Unknown base rejected.
	assert.ErrorIs(t, s.SetBase(ctx, ws, "epic:E1", "T2", "ghost"), sl.ErrMissingPredecessor)
}

func TestRemoveNodeReparentsChildren(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seedStack(t, s)
	for _, id := range []string{"T1", "T2", "T3"} {
		base := map[string]string{"T1": "", "T2": "T1", "T3": "T2"}[id]
		_, err := s.AddNode(ctx, ws, "epic:E1", id, base, "")
		require.NoError(t, err)
	}
	// Drop the middle unit; T3 must reparent onto T1.
	require.NoError(t, s.RemoveNode(ctx, ws, "epic:E1", "T2"))
	nodes, err := s.ListNodes(ctx, ws, "epic:E1")
	require.NoError(t, err)
	require.Len(t, nodes, 2)
	byTask := sl.ByTask(nodes)
	assert.Equal(t, "T1", byTask["T3"].BaseTaskID)

	// Drop the root; T3 becomes the root (base "").
	require.NoError(t, s.RemoveNode(ctx, ws, "epic:E1", "T1"))
	nodes, err = s.ListNodes(ctx, ws, "epic:E1")
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	assert.Equal(t, "", nodes[0].BaseTaskID)
}

func TestUpdateNode(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seedStack(t, s)
	_, err := s.AddNode(ctx, ws, "epic:E1", "T1", "", "")
	require.NoError(t, err)

	require.NoError(t, s.UpdateNode(ctx, ws, "epic:E1", "T1", func(n *sl.Node) error {
		n.State = sl.NodeStatePublished
		n.PRNumber = 42
		n.PRURL = "https://example/pr/42"
		return nil
	}))
	nodes, err := s.ListNodes(ctx, ws, "epic:E1")
	require.NoError(t, err)
	assert.Equal(t, sl.NodeStatePublished, nodes[0].State)
	assert.Equal(t, 42, nodes[0].PRNumber)
}

func TestConcurrentAddNode(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seedStack(t, s)
	_, err := s.AddNode(ctx, ws, "epic:E1", "root", "", "")
	require.NoError(t, err)

	// Many concurrent appends onto the same predecessor would be non-linear, so
	// instead exercise concurrent UpdateNode on the root to prove the lock holds.
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.UpdateNode(ctx, ws, "epic:E1", "root", func(n *sl.Node) error {
				n.PRNumber++
				return nil
			})
		}()
	}
	wg.Wait()
	nodes, err := s.ListNodes(ctx, ws, "epic:E1")
	require.NoError(t, err)
	assert.Equal(t, 20, nodes[0].PRNumber, "lock must serialize read-modify-write")
}
