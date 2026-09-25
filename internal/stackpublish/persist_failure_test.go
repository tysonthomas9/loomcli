package stackpublish

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sl "github.com/tysonthomas9/loomcli/internal/stacklineage"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
)

// Every phase-4 path that follows a forge mutation (or forge truth) must fail
// closed when the store write fails: the error carries ErrStatePersist plus the
// store's cause and recovery guidance, the node keeps its prior state, and the
// report never claims the unit as done.
func TestPublishFailsClosedWhenStateWriteFails(t *testing.T) {
	id := sl.StackID("epic:E")
	head := sl.OutputBranchName(id, "A")
	url := "https://github.com/o/r/pull/42"

	cases := []struct {
		name     string
		prs      []PR
		empty    bool // branch A adds no commits over main
		phase    string
		reported func(*Report) []string
	}{
		{
			name:     "create",
			phase:    "phase4 mark published A",
			reported: func(r *Report) []string { return r.Created },
		},
		{
			name:     "reparent",
			prs:      []PR{{Number: 42, URL: url, Head: head, Base: "other", State: "open"}},
			phase:    "phase4 mark published A",
			reported: func(r *Report) []string { return r.Reparented },
		},
		{
			name:     "skip",
			prs:      []PR{{Number: 42, URL: url, Head: head, Base: "main", State: "open"}},
			phase:    "phase4 mark published A",
			reported: func(r *Report) []string { return r.Skipped },
		},
		{
			name:     "merged",
			prs:      []PR{{Number: 42, URL: url, Head: head, Base: "main", State: "closed", Merged: true}},
			phase:    "phase4 mark merged A",
			reported: func(r *Report) []string { return r.Merged },
		},
		{
			name:     "empty",
			prs:      []PR{{Number: 42, URL: url, Head: head, Base: "main", State: "open"}},
			empty:    true,
			phase:    "phase4 mark empty A",
			reported: func(r *Report) []string { return r.Empty },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			repoPath := gitRepoWithBranches(t, id, "A")
			if tc.empty {
				out, err := exec.Command("git", "-C", repoPath, "branch", "-f", head, "main").CombinedOutput() //nolint:norawexec
				require.NoErrorf(t, err, "reset branch: %s", out)
			}
			store := stackstore.New(t.TempDir())
			require.NoError(t, store.EnsureStack(ctx, sl.Stack{ID: id, WorkspaceKey: "WS", RepoName: "r", RootBase: "main"}))
			_, err := store.AddNode(ctx, "WS", id, "A", "", sl.CommitModeLoom)
			require.NoError(t, err)

			persistErr := errors.New("fleetdb write unavailable")
			forge := &fakeForge{prs: tc.prs, createPR: PR{Number: 42, URL: url, Head: head, Base: "main", State: "open"}}
			rec := &Reconciler{Store: updateFailStore{Store: store, err: persistErr}, Forge: forge}

			rep, err := rec.Publish(ctx, "WS", id, repoPath, Options{})
			require.Error(t, err)
			require.ErrorIs(t, err, ErrStatePersist)
			require.ErrorIs(t, err, persistErr)
			assert.Contains(t, err.Error(), tc.phase)
			assert.Contains(t, err.Error(), "loom stack publish epic:E")
			require.NotNil(t, rep)
			assert.NotContains(t, tc.reported(rep), "A", "a unit whose state was not persisted must not be reported as done")
			assert.False(t, forge.bodyUpdated, "publish must stop before phase 5 once state persistence fails")

			nodes, err := store.ListNodes(ctx, "WS", id)
			require.NoError(t, err)
			require.Len(t, nodes, 1)
			assert.Equal(t, sl.NodeStatePending, nodes[0].State, "node state must be unchanged on persistence failure")
			assert.Zero(t, nodes[0].PRNumber)
		})
	}
}

// Once the store recovers, re-running publish reconciles node state from forge
// truth without creating a duplicate PR — the recovery path the error names.
func TestPublishRerunAfterPersistFailureReconciles(t *testing.T) {
	ctx := context.Background()
	id := sl.StackID("epic:E")
	head := sl.OutputBranchName(id, "A")
	repoPath := gitRepoWithBranches(t, id, "A")
	store := stackstore.New(t.TempDir())
	require.NoError(t, store.EnsureStack(ctx, sl.Stack{ID: id, WorkspaceKey: "WS", RepoName: "r", RootBase: "main"}))
	_, err := store.AddNode(ctx, "WS", id, "A", "", sl.CommitModeLoom)
	require.NoError(t, err)

	created := PR{Number: 42, URL: "https://github.com/o/r/pull/42", Head: head, Base: "main", State: "open"}
	forge := &fakeForge{createPR: created}
	_, err = (&Reconciler{Store: updateFailStore{Store: store, err: errors.New("down")}, Forge: forge}).Publish(ctx, "WS", id, repoPath, Options{})
	require.ErrorIs(t, err, ErrStatePersist)

	// GitHub now holds the PR; the healthy store re-run must adopt it (skip).
	forge = &fakeForge{prs: []PR{created}}
	rep, err := (&Reconciler{Store: store, Forge: forge}).Publish(ctx, "WS", id, repoPath, Options{})
	require.NoError(t, err)
	assert.Equal(t, []string{"A"}, rep.Skipped)
	assert.Empty(t, rep.Created)

	nodes, err := store.ListNodes(ctx, "WS", id)
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	assert.Equal(t, sl.NodeStatePublished, nodes[0].State)
	assert.Equal(t, 42, nodes[0].PRNumber)
}
