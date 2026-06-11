package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	storepkg "github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// testSSEClient wraps a realtime.Client for tests, providing convenience methods.
type testSSEClient struct {
	client *realtime.Client
	hub    *realtime.Hub
}

// WaitForMutation waits for a mutation to arrive on the client's send channel.
func (tc *testSSEClient) WaitForMutation(timeout time.Duration) (*realtime.MutationPayload, error) {
	select {
	case m, ok := <-tc.client.Send():
		if !ok {
			return nil, context.DeadlineExceeded
		}
		return m, nil
	case <-time.After(timeout):
		return nil, context.DeadlineExceeded
	}
}

// WaitForMutations waits for n mutations to arrive.
func (tc *testSSEClient) WaitForMutations(n int, timeout time.Duration) ([]*realtime.MutationPayload, error) {
	var results []*realtime.MutationPayload
	deadline := time.After(timeout)
	for i := 0; i < n; i++ {
		select {
		case m, ok := <-tc.client.Send():
			if !ok {
				return results, context.DeadlineExceeded
			}
			results = append(results, m)
		case <-deadline:
			return results, context.DeadlineExceeded
		}
	}
	return results, nil
}

// DrainMutations returns all buffered mutations without blocking.
func (tc *testSSEClient) DrainMutations() []*realtime.MutationPayload {
	var results []*realtime.MutationPayload
	for {
		select {
		case m, ok := <-tc.client.Send():
			if !ok {
				return results
			}
			results = append(results, m)
		default:
			return results
		}
	}
}

// Close unregisters the client from the hub and closes the done channel.
func (tc *testSSEClient) Close() {
	tc.hub.UnregisterClient(tc.client)
	close(tc.client.Done())
}

// stubPool is a minimal daemon.Pool for handler tests.
type stubPool struct{}

func (s *stubPool) Get(_ context.Context) (*rpc.Client, error) { return &rpc.Client{}, nil }
func (s *stubPool) Put(_ *rpc.Client)                          {}
func (s *stubPool) PutAfterError(_ *rpc.Client)                {}
func (s *stubPool) Discard(_ *rpc.Client)                      {}
func (s *stubPool) Stats() daemon.PoolStats {
	return daemon.PoolStats{Size: 10, Created: 2, Active: 1, Available: 1}
}
func (s *stubPool) Close() error { return nil }

