package taskcopy_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit/agentcapture"
	"github.com/tysonthomas9/loomcli/internal/loomgit/driverfreeze"
	"github.com/tysonthomas9/loomcli/internal/loomgit/taskcopy"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec // Real temporary Git repositories are the test subject.
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func fixture(t *testing.T) (string, string, string, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	source, copyPath := filepath.Join(root, "source"), filepath.Join(root, "copy")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	git(t, source, "init", "-q")
	if err := os.WriteFile(filepath.Join(source, "tracked"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, source, "add", "tracked")
	git(t, source, "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "-qm", "base")
	return source, copyPath, filepath.Join(root, "journal.db"), git(t, source, "rev-parse", "HEAD")
}

func captureCopy(t *testing.T, source, copyPath, journal, base string, importSource bool) (string, int) {
	t.Helper()
	ctx := context.Background()
	created, err := taskcopy.CreateDetailedAt(ctx, journal, source, copyPath, "W", "A", "", base)
	if err != nil {
		t.Fatal(err)
	}
	if created.Kind != "cow" {
		t.Skipf("CoW unavailable: %s (%s)", created.Kind, created.Reason)
	}
	if err := os.WriteFile(filepath.Join(copyPath, "committed"), []byte("committed work\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, copyPath, "add", "committed")
	git(t, copyPath, "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "-qm", "agent")
	if err := os.WriteFile(filepath.Join(copyPath, "untracked"), []byte("untracked work\n"), 0600); err != nil {
		t.Fatal(err)
	}
	captured, err := agentcapture.Capture(ctx, copyPath, "W", "A", "T", "task")
	if err != nil {
		t.Fatal(err)
	}
	sourceRepo := ""
	if importSource {
		sourceRepo = source
	}
	revision, err := driverfreeze.FreezeCaptureAt(ctx, journal, driverfreeze.CaptureRequest{
		Workspace: "W", Task: "T", Repo: "source", Attempt: "A", Worktree: copyPath,
		Base: base, CaptureSHA: captured.SHA, Outcome: "cancelled", Complete: captured.Complete, SourceRepo: sourceRepo,
	})
	if err != nil {
		t.Fatal(err)
	}
	return revision.Change, revision.Number
}

func TestP118SnapshotImportsCaptureAndRevision(t *testing.T) {
	source, copyPath, journal, base := fixture(t)
	change, revision := captureCopy(t, source, copyPath, journal, base, true)
	refs := []string{"refs/loom/ws/W/attempt/A/capture", "refs/loom/ws/W/change/" + change + "/" + strconv.Itoa(revision) + "/base", "refs/loom/ws/W/change/" + change + "/" + strconv.Itoa(revision) + "/head"}
	for _, ref := range refs {
		if got, want := git(t, source, "rev-parse", ref+"^{tree}"), git(t, copyPath, "rev-parse", ref+"^{tree}"); got != want {
			t.Fatalf("%s tree = %s, want %s", ref, got, want)
		}
	}
}

func TestP118FailedFetchKeepsCloneAndSourceRefsUnchanged(t *testing.T) {
	source, copyPath, journal, base := fixture(t)
	change, revision := captureCopy(t, source, copyPath, journal, base, false)
	head := "refs/loom/ws/W/change/" + change + "/" + strconv.Itoa(revision) + "/head"
	if err := os.WriteFile(filepath.Join(source, "conflict"), []byte("different work"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, source, "add", "conflict")
	git(t, source, "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "-qm", "conflict")
	conflict := git(t, source, "rev-parse", "HEAD")
	git(t, source, "update-ref", head, conflict)
	captureSHA := git(t, copyPath, "rev-parse", "refs/loom/ws/W/attempt/A/capture")
	if _, err := driverfreeze.FreezeCaptureAt(context.Background(), journal, driverfreeze.CaptureRequest{
		Workspace: "W", Task: "T", Repo: "source", Attempt: "A", Worktree: copyPath,
		Base: base, CaptureSHA: captureSHA, Outcome: "cancelled", Complete: true, SourceRepo: source,
	}); err == nil {
		t.Fatal("conflicting fetch unexpectedly succeeded")
	}
	if got := git(t, source, "rev-parse", head); got != conflict {
		t.Fatalf("source revision ref changed: %s", got)
	}
	if out := git(t, source, "for-each-ref", "--format=%(refname)", "refs/loom/ws/W/attempt/A/capture"); out != "" {
		t.Fatalf("capture ref installed after failed fetch: %s", out)
	}
	if _, err := os.Stat(copyPath); err != nil {
		t.Fatalf("retained task copy missing: %v", err)
	}
}

func TestP118PatchFreezeImportsRevisionWithoutCapture(t *testing.T) {
	source, copyPath, journal, base := fixture(t)
	created, err := taskcopy.CreateDetailedAt(context.Background(), journal, source, copyPath, "W", "A", "", base)
	if err != nil {
		t.Fatal(err)
	}
	if created.Kind != "cow" {
		t.Skipf("CoW unavailable: %s", created.Kind)
	}
	if err := os.WriteFile(filepath.Join(copyPath, "tracked"), []byte("agent edit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	patch := git(t, copyPath, "diff", "--binary") + "\n"
	revision, err := driverfreeze.FreezeAt(context.Background(), journal, driverfreeze.Request{
		Workspace: "W", Task: "T", Repo: "source", Attempt: "A", Worktree: copyPath,
		Base: base, Patch: []byte(patch), Outcome: "completed", SourceRepo: source,
	})
	if err != nil {
		t.Fatal(err)
	}
	head := "refs/loom/ws/W/change/" + revision.Change + "/" + strconv.Itoa(revision.Number) + "/head"
	if got := git(t, source, "rev-parse", head+"^{tree}"); got != git(t, copyPath, "rev-parse", head+"^{tree}") {
		t.Fatalf("source revision tree = %s", got)
	}
	if got := git(t, source, "show", "HEAD:tracked"); got != "base" {
		t.Fatalf("source checkout changed: %q", got)
	}
}
