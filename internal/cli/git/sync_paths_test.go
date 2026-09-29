package git

import (
	"os"
	"os/exec"
	"testing"
)

func TestRunWorkspaceSync_MultipleWorkspaces(t *testing.T) {
	tmpDir := t.TempDir()
	wsADir := tmpDir + "/ws-a"
	repoA := wsADir + "/repo-a"
	os.MkdirAll(repoA+"/.git", 0755)

	wsBDir := tmpDir + "/ws-b"
	repoB := wsBDir + "/repo-b"
	os.MkdirAll(repoB+"/.git", 0755)

	setupWorkspaceConfig(t, &LoomConfig{
		DefaultWorkspace: "ws-a",
		Workspaces: map[string]WorkspaceConfig{
			"ws-a": {
				Path:  wsADir,
				Repos: []RepoConfig{{Name: "repo-a", Path: repoA, DefaultBranch: "main"}},
			},
			"ws-b": {
				Path:  wsBDir,
				Repos: []RepoConfig{{Name: "repo-b", Path: repoB, DefaultBranch: "main"}},
			},
		},
	})

	origResolver := defaultResolver
	defaultResolver = nil
	t.Cleanup(func() { defaultResolver = origResolver })

	// FlexibleCommandMock for GetCurrentBranch calls (one per repo during discovery)
	flexMock := NewFlexibleCommandMock(t)
	flexMock.AddStub("git", []string{"branch", "--show-current"}, CommandResult{Stdout: "dev-branch\n"}).WithMinCalls(2)
	flexMock.Install()

	// OutputCommandMock for pull phase of both workspaces
	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "dev-branch"}, Err: nil},
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "dev-branch"}, Err: nil},
	})
	outputMock.Install()

	origAgent := defaultDeps.Agent
	defaultDeps.Agent = &MockAgentInvoker{
		InteractiveFunc: func(workDir, prompt, agentName string) error {
			t.Error("unexpected claude invocation")
			return nil
		},
	}
	t.Cleanup(func() { defaultDeps.Agent = origAgent })

	runWorkspaceSync(defaultDeps, "")
}

func TestRunWorkspaceSync_SpecificWorkspaceFlag(t *testing.T) {
	tmpDir := t.TempDir()
	wsADir := tmpDir + "/ws-a"
	repoA := wsADir + "/repo-a"
	os.MkdirAll(repoA+"/.git", 0755)

	wsBDir := tmpDir + "/ws-b"
	repoB := wsBDir + "/repo-b"
	os.MkdirAll(repoB+"/.git", 0755)

	setupWorkspaceConfig(t, &LoomConfig{
		DefaultWorkspace: "ws-a",
		Workspaces: map[string]WorkspaceConfig{
			"ws-a": {
				Path:  wsADir,
				Repos: []RepoConfig{{Name: "repo-a", Path: repoA, DefaultBranch: "main"}},
			},
			"ws-b": {
				Path:  wsBDir,
				Repos: []RepoConfig{{Name: "repo-b", Path: repoB, DefaultBranch: "main"}},
			},
		},
	})

	origResolver := defaultResolver
	defaultResolver = nil
	t.Cleanup(func() { defaultResolver = origResolver })

	// Only ws-a should be processed
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature-a\n"},
	})
	cmdMock.Install()

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "feature-a"}, Err: nil},
	})
	outputMock.Install()

	origAgent := defaultDeps.Agent
	defaultDeps.Agent = &MockAgentInvoker{
		InteractiveFunc: func(workDir, prompt, agentName string) error {
			t.Error("unexpected claude invocation")
			return nil
		},
	}
	t.Cleanup(func() { defaultDeps.Agent = origAgent })

	runWorkspaceSync(defaultDeps, "ws-a")
}

func TestRunWorkspaceSync_UnknownWorkspace(t *testing.T) {
	if os.Getenv("TEST_SUBPROCESS") == "1" {
		tmpDir := os.Getenv("TEST_TMPDIR")
		configDir := tmpDir + "/.loom"

		os.Setenv("LOOM_CONFIG_DIR", configDir)
		defaultResolver = nil

		runWorkspaceSync(defaultDeps, "nonexistent")
		return
	}

	tmpDir := t.TempDir()
	configDir := tmpDir + "/.loom"
	wsDir := tmpDir + "/ws-a"
	os.MkdirAll(wsDir, 0755)

	setupWorkspaceConfigInDir(t, configDir, &LoomConfig{
		DefaultWorkspace: "ws-a",
		Workspaces: map[string]WorkspaceConfig{
			"ws-a": {Path: wsDir},
		},
	})

	cmd := exec.Command(os.Args[0], "-test.run=TestRunWorkspaceSync_UnknownWorkspace") //nolint:norawexec // subprocess pattern for testing os.Exit
	cmd.Env = append(os.Environ(),
		"TEST_SUBPROCESS=1",
		"TEST_TMPDIR="+tmpDir,
		"LOOM_CONFIG_DIR="+configDir,
	)

	err := cmd.Run()
	if err == nil {
		t.Fatal("expected subprocess to exit with non-zero status")
	}

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected *exec.ExitError, got %T: %v", err, err)
	}
	if exitErr.ExitCode() == 0 {
		t.Fatal("expected non-zero exit code")
	}
}

func TestRunFullSync_DispatchesToWorkspaceMode(t *testing.T) {
	tmpDir := t.TempDir()
	wsDir := tmpDir + "/ws"
	repo := wsDir + "/api"
	os.MkdirAll(repo+"/.git", 0755)

	setupWorkspaceConfig(t, &LoomConfig{
		DefaultWorkspace: "myws",
		Workspaces: map[string]WorkspaceConfig{
			"myws": {
				Path:  wsDir,
				Repos: []RepoConfig{{Name: "api", Path: repo, DefaultBranch: "main"}},
			},
		},
	})

	origResolver := defaultResolver
	defaultResolver = nil
	t.Cleanup(func() { defaultResolver = origResolver })

	// Workspace discovery: GetCurrentBranch for the repo
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "api-branch\n"},
	})
	cmdMock.Install()

	// Workspace pull: fetch, merge, push
	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "api-branch"}, Err: nil},
	})
	outputMock.Install()

	origAgent := defaultDeps.Agent
	defaultDeps.Agent = &MockAgentInvoker{
		InteractiveFunc: func(workDir, prompt, agentName string) error {
			t.Error("unexpected claude invocation")
			return nil
		},
	}
	t.Cleanup(func() { defaultDeps.Agent = origAgent })

	// Call runWorkspaceSync - should dispatch to workspace mode since config exists
	runWorkspaceSync(defaultDeps, "")
}
