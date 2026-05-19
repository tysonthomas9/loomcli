package cli

import (
	"os"
	"testing"

	"github.com/spf13/cobra"
)

func TestRootCommandRegistrationAndExecuteVersion(t *testing.T) {
	oldArgs := os.Args
	oldPending := pendingCmds
	tempCmd := &cobra.Command{Use: "coverage-temp"}
	t.Cleanup(func() {
		os.Args = oldArgs
		rootCmd.SetArgs(nil)
		rootCmd.RemoveCommand(tempCmd)
		pendingCmds = oldPending
	})

	pendingCmds = nil
	RegisterCommand(tempCmd)
	if GetRootCmd() != rootCmd {
		t.Fatal("GetRootCmd did not return root command")
	}
	TestingResetBackendState(t)
	RegisterBackend(&mockBackend{name: "codex"})

	os.Args = []string{"loom", "--version"}
	rootCmd.SetArgs([]string{"--version"})
	if err := Execute(); err != nil {
		t.Fatalf("Execute --version: %v", err)
	}
}
