package stackstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sl "github.com/tysonthomas9/loomcli/internal/stacklineage"
)

var fixedNow = func() time.Time { return time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC) }

// seedLocal builds a local stacks.json with epic:E1 = T1 (published PR 11) <- T2 (pending).
func seedLocal(t *testing.T) *LocalStore {
	t.Helper()
	ctx := context.Background()
	src := newStore(t)
	seedStack(t, src)
	_, err := src.AddNode(ctx, ws, "epic:E1", "T1", "", "")
	require.NoError(t, err)
	_, err = src.AddNode(ctx, ws, "epic:E1", "T2", "T1", sl.CommitModeAgent)
	require.NoError(t, err)
	published := time.Date(2026, 9, 1, 8, 30, 0, 0, time.UTC)
	require.NoError(t, src.UpdateNode(ctx, ws, "epic:E1", "T1", func(n *sl.Node) error {
		n.State = sl.NodeStatePublished
		n.PRNumber = 11
		n.PRURL = "https://github.com/o/r/pull/11"
		n.OutputSHA = "abc1234"
		n.LastPublishedAt = &published
		return nil
	}))
	return src
}

func migrate(t *testing.T, src *LocalStore, dst Store, dryRun bool) (*MigrateReport, error) {
	t.Helper()
	return Migrate(context.Background(), src, dst, MigrateOptions{Workspace: ws, DryRun: dryRun, Now: fixedNow})
}

func backups(t *testing.T, src *LocalStore) []string {
	t.Helper()
	m, err := filepath.Glob(src.Path() + ".pre-migrate-*.bak")
	require.NoError(t, err)
	return m
}

