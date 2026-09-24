// Round-trip contract test: one behavioral suite run against both stack
// stores — LocalStore (stacks.json) and FleetDBStore over an ephemeral
// embedded fleet-db — proving callers see the same lineage semantics,
// branch names and sentinel errors whichever store they hold.
//
// Env-gated like fleetdb's TestFleetDBAwaitConformanceRoundTrip: it needs a
// fleet-db binary BUILT FROM A TREE THAT INCLUDES THE STACK API (an older
// binary 404s every stack call). Run with:
//
//	LOOM_RUN_EMBEDDED_SMOKE=1 FLEET_DB_BIN=/path/to/fleet-db \
//	  go test ./internal/stackstore/ -run Embedded -v -count=1
package stackstore_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	sl "github.com/tysonthomas9/loomcli/internal/stacklineage"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const embeddedWS = "STKEMB"

func TestEmbeddedStackStoreParity(t *testing.T) {
	if os.Getenv("LOOM_RUN_EMBEDDED_SMOKE") != "1" {
		t.Skip("set LOOM_RUN_EMBEDDED_SMOKE=1 (with a fleet-db binary that has the stack API) to run the stack store round-trip")
	}
	if diag := bootstrap.DiagnoseFleetDBBinary(); diag.Err != nil {
		t.Skipf("fleet-db binary unavailable: %v", diag.Err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	emb, err := bootstrap.StartEmbedded(ctx, t.TempDir(), slog.Default())
	require.NoError(t, err, "StartEmbedded")
	t.Cleanup(func() { _ = emb.Stop() })

	client, err := fleetdb.New(fleetdb.Config{BaseURL: emb.URL(), Actor: "stack-roundtrip"})
	require.NoError(t, err)
	_, err = client.Workspaces().Create(ctx, store.WorkspaceCreate{Key: embeddedWS, Name: embeddedWS})
	require.NoError(t, err, "create workspace")

	local := stackstore.New(t.TempDir())
	fdb := stackstore.NewFleetDB(client.Stacks())
	stores := []struct {
		name string
		s    stackstore.Store
	}{{"local", local}, {"fleetdb", fdb}}

	for _, st := range stores {
		t.Run(st.name, func(t *testing.T) { runStackStoreSuite(t, st.s) })
	}

	t.Run("output branch parity", func(t *testing.T) {
		// "T:1" and "T/1" sanitize to the same ref, so the second one takes
		// the hash-suffixed name: both stores must agree on it.
		taskIDs := []string{"PROJ-12", "T:1", "T/1", "a..b", "x.lock"}
		branches := make(map[string][]string, len(stores))
		for _, st := range stores {
			id := sl.StackID("manual:repo/parity")
			ensure(t, st.s, id)
			base := ""
			for _, task := range taskIDs {
				n, err := st.s.AddNode(ctx, embeddedWS, id, task, base, "")
				require.NoError(t, err, "%s: add %s", st.name, task)
				branches[st.name] = append(branches[st.name], n.OutputBranch)
				base = task
			}
			nodes, err := st.s.ListNodes(ctx, embeddedWS, id)
			require.NoError(t, err)
			stored := make([]string, 0, len(nodes))
			for _, n := range nodes {
				stored = append(stored, n.OutputBranch)
			}
			assert.Equal(t, branches[st.name], stored, "%s: AddNode result matches stored branch", st.name)
		}
		assert.Equal(t, branches["local"], branches["fleetdb"])
		assert.NotEqual(t, branches["local"][1], branches["local"][2], "collision resolved")
	})

	t.Run("fleetdb merged node is terminal", func(t *testing.T) {
		id := sl.StackID("epic:terminal")
		ensure(t, fdb, id)
		addChain(t, fdb, id, "T1", "T2")
		for _, state := range []sl.NodeState{sl.NodeStatePublished, sl.NodeStateMerged} {
			require.NoError(t, fdb.UpdateNode(ctx, embeddedWS, id, "T1", func(n *sl.Node) error {
				n.State = state
				return nil
			}))
		}
		err := fdb.UpdateNode(ctx, embeddedWS, id, "T1", func(n *sl.Node) error {
			n.State = sl.NodeStateClosed
			return nil
		})
		assert.ErrorIs(t, err, stackstore.ErrNodeTerminal)
		nodes, err := fdb.ListNodes(ctx, embeddedWS, id)
		require.NoError(t, err)
		assert.Equal(t, sl.NodeStateMerged, sl.ByTask(nodes)["T1"].State)

		// A move that would retarget the merged node is refused before any write.
		assert.ErrorIs(t, fdb.MoveNode(ctx, embeddedWS, id, "T1", "T2"), stackstore.ErrNodeTerminal)
		assert.Equal(t, []string{"T1", "T2"}, order(t, fdb, id))
	})
}

func ensure(t *testing.T, s stackstore.Store, id sl.StackID) {
	t.Helper()
	require.NoError(t, s.EnsureStack(context.Background(), sl.Stack{
		ID: id, WorkspaceKey: embeddedWS, RepoName: "loomcli", RootBase: "main",
	}))
}

func addChain(t *testing.T, s stackstore.Store, id sl.StackID, tasks ...string) {
	t.Helper()
	base := ""
	for _, task := range tasks {
		_, err := s.AddNode(context.Background(), embeddedWS, id, task, base, "")
		require.NoError(t, err, "add %s on %q", task, base)
		base = task
	}
}

func order(t *testing.T, s stackstore.Store, id sl.StackID) []string {
	t.Helper()
	nodes, err := s.ListNodes(context.Background(), embeddedWS, id)
	require.NoError(t, err)
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.TaskID
	}
	return out
}

func bases(t *testing.T, s stackstore.Store, id sl.StackID) map[string]string {
	t.Helper()
	nodes, err := s.ListNodes(context.Background(), embeddedWS, id)
	require.NoError(t, err)
	out := make(map[string]string, len(nodes))
	for _, n := range nodes {
		out[n.TaskID] = n.BaseTaskID
	}
	return out
}

func stackIDs(t *testing.T, s stackstore.Store) []sl.StackID {
	t.Helper()
	stacks, err := s.ListStacks(context.Background(), embeddedWS)
	require.NoError(t, err)
	out := make([]sl.StackID, 0, len(stacks))
	for _, st := range stacks {
		out = append(out, st.ID)
	}
	return out
}

// runStackStoreSuite is the shared behavioral contract for stackstore.Store.
func runStackStoreSuite(t *testing.T, s stackstore.Store) {
	ctx := context.Background()

	t.Run("stack lifecycle", func(t *testing.T) {
		id := sl.StackID("epic:crud")
		require.NoError(t, s.EnsureStack(ctx, sl.Stack{
			ID: id, WorkspaceKey: embeddedWS, RepoName: "loomcli", RootBase: "main",
			DefaultCommitMode: sl.CommitModeAgent,
		}))
		got, err := s.GetStack(ctx, embeddedWS, id)
		require.NoError(t, err)
		assert.Equal(t, id, got.ID)
		assert.Equal(t, embeddedWS, got.WorkspaceKey)
		assert.Equal(t, "loomcli", got.RepoName)
		assert.Equal(t, "main", got.RootBase)
		assert.Equal(t, sl.CommitModeAgent, got.DefaultCommitMode)
		assert.False(t, got.CreatedAt.IsZero())

		// Re-ensure updates the header; an empty mode keeps the old default.
		require.NoError(t, s.EnsureStack(ctx, sl.Stack{
			ID: id, WorkspaceKey: embeddedWS, RepoName: "loomcli2", RootBase: "develop",
		}))
		got, err = s.GetStack(ctx, embeddedWS, id)
		require.NoError(t, err)
		assert.Equal(t, "loomcli2", got.RepoName)
		assert.Equal(t, "develop", got.RootBase)
		assert.Equal(t, sl.CommitModeAgent, got.DefaultCommitMode)

		// Nodes added without a mode take the stack default.
		n, err := s.AddNode(ctx, embeddedWS, id, "T1", "", "")
		require.NoError(t, err)
		assert.Equal(t, sl.CommitModeAgent, n.CommitMode)

		assert.Contains(t, stackIDs(t, s), id)

		require.NoError(t, s.DeleteStack(ctx, embeddedWS, id))
		_, err = s.GetStack(ctx, embeddedWS, id)
		assert.ErrorIs(t, err, stackstore.ErrStackNotFound)
		assert.NotContains(t, stackIDs(t, s), id)
		assert.ErrorIs(t, s.DeleteStack(ctx, embeddedWS, id), stackstore.ErrStackNotFound)
		_, err = s.ListNodes(ctx, embeddedWS, id)
		assert.ErrorIs(t, err, stackstore.ErrStackNotFound)

		_, err = s.GetStack(ctx, embeddedWS, "epic:never")
		assert.ErrorIs(t, err, stackstore.ErrStackNotFound)
		_, err = s.AddNode(ctx, embeddedWS, "epic:never", "T1", "", "")
		assert.ErrorIs(t, err, stackstore.ErrStackNotFound)
		err = s.UpdateNode(ctx, embeddedWS, "epic:never", "T1", func(*sl.Node) error { return nil })
		assert.ErrorIs(t, err, stackstore.ErrStackNotFound)
	})

	t.Run("add nodes", func(t *testing.T) {
		id := sl.StackID("manual:repo/add")
		ensure(t, s, id)
		t1, err := s.AddNode(ctx, embeddedWS, id, "T1", "", "")
		require.NoError(t, err)
		assert.Equal(t, id, t1.StackID)
		assert.Equal(t, "T1", t1.TaskID)
		assert.Equal(t, "", t1.BaseTaskID)
		assert.Equal(t, sl.CommitModeLoom, t1.CommitMode)
		assert.Equal(t, sl.NodeStatePending, t1.State)
		assert.Equal(t, "loom/stack/manual-repo-add/T1", t1.OutputBranch)

		t2, err := s.AddNode(ctx, embeddedWS, id, "T2", "T1", sl.CommitModeSquash)
		require.NoError(t, err)
		assert.Equal(t, "T1", t2.BaseTaskID)
		assert.Equal(t, sl.CommitModeSquash, t2.CommitMode)

		_, err = s.AddNode(ctx, embeddedWS, id, "T1", "", "")
		assert.ErrorIs(t, err, stackstore.ErrNodeExists)
		_, err = s.AddNode(ctx, embeddedWS, id, "T3", "ghost", "")
		assert.ErrorIs(t, err, sl.ErrMissingPredecessor)
		_, err = s.AddNode(ctx, embeddedWS, id, "T3", "T1", "")
		assert.ErrorIs(t, err, sl.ErrBranching)

		// A second root starts a parallel chain.
		_, err = s.AddNode(ctx, embeddedWS, id, "R2", "", "")
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"T1": "", "T2": "T1", "R2": ""}, bases(t, s, id))
	})

	t.Run("set base", func(t *testing.T) {
		id := sl.StackID("epic:setbase")
		ensure(t, s, id)
		addChain(t, s, id, "T1", "T2", "T3")

		require.NoError(t, s.SetBase(ctx, embeddedWS, id, "T3", ""))
		assert.Equal(t, map[string]string{"T1": "", "T2": "T1", "T3": ""}, bases(t, s, id))
		require.NoError(t, s.SetBase(ctx, embeddedWS, id, "T3", "T2"))
		require.NoError(t, s.SetBase(ctx, embeddedWS, id, "T2", "T1"), "idempotent")
		assert.Equal(t, []string{"T1", "T2", "T3"}, order(t, s, id))

		err := s.SetBase(ctx, embeddedWS, id, "T1", "T3")
		require.Error(t, err)
		assert.True(t, errors.Is(err, sl.ErrCycle) || errors.Is(err, sl.ErrNoRoot), "cycle rejected: %v", err)
		assert.ErrorIs(t, s.SetBase(ctx, embeddedWS, id, "T2", "T2"), sl.ErrCycle)
		assert.ErrorIs(t, s.SetBase(ctx, embeddedWS, id, "T2", "ghost"), sl.ErrMissingPredecessor)
		assert.ErrorIs(t, s.SetBase(ctx, embeddedWS, id, "ghost", "T1"), stackstore.ErrNodeNotFound)
		assert.ErrorIs(t, s.SetBase(ctx, embeddedWS, id, "T3", "T1"), sl.ErrBranching)
		assert.Equal(t, []string{"T1", "T2", "T3"}, order(t, s, id), "rejected writes change nothing")
	})

	t.Run("remove node reparents", func(t *testing.T) {
		id := sl.StackID("epic:remove")
		ensure(t, s, id)
		addChain(t, s, id, "T1", "T2", "T3")

		require.NoError(t, s.RemoveNode(ctx, embeddedWS, id, "T2"))
		assert.Equal(t, map[string]string{"T1": "", "T3": "T1"}, bases(t, s, id))
		require.NoError(t, s.RemoveNode(ctx, embeddedWS, id, "T1"))
		assert.Equal(t, map[string]string{"T3": ""}, bases(t, s, id))
		assert.ErrorIs(t, s.RemoveNode(ctx, embeddedWS, id, "ghost"), stackstore.ErrNodeNotFound)
	})

	t.Run("update node publish state", func(t *testing.T) {
		id := sl.StackID("epic:update")
		ensure(t, s, id)
		addChain(t, s, id, "T1", "T2")
		published := time.Date(2026, 9, 20, 10, 30, 15, 0, time.UTC)

		require.NoError(t, s.UpdateNode(ctx, embeddedWS, id, "T2", func(n *sl.Node) error {
			n.State = sl.NodeStatePublished
			n.PRNumber = 101
			n.PRURL = "https://github.com/o/r/pull/101"
			n.OutputSHA = "0123456789abcdef0123456789abcdef01234567"
			n.LastPublishedAt = &published
			return nil
		}))
		n := sl.ByTask(mustList(t, s, id))["T2"]
		assert.Equal(t, sl.NodeStatePublished, n.State)
		assert.Equal(t, 101, n.PRNumber)
		assert.Equal(t, "https://github.com/o/r/pull/101", n.PRURL)
		assert.Equal(t, "0123456789abcdef0123456789abcdef01234567", n.OutputSHA)
		require.NotNil(t, n.LastPublishedAt)
		assert.True(t, published.Equal(*n.LastPublishedAt), "last published %v", n.LastPublishedAt)
		assert.Equal(t, "T1", n.BaseTaskID, "lineage untouched")

		// fn sees the stored values.
		require.NoError(t, s.UpdateNode(ctx, embeddedWS, id, "T2", func(n *sl.Node) error {
			assert.Equal(t, 101, n.PRNumber)
			n.State = sl.NodeStateConflicted
			return nil
		}))
		assert.Equal(t, sl.NodeStateConflicted, sl.ByTask(mustList(t, s, id))["T2"].State)

		boom := errors.New("boom")
		assert.ErrorIs(t, s.UpdateNode(ctx, embeddedWS, id, "T2", func(n *sl.Node) error {
			n.PRNumber = 999
			return boom
		}), boom)
		assert.Equal(t, 101, sl.ByTask(mustList(t, s, id))["T2"].PRNumber, "fn error writes nothing")

		assert.ErrorIs(t, s.UpdateNode(ctx, embeddedWS, id, "ghost", func(*sl.Node) error { return nil }),
			stackstore.ErrNodeNotFound)
	})

	t.Run("move node", func(t *testing.T) {
		mover, ok := s.(stackstore.Mover)
		require.True(t, ok, "%T implements Mover", s)
		id := sl.StackID("epic:move")
		ensure(t, s, id)
		addChain(t, s, id, "T1", "T2", "T3", "T4")

		require.NoError(t, mover.MoveNode(ctx, embeddedWS, id, "T4", "T1"))
		assert.Equal(t, []string{"T1", "T4", "T2", "T3"}, order(t, s, id))
		require.NoError(t, mover.MoveNode(ctx, embeddedWS, id, "T1", "T2"))
		assert.Equal(t, []string{"T4", "T2", "T1", "T3"}, order(t, s, id))
		require.NoError(t, mover.MoveNode(ctx, embeddedWS, id, "T3", "T4"))
		assert.Equal(t, []string{"T4", "T3", "T2", "T1"}, order(t, s, id))
		require.NoError(t, mover.MoveNode(ctx, embeddedWS, id, "T3", "T4"), "already in place")
		assert.Equal(t, []string{"T4", "T3", "T2", "T1"}, order(t, s, id))

		assert.ErrorIs(t, mover.MoveNode(ctx, embeddedWS, id, "T2", "T2"), sl.ErrCycle)
		assert.ErrorIs(t, mover.MoveNode(ctx, embeddedWS, id, "ghost", "T1"), stackstore.ErrNodeNotFound)
		assert.ErrorIs(t, mover.MoveNode(ctx, embeddedWS, id, "T1", "ghost"), stackstore.ErrNodeNotFound)
	})

	t.Run("concurrent updates lose nothing", func(t *testing.T) {
		id := sl.StackID("epic:concurrent")
		ensure(t, s, id)
		addChain(t, s, id, "T1")

		const workers = 8
		var (
			wg   sync.WaitGroup
			mu   sync.Mutex
			errs []error
		)
		for i := range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sha := fmt.Sprintf("abc%d", i) // fleet-db requires hex
				err := s.UpdateNode(ctx, embeddedWS, id, "T1", func(n *sl.Node) error {
					n.OutputSHA = sha
					n.PRNumber++
					return nil
				})
				if err != nil {
					mu.Lock()
					errs = append(errs, err)
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		require.Empty(t, errs)
		n := sl.ByTask(mustList(t, s, id))["T1"]
		assert.Equal(t, workers, n.PRNumber, "no lost updates")
		assert.Regexp(t, `^abc[0-7]$`, n.OutputSHA)
	})
}

func mustList(t *testing.T, s stackstore.Store, id sl.StackID) []sl.Node {
	t.Helper()
	nodes, err := s.ListNodes(context.Background(), embeddedWS, id)
	require.NoError(t, err)
	return nodes
}
