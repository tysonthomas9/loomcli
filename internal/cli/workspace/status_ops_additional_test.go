package workspace

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestRunStatusJSONAndDaemonCollector(t *testing.T) {
	oldJSON, oldBranch := statusJSON, statusBranch
	t.Cleanup(func() {
		statusJSON, statusBranch = oldJSON, oldBranch
	})
	statusJSON = true
	statusBranch = "main"
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	out := captureWorkspaceStdout(t, func() {
		if err := runStatus(&cobra.Command{}, nil); err != nil {
			t.Fatalf("runStatus: %v", err)
		}
	})
	var data StatusData
	if err := json.Unmarshal([]byte(out), &data); err != nil {
		t.Fatalf("status json = %q: %v", out, err)
	}
	if data.Backend.Name == "" || data.IssueBackend == "" {
		t.Fatalf("status data missing backend fields: %+v", data)
	}

	if got := collectDaemonStatus(); got.Running {
		t.Fatalf("collectDaemonStatus unexpectedly running: %+v", got)
	}
}

func TestRunWorkspaceListWrapperAndLoadOpsStatus(t *testing.T) {
	handle := setupWorkspaceCommandFleetStore(t)
	defer handle.Close()
	resetWorkspaceCommandFlags(t)

	if _, err := handle.Store.Workspaces().Create(context.Background(), store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := handle.Store.Repos().Create(context.Background(), store.RepoCreate{WorkspaceKey: "WS", Name: "api", RemoteURL: "/tmp/api"}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if err := bootstrap.SetActiveWorkspaceKey("WS"); err != nil {
		t.Fatalf("set active workspace: %v", err)
	}

	out := captureWorkspaceStdout(t, func() {
		runWorkspaceList(&cobra.Command{}, nil)
	})
	if !strings.Contains(out, "WS") {
		t.Fatalf("workspace list output = %q", out)
	}

	status, err := loadWorkspaceOpsStatus(context.Background(), "WS")
	if err != nil {
		t.Fatalf("loadWorkspaceOpsStatus: %v", err)
	}
	if status.Workspace.Key != "WS" || len(status.Repos) != 1 {
		t.Fatalf("ops status = %+v", status)
	}
}
