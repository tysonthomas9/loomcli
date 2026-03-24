package cli

import (
	"os"
	"os/exec"
	"testing"
)

func TestRunLegacySync_WithWorktrees(t *testing.T) {
	tmpDir := t.TempDir()
	worktreesDir := tmpDir + "/worktrees"
	wtDir := worktreesDir + "/feature-x"
	os.MkdirAll(wtDir+"/.git", 0755)

	SetupTestEnv(t, map[string]string{
		"LOOM_WORKTREES_DIR": worktreesDir,
	})

	// Reset defaultResolver to legacy mode so it uses LOOM_WORKTREES_DIR
	t.Setenv("LOOM_CONFIG_DIR", tmpDir+"/no-config")
	origResolver := defaultResolver
	defaultResolver = NewResolverFromConfig(nil)
	t.Cleanup(func() { defaultResolver = origResolver })

	origPushOnly := syncPushOnly
	origPullOnly := syncPullOnly
	t.Cleanup(func() {
		syncPushOnly = origPushOnly
		syncPullOnly = origPullOnly
	})
	syncPushOnly = false
	syncPullOnly = false

	SetupMockAgentInvoker(t, nil)

	// CommandMock stubs (execCommand):
	// 1. DiscoverWorktrees: GetCurrentBranch
	// 2. GetDefaultBranch -> DiscoverWorktrees again: GetCurrentBranch
	// 3-4. Push phase: stash list x2
	// 5. Push phase: checkoutTarget -> GetCurrentBranch
	// 6. Push phase: HasCommitsBetween
	// 7. Pull phase: GetCurrentBranch
	cmdMock := NewCommandMock(t, []CommandStub{
		// DiscoverWorktrees -> GetCurrentBranch
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature-x\n"},
		// GetDefaultBranch -> DiscoverWorktrees -> GetCurrentBranch
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature-x\n"},
		// Push: stashIfDirty -> getStashCount before
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		// Push: stashIfDirty -> getStashCount after
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		// Push: checkoutTarget -> GetCurrentBranch
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature-x\n"},
		// Push: HasCommitsBetween (legacy uses main..feature-x, not origin/main)
		{Name: "git", Args: []string{"log", "main..feature-x", "--oneline"}, Stdout: "abc123 some commit\n"},
		// Pull: GetCurrentBranch
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature-x\n"},
	})
	cmdMock.Install()

	// OutputCommandMock stubs (runGitWithOutputFunc):
	// Push phase: fetch, stash, checkout main, pull, merge, push, checkout feature-x (restore)
	// Pull phase: fetch, merge origin/main, push
	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		// Push: fetch
		{Args: []string{"fetch", "origin"}, Err: nil},
		// Push: stash
		{Args: []string{"stash"}, Err: nil},
		// Push: checkout main
		{Args: []string{"checkout", "main"}, Err: nil},
		// Push: pull
		{Args: []string{"pull", "origin", "main"}, Err: nil},
		// Push: merge feature-x into main
		{Args: []string{"merge", "-m", "Merge feature-x into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", "--", "feature-x"}, Err: nil},
		// Push: push main
		{Args: []string{"push", "origin", "main"}, Err: nil},
		// Push: restore checkout to feature-x
		{Args: []string{"checkout", "feature-x"}, Err: nil},
		// Pull: fetch
		{Args: []string{"fetch", "origin"}, Err: nil},
		// Pull: merge origin/main
		{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		// Pull: push
		{Args: []string{"push", "origin", "feature-x"}, Err: nil},
	})
	outputMock.Install()

	runLegacySync()
}

func TestRunWorkspaceSync_MultipleWorkspaces(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := tmpDir + "/.loom"
	os.MkdirAll(configDir, 0755)

	wsADir := tmpDir + "/ws-a"
	repoA := wsADir + "/repo-a"
	os.MkdirAll(repoA+"/.git", 0755)

	wsBDir := tmpDir + "/ws-b"
	repoB := wsBDir + "/repo-b"
	os.MkdirAll(repoB+"/.git", 0755)

	configContent := `workspaces:
  ws-a:
    path: ` + wsADir + `
    repos:
      - name: repo-a
        path: ` + repoA + `
        default_branch: main
  ws-b:
    path: ` + wsBDir + `
    repos:
      - name: repo-b
        path: ` + repoB + `
        default_branch: main
`
	os.WriteFile(configDir+"/config.yaml", []byte(configContent), 0644)

	SetupTestEnv(t, map[string]string{
		"LOOM_CONFIG_DIR": configDir,
	})

	origResolver := defaultResolver
	defaultResolver = nil
	t.Cleanup(func() { defaultResolver = origResolver })

	origPushOnly := syncPushOnly
	origPullOnly := syncPullOnly
	origWsFlag := syncWorkspaceFlag
	t.Cleanup(func() {
		syncPushOnly = origPushOnly
		syncPullOnly = origPullOnly
		syncWorkspaceFlag = origWsFlag
	})
	syncPushOnly = false
	syncPullOnly = true // pull-only to simplify stubs
	syncWorkspaceFlag = ""

	SetupMockAgentInvoker(t, nil)

	// FlexibleCommandMock for GetCurrentBranch calls (one per repo during discovery)
	flexMock := NewFlexibleCommandMock(t)
	flexMock.AddStub("git", []string{"branch", "--show-current"}, CommandResult{Stdout: "dev-branch\n"}).WithMinCalls(2)
	flexMock.Install()

	// OutputCommandMock for pull phase of both workspaces:
	// ws-a: fetch, merge, push
	// ws-b: fetch, merge, push
	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		// ws-a pull: fetch
		{Args: []string{"fetch", "origin"}, Err: nil},
		// ws-a pull: merge
		{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		// ws-a pull: push
		{Args: []string{"push", "origin", "dev-branch"}, Err: nil},
		// ws-b pull: fetch
		{Args: []string{"fetch", "origin"}, Err: nil},
		// ws-b pull: merge
		{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		// ws-b pull: push
		{Args: []string{"push", "origin", "dev-branch"}, Err: nil},
	})
	outputMock.Install()

	runWorkspaceSync()
}

