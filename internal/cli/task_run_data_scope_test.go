package cli

import (
	"strings"
	"testing"

	clidatacmd "github.com/tysonthomas9/loomcli/internal/cli/data"
)

func TestRootPreRunRejectsTaskRunDataEscapeBeforeBackendResolution(t *testing.T) {
	restoreGuard := RegisterPreBackendCommandGuard(clidatacmd.EnforceTaskRunCommandScope)
	t.Cleanup(restoreGuard)

	previousBackendFlag := backendFlag
	previousServerFlag := serverFlag
	previousWorkspaceFlag := workspaceFlag
	t.Cleanup(func() {
		backendFlag = previousBackendFlag
		serverFlag = previousServerFlag
		workspaceFlag = previousWorkspaceFlag
	})

	backendFlag = "backend-resolution-must-not-run"
	serverFlag = ""
	workspaceFlag = ""
	t.Setenv("LOOM_TASK_RUN_ID", "task-run-1")

	var listCommandFound bool
	for _, command := range clidatacmd.Commands()[0].Commands() {
		if command.Name() != "list" {
			continue
		}
		listCommandFound = true
		err := rootCmd.PersistentPreRunE(command, nil)
		if err == nil || !strings.Contains(err.Error(), "task-run data mode only permits") {
			t.Fatalf("root pre-run error = %v, want TaskRun scope rejection before invalid backend resolution", err)
		}
	}
	if !listCommandFound {
		t.Fatal("loom data list command not found")
	}
}
