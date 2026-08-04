package stackpublish

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	sl "github.com/tysonthomas9/loomcli/internal/stacklineage"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
)

func TestReparentPRNumbersAndConflicts(t *testing.T) {
	plan := []action{
		{Kind: actReparent, PR: &PR{Number: 11}},
		{Kind: actSkip, PR: &PR{Number: 12}},
		{Kind: actReparent, PR: &PR{Number: 13}},
		{Kind: actCreate},
		{Kind: actClose, PR: &PR{Number: 14}},
	}
	assert.ElementsMatch(t, []int{11, 13}, reparentPRNumbers(plan))
	assert.ElementsMatch(t, []int{13}, queuedConflicts([]int{11, 13}, map[int]bool{13: true, 99: true}))
	assert.Empty(t, queuedConflicts([]int{11}, map[int]bool{13: true}))
}

// fakeForge records whether any mutating call happened.
type fakeForge struct {
	prs          []PR
	createPR     PR
	queued       map[int]bool
	statuses     map[string]PRStatus
	mutated      bool
	queueChecked bool
	bodyUpdated  bool
	createdTitle string // title passed to the most recent CreatePR
	createdBody  string // body passed to the most recent CreatePR
}

func (f *fakeForge) PRStatuses(context.Context, string, string, string) (map[string]PRStatus, error) {
	return f.statuses, nil
}
func (f *fakeForge) UpdatePRBody(context.Context, string, string, int, string) error {
	f.mutated = true
	f.bodyUpdated = true
	return nil
}

func (f *fakeForge) ListStackPRs(context.Context, string, string, string) ([]PR, error) {
	return f.prs, nil
}
func (f *fakeForge) CreatePR(_ context.Context, _, _, _, _, title, body string) (PR, error) {
	f.mutated = true
	f.createdTitle = title
	f.createdBody = body
	if f.createPR.Number != 0 || f.createPR.URL != "" {
		return f.createPR, nil
	}
	return PR{}, nil
}
func (f *fakeForge) UpdatePRBase(context.Context, string, string, int, string) error {
	f.mutated = true
	return nil
}
func (f *fakeForge) ClosePR(context.Context, string, string, int, string) error {
	f.mutated = true
	return nil
}
func (f *fakeForge) PushBranches(context.Context, string, []BranchPush) error {
	f.mutated = true
	return nil
}
func (f *fakeForge) QueuedPRNumbers(context.Context, string, string) (map[int]bool, error) {
	f.queueChecked = true
	return f.queued, nil
}

type updateFailLifecycle struct {
	sourcecontrol.StackLifecycle
	err error
}

func (s updateFailLifecycle) RecordStackNodePublication(context.Context, sourcecontrol.RecordStackNodePublicationCommand) error {
	return s.err
}

func gitRepoWithBranches(t *testing.T, id sl.StackID, tasks ...string) string {
	t.Helper()
	dir := t.TempDir()
	rg := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput() //nolint:norawexec
		require.NoErrorf(t, err, "git %v: %s", args, out)
	}
	rg("init", "-q")
	rg("config", "user.email", "t@t")
	rg("config", "user.name", "t")
	rg("remote", "add", "origin", "https://github.com/o/r.git")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README"), []byte("x"), 0o644))
	rg("add", "-A")
	rg("commit", "-q", "-m", "init")
	rg("branch", "-M", "main")
	for i, task := range tasks {
		base := "main"
		if i > 0 {
			base = sl.OutputBranchName(id, tasks[i-1])
		}
		rg("checkout", "-q", "-B", sl.OutputBranchName(id, task), base)
		require.NoError(t, os.WriteFile(filepath.Join(dir, task+".txt"), []byte(task), 0o644))
		rg("add", "-A")
		rg("commit", "-q", "-m", task)
	}
	rg("checkout", "-q", "main")
	return dir
}