// newTestSessionStore creates a sessions.Store rooted in a temporary directory.
func newTestSessionStore(t *testing.T) *sessions.Store {
	t.Helper()
	dir := t.TempDir()
	store, err := sessions.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

// newTestSessionStoreWithDir creates a sessions.Store and returns the base dir (for configByIDFn).
func newTestSessionStoreWithDir(t *testing.T) (*sessions.Store, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := sessions.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store, dir
}

// createTestSession creates a session via the store and finalizes it.
func createTestSession(t *testing.T, store *sessions.Store, taskID string) *sessions.Session {
	t.Helper()
	sess, err := store.CreateSession(sessions.CreateOptions{
		AgentName:  "testagent",
		Backend:    "claude",
		AttemptNum: 1,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	err = sess.Finalize(sessions.FinalizeOptions{
		TaskID:   taskID,
		ExitCode: 0,
		DiffStats: sessions.DiffStats{
			FilesChanged: 2,
			LinesAdded:   10,
			LinesRemoved: 3,
		},
		DiffPatch: "diff --git a/foo.go b/foo.go\n+hello\n",
	})
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	return sess
}

// testWorkspaceStore returns a FleetDB-style workspace store for testing.
func testWorkspaceStore(_ string, workspaces []ops.WorkspaceSummary) storepkg.Store {
	st := memstore.New()
	for _, ws := range workspaces {
		key := ws.ID
		if key == "" {
			key = ws.Name
		}
		if key == "" {
			continue
		}
		_, _ = st.Workspaces().Create(context.Background(), storepkg.WorkspaceCreate{Key: key, Name: ws.Name})
	}
	return st
}

// testWorktree returns a standard ops.AgentWorktree used across tests.
func testWorktree() *ops.AgentWorktree {
	return &ops.AgentWorktree{
		Name:          "test-agent",
		Path:          "/tmp/worktrees/test-agent",
		Branch:        "loomcli-test-agent",
		DefaultBranch: "main",
		Remote:        "origin",
		RepoName:      "myrepo",
		IsWorkspace:   true,
	}
}

// mockFileOps implements ops.FileOps for testing.
type mockFileOps struct {
	resolveFunc func(name string) (*ops.AgentWorktree, error)
}

func (m *mockFileOps) ResolveAgentWorktree(_, name string) (*ops.AgentWorktree, error) {
	if m.resolveFunc != nil {
		return m.resolveFunc(name)
	}
	return nil, errors.New("not found")
}

// mockGitOps implements ops.GitOps for testing in the root package.
type mockGitOps struct {
	resolveFunc            func(name string) (*ops.AgentWorktree, error)
	pushFunc               func(worktreePath, sourceBranch, targetBranch, remote string) (*ops.GitPushResult, error)
	pullFunc               func(worktreePath, currentBranch, sourceBranch, remote string) (*ops.GitPullResult, error)
	createPRFunc           func(worktreePath, sourceBranch, targetBranch, remote string) (*ops.GitPRResult, error)
	resetFunc              func(worktreePath, worktreeName, targetBranch string, force, push bool) (*ops.GitResetResult, error)
	statusFunc             func(worktreePath, targetBranch string) (*ops.GitStatusResult, error)
	getCurrentBranchFunc   func(worktreePath string) (string, error)
	checkGhInstalledFunc   func() error
	setRepoDefaultFunc     func(repoName, branch string) error
	listAgentWorktreesFunc func() ([]ops.AgentWorktree, error)
	diffStatFunc           func(worktreePath, fromRef string) ops.DiffStatResult
	resolveMergeBaseFunc   func(worktreePath, branch string) (string, error)
	diffCommitsFunc        func(worktreePath, mergeBase string, limit int) ([]ops.DiffCommitResult, error)
	diffFilesFunc          func(worktreePath, from, to string) ([]ops.DiffFileResult, error)
	diffFilePatchFunc      func(worktreePath, from, to, path string) (*ops.DiffFilePatchResult, error)
}

func (m *mockGitOps) ResolveAgentWorktree(_, name string) (*ops.AgentWorktree, error) {
	if m.resolveFunc != nil {
		return m.resolveFunc(name)
	}
	return nil, errors.New("not found")
}
func (m *mockGitOps) Push(worktreePath, sourceBranch, targetBranch, remote string) (*ops.GitPushResult, error) {
	if m.pushFunc != nil {
		return m.pushFunc(worktreePath, sourceBranch, targetBranch, remote)
	}
	return &ops.GitPushResult{Success: true, Message: "pushed"}, nil
}
func (m *mockGitOps) Pull(worktreePath, currentBranch, sourceBranch, remote string) (*ops.GitPullResult, error) {
	if m.pullFunc != nil {
		return m.pullFunc(worktreePath, currentBranch, sourceBranch, remote)
	}
	return &ops.GitPullResult{Success: true, Message: "pulled"}, nil
}
func (m *mockGitOps) CreatePR(worktreePath, sourceBranch, targetBranch, remote string) (*ops.GitPRResult, error) {
	if m.createPRFunc != nil {
		return m.createPRFunc(worktreePath, sourceBranch, targetBranch, remote)
	}
	return &ops.GitPRResult{URL: "https://github.com/test/pr/1", Created: true}, nil
}

func (m *mockGitOps) ListWorkspacePullRequests(string, string, int) (*ops.GitPullRequestList, error) {
	return &ops.GitPullRequestList{PullRequests: []ops.GitPullRequest{}}, nil
}

func (m *mockGitOps) Reset(worktreePath, worktreeName, targetBranch string, force, push bool) (*ops.GitResetResult, error) {
	if m.resetFunc != nil {
		return m.resetFunc(worktreePath, worktreeName, targetBranch, force, push)
	}
	return &ops.GitResetResult{Success: true, Message: "reset done"}, nil
}
func (m *mockGitOps) Status(worktreePath, targetBranch string) (*ops.GitStatusResult, error) {
	if m.statusFunc != nil {
		return m.statusFunc(worktreePath, targetBranch)
	}
	return &ops.GitStatusResult{Branch: "feature", TargetBranch: "main", IsClean: true}, nil
}
func (m *mockGitOps) GetCurrentBranch(worktreePath string) (string, error) {
	if m.getCurrentBranchFunc != nil {
		return m.getCurrentBranchFunc(worktreePath)
	}
	return "feature-branch", nil
}
func (m *mockGitOps) CheckGhInstalled() error {
	if m.checkGhInstalledFunc != nil {
		return m.checkGhInstalledFunc()
	}
	return nil
}
func (m *mockGitOps) SetRepoDefaultBranch(_, repoName, branch string) error {
	if m.setRepoDefaultFunc != nil {
		return m.setRepoDefaultFunc(repoName, branch)
	}
	return nil
}
func (m *mockGitOps) ListAgentWorktrees(_ string) ([]ops.AgentWorktree, error) {
	if m.listAgentWorktreesFunc != nil {
		return m.listAgentWorktreesFunc()
	}
	return nil, nil
}
func (m *mockGitOps) DiffStat(worktreePath, fromRef string) ops.DiffStatResult {
	if m.diffStatFunc != nil {
		return m.diffStatFunc(worktreePath, fromRef)
	}
	return ops.DiffStatResult{}
}
func (m *mockGitOps) ResolveMergeBase(worktreePath, branch string) (string, error) {
	if m.resolveMergeBaseFunc != nil {
		return m.resolveMergeBaseFunc(worktreePath, branch)
	}
	return "abc123", nil
}
func (m *mockGitOps) DiffCommits(_ context.Context, worktreePath, mergeBase string, limit int) ([]ops.DiffCommitResult, error) {
	if m.diffCommitsFunc != nil {
		return m.diffCommitsFunc(worktreePath, mergeBase, limit)
	}
	return []ops.DiffCommitResult{}, nil
}
func (m *mockGitOps) DiffFiles(_ context.Context, worktreePath, from, to string) ([]ops.DiffFileResult, error) {
	if m.diffFilesFunc != nil {
		return m.diffFilesFunc(worktreePath, from, to)
	}
	return []ops.DiffFileResult{}, nil
}
func (m *mockGitOps) DiffFilePatch(_ context.Context, worktreePath, from, to, path string) (*ops.DiffFilePatchResult, error) {
	if m.diffFilePatchFunc != nil {
		return m.diffFilePatchFunc(worktreePath, from, to, path)
	}
	return &ops.DiffFilePatchResult{}, nil
}
