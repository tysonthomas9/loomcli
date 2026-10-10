package doctor

import (
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

func TestDoctorReportsEligibleCloneAndFixRemovesIt(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source, copyPath, path := filepath.Join(root, "source"), filepath.Join(root, "A"), filepath.Join(root, "store.db")
	git := func(dir string, args ...string) string {
		command := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...) //nolint:norawexec // Scratch repositories prove retention behavior with real Git.
		command.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s: %v", args, output, err)
		}
		return strings.TrimSpace(string(output))
	}
	git(root, "init", "-b", "main", source)
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
	report := checkLoomGitRetention(ctx, path, false)
	if report.Status != StatusWarn || !strings.Contains(report.Summary, "loom doctor --fix") ||
		!strings.Contains(report.Detail, "remove W "+revision.Change+" "+copyPath+": dry run") {
		t.Fatalf("doctor report omitted eligible removal: %+v", report)
	}
	if _, err := os.Stat(copyPath); err != nil {
		t.Fatalf("report alone removed the clone: %v", err)
	}
	fixed := checkLoomGitRetention(ctx, path, true)
	if fixed.Status != StatusPass || !strings.Contains(fixed.Detail, "remove W "+revision.Change+" "+copyPath) {
		t.Fatalf("doctor --fix omitted removal: %+v", fixed)
	}
	if _, err := os.Stat(copyPath); !os.IsNotExist(err) {
		t.Fatalf("apply retained eligible clone: %v", err)
	}
}

func TestDoctorFixKeepsIncompleteCopy(t *testing.T) {
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
	report := checkLoomGitRetention(ctx, path, true)
	if !strings.Contains(report.Detail, "keep W C /missing/copy: capture incomplete") {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestDoctorRetentionWithoutAStore(t *testing.T) {
	report := checkLoomGitRetention(context.Background(), filepath.Join(t.TempDir(), "store.db"), false)
	if report.Status != StatusPass || report.Summary != "Loom Git retention: no records" {
		t.Fatalf("report=%+v", report)
	}
}
