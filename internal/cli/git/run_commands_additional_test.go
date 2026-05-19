package git

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

func TestRunPullAndPRAllCommandsUseFlagValues(t *testing.T) {
	tmp := t.TempDir()
	ws := filepath.Join(tmp, "ws")
	setupWorkspaceConfig(t, &LoomConfig{
		DefaultWorkspace: "ws1",
		Workspaces: map[string]WorkspaceConfig{
			"ws1": {Path: ws},
		},
	})

	t.Run("pull all", func(t *testing.T) {
		deps, _, _, _, _ := NewTestDeps(t)
		cmd := &cobra.Command{}
		cmd.Flags().Bool("all", false, "")
		cmd.Flags().String("workspace", "", "")
		cmd.SetContext(cli.WithDeps(context.Background(), deps))
		if err := cmd.Flags().Set("all", "true"); err != nil {
			t.Fatalf("set all: %v", err)
		}
		if err := runPull(cmd, []string{"main"}); err != nil {
			t.Fatalf("runPull: %v", err)
		}
	})

	t.Run("pr all", func(t *testing.T) {
		deps, _, _, _, _ := NewTestDeps(t)
		cmdMock := NewCommandMock(t, []CommandStub{
			{Name: "gh", Args: []string{"--version"}, Stdout: "gh version 2.0\n"},
		})
		cmdMock.InstallOn(deps)

		cmd := &cobra.Command{}
		cmd.Flags().Bool("all", false, "")
		cmd.Flags().String("workspace", "", "")
		cmd.SetContext(cli.WithDeps(context.Background(), deps))
		if err := cmd.Flags().Set("all", "true"); err != nil {
			t.Fatalf("set all: %v", err)
		}
		if err := runPR(cmd, []string{"main"}); err != nil {
			t.Fatalf("runPR: %v", err)
		}
	})
}

func TestRunPushAndResetAllCommandsUseFlagValues(t *testing.T) {
	tmp := t.TempDir()
	ws := filepath.Join(tmp, "ws")
	setupWorkspaceConfig(t, &LoomConfig{
		DefaultWorkspace: "ws1",
		Workspaces: map[string]WorkspaceConfig{
			"ws1": {Path: ws},
		},
	})

	t.Run("push all", func(t *testing.T) {
		deps, _, _, _, _ := NewTestDeps(t)
		cmd := &cobra.Command{}
		cmd.Flags().Bool("all", false, "")
		cmd.Flags().String("workspace", "", "")
		cmd.SetContext(cli.WithDeps(context.Background(), deps))
		if err := cmd.Flags().Set("all", "true"); err != nil {
			t.Fatalf("set all: %v", err)
		}
		if err := runPush(cmd, []string{"main"}); err != nil {
			t.Fatalf("runPush: %v", err)
		}

		resolver, err := NewResolver()
		if err != nil {
			t.Fatalf("NewResolver: %v", err)
		}
		if err := resolver.SetWorkspace("ws1"); err != nil {
			t.Fatalf("SetWorkspace: %v", err)
		}
		if err := pushWorkspaceRepos(deps, resolver, "feature", "main"); err != nil {
			t.Fatalf("pushWorkspaceRepos with empty workspace: %v", err)
		}
	})

	t.Run("reset all", func(t *testing.T) {
		oldResetAll, oldResetForce := resetAll, resetForce
		t.Cleanup(func() {
			resetAll, resetForce = oldResetAll, oldResetForce
		})
		resetAll = true
		resetForce = true

		deps, _, _, _, _ := NewTestDeps(t)
		cmd := &cobra.Command{}
		cmd.SetContext(cli.WithDeps(context.Background(), deps))
		runReset(cmd, []string{"main"})
	})
}
