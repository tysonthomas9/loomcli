package git

import (
	"os"
	"testing"
)

func TestSyncSingleWorkspace_RestacksWithoutPush(t *testing.T) {
	stubPullLocal(t)
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

	outputMock := NewOutputCommandMock(t, nil)

	cmdMock := NewCommandMock(t, []CommandStub{
		// DiscoverWorktrees: GetCurrentBranch for api
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "api-branch\n"},
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

	if err := syncSingleWorkspace(defaultDeps, resolver, false, false); err != nil {
		t.Fatal(err)
	}
}

func TestSyncSingleWorkspace_PushOnly(t *testing.T) {
	if err := syncSingleWorkspace(defaultDeps, nil, true, false); err == nil {
		t.Fatal("push-only should require explicit publish")
	}
}

func TestSyncSingleWorkspace_PullOnly(t *testing.T) {
	stubPullLocal(t)
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

	outputMock := NewOutputCommandMock(t, nil)

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
