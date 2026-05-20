package git

import (
	"context"
	"errors"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

type gitExitCode int

func expectGitExit(t *testing.T, want int, fn func()) {
	t.Helper()
	testingSetExitProcess(t, func(code int) {
		panic(gitExitCode(code))
	})
	defer func() {
		t.Helper()
		got := recover()
		if got == nil {
			t.Fatalf("expected exitProcess(%d)", want)
		}
		code, ok := got.(gitExitCode)
		if !ok {
			panic(got)
		}
		if int(code) != want {
			t.Fatalf("exit code = %d, want %d", code, want)
		}
	}()
	fn()
}

func gitCommandWithDeps(t *testing.T, deps *cli.Deps) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.SetContext(cli.WithDeps(context.Background(), deps))
	return cmd
}

func TestGitCommandExitBranchesWithInjectedExit(t *testing.T) {
	t.Run("push rejects all with workspace", func(t *testing.T) {
		deps, _, _, _, _ := NewTestDeps(t)
		cmd := gitCommandWithDeps(t, deps)
		cmd.Flags().Bool("all", false, "")
		cmd.Flags().String("workspace", "", "")
		_ = cmd.Flags().Set("all", "true")
		_ = cmd.Flags().Set("workspace", "api")

		expectGitExit(t, 1, func() {
			_ = runPush(cmd, []string{"main"})
		})
	})

	t.Run("pull rejects all with workspace", func(t *testing.T) {
		deps, _, _, _, _ := NewTestDeps(t)
		cmd := gitCommandWithDeps(t, deps)
		cmd.Flags().Bool("all", false, "")
		cmd.Flags().String("workspace", "", "")
		_ = cmd.Flags().Set("all", "true")
		_ = cmd.Flags().Set("workspace", "api")

		expectGitExit(t, 1, func() {
			_ = runPull(cmd, []string{"main"})
		})
	})

	t.Run("pr exits when gh is missing", func(t *testing.T) {
		deps, _, _, _, _ := NewTestDeps(t)
		cmdMock := NewCommandMock(t, []CommandStub{
			{Name: "gh", Args: []string{"--version"}, Err: errors.New("missing gh")},
		})
		cmdMock.InstallOn(deps)
		cmd := gitCommandWithDeps(t, deps)
		cmd.Flags().Bool("all", false, "")
		cmd.Flags().String("workspace", "", "")

		expectGitExit(t, 1, func() {
			_ = runPR(cmd, []string{"feature"})
		})
	})

	t.Run("pr rejects all with workspace", func(t *testing.T) {
		deps, _, _, _, _ := NewTestDeps(t)
		cmdMock := NewCommandMock(t, []CommandStub{
			{Name: "gh", Args: []string{"--version"}, Stdout: "gh version 2.0\n"},
		})
		cmdMock.InstallOn(deps)
		cmd := gitCommandWithDeps(t, deps)
		cmd.Flags().Bool("all", false, "")
		cmd.Flags().String("workspace", "", "")
		_ = cmd.Flags().Set("all", "true")
		_ = cmd.Flags().Set("workspace", "api")

		expectGitExit(t, 1, func() {
			_ = runPR(cmd, []string{"main"})
		})
	})

	t.Run("sync rejects push only with pull only", func(t *testing.T) {
		deps, _, _, _, _ := NewTestDeps(t)
		cmd := gitCommandWithDeps(t, deps)
		cmd.Flags().Bool("push-only", false, "")
		cmd.Flags().Bool("pull-only", false, "")
		cmd.Flags().String("workspace", "", "")
		_ = cmd.Flags().Set("push-only", "true")
		_ = cmd.Flags().Set("pull-only", "true")

		expectGitExit(t, 1, func() {
			_ = runFullSync(cmd, nil)
		})
	})
}
