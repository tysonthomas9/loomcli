package git

import (
	"os"
	"testing"
)

func TestSyncSingleWorkspace_PushAndPull(t *testing.T) {
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

	pullPhaseStubs := []CommandStub{
		// DiscoverWorktrees: GetCurrentBranch for api
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "api-branch\n"},
		// Push phase: stash list x2, GetCurrentBranch, HasCommitsBetweenRemote
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "api-branch\n"},
		{Name: "git", Args: []string{"log", "origin/main..api-branch", "--oneline"}, Stdout: "abc commit\n"},
	}
	// Pull phase: the post-pull verification reads.
	pullPhaseStubs = append(pullPhaseStubs, verifyStubs("origin", "main", "aaaaaaaaaaaa", "bbbbbbbbbbbb", 0)...)
	cmdMock := NewCommandMock(t, pullPhaseStubs)
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

	if err := syncSingleWorkspace(defaultDeps, resolver, false, false); err != nil {
		t.Errorf("expected nil error for an in-sync workspace, got %v", err)
	}
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

	if err := syncSingleWorkspace(defaultDeps, resolver, true, false); err != nil {
		t.Errorf("push-only sync must not fail: %v", err)
	}
}

func TestSyncSingleWorkspace_PullOnly(t *testing.T) {
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
		// Pull phase only
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "api-branch"}, Err: nil},
	})

	stubs := []CommandStub{
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "api-branch\n"},
	}
	stubs = append(stubs, verifyStubs("origin", "main", "aaaaaaaaaaaa", "bbbbbbbbbbbb", 0)...)
	cmdMock := NewCommandMock(t, stubs)
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

	if err := syncSingleWorkspace(defaultDeps, resolver, false, true); err != nil {
		t.Errorf("expected nil error for an in-sync workspace, got %v", err)
	}
}
