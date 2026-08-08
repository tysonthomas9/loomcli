package stackpublish

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	stackstore "github.com/tysonthomas9/loomcli/internal/infra/sourcecontrolstackstore"
	sl "github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
)

// repoWithOwnedCommit builds an offline repo (dummy origin) whose stack branch
// for taskID has a single owned commit with the given subject, and returns the
// repo path plus a store with a one-node stack rooted on main.
func repoWithOwnedCommit(t *testing.T, ctx context.Context, id sl.StackID, taskID, subject string) (string, sl.StackLifecycleStore) {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "t@t")
	git(t, dir, "config", "user.name", "t")
	git(t, dir, "remote", "add", "origin", "https://github.com/o/r.git")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README"), []byte("x"), 0o644))
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "init")
	git(t, dir, "branch", "-M", "main")

	branch := sl.OutputBranchName(id, taskID)
	git(t, dir, "checkout", "-q", "-B", branch, "main")
	require.NoError(t, os.WriteFile(filepath.Join(dir, taskID+".txt"), []byte(taskID), 0o644))
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", subject)
	git(t, dir, "checkout", "-q", "main")

	store := stackstore.New(t.TempDir())
	require.NoError(t, store.EnsureStackRecord(ctx, sl.Stack{ID: id, WorkspaceKey: "WS", Repository: "r", RootBase: "main"}))
	_, err := store.AddStackNodeRecord(ctx, "WS", id, taskID, "", sl.CommitModeLoom)
	require.NoError(t, err)
	return dir, store
}

// With no issue metadata injected, a newly created stacked PR's title/body are
// derived from the owned commit (subject → title, with the "(TASK)" suffix
// stripped) and the body carries the skeleton — not the legacy one-liner.
func TestPublishCreate_FallsBackToCommitSubject(t *testing.T) {
	ctx := context.Background()
	id := sl.StackID("epic:E")
	dir, store := repoWithOwnedCommit(t, ctx, id, "T1", "Add merge command (T1)")

	forge := &fakeForge{createPR: PR{Number: 7, URL: "https://github.com/o/r/pull/7", Head: sl.OutputBranchName(id, "T1"), Base: "main", State: "open"}}
	rec := &Reconciler{Stacks: mustStackLifecycle(t, store), Forge: forge}

	rep, err := rec.Publish(ctx, "WS", id, dir, Options{})
	require.NoError(t, err)
	require.Equal(t, []string{"T1"}, rep.Created)

	assert.Equal(t, "Add merge command", forge.createdTitle)
	assert.Contains(t, forge.createdBody, "## Summary")
	assert.Contains(t, forge.createdBody, "## Owned change")
	assert.Contains(t, forge.createdBody, "`T1`")
	// The create body must not carry the legacy one-liner or the managed markers.
	assert.NotContains(t, forge.createdBody, "managed by Loom")
	assert.NotContains(t, forge.createdBody, "loom-stack:start")
}

// Injected issue metadata wins over the commit subject for both title and body.
func TestPublishCreate_PrefersIssueMetadata(t *testing.T) {
	ctx := context.Background()
	id := sl.StackID("epic:E")
	dir, store := repoWithOwnedCommit(t, ctx, id, "T1", "Add merge command (T1)")

	forge := &fakeForge{createPR: PR{Number: 7, URL: "https://github.com/o/r/pull/7", Head: sl.OutputBranchName(id, "T1"), Base: "main", State: "open"}}
	rec := &Reconciler{Stacks: mustStackLifecycle(t, store), Forge: forge}

	opts := Options{PRMetaFor: func(_ context.Context, taskID string) (PRMeta, bool) {
		assert.Equal(t, "T1", taskID)
		return PRMeta{Title: "Implement loom stack merge", Summary: "Adds the merge command.", AcceptanceCriteria: "- merges next-to-merge PR"}, true
	}}
	_, err := rec.Publish(ctx, "WS", id, dir, opts)
	require.NoError(t, err)

	assert.Equal(t, "Implement loom stack merge", forge.createdTitle)
	assert.Contains(t, forge.createdBody, "Adds the merge command.")
	assert.Contains(t, forge.createdBody, "## Acceptance criteria")
	assert.Contains(t, forge.createdBody, "- merges next-to-merge PR")
}
