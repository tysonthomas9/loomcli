package changeset

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/capture"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

func fixture(t *testing.T) (string, *gitexec.Runner, *journal.SQLite) {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil { //nolint:norawexec // Test fixture creates a real temporary repository.
		t.Fatalf("git init: %v: %s", err, out)
	}
	config := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(config, []byte("[user]\nname = Test\nemail = test@example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runner, err := gitexec.New(dir, gitexec.Options{GlobalConfig: config, SystemConfig: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	store, err := journal.OpenSQLite(filepath.Join(t.TempDir(), "changes.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	write(t, dir, "base", "base")
	must(t, runner, "add", "base")
	must(t, runner, "commit", "-qm", "base")
	return dir, runner, store
}

func write(t *testing.T, dir, name, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}

func must(t *testing.T, runner *gitexec.Runner, args ...string) string {
	t.Helper()
	out, err := runner.Run(context.Background(), args...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func source(t *testing.T, dir string, runner *gitexec.Runner, store *journal.SQLite, request string) (loomgit.Revision, string) {
	t.Helper()
	base := must(t, runner, "rev-parse", "HEAD")
	write(t, dir, "edit", request)
	result, err := capture.Capture(context.Background(), runner, dir, capture.Params{
		Workspace: "W", Attempt: request, TaskID: "task", TaskTitle: "change"})
	if err != nil {
		t.Fatal(err)
	}
	rev, err := FreezeSource(context.Background(), store, runner, SourceInput{
		Workspace: "W", Change: "C", RequestID: request, Attempt: request,
		TaskID: "task", BaseSHA: base, CaptureSHA: result.CaptureSHA,
		Outcome: "completed", Complete: result.Manifest.Complete})
	if err != nil {
		t.Fatal(err)
	}
	return rev, result.CaptureSHA
}

func TestFreezeSourceStoresRewrittenChainAndReplay(t *testing.T) {
	dir, runner, store := fixture(t)
	base := must(t, runner, "rev-parse", "HEAD")
	write(t, dir, "committed", "agent")
	must(t, runner, "add", "committed")
	must(t, runner, "commit", "-qm", "agent work")
	write(t, dir, "edit", "working edit")
	result, err := capture.Capture(context.Background(), runner, dir, capture.Params{
		Workspace: "W", Attempt: "A", TaskID: "task", TaskTitle: "change"})
	if err != nil {
		t.Fatal(err)
	}
	in := SourceInput{Workspace: "W", Change: "C", RequestID: "request-1", Attempt: "A",
		TaskID: "task", BaseSHA: base, CaptureSHA: result.CaptureSHA, Outcome: "completed", Complete: result.Manifest.Complete}
	rev, err := FreezeSource(context.Background(), store, runner, in)
	if err != nil {
		t.Fatal(err)
	}
	if rev.Number != 1 || rev.Kind != "source" || rev.Outcome != "completed" || !rev.Ready ||
		rev.BaseSHA != base || rev.HeadSHA == result.CaptureSHA || rev.TreeHash != must(t, runner, "rev-parse", result.CaptureSHA+"^{tree}") {
		t.Fatalf("bad source revision: %+v", rev)
	}
	if must(t, runner, "rev-parse", "refs/loom/ws/W/change/C/1/base") != base ||
		must(t, runner, "rev-parse", "refs/loom/ws/W/change/C/1/head") != rev.HeadSHA ||
		must(t, runner, "rev-parse", result.CaptureRef) != result.CaptureSHA {
		t.Fatal("source refs or original capture changed")
	}
	stored, err := store.GetRevision(context.Background(), "W", "C", 1)
	if err != nil || stored != rev {
		t.Fatalf("stored revision: %+v, %v", stored, err)
	}
	for _, commit := range strings.Fields(must(t, runner, "rev-list", "--reverse", base+".."+rev.HeadSHA)) {
		if !strings.Contains(must(t, runner, "show", "-s", "--format=%B", commit), "Loom-Revision: 1") {
			t.Fatal("missing revision trailer")
		}
	}
	again, err := FreezeSource(context.Background(), store, runner, in)
	if err != nil || again.Number != 1 || again.HeadSHA != rev.HeadSHA {
		t.Fatalf("replay: %+v, %v", again, err)
	}
}

func TestIncompleteCaptureFreezesAsInterruptedOutcome(t *testing.T) {
	dir, runner, store := fixture(t)
	base := must(t, runner, "rev-parse", "HEAD")
	write(t, dir, "edit", "partial work")
	result, err := capture.Capture(context.Background(), runner, dir, capture.Params{
		Workspace: "W", Attempt: "A", TaskID: "task", TaskTitle: "change"})
	if err != nil {
		t.Fatal(err)
	}
	in := SourceInput{Workspace: "W", Change: "C", RequestID: "cancel", Attempt: "A",
		TaskID: "task", BaseSHA: base, CaptureSHA: result.CaptureSHA, Complete: false}
	in.Outcome = "running"
	in.RequestID = "running"
	if _, err := FreezeSource(context.Background(), store, runner, in); !errors.Is(err, loomgit.NewError(loomgit.CaptureIncomplete, "", nil)) {
		t.Fatalf("incomplete capture without a terminal outcome = %v, want capture_incomplete", err)
	}
	// D18: a successful run that left an untracked secret-pattern path out
	// freezes as an incomplete completed revision.
	in.Outcome = "completed"
	in.RequestID = "completed"
	completed, err := FreezeSource(context.Background(), store, runner, in)
	if err != nil || !completed.Ready || !completed.Incomplete || completed.NoChanges || completed.Outcome != "completed" {
		t.Fatalf("completed incomplete revision = %+v, %v", completed, err)
	}
	in.Outcome = "failed"
	in.RequestID = "failed"
	failed, err := FreezeSource(context.Background(), store, runner, in)
	if err != nil || !failed.Ready || !failed.Incomplete || failed.Outcome != "failed" {
		t.Fatalf("failed incomplete revision = %+v, %v", failed, err)
	}
	in.Outcome = "cancelled"
	in.RequestID = "cancel"
	rev, err := FreezeSource(context.Background(), store, runner, in)
	if err != nil || !rev.Ready || !rev.Incomplete || rev.Outcome != "cancelled" {
		t.Fatalf("cancelled incomplete revision = %+v, %v", rev, err)
	}
	stored, err := store.GetRevision(context.Background(), "W", "C", rev.Number)
	if err != nil || !stored.Incomplete {
		t.Fatalf("incomplete flag not persisted: %+v, %v", stored, err)
	}
	if got := must(t, runner, "show", rev.HeadSHA+":edit"); got != "partial work" {
		t.Fatalf("captured edit = %q", got)
	}
	derived, err := RecordDerived(context.Background(), store, runner, DerivedInput{
		Workspace: "W", Change: "C", RequestID: "derived-cancel", FromNumber: rev.Number,
		Operation: "apply", BaseSHA: base, HeadSHA: rev.HeadSHA, Outcome: "cancelled",
	})
	if err != nil || !derived.Incomplete {
		t.Fatalf("derived revision lost incomplete flag: %+v, %v", derived, err)
	}
}

func TestImportRejectsMismatchedTreeBeforeRefsAndKeepsSource(t *testing.T) {
	dir, runner, store := fixture(t)
	base := must(t, runner, "rev-parse", "HEAD")
	write(t, dir, "edit", "one")
	must(t, runner, "add", "edit")
	must(t, runner, "commit", "-qm", "imported")
	head := must(t, runner, "rev-parse", "HEAD")
	_, err := ImportSource(context.Background(), store, runner, ImportInput{Workspace: "W", Change: "C",
		RequestID: "import", BaseSHA: base, HeadSHA: head, TreeHash: must(t, runner, "rev-parse", base+"^{tree}"), Outcome: "completed"})
	var coded *loomgit.Error
	if !errors.As(err, &coded) || coded.Code() != string(loomgit.HashMismatch) {
		t.Fatalf("wanted hash mismatch, got %v", err)
	}
	if _, err := store.GetRevision(context.Background(), "W", "C", 1); !errors.Is(err, journal.ErrNotFound) {
		t.Fatalf("revision was reserved: %v", err)
	}
	if _, err := runner.Run(context.Background(), "show-ref", "--verify", "refs/loom/ws/W/change/C/1/head"); err == nil {
		t.Fatal("mismatched import wrote revision ref")
	}
	if _, err := runner.Run(context.Background(), "show-ref", "--verify", "refs/loom/ws/W/change/C/1/base"); err == nil {
		t.Fatal("mismatched import wrote base ref")
	}
	if got := must(t, runner, "rev-parse", "HEAD"); got != head {
		t.Fatal("import source moved")
	}
}

func TestImportMatchingTreeAndSecondSourceRevision(t *testing.T) {
	dir, runner, store := fixture(t)
	base := must(t, runner, "rev-parse", "HEAD")
	write(t, dir, "edit", "first")
	must(t, runner, "add", "edit")
	must(t, runner, "commit", "-qm", "first")
	head := must(t, runner, "rev-parse", "HEAD")
	in := ImportInput{Workspace: "W", Change: "C", RequestID: "import-1",
		BaseSHA: base, HeadSHA: head, TreeHash: must(t, runner, "rev-parse", head+"^{tree}"), Outcome: "completed"}
	r1, err := ImportSource(context.Background(), store, runner, in)
	if err != nil || r1.Number != 1 || r1.HeadSHA != head || !r1.Ready {
		t.Fatalf("import: %+v %v", r1, err)
	}
	write(t, dir, "next", "second")
	must(t, runner, "add", "next")
	must(t, runner, "commit", "-qm", "second")
	r2, err := ImportSource(context.Background(), store, runner, ImportInput{Workspace: "W", Change: "C",
		RequestID: "import-2", BaseSHA: base, HeadSHA: must(t, runner, "rev-parse", "HEAD"),
		TreeHash: must(t, runner, "rev-parse", "HEAD^{tree}"), Outcome: "failed"})
	if err != nil || r2.Number != 2 || r2.Outcome != "failed" {
		t.Fatalf("second import: %+v %v", r2, err)
	}
	again, err := ImportSource(context.Background(), store, runner, in)
	if err != nil || again != r1 {
		t.Fatalf("import replay: %+v %v", again, err)
	}
	if must(t, runner, "rev-parse", "refs/loom/ws/W/change/C/1/head") != head {
		t.Fatal("second revision changed first ref")
	}
}

func TestDerivedOperationsAndNextAttemptKeepOldRevision(t *testing.T) {
	dir, runner, store := fixture(t)
	r1, captureSHA := source(t, dir, runner, store, "attempt-1")
	oldHead := must(t, runner, "rev-parse", "refs/loom/ws/W/change/C/1/head")
	for i, op := range []string{"apply", "restack", "pull", "reorder", "unapply"} {
		got, err := RecordDerived(context.Background(), store, runner, DerivedInput{Workspace: "W", Change: "C",
			RequestID: op, FromNumber: r1.Number, Operation: op, BaseSHA: r1.BaseSHA,
			HeadSHA: r1.HeadSHA, Outcome: "completed"})
		if err != nil || got.Number != i+2 || got.Kind != "derived" || got.DerivedFromChange != "C" ||
			got.DerivedFromNumber != 1 || got.Operation != op || !got.Ready {
			t.Fatalf("%s: %+v %v", op, got, err)
		}
		if must(t, runner, "rev-parse", "refs/loom/ws/W/change/C/"+strconv.Itoa(got.Number)+"/head") != got.HeadSHA {
			t.Fatal("derived head ref missing")
		}
	}
	if must(t, runner, "rev-parse", "refs/loom/ws/W/change/C/1/head") != oldHead ||
		must(t, runner, "rev-parse", "refs/loom/ws/W/change/C/1/base") != r1.BaseSHA {
		t.Fatal("source refs changed")
	}
	if must(t, runner, "rev-parse", "refs/loom/ws/W/attempt/attempt-1/capture") != captureSHA {
		t.Fatal("original capture changed")
	}
	write(t, dir, "later", "second attempt")
	must(t, runner, "add", "later")
	must(t, runner, "commit", "-qm", "second attempt")
	r2, err := FreezeSource(context.Background(), store, runner, SourceInput{Workspace: "W", Change: "C",
		RequestID: "attempt-2", Attempt: "attempt-2", TaskID: "task", BaseSHA: r1.BaseSHA,
		CaptureSHA: must(t, runner, "rev-parse", "HEAD"), Outcome: "failed", Complete: true})
	if err != nil || r2.Number != 7 || r2.Kind != "source" || r2.Outcome != "failed" {
		t.Fatalf("later attempt: %+v %v", r2, err)
	}
	if must(t, runner, "rev-parse", "refs/loom/ws/W/change/C/1/head") != oldHead {
		t.Fatal("later attempt changed r1")
	}
}

// D29 (4): an attempt that changed nothing freezes as a "no changes" source
// revision, whether it made no commits or commits that net to nothing.
func TestFreezeSourceMarksNoChangesOnlyForCompleteEmptySource(t *testing.T) {
	ctx := context.Background()
	dir, runner, store := fixture(t)
	base := must(t, runner, "rev-parse", "HEAD")
	freeze := func(request, capture string, complete bool, outcome string) loomgit.Revision {
		t.Helper()
		rev, err := FreezeSource(ctx, store, runner, SourceInput{Workspace: "W", Change: "C", RequestID: request,
			Attempt: request, TaskID: "task", BaseSHA: base, CaptureSHA: capture, Outcome: outcome, Complete: complete})
		if err != nil {
			t.Fatal(err)
		}
		return rev
	}
	if r := freeze("no-commits", base, true, "completed"); !r.NoChanges || r.HeadSHA != base {
		t.Fatalf("zero-commit attempt: %+v", r)
	}
	must(t, runner, "commit", "--allow-empty", "-qm", "nothing")
	if r := freeze("empty-commit", must(t, runner, "rev-parse", "HEAD"), true, "completed"); !r.NoChanges {
		t.Fatalf("empty commit attempt: %+v", r)
	}
	if r := freeze("incomplete", base, false, "cancelled"); r.NoChanges || !r.Incomplete {
		t.Fatalf("incomplete capture must follow the incomplete path: %+v", r)
	}
	write(t, dir, "edit", "work")
	must(t, runner, "add", "edit")
	must(t, runner, "commit", "-qm", "work")
	changed := freeze("changed", must(t, runner, "rev-parse", "HEAD"), true, "completed")
	if changed.NoChanges {
		t.Fatalf("changed attempt marked no changes: %+v", changed)
	}
	stored, err := store.GetRevision(ctx, "W", "C", 1)
	if err != nil || !stored.NoChanges {
		t.Fatalf("no_changes not persisted: %+v %v", stored, err)
	}
	// An applied layer that nets to nothing (base == head) is derived and is
	// never "no changes" (P4.1c's empty derived revision is a separate bug).
	derived, err := RecordDerived(ctx, store, runner, DerivedInput{Workspace: "W", Change: "C", RequestID: "derived",
		FromNumber: changed.Number, Operation: "apply", BaseSHA: changed.HeadSHA, HeadSHA: changed.HeadSHA, Outcome: "completed"})
	if err != nil || derived.NoChanges || derived.Kind != "derived" {
		t.Fatalf("derived revision: %+v %v", derived, err)
	}
	imported, err := ImportSource(ctx, store, runner, ImportInput{Workspace: "W", Change: "I", RequestID: "import-empty",
		BaseSHA: base, HeadSHA: base, TreeHash: must(t, runner, "rev-parse", base+"^{tree}"), Outcome: "completed"})
	if err != nil || !imported.NoChanges {
		t.Fatalf("empty import: %+v %v", imported, err)
	}
}
