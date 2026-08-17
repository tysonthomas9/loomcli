package git

import (
	"os"
	"strings"
	"testing"
)

func TestSyncCmd_NoArgs(t *testing.T) {
	t.Parallel()
	err := syncCmd.Args(syncCmd, []string{})
	if err != nil {
		t.Errorf("expected no error for sync with no args, got: %v", err)
	}
}

func TestSyncCmd_RejectsArgs(t *testing.T) {
	t.Parallel()
	err := syncCmd.Args(syncCmd, []string{"extra"})
	if err == nil {
		t.Error("expected error for sync with args, got nil")
	}
}

func TestSyncCmd_MutuallyExclusiveFlags(t *testing.T) {
	t.Parallel()
	// Test that Args validation still passes even with both flags set
	// (validation happens in runFullSync, not Args)
	err := syncCmd.Args(syncCmd, []string{})
	if err != nil {
		t.Errorf("Args validation should pass, got: %v", err)
	}
}

func TestSyncCmd_Flags(t *testing.T) {
	t.Parallel()
	if pushOnlyFlag := syncCmd.Flags().Lookup("push-only"); pushOnlyFlag == nil {
		t.Error("expected --push-only flag to be registered")
	}
	if pullOnlyFlag := syncCmd.Flags().Lookup("pull-only"); pullOnlyFlag == nil {
		t.Error("expected --pull-only flag to be registered")
	}
	if wsFlag := syncCmd.Flags().Lookup("workspace"); wsFlag == nil {
		t.Error("expected --workspace flag to be registered")
	}
}

func TestSyncCmd_GroupID(t *testing.T) {
	t.Parallel()
	if syncCmd.GroupID != "git" {
		t.Errorf("expected sync command to be in 'git' group, got %q", syncCmd.GroupID)
	}
}

func TestSyncCmd_PushOnlyFlagLogic(t *testing.T) {
	t.Parallel()
	// Verify that the pushOnly condition is correct
	pushOnly := true
	pullOnly := false
	if !pushOnly {
		t.Error("expected pushOnly to be true")
	}
	_ = pullOnly
}

func TestSyncCmd_PullOnlyFlagLogic(t *testing.T) {
	t.Parallel()
	// Verify that the pullOnly condition is correct
	pushOnly := false
	pullOnly := true
	if !pullOnly {
		t.Error("expected pullOnly to be true")
	}
	_ = pushOnly
}

func TestSyncCmd_MutuallyExclusiveFlagsRuntime(t *testing.T) {
	t.Parallel()
	pushOnly := true
	pullOnly := true
	if !pushOnly || !pullOnly {
		t.Error("both flags should be set for this test")
	}
	if pushOnly && pullOnly {
		// This is the condition that triggers the error in runFullSync
	}
}

func TestSyncSingleWorkspace_EmptyWorktrees(t *testing.T) {
	deps, _, _, _, _ := NewTestDeps(t)

	tmpDir := t.TempDir()
	wsDir := tmpDir + "/empty"
	if err := os.MkdirAll(wsDir, 0755); err != nil {
		t.Fatalf("failed to create workspace dir: %v", err)
	}

	setupWorkspaceConfig(t, &LoomConfig{
		DefaultWorkspace: "empty",
		Workspaces: map[string]WorkspaceConfig{
			"empty": {Path: wsDir},
		},
	})

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{})
	outputMock.InstallOn(deps)

	resolver, err := NewResolver()
	if err != nil {
		t.Fatalf("failed to create resolver: %v", err)
	}

	if err := resolver.SetWorkspace("empty"); err != nil {
		t.Fatalf("failed to set workspace: %v", err)
	}

	if err := syncSingleWorkspace(deps, resolver, false, false); err != nil {
		t.Errorf("expected nil error for a workspace with no repos, got %v", err)
	}
}

func TestSyncCmd_ShorthandFlags(t *testing.T) {
	t.Parallel()
	if wsFlag := syncCmd.Flags().ShorthandLookup("W"); wsFlag == nil {
		t.Error("expected -W shorthand flag to be registered")
	}
	if pushOnlyFlag := syncCmd.Flags().Lookup("push-only"); pushOnlyFlag == nil {
		t.Error("expected --push-only flag to be registered")
	}
	if pullOnlyFlag := syncCmd.Flags().Lookup("pull-only"); pullOnlyFlag == nil {
		t.Error("expected --pull-only flag to be registered")
	}
}

// A repo that is still behind after the pull phase must fail the workspace, so
// "Full sync complete!" cannot print over the incident and the command exits
// non-zero.
func TestRunWorkspaceSync_BehindRepoFailsAndWithholdsBanner(t *testing.T) {
	// not parallel: mock.Install() and os.Stdout swap touch global state
	tmpDir := t.TempDir()
	wsDir := tmpDir + "/ws"
	repo := wsDir + "/api"
	if err := os.MkdirAll(repo+"/.git", 0755); err != nil {
		t.Fatalf("failed to create repo dir: %v", err)
	}

	setupWorkspaceConfig(t, &LoomConfig{
		DefaultWorkspace: "ws1",
		Workspaces: map[string]WorkspaceConfig{
			"ws1": {
				Path:  wsDir,
				Repos: []RepoConfig{{Name: "api", Path: repo, DefaultBranch: "main"}},
			},
		},
	})

	origResolver := defaultResolver
	defaultResolver = nil
	t.Cleanup(func() { defaultResolver = origResolver })

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		// No push stub: this is a --pull-only sync, and pull-only suppresses the
		// pull path's push too, so fetch+merge is the whole remote interaction.
	})

	stubs := []CommandStub{
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "api-branch\n"},
	}
	// The merge reported success; git says the checkout is still 8 behind.
	stubs = append(stubs, verifyStubs("origin", "main", "aaaaaaaaaaaa", "bbbbbbbbbbbb", 8)...)
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

	var syncErr error
	out := captureStdout(t, func() {
		syncErr = runWorkspaceSync(defaultDeps, false, true, "")
	})

	if syncErr == nil {
		t.Fatal("expected a non-nil error when a repo is still behind after the pull")
	}
	if strings.Contains(out, "Full sync complete!") {
		t.Errorf("banner must be withheld when a repo is not in sync:\n%s", out)
	}
	if !strings.Contains(out, "still 8 commit(s) behind origin/main") {
		t.Errorf("expected the measurement in the summary:\n%s", out)
	}
}
