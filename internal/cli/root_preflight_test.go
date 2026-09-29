package cli

import (
	"context"
	"os"
	"testing"

	"github.com/spf13/cobra"
)

func TestPrepareCommandMirrorsRootFlagsWithoutResolvingBackend(t *testing.T) {
	previousServer := serverFlag
	previousWorkspace := workspaceFlag
	previousBackend := backendFlag
	previousLogFormat := logFormat
	previousLogOutput := logOutput
	previousDeps := defaultDeps
	t.Cleanup(func() {
		serverFlag = previousServer
		workspaceFlag = previousWorkspace
		backendFlag = previousBackend
		logFormat = previousLogFormat
		logOutput = previousLogOutput
		defaultDeps = previousDeps
	})
	t.Setenv("LOOM_SERVER_URL", "")
	t.Setenv("LOOM_WORKSPACE", "")
	t.Setenv("LOOM_ISSUE_BACKEND", IssueBackendFleetDB)
	serverFlag = "http://127.0.0.1:9999"
	workspaceFlag = "ACME"
	backendFlag = "definitely-not-registered"
	logFormat = "text"
	logOutput = "stderr"

	cmd := &cobra.Command{Use: "diagnostic"}
	cmd.SetContext(context.Background())
	if err := PrepareCommand(cmd, nil); err != nil {
		t.Fatalf("PrepareCommand() error = %v", err)
	}
	if got := os.Getenv("LOOM_SERVER_URL"); got != serverFlag {
		t.Fatalf("LOOM_SERVER_URL = %q, want %q", got, serverFlag)
	}
	if got := os.Getenv("LOOM_WORKSPACE"); got != workspaceFlag {
		t.Fatalf("LOOM_WORKSPACE = %q, want %q", got, workspaceFlag)
	}
	if GetDeps(cmd) == nil {
		t.Fatal("PrepareCommand() did not inject command dependencies")
	}
}
