package git

import (
	"os"
	"testing"
)

// TestSyncSingleWorkspace_PushAndPull_StillPushesAfterPull pins the plain
// `loom sync` path: with neither --push-only nor --pull-only, phase 2 must
// still push the worktree branch after the merge, so remote agent branches stay
// current. It guards against over-correcting PUPPET-42 by deleting that push
// outright instead of making it conditional.
func TestSyncSingleWorkspace_PushAndPull_StillPushesAfterPull(t *testing.T) {
	// not parallel: uses SetupTestEnv, mock.Install(), defaultDeps.Agent mutation
	tmpDir := t.TempDir()
	wsDir := tmpDir + "/ws"
	repo1 := wsDir + "/api"
	os.MkdirAll(repo1+"/.git", 0755)

	setupWorkspaceConfig(t, &LoomConfig{
		DefaultWorkspace: "ws1",
		Workspaces: map[string]WorkspaceConfig{
			"ws1": {
				Path:  wsDir,
				Repos: []RepoConfig{{Name: "api", Path: repo1, DefaultBranch: "main"}},
			},
		},
	})

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		// Push phase: fetch, stash, checkout, pull, merge, push, restore-checkout
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"stash"}, Err: nil},
		{Args: []string{"checkout", "main"}, Err: nil},
		{Args: []string{"pull", "origin", "main"}, Err: nil},
		{Args: []string{"merge", "-m", "Merge api-branch into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", "--", "api-branch"}, Err: nil},
		{Args: []string{"push", "origin", "main"}, Err: nil},
		{Args: []string{"checkout", "api-branch"}, Err: nil},
		// Pull phase: fetch, merge, push
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "api-branch"}, Err: nil},
	})

	cmdMock := NewCommandMock(t, []CommandStub{
		// DiscoverWorktrees: GetCurrentBranch for api
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "api-branch\n"},
		// Push phase: stash list x2, GetCurrentBranch, HasCommitsBetweenRemote
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "api-branch\n"},
		{Name: "git", Args: []string{"log", "origin/main..api-branch", "--oneline"}, Stdout: "abc commit\n"},
	})
	cmdMock.Install()
	outputMock.Install()

	// Set agent mock on defaultDeps (cleaned up by test)
	origAgent := defaultDeps.Agent
	defaultDeps.Agent = &MockAgentInvoker{
		InteractiveFunc: func(workDir, prompt, agentName string) error {
			t.Error("unexpected claude invocation")
			return nil
		},
	}
	t.Cleanup(func() { defaultDeps.Agent = origAgent })

	resolver, err := NewResolver()
	if err != nil {
		t.Fatalf("failed to create resolver: %v", err)
	}
	if err := resolver.SetWorkspace("ws1"); err != nil {
		t.Fatalf("failed to set workspace: %v", err)
	}

	// outputMock.Install() verifies on cleanup that every stub was consumed, so
	// the phase-2 push stub above is load-bearing: a push that stopped
	// happening fails this test.
	syncSingleWorkspace(defaultDeps, resolver, false, false)
}

func TestSyncSingleWorkspace_PushOnly(t *testing.T) {
	// not parallel: uses SetupTestEnv, mock.Install(), defaultDeps.Agent mutation
	tmpDir := t.TempDir()
	wsDir := tmpDir + "/ws"
	repo1 := wsDir + "/api"
	os.MkdirAll(repo1+"/.git", 0755)

	setupWorkspaceConfig(t, &LoomConfig{
		DefaultWorkspace: "ws1",
		Workspaces: map[string]WorkspaceConfig{
			"ws1": {
				Path:  wsDir,
				Repos: []RepoConfig{{Name: "api", Path: repo1, DefaultBranch: "main"}},
			},
		},
	})

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		// Push phase only: fetch, stash, checkout, pull, merge, push, restore-checkout
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"stash"}, Err: nil},
		{Args: []string{"checkout", "main"}, Err: nil},
		{Args: []string{"pull", "origin", "main"}, Err: nil},
		{Args: []string{"merge", "-m", "Merge api-branch into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", "--", "api-branch"}, Err: nil},
		{Args: []string{"push", "origin", "main"}, Err: nil},
		{Args: []string{"checkout", "api-branch"}, Err: nil},
	})

	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "api-branch\n"},
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "api-branch\n"},
		{Name: "git", Args: []string{"log", "origin/main..api-branch", "--oneline"}, Stdout: "abc commit\n"},
	})
	cmdMock.Install()
	outputMock.Install()

	origAgent := defaultDeps.Agent
	defaultDeps.Agent = &MockAgentInvoker{
		InteractiveFunc: func(workDir, prompt, agentName string) error {
			t.Error("unexpected claude invocation")
			return nil
		},
	}
	t.Cleanup(func() { defaultDeps.Agent = origAgent })

	resolver, err := NewResolver()
	if err != nil {
		t.Fatalf("failed to create resolver: %v", err)
	}
	if err := resolver.SetWorkspace("ws1"); err != nil {
		t.Fatalf("failed to set workspace: %v", err)
	}

	syncSingleWorkspace(defaultDeps, resolver, true, false)
}

// TestSyncSingleWorkspace_PullOnly_DoesNotPush is the PUPPET-42 regression
// test. --pull-only must issue no remote-writing command at all: phase 1 is
// skipped and phase 2's post-merge push is suppressed. This test previously
// stubbed {"push", "origin", "api-branch"} — it encoded the bug, where
// --pull-only published every worktree's current branch.
func TestSyncSingleWorkspace_PullOnly_DoesNotPush(t *testing.T) {
	// not parallel: uses SetupTestEnv, mock.Install(), defaultDeps.Agent mutation
	tmpDir := t.TempDir()
	wsDir := tmpDir + "/ws"
	repo1 := wsDir + "/api"
	os.MkdirAll(repo1+"/.git", 0755)

	setupWorkspaceConfig(t, &LoomConfig{
		DefaultWorkspace: "ws1",
		Workspaces: map[string]WorkspaceConfig{
			"ws1": {
				Path:  wsDir,
				Repos: []RepoConfig{{Name: "api", Path: repo1, DefaultBranch: "main"}},
			},
		},
	})

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		// Pull phase only — fetch and merge, and deliberately no push stub.
		// OutputCommandMock t.Fatal's on any call past its stubs, so a
		// reintroduced push fails here.
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
	})

	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "api-branch\n"},
	})
	cmdMock.Install()
	outputMock.Install()

	origAgent := defaultDeps.Agent
	defaultDeps.Agent = &MockAgentInvoker{
		InteractiveFunc: func(workDir, prompt, agentName string) error {
			t.Error("unexpected claude invocation")
			return nil
		},
	}
	t.Cleanup(func() { defaultDeps.Agent = origAgent })

	resolver, err := NewResolver()
	if err != nil {
		t.Fatalf("failed to create resolver: %v", err)
	}
	if err := resolver.SetWorkspace("ws1"); err != nil {
		t.Fatalf("failed to set workspace: %v", err)
	}

	syncSingleWorkspace(defaultDeps, resolver, false, true)
}
