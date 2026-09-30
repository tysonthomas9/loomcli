package git

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/loomgit/driverfreeze"
	"github.com/tysonthomas9/loomcli/internal/loomgit/taskcopy"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type abandonClaims struct{ backend.IssueBackend }

func (abandonClaims) CurrentIssueLockHolder(context.Context, string) (string, error) {
	return "agent", nil
}

func abandonGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...) //nolint:norawexec
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func TestAbandonCommandCapturesTaskRevision(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", root)
	t.Setenv("HOME", root)
	source, copyPath := filepath.Join(root, "source"), filepath.Join(root, "copy")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	abandonGit(t, source, "init", "-q")
	abandonGit(t, source, "config", "user.name", "Test")
	abandonGit(t, source, "config", "user.email", "test@example.test")
	if err := os.WriteFile(filepath.Join(source, "base"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	abandonGit(t, source, "add", "base")
	abandonGit(t, source, "commit", "-qm", "base")
	base := abandonGit(t, source, "rev-parse", "HEAD")
	journalPath := filepath.Join(root, "loomgit", "store.db")
	if _, err := taskcopy.CreateDetailedAt(context.Background(), journalPath, source, copyPath, "W", "A", "", base); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copyPath, "work"), []byte("saved\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := newAbandonCommand()
	deps, _, _, _, issueBackend := NewTestDeps(t)
	deps.IssueBackend = abandonClaims{IssueBackend: issueBackend}
	handle, err := cmdstore.OpenStore(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handle.Store.Workspaces().Create(context.Background(), store.WorkspaceCreate{Key: "W", Name: "W"}); err != nil {
		t.Fatal(err)
	}
	if err := handle.Close(); err != nil {
		t.Fatal(err)
	}
	cmd.SetContext(cli.WithDeps(context.Background(), deps))
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"T", "--workspace-id", "W", "--repo-name", "source", "--attempt", "A",
		"--task-copy", copyPath, "--source-repo", source, "--reason", "no longer needed"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Abandoned change") {
		t.Fatalf("command output: %s", output.String())
	}
	if len(issueBackend.Calls) == 0 {
		t.Fatal("CLI did not release task claim")
	}
	change, err := driverfreeze.ChangeForTaskAt(context.Background(), journalPath, "W", "T", "source")
	if err != nil {
		t.Fatal(err)
	}
	ref := "refs/loom/ws/W/change/" + change + "/1/head"
	if got := abandonGit(t, source, "show", ref+":work"); got != "saved" {
		t.Fatalf("captured work = %q", got)
	}
}
