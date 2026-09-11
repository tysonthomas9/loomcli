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
	mergedNumber int
	mergeOpts    MergeOptions
	createdOpts  []PullRequestOptions
	updatedOpts  []PullRequestOptions
	draftUpdates []bool
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
func (f *fakeForge) CreatePR(_ context.Context, _, _ string, head, base string, opts PullRequestOptions) (PR, error) {
	f.mutated = true
	f.createdOpts = append(f.createdOpts, opts)
	f.createdTitle = opts.Title
	f.createdBody = opts.Body
	if f.createPR.Number != 0 || f.createPR.URL != "" {
		pr := f.createPR
		if pr.Head == "" {
			pr.Head = head
		}
		if pr.Base == "" {
			pr.Base = base
		}
		if pr.Title == "" {
			pr.Title = opts.Title
		}
		if pr.Body == "" {
			pr.Body = opts.Body
		}
		pr.Draft = opts.Draft
		return pr, nil
	}
	return PR{
		Number: len(f.createdOpts), NodeID: "PR_node", Head: head, Base: base,
		State: "open", Draft: opts.Draft, Title: opts.Title, Body: opts.Body,
		URL: "https://github.com/o/r/pull/1",
	}, nil
}
func (f *fakeForge) UpdatePRMetadata(_ context.Context, _, _ string, number int, opts PullRequestOptions) (PR, error) {
	f.mutated = true
	f.updatedOpts = append(f.updatedOpts, opts)
	for _, pr := range f.prs {
		if pr.Number == number {
			if opts.TitleSet {
				pr.Title = opts.Title
			}
			if opts.BodySet {
				pr.Body = opts.Body
			}
			return pr, nil
		}
	}
	return PR{Number: number, State: "open", URL: "https://github.com/o/r/pull/1"}, nil
}
func (f *fakeForge) SetPRDraft(_ context.Context, _, _ string, pr PR, draft bool) error {
	f.mutated = true
	f.draftUpdates = append(f.draftUpdates, draft)
	for i := range f.prs {
		if f.prs[i].Number == pr.Number {
			f.prs[i].Draft = draft
		}
	}
	return nil
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
func (f *fakeForge) MergePR(_ context.Context, _ string, _, _ string, number int, opts MergeOptions) error {
	f.mutated = true
	f.mergedNumber = number
	f.mergeOpts = opts
	for i := range f.prs {
		if f.prs[i].Number == number {
			f.prs[i].State = "closed"
			f.prs[i].Merged = true
		}
	}
	return nil
}

type updateFailStore struct {
	stackstore.Store
	err error
}

func (s updateFailStore) UpdateNode(context.Context, string, sl.StackID, string, func(*sl.Node) error) error {
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
	rec := &Reconciler{Store: updateFailStore{Store: store, err: persistErr}, Forge: forge}

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
	rec := &Reconciler{Store: store, Forge: ff}

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
	rec := &Reconciler{Store: store, Forge: ff}
	rep, err := rec.Publish(ctx, "WS", id, repoPath, Options{DryRun: true})
	require.NoError(t, err)
	assert.True(t, rep.DryRun)
	assert.False(t, ff.queueChecked, "dry-run does not query the merge queue")
	assert.False(t, ff.mutated)
}

func TestPublish_CreatesPRWithMetadataOptions(t *testing.T) {
	ctx := context.Background()
	id := sl.StackID("epic:Meta")
	store := stackstore.New(t.TempDir())
	require.NoError(t, store.EnsureStack(ctx, sl.Stack{ID: id, WorkspaceKey: "WS", RepoName: "r", RootBase: "main"}))
	_, err := store.AddNode(ctx, "WS", id, "T1", "", "")
	require.NoError(t, err)
	repoPath := gitRepoWithBranches(t, id, "T1")
	ff := &fakeForge{}
	rec := &Reconciler{Store: store, Forge: ff}

	_, err = rec.Publish(ctx, "WS", id, repoPath, Options{PR: PullRequestOptions{
		Title: "custom title", TitleSet: true,
		Body: "custom body", BodySet: true,
		Draft: true, DraftSet: true,
		MaintainerCanModify: false, MaintainerCanModifySet: true,
	}})
	require.NoError(t, err)
	require.Len(t, ff.createdOpts, 1)
	assert.Equal(t, "custom title", ff.createdOpts[0].Title)
	assert.Equal(t, "custom body", ff.createdOpts[0].Body)
	assert.True(t, ff.createdOpts[0].Draft)
	assert.True(t, ff.createdOpts[0].MaintainerCanModifySet)
	assert.False(t, ff.createdOpts[0].MaintainerCanModify)
}

func TestPublish_UpdatesExistingPRMetadataOptions(t *testing.T) {
	ctx := context.Background()
	id := sl.StackID("epic:Meta")
	store := stackstore.New(t.TempDir())
	require.NoError(t, store.EnsureStack(ctx, sl.Stack{ID: id, WorkspaceKey: "WS", RepoName: "r", RootBase: "main"}))
	_, err := store.AddNode(ctx, "WS", id, "T1", "", "")
	require.NoError(t, err)
	repoPath := gitRepoWithBranches(t, id, "T1")
	ff := &fakeForge{prs: []PR{{
		Number: 7, NodeID: "PR_node", Head: sl.OutputBranchName(id, "T1"), Base: "main",
		State: "open", Draft: true, URL: "https://github.com/o/r/pull/7",
	}}}
	rec := &Reconciler{Store: store, Forge: ff}

	_, err = rec.Publish(ctx, "WS", id, repoPath, Options{PR: PullRequestOptions{
		Title: "updated title", TitleSet: true,
		Body: "updated body", BodySet: true,
		Draft: false, DraftSet: true,
	}})
	require.NoError(t, err)
	require.Len(t, ff.updatedOpts, 1)
	assert.Equal(t, "updated title", ff.updatedOpts[0].Title)
	assert.Equal(t, "updated body", ff.updatedOpts[0].Body)
	assert.Equal(t, []bool{false}, ff.draftUpdates)
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
