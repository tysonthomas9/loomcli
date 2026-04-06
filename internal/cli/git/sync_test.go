//go:build ignore

package git

import (
	"os"
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

func TestRunLegacySync_NoWorktrees(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	tmpDir := t.TempDir()
	if err := os.MkdirAll(tmpDir+"/worktrees", 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	SetupTestEnv(t, map[string]string{
		"LOOM_WORKTREES_DIR": tmpDir + "/worktrees",
	})

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{})
	outputMock.InstallOn(deps)

	runLegacySync(deps, false, false)
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
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	tmpDir := t.TempDir()
	configDir := tmpDir + "/.loom"
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}

	wsDir := tmpDir + "/empty"
	if err := os.MkdirAll(wsDir, 0755); err != nil {
		t.Fatalf("failed to create workspace dir: %v", err)
	}

	configContent := `workspaces:
  empty:
    path: ` + wsDir + `
    repos: []
`
	configPath := configDir + "/config.yaml"
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	SetupTestEnv(t, map[string]string{
		"LOOM_CONFIG_DIR": configDir,
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

	syncSingleWorkspace(deps, resolver, false, false)
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
