package stackpublish

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sl "github.com/tysonthomas9/loomcli/internal/stacklineage"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
)

func TestMergePRArgsMirrorsGHFlags(t *testing.T) {
	got := mergePRArgs("o", "r", 42, MergeOptions{
		Method: MergeMethodSquash, Auto: true, Admin: true,
		MatchHeadCommit: "abc123", AuthorEmail: "dev@example.com",
		Subject: "subject", SubjectSet: true,
		Body: "body", BodySet: true,
		DeleteBranch: true,
	})
	assert.Equal(t, []string{
		"pr", "merge", "42", "--repo", "o/r",
		"--squash", "--auto", "--admin",
		"--match-head-commit", "abc123",
		"--author-email", "dev@example.com",
		"--subject", "subject",
		"--body", "body",
		"--delete-branch",
	}, got)
}

func TestMergeNextMergesDefaultNextAndPublishes(t *testing.T) {
	ctx := context.Background()
	id := sl.StackID("epic:M")
	store := stackstore.New(t.TempDir())
	require.NoError(t, store.EnsureStack(ctx, sl.Stack{ID: id, WorkspaceKey: "WS", RepoName: "r", RootBase: "main"}))
	_, err := store.AddNode(ctx, "WS", id, "T1", "", "")
	require.NoError(t, err)
	repoPath := gitRepoWithBranches(t, id, "T1")
	pr := PR{Number: 101, Head: sl.OutputBranchName(id, "T1"), Base: "main", State: "open", URL: "https://github.com/o/r/pull/101"}
	ff := &fakeForge{prs: []PR{pr}}
	rec := &Reconciler{Store: store, Forge: ff}

	report, err := rec.MergeNext(ctx, "WS", id, repoPath, "", MergeOptions{Method: MergeMethodMerge, DeleteBranch: true})
	require.NoError(t, err)

	assert.Equal(t, 101, ff.mergedNumber)
	assert.Equal(t, MergeMethodMerge, ff.mergeOpts.Method)
	assert.True(t, ff.mergeOpts.DeleteBranch)
	assert.Equal(t, "T1", report.MergedPR.TaskID)
	assert.Equal(t, 101, report.MergedPR.Number)
	assert.Contains(t, report.Publish.Merged, "T1")
	assert.Empty(t, report.NextToMerge)
	nodes, err := store.ListNodes(ctx, "WS", id)
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	assert.Equal(t, sl.NodeStateMerged, nodes[0].State)
}

func TestMergeNextRejectsExplicitTargetThatIsNotNext(t *testing.T) {
	ctx := context.Background()
	id := sl.StackID("epic:M")
	store := stackstore.New(t.TempDir())
	require.NoError(t, store.EnsureStack(ctx, sl.Stack{ID: id, WorkspaceKey: "WS", RepoName: "r", RootBase: "main"}))
	_, err := store.AddNode(ctx, "WS", id, "T1", "", "")
	require.NoError(t, err)
	_, err = store.AddNode(ctx, "WS", id, "T2", "T1", "")
	require.NoError(t, err)
	repoPath := gitRepoWithBranches(t, id, "T1", "T2")
	ff := &fakeForge{prs: []PR{
		{Number: 101, Head: sl.OutputBranchName(id, "T1"), Base: "main", State: "open"},
		{Number: 102, Head: sl.OutputBranchName(id, "T2"), Base: sl.OutputBranchName(id, "T1"), State: "open"},
	}}
	rec := &Reconciler{Store: store, Forge: ff}

	_, err = rec.MergeNext(ctx, "WS", id, repoPath, "T2", MergeOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not nextToMerge")
	assert.Zero(t, ff.mergedNumber)
}

func TestMergeNextRejectsAmbiguousDefaultTarget(t *testing.T) {
	ctx := context.Background()
	id := sl.StackID("epic:M")
	store := stackstore.New(t.TempDir())
	require.NoError(t, store.EnsureStack(ctx, sl.Stack{ID: id, WorkspaceKey: "WS", RepoName: "r", RootBase: "main"}))
	_, err := store.AddNode(ctx, "WS", id, "A1", "", "")
	require.NoError(t, err)
	_, err = store.AddNode(ctx, "WS", id, "B1", "", "")
	require.NoError(t, err)
	repoPath := gitRepoWithBranches(t, id, "A1") // target resolution only needs the repo slug and live PR map.
	ff := &fakeForge{prs: []PR{
		{Number: 101, Head: sl.OutputBranchName(id, "A1"), Base: "main", State: "open"},
		{Number: 102, Head: sl.OutputBranchName(id, "B1"), Base: "main", State: "open"},
	}}
	rec := &Reconciler{Store: store, Forge: ff}

	_, err = rec.MergeNext(ctx, "WS", id, repoPath, "", MergeOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "multiple nextToMerge")
	assert.Zero(t, ff.mergedNumber)
}

func TestMergeNextAcceptsPRURLTarget(t *testing.T) {
	ctx := context.Background()
	id := sl.StackID("epic:M")
	store := stackstore.New(t.TempDir())
	require.NoError(t, store.EnsureStack(ctx, sl.Stack{ID: id, WorkspaceKey: "WS", RepoName: "r", RootBase: "main"}))
	_, err := store.AddNode(ctx, "WS", id, "T1", "", "")
	require.NoError(t, err)
	repoPath := gitRepoWithBranches(t, id, "T1")
	ff := &fakeForge{prs: []PR{
		{Number: 101, Head: sl.OutputBranchName(id, "T1"), Base: "main", State: "open", URL: "https://github.com/o/r/pull/101"},
	}}
	rec := &Reconciler{Store: store, Forge: ff}

	_, err = rec.MergeNext(ctx, "WS", id, repoPath, "https://github.com/o/r/pull/101", MergeOptions{})
	require.NoError(t, err)
	assert.Equal(t, 101, ff.mergedNumber)
}
