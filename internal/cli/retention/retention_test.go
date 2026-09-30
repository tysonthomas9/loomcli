package retention

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/tysonthomas9/loomcli/internal/loomgit/agentcapture"
	"github.com/tysonthomas9/loomcli/internal/loomgit/driverfreeze"
	loomretention "github.com/tysonthomas9/loomcli/internal/loomgit/retention"
)

func TestRetentionCLIDefaultReportsEligibleCloneAsKeep(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source, copyPath, path := filepath.Join(root, "source"), filepath.Join(root, "A"), filepath.Join(root, "store.db")
	git := func(dir string, args ...string) string {
		command := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
		command.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s: %v", args, output, err)
		}
		return strings.TrimSpace(string(output))
	}
	git(root, "init", source)
	git(source, "commit", "--allow-empty", "-m", "base")
	base := git(source, "rev-parse", "HEAD")
	git(root, "clone", "--local", source, copyPath)
	git(copyPath, "commit", "--allow-empty", "-m", "agent work")
	if err := os.WriteFile(filepath.Join(copyPath, "untracked.txt"), []byte("captured"), 0o600); err != nil {
		t.Fatal(err)
	}
	captured, err := agentcapture.Capture(ctx, copyPath, "W", "A", "T", "task")
	if err != nil || !captured.Complete {
		t.Fatalf("capture: %+v, %v", captured, err)
	}
	revision, err := driverfreeze.FreezeCaptureAt(ctx, path, driverfreeze.CaptureRequest{
		Workspace: "W", Task: "T", Repo: "source", Attempt: "A", Worktree: copyPath,
		Base: base, CaptureSHA: captured.SHA, Outcome: "cancelled", Complete: true, SourceRepo: source,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(copyPath, "untracked.txt")); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`INSERT INTO landed_changes(workspace,change_id) VALUES('W',?)`, revision.Change); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE retained_task_copies SET eligible_at=? WHERE workspace='W' AND attempt='A'`,
		time.Now().Add(-8*24*time.Hour).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	cmd := *retentionCmd
	cmd.SetOut(&output)
	if err := runRetention(ctx, path, false, &cmd); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "keep W "+revision.Change+" "+copyPath+": clone cleanup is disabled") {
		t.Fatalf("default report promised deletion: %q", output.String())
	}
}

func TestRetentionCLIReportsIncompleteCopyWithoutRemovingIt(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	if _, err := loomretention.RunAt(ctx, path, false); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`INSERT INTO retained_task_copies
		(workspace,change_id,attempt,path,source_repo,complete)
		VALUES('W','C','A','/missing/copy','/missing/source',0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO landed_changes(workspace,change_id) VALUES('W','C')`); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	cmd := *retentionCmd
	cmd.SetOut(&output)
	if err := runRetention(ctx, path, true, &cmd); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "keep W C /missing/copy: capture incomplete") {
		t.Fatalf("unexpected report: %q", output.String())
	}
}
