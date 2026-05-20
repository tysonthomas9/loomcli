package git

import (
	"os"
	"testing"

	"github.com/spf13/cobra"
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

func TestRunFullSyncNoRepoBranches(t *testing.T) {
	tmpDir := t.TempDir()
	emptyWS := tmpDir + "/empty"
	if err := os.MkdirAll(emptyWS, 0755); err != nil {
		t.Fatal(err)
	}
	setupWorkspaceConfig(t, &LoomConfig{
		DefaultWorkspace: "empty",
		Workspaces: map[string]WorkspaceConfig{
			"empty": {Path: emptyWS},
		},
	})

	if err := runFullSync(newSyncTestCmd(false, false, ""), nil); err != nil {
		t.Fatalf("runFullSync all workspaces: %v", err)
	}
	if err := runFullSync(newSyncTestCmd(true, false, "empty"), nil); err != nil {
		t.Fatalf("runFullSync workspace push-only: %v", err)
	}
}

func newSyncTestCmd(pushOnly, pullOnly bool, workspace string) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Flags().Bool("push-only", false, "")
	cmd.Flags().Bool("pull-only", false, "")
	cmd.Flags().String("workspace", "", "")
	_ = cmd.Flags().Set("push-only", boolFlag(pushOnly))
	_ = cmd.Flags().Set("pull-only", boolFlag(pullOnly))
	_ = cmd.Flags().Set("workspace", workspace)
	return cmd
}

func boolFlag(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
