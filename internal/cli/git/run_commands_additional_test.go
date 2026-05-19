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