func TestPublishReturnsErrorWhenMarkPublishedFails(t *testing.T) {
	ctx := context.Background()
	id := sl.StackID("epic:E")
	repoPath := gitRepoWithBranches(t, id, "A")
	store := stackstore.New(t.TempDir())
	require.NoError(t, store.EnsureStack(ctx, sl.Stack{ID: id, WorkspaceKey: "WS", RepoName: "r", RootBase: "main"}))
	_, err := store.AddNode(ctx, "WS", id, "A", "", sl.CommitModeLoom)
	require.NoError(t, err)

	persistErr := errors.New("persist published state")
	forge := &fakeForge{createPR: PR{Number: 42, URL: "https://github.com/o/r/pull/42", Head: sl.OutputBranchName(id, "A"), Base: "main", State: "open"}}
	rec := &Reconciler{
		Stacks: updateFailLifecycle{StackLifecycle: mustStackLifecycle(t, store), err: persistErr}, Forge: forge,
	}

	_, err = rec.Publish(ctx, "WS", id, repoPath, Options{})
	require.ErrorIs(t, err, persistErr)
	assert.Contains(t, err.Error(), "phase4 mark published A")
	assert.True(t, forge.mutated, "GitHub mutation should have happened before the local persistence failure")
}

// The pre-flight must abort a reorder BEFORE any GitHub mutation when a
// reparent-target PR is in the merge queue.
func TestPublish_MergeQueuePreflightAborts(t *testing.T) {
	ctx := context.Background()
	id := sl.StackID("epic:Q")
	store := stackstore.New(t.TempDir())
	require.NoError(t, store.EnsureStack(ctx, sl.Stack{ID: id, WorkspaceKey: "WS", RepoName: "r", RootBase: "main"}))
	_, err := store.AddNode(ctx, "WS", id, "T1", "", "")
	require.NoError(t, err)
	_, err = store.AddNode(ctx, "WS", id, "T2", "T1", "")
	require.NoError(t, err)

	repoPath := gitRepoWithBranches(t, id, "T1", "T2")
	ff := &fakeForge{
		prs: []PR{
			{Number: 101, Head: sl.OutputBranchName(id, "T1"), Base: "main", State: "open"},
			{Number: 102, Head: sl.OutputBranchName(id, "T2"), Base: "main", State: "open"}, // wrong base → reparent
		},
		queued: map[int]bool{102: true}, // the reparent target is queued
	}
	rec := &Reconciler{Stacks: mustStackLifecycle(t, store), Forge: ff}

	_, err = rec.Publish(ctx, "WS", id, repoPath, Options{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "merge queue")
	assert.True(t, ff.queueChecked, "pre-flight must query the merge queue")
	assert.False(t, ff.mutated, "no GitHub mutation may occur when a reparent target is queued")
}

// Dry-run mutates nothing, so it must not be blocked by the queue pre-flight.
func TestPublish_DryRunSkipsQueueCheck(t *testing.T) {
	ctx := context.Background()
	id := sl.StackID("epic:Q")
	store := stackstore.New(t.TempDir())
	require.NoError(t, store.EnsureStack(ctx, sl.Stack{ID: id, WorkspaceKey: "WS", RepoName: "r", RootBase: "main"}))
	_, err := store.AddNode(ctx, "WS", id, "T1", "", "")
	require.NoError(t, err)
	_, err = store.AddNode(ctx, "WS", id, "T2", "T1", "")
	require.NoError(t, err)
	repoPath := gitRepoWithBranches(t, id, "T1", "T2")
	ff := &fakeForge{
		prs: []PR{
			{Number: 101, Head: sl.OutputBranchName(id, "T1"), Base: "main", State: "open"},
			{Number: 102, Head: sl.OutputBranchName(id, "T2"), Base: "main", State: "open"},
		},
		queued: map[int]bool{102: true},
	}
	rec := &Reconciler{Stacks: mustStackLifecycle(t, store), Forge: ff}
	rep, err := rec.Publish(ctx, "WS", id, repoPath, Options{DryRun: true})
	require.NoError(t, err)
	assert.True(t, rep.DryRun)
	assert.False(t, ff.queueChecked, "dry-run does not query the merge queue")
	assert.False(t, ff.mutated)
}

func TestQueuedPRNumbers_GraphQLParse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/graphql", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"repository":{"pullRequests":{"nodes":[`+
			`{"number":1,"mergeQueueEntry":{"id":"MQ_1"}},`+
			`{"number":2,"mergeQueueEntry":null},`+
			`{"number":3,"mergeQueueEntry":{"id":"MQ_3"}}`+
			`],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}`)
	}))
	defer srv.Close()

	f := NewGitHubForge("tok", srv.Client(), srv.URL)
	got, err := f.QueuedPRNumbers(context.Background(), "o", "r")
	require.NoError(t, err)
	assert.Equal(t, map[int]bool{1: true, 3: true}, got)
}