func TestMigrateImportsLineageAndPublishState(t *testing.T) {
	ctx := context.Background()
	src := seedLocal(t)
	before, err := os.ReadFile(src.Path())
	require.NoError(t, err)
	dst := newStore(t)

	rep, err := migrate(t, src, dst, false)
	require.NoError(t, err)
	require.Len(t, rep.Stacks, 1)
	assert.Equal(t, MigrateCreate, rep.Stacks[0].Action)
	assert.Equal(t, []string{"T1", "T2"}, rep.Stacks[0].AddNodes)
	assert.True(t, rep.Applied)

	stack, err := dst.GetStack(ctx, ws, "epic:E1")
	require.NoError(t, err)
	assert.Equal(t, "main", stack.RootBase)
	assert.Equal(t, "loomcli", stack.RepoName)

	srcNodes, err := src.ListNodes(ctx, ws, "epic:E1")
	require.NoError(t, err)
	dstNodes, err := dst.ListNodes(ctx, ws, "epic:E1")
	require.NoError(t, err)
	require.Len(t, dstNodes, 2)
	for i := range srcNodes {
		assert.Empty(t, nodeDiffs(srcNodes[i], dstNodes[i]), srcNodes[i].TaskID)
	}
	assert.Equal(t, sl.CommitModeAgent, dstNodes[1].CommitMode)

	// Backup is a byte-for-byte copy and the source file is untouched.
	require.Equal(t, rep.Backup, src.Path()+".pre-migrate-20260924T120000Z.bak")
	bak, err := os.ReadFile(rep.Backup)
	require.NoError(t, err)
	assert.Equal(t, before, bak)
	after, err := os.ReadFile(src.Path())
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestMigrateRerunIsIdempotent(t *testing.T) {
	src := seedLocal(t)
	dst := newStore(t)
	_, err := migrate(t, src, dst, false)
	require.NoError(t, err)
	for _, b := range backups(t, src) {
		require.NoError(t, os.Remove(b))
	}
	snapshot, err := os.ReadFile(dst.Path())
	require.NoError(t, err)

	rep, err := migrate(t, src, dst, false)
	require.NoError(t, err)
	assert.Equal(t, MigrateUnchanged, rep.Stacks[0].Action)
	assert.False(t, rep.Applied)
	assert.Empty(t, rep.Backup)
	assert.Empty(t, backups(t, src), "no-op re-run must not write a backup")
	again, err := os.ReadFile(dst.Path())
	require.NoError(t, err)
	assert.Equal(t, snapshot, again, "no-op re-run must not write the destination")
}

func TestMigrateResumesPartialImport(t *testing.T) {
	ctx := context.Background()
	src := seedLocal(t)
	dst := newStore(t)
	// Simulate a run that died after the stack header and T1 landed.
	seedStack(t, dst)
	_, err := dst.AddNode(ctx, ws, "epic:E1", "T1", "", "")
	require.NoError(t, err)
	srcT1 := nodeByTask(t, src, "T1")
	require.NoError(t, dst.UpdateNode(ctx, ws, "epic:E1", "T1", func(n *sl.Node) error {
		n.State, n.PRNumber, n.PRURL, n.OutputSHA, n.LastPublishedAt = srcT1.State, srcT1.PRNumber, srcT1.PRURL, srcT1.OutputSHA, srcT1.LastPublishedAt
		return nil
	}))

	rep, err := migrate(t, src, dst, false)
	require.NoError(t, err)
	assert.Equal(t, MigrateExtend, rep.Stacks[0].Action)
	assert.Equal(t, []string{"T2"}, rep.Stacks[0].AddNodes)
	nodes, err := dst.ListNodes(ctx, ws, "epic:E1")
	require.NoError(t, err)
	require.Len(t, nodes, 2)
	assert.Equal(t, "T1", nodes[1].BaseTaskID)
}

func TestMigrateRefusesConflictingPublishState(t *testing.T) {
	ctx := context.Background()
	src := seedLocal(t)
	// A second, clean local stack must not be imported either: refusal is all-or-nothing.
	require.NoError(t, src.EnsureStack(ctx, sl.Stack{ID: "manual:m", WorkspaceKey: ws, RepoName: "loomcli", RootBase: "main"}))
	dst := newStore(t)
	seedStack(t, dst)
	_, err := dst.AddNode(ctx, ws, "epic:E1", "T1", "", "")
	require.NoError(t, err)
	require.NoError(t, dst.UpdateNode(ctx, ws, "epic:E1", "T1", func(n *sl.Node) error {
		n.State, n.PRNumber = sl.NodeStatePublished, 99
		return nil
	}))
	snapshot, err := os.ReadFile(dst.Path())
	require.NoError(t, err)

	rep, err := migrate(t, src, dst, false)
	require.ErrorIs(t, err, ErrMigrateConflict)
	require.True(t, rep.Conflicted())
	byID := map[sl.StackID]MigrateStackPlan{}
	for _, p := range rep.Stacks {
		byID[p.StackID] = p
	}
	assert.Equal(t, MigrateConflict, byID["epic:E1"].Action)
	require.NotEmpty(t, byID["epic:E1"].Conflicts)
	assert.Contains(t, byID["epic:E1"].Conflicts[0], "prNumber local=11 destination=99")
	assert.Equal(t, MigrateCreate, byID["manual:m"].Action)

	assert.False(t, rep.Applied)
	assert.Empty(t, backups(t, src))
	after, err := os.ReadFile(dst.Path())
	require.NoError(t, err)
	assert.Equal(t, snapshot, after, "refused migration must not write the destination")
}

func TestMigrateRefusesHeaderMismatch(t *testing.T) {
	src := seedLocal(t)
	dst := newStore(t)
	require.NoError(t, dst.EnsureStack(context.Background(), sl.Stack{ID: "epic:E1", WorkspaceKey: ws, RepoName: "loomcli", RootBase: "v5"}))

	rep, err := migrate(t, src, dst, false)
	require.ErrorIs(t, err, ErrMigrateConflict)
	assert.Contains(t, rep.Stacks[0].Conflicts, `root base differs: local "main", destination "v5"`)
}

func TestMigrateRefusesTaskAlreadyInAnotherDestinationStack(t *testing.T) {
	ctx := context.Background()
	src := seedLocal(t)
	dst := newStore(t)
	require.NoError(t, dst.EnsureStack(ctx, sl.Stack{ID: "task:T2", WorkspaceKey: ws, RepoName: "loomcli", RootBase: "main"}))
	_, err := dst.AddNode(ctx, ws, "task:T2", "T2", "", "")
	require.NoError(t, err)

	rep, err := migrate(t, src, dst, false)
	require.ErrorIs(t, err, ErrMigrateConflict)
	assert.Contains(t, rep.Stacks[0].Conflicts, "task T2 is already in destination stack task:T2")
	_, err = dst.GetStack(ctx, ws, "epic:E1")
	require.ErrorIs(t, err, ErrStackNotFound)
}

func TestMigrateRefusesBranchingMergedLineage(t *testing.T) {
	ctx := context.Background()
	src := seedLocal(t)
	dst := newStore(t)
	seedStack(t, dst)
	_, err := dst.AddNode(ctx, ws, "epic:E1", "T1", "", "")
	require.NoError(t, err)
	srcT1 := nodeByTask(t, src, "T1")
	require.NoError(t, dst.UpdateNode(ctx, ws, "epic:E1", "T1", func(n *sl.Node) error {
		n.State, n.PRNumber, n.PRURL, n.OutputSHA, n.LastPublishedAt = srcT1.State, srcT1.PRNumber, srcT1.PRURL, srcT1.OutputSHA, srcT1.LastPublishedAt
		return nil
	}))
	// Destination already chained a different task onto T1; importing T2 onto T1 would branch.
	_, err = dst.AddNode(ctx, ws, "epic:E1", "X9", "T1", "")
	require.NoError(t, err)

	rep, err := migrate(t, src, dst, false)
	require.ErrorIs(t, err, ErrMigrateConflict)
	require.Len(t, rep.Stacks[0].Conflicts, 1)
	assert.Contains(t, rep.Stacks[0].Conflicts[0], "merged lineage with destination is invalid")
}

func TestMigrateCorruptLocalFile(t *testing.T) {
	src := newStore(t)
	require.NoError(t, os.WriteFile(src.Path(), []byte("{not json"), 0o600))
	dst := newStore(t)

	_, err := migrate(t, src, dst, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse")
	assert.NoFileExists(t, dst.Path())
	assert.Empty(t, backups(t, src))
}

func TestMigrateUnreadableLocalFile(t *testing.T) {
	src := newStore(t)
	// A directory where the file should be fails every read, regardless of uid.
	require.NoError(t, os.Mkdir(src.Path(), 0o700))
	dst := newStore(t)

	_, err := migrate(t, src, dst, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read")
	assert.NoFileExists(t, dst.Path())
}

func TestMigrateMissingLocalFileIsNoop(t *testing.T) {
	src := newStore(t)
	dst := newStore(t)
	rep, err := migrate(t, src, dst, false)
	require.NoError(t, err)
	assert.True(t, rep.SourceMissing)
	assert.Empty(t, rep.Stacks)
	assert.NoFileExists(t, dst.Path())
}

func TestMigrateDryRunWritesNothing(t *testing.T) {
	src := seedLocal(t)
	dst := newStore(t)
	rep, err := migrate(t, src, dst, true)
	require.NoError(t, err)
	assert.Equal(t, MigrateCreate, rep.Stacks[0].Action)
	assert.False(t, rep.Applied)
	assert.NoFileExists(t, dst.Path())
	assert.Empty(t, backups(t, src))
}

func TestMigrateRefusesSameStore(t *testing.T) {
	src := seedLocal(t)
	_, err := migrate(t, src, New(src.dir), false)
	require.ErrorIs(t, err, ErrMigrateSameStore)
}

func TestMigrateReportsOtherWorkspaces(t *testing.T) {
	src := seedLocal(t)
	require.NoError(t, src.EnsureStack(context.Background(), sl.Stack{ID: "epic:X", WorkspaceKey: "OTHER", RepoName: "r", RootBase: "main"}))
	dst := newStore(t)
	rep, err := migrate(t, src, dst, true)
	require.NoError(t, err)
	assert.Equal(t, []string{"OTHER"}, rep.OtherWorkspaces)
	require.Len(t, rep.Stacks, 1)
	assert.Equal(t, sl.StackID("epic:E1"), rep.Stacks[0].StackID)
}

func nodeByTask(t *testing.T, s *LocalStore, taskID string) sl.Node {
	t.Helper()
	nodes, err := s.ListNodes(context.Background(), ws, "epic:E1")
	require.NoError(t, err)
	for _, n := range nodes {
		if n.TaskID == taskID {
			return n
		}
	}
	t.Fatalf("node %s not found", taskID)
	return sl.Node{}
}

// failingUpdate wraps a Store so UpdateNode always fails.
type failingUpdate struct{ Store }

func (failingUpdate) UpdateNode(context.Context, string, sl.StackID, string, func(*sl.Node) error) error {
	return errors.New("boom")
}

func TestMigrateRollsBackHalfImportedNode(t *testing.T) {
	ctx := context.Background()
	src := seedLocal(t)
	dst := newStore(t)

	_, err := migrate(t, src, failingUpdate{dst}, false)
	require.ErrorContains(t, err, "boom")
	nodes, err := dst.ListNodes(ctx, ws, "epic:E1")
	require.NoError(t, err)
	assert.Empty(t, nodes, "node whose publish state failed to copy must be removed")

	// A clean re-run resumes instead of reporting a conflict.
	rep, err := migrate(t, src, dst, false)
	require.NoError(t, err)
	assert.Equal(t, MigrateExtend, rep.Stacks[0].Action)
	assert.Equal(t, []string{"T1", "T2"}, rep.Stacks[0].AddNodes)
}

func TestMigrateRefusesTaskDuplicatedAcrossLocalStacks(t *testing.T) {
	ctx := context.Background()
	src := seedLocal(t)
	require.NoError(t, src.EnsureStack(ctx, sl.Stack{ID: "manual:dup", WorkspaceKey: ws, RepoName: "loomcli", RootBase: "main"}))
	_, err := src.AddNode(ctx, ws, "manual:dup", "T1", "", "")
	require.NoError(t, err)
	dst := newStore(t)

	rep, err := migrate(t, src, dst, false)
	require.ErrorIs(t, err, ErrMigrateConflict)
	assert.Contains(t, rep.Stacks[1].Conflicts, "task T1 is already in destination stack epic:E1")
	assert.NoFileExists(t, dst.Path())
}
