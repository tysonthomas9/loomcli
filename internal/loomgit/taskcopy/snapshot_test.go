package taskcopy_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/agentcapture"
	"github.com/tysonthomas9/loomcli/internal/loomgit/driverfreeze"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
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

func TestResumeStartsFreshCopyAtCaptureWithOriginalBase(t *testing.T) {
	source, oldCopy, journal, base := fixture(t)
	ctx := context.Background()
	if _, err := taskcopy.CreateDetailedAt(ctx, journal, source, oldCopy, "W", "A", "", base); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldCopy, "untracked"), []byte("earlier work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	captured, err := agentcapture.Capture(ctx, oldCopy, "W", "A", "T", "task")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := driverfreeze.FreezeCaptureAt(ctx, journal, driverfreeze.CaptureRequest{
		Workspace: "W", Task: "T", Repo: "source", Attempt: "A", Worktree: oldCopy,
		Base: base, CaptureSHA: captured.SHA, Outcome: "failed", Complete: captured.Complete, SourceRepo: source,
	}); err != nil {
		t.Fatal(err)
	}
	newCopy := filepath.Join(filepath.Dir(oldCopy), "resumed")
	resumed, err := taskcopy.ResumeDetailedAt(ctx, journal, source, newCopy, "W", "B", "A")
	if err != nil {
		t.Fatal(err)
	}
	if resumed.BaseSHA != base || git(t, source, "rev-parse", "refs/loom/ws/W/attempt/B/base") != base {
		t.Fatalf("resume base = %s, want original %s", resumed.BaseSHA, base)
	}
	if got := git(t, newCopy, "show", "HEAD:untracked"); got != "earlier work" {
		t.Fatalf("resume omitted captured work: %q", got)
	}
	if got := git(t, newCopy, "merge-base", base, "HEAD"); got != base {
		t.Fatalf("resume head left original base: %s", got)
	}
	if data, err := os.ReadFile(filepath.Join(oldCopy, "untracked")); err != nil || string(data) != "earlier work\n" {
		t.Fatalf("old copy changed: %q, %v", data, err)
	}
}

func TestConflictResolutionCopyCreatesNewRevisionForReview(t *testing.T) {
	source, copyPath, _, base := fixture(t)
	ctx := context.Background()
	configDir := filepath.Dir(source)
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	journalPath := filepath.Join(configDir, "loomgit", "store.db")
	if err := os.MkdirAll(filepath.Dir(journalPath), 0700); err != nil {
		t.Fatal(err)
	}
	store, err := journal.OpenSQLite(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	git(t, source, "checkout", "-q", "-b", "agent", base)
	if err := os.WriteFile(filepath.Join(source, "user-only"), []byte("uncommitted user work\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, source, "add", "user-only")
	git(t, source, "-c", "user.name=Loom", "-c", "user.email=loom@localhost", "commit", "-qm", "workspace WIP")
	wip := git(t, source, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(source, "tracked"), []byte("agent\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, source, "add", "tracked")
	git(t, source, "-c", "user.name=Agent", "-c", "user.email=agent@example.test", "commit", "-qm", "agent")
	agentHead := git(t, source, "rev-parse", "HEAD")
	git(t, source, "checkout", "-q", "-b", "loom/ws/W/interactive/L", base)
	if err := os.WriteFile(filepath.Join(source, "tracked"), []byte("lead\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, source, "add", "tracked")
	git(t, source, "-c", "user.name=Lead", "-c", "user.email=lead@example.test", "commit", "-qm", "lead")
	leadTip := git(t, source, "rev-parse", "HEAD")
	if _, err := store.DriverChange(ctx, "W", "T", "source", "C"); err != nil {
		t.Fatal(err)
	}
	revision, err := store.ReserveRevision(ctx, loomgit.Revision{Workspace: "W", Change: "C", RequestID: "agent-1", Kind: "source", Operation: "snapshot", Outcome: "completed", BaseSHA: wip, TreeHash: git(t, source, "rev-parse", agentHead+"^{tree}"), SourceHeadSHA: agentHead})
	if err != nil {
		t.Fatal(err)
	}
	revision.HeadSHA = agentHead
	if err := store.FinishRevision(ctx, revision); err != nil {
		t.Fatal(err)
	}
	if _, err := review.Submit(ctx, store, "W", "C", 1, agentHead, "approve", "", review.Actor{Kind: "human", ID: "reviewer"}); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcopy.CreateDetailedAt(ctx, journalPath, source, copyPath, "W", "resolution", "", leadTip); err != nil {
		t.Fatal(err)
	}
	if err := taskcopy.PrepareConflictResolution(ctx, source, copyPath, "W", "L", "T", "source", "C", 1); err != nil {
		t.Fatal(err)
	}
	if got := git(t, copyPath, "rev-parse", "HEAD"); got != leadTip {
		t.Fatalf("resolution copy base = %s, want lead tip %s", got, leadTip)
	}
	if got := git(t, copyPath, "ls-files", "user-only"); got != "" {
		t.Fatalf("WIP-only file entered resolution copy: %q", got)
	}
	if got := git(t, copyPath, "ls-files", "-u"); !strings.Contains(got, "tracked") {
		t.Fatalf("conflict is not exposed in task copy: %q", got)
	}
	if got := git(t, source, "status", "--porcelain"); got != "" {
		t.Fatalf("lead checkout changed: %q", got)
	}
	if err := os.WriteFile(filepath.Join(copyPath, "tracked"), []byte("resolved\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, copyPath, "add", "tracked")
	git(t, copyPath, "-c", "user.name=Agent", "-c", "user.email=agent@example.test", "commit", "-qm", "resolve")
	patch := git(t, copyPath, "diff", "--binary", leadTip, "HEAD") + "\n"
	next, err := driverfreeze.FreezeAt(ctx, journalPath, driverfreeze.Request{Workspace: "W", Task: "T", Repo: "source", Attempt: "resolution", Worktree: copyPath, Base: leadTip, Patch: []byte(patch), Outcome: "completed"})
	if err != nil {
		t.Fatal(err)
	}
	if next.Change != "C" || next.Number != 2 || next.BaseSHA != leadTip {
		t.Fatalf("resolution revision = %+v", next)
	}
	if err := review.RequireVerdict(ctx, store, "W", "C", 2, next.HeadSHA, "apply", "L"); err == nil {
		t.Fatal("resolution revision bypassed review")
	}
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