func TestRunWorkspaceSync_SpecificWorkspaceFlag(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := tmpDir + "/.loom"
	os.MkdirAll(configDir, 0755)

	wsADir := tmpDir + "/ws-a"
	repoA := wsADir + "/repo-a"
	os.MkdirAll(repoA+"/.git", 0755)

	wsBDir := tmpDir + "/ws-b"
	repoB := wsBDir + "/repo-b"
	os.MkdirAll(repoB+"/.git", 0755)

	configContent := `workspaces:
  ws-a:
    path: ` + wsADir + `
    repos:
      - name: repo-a
        path: ` + repoA + `
        default_branch: main
  ws-b:
    path: ` + wsBDir + `
    repos:
      - name: repo-b
        path: ` + repoB + `
        default_branch: main
`
	os.WriteFile(configDir+"/config.yaml", []byte(configContent), 0644)

	SetupTestEnv(t, map[string]string{
		"LOOM_CONFIG_DIR": configDir,
	})

	origResolver := defaultResolver
	defaultResolver = nil
	t.Cleanup(func() { defaultResolver = origResolver })

	origPushOnly := syncPushOnly
	origPullOnly := syncPullOnly
	origWsFlag := syncWorkspaceFlag
	t.Cleanup(func() {
		syncPushOnly = origPushOnly
		syncPullOnly = origPullOnly
		syncWorkspaceFlag = origWsFlag
	})
	syncPushOnly = false
	syncPullOnly = true
	syncWorkspaceFlag = "ws-a"

	SetupMockAgentInvoker(t, nil)

	// Only ws-a should be processed, so only one GetCurrentBranch call
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature-a\n"},
	})
	cmdMock.Install()

	// Only ws-a pull stubs: fetch, merge, push
	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "feature-a"}, Err: nil},
	})
	outputMock.Install()

	runWorkspaceSync()
}

func TestRunWorkspaceSync_UnknownWorkspace(t *testing.T) {
	// Use subprocess pattern to test os.Exit(1) path
	if os.Getenv("TEST_SUBPROCESS") == "1" {
		tmpDir := os.Getenv("TEST_TMPDIR")
		configDir := tmpDir + "/.loom"

		os.Setenv("LOOM_CONFIG_DIR", configDir)
		defaultResolver = nil
		syncWorkspaceFlag = "nonexistent"

		runWorkspaceSync()
		return
	}

	tmpDir := t.TempDir()
	configDir := tmpDir + "/.loom"
	os.MkdirAll(configDir, 0755)

	wsDir := tmpDir + "/ws-a"
	os.MkdirAll(wsDir, 0755)

	configContent := `workspaces:
  ws-a:
    path: ` + wsDir + `
    repos: []
`
	os.WriteFile(configDir+"/config.yaml", []byte(configContent), 0644)

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
	configDir := tmpDir + "/.loom"
	os.MkdirAll(configDir, 0755)

	wsDir := tmpDir + "/ws"
	repo := wsDir + "/api"
	os.MkdirAll(repo+"/.git", 0755)

	configContent := `workspaces:
  myws:
    path: ` + wsDir + `
    repos:
      - name: api
        path: ` + repo + `
        default_branch: main
`
	os.WriteFile(configDir+"/config.yaml", []byte(configContent), 0644)

	SetupTestEnv(t, map[string]string{
		"LOOM_CONFIG_DIR": configDir,
	})

	origResolver := defaultResolver
	defaultResolver = nil
	t.Cleanup(func() { defaultResolver = origResolver })

	origPushOnly := syncPushOnly
	origPullOnly := syncPullOnly
	origWsFlag := syncWorkspaceFlag
	t.Cleanup(func() {
		syncPushOnly = origPushOnly
		syncPullOnly = origPullOnly
		syncWorkspaceFlag = origWsFlag
	})
	syncPushOnly = false
	syncPullOnly = true // pull-only to simplify stubs
	syncWorkspaceFlag = ""

	SetupMockAgentInvoker(t, nil)

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

	// Call runFullSync - should dispatch to workspace mode since config exists
	runFullSync(syncCmd, []string{})
}
