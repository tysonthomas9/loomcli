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

func TestSyncCmd_Flags(t *testing.T) {
	t.Parallel()
	if wsFlag := syncCmd.Flags().Lookup("workspace"); wsFlag == nil {
		t.Error("expected --workspace flag to be registered")
	}
	if pushOnlyFlag := syncCmd.Flags().Lookup("push-only"); pushOnlyFlag != nil {
		t.Error("obsolete --push-only flag must not be registered")
	}
	if pullOnlyFlag := syncCmd.Flags().Lookup("pull-only"); pullOnlyFlag != nil {
		t.Error("obsolete --pull-only flag must not be registered")
	}
}

func TestSyncCmd_GroupID(t *testing.T) {
	t.Parallel()
	if syncCmd.GroupID != "git" {
		t.Errorf("expected sync command to be in 'git' group, got %q", syncCmd.GroupID)
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

	syncSingleWorkspace(deps, resolver)
}

func TestSyncCmd_ShorthandFlags(t *testing.T) {
	t.Parallel()
	if wsFlag := syncCmd.Flags().ShorthandLookup("W"); wsFlag == nil {
		t.Error("expected -W shorthand flag to be registered")
	}
}
