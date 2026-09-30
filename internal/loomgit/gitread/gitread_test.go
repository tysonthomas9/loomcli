package gitread

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/capture"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/changeset"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

type repoList []loomgit.WorkspaceRepo

func (l repoList) WorkspaceRepos(context.Context, string) ([]loomgit.WorkspaceRepo, error) {
	return l, nil
}

func run(t *testing.T, r *gitexec.Runner, args ...string) string {
	t.Helper()
	out, err := r.Run(context.Background(), args...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, name, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func setup(t *testing.T) (string, *gitexec.Runner, *journal.SQLite, *Reader) {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil { //nolint:norawexec // Real temporary Git fixture.
		t.Fatalf("init: %v %s", err, out)
	}
	config := filepath.Join(t.TempDir(), "config")
	write(t, filepath.Dir(config), filepath.Base(config), "[user]\nname = Test\nemail = test@example.com\n")
	r, err := gitexec.New(dir, gitexec.Options{GlobalConfig: config, SystemConfig: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	store, err := journal.OpenSQLite(filepath.Join(t.TempDir(), "journal.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	write(t, dir, "base.txt", "base\n")
	run(t, r, "add", "base.txt")
	run(t, r, "commit", "-qm", "base")
	reader := &Reader{Revisions: store, Workspaces: repoList{{Workspace: "W", Repo: "repo"}},
		OpenRepo: func(_, _ string) (loomgit.RepoStore, error) { return r, nil }}
	return dir, r, store, reader
}

func freeze(t *testing.T, dir string, r *gitexec.Runner, store *journal.SQLite, base, request string) loomgit.Revision {
	t.Helper()
	cap, err := capture.Capture(context.Background(), r, dir, capture.Params{
		Workspace: "W", Attempt: request, TaskID: "task", TaskTitle: "change"})
	if err != nil {
		t.Fatal(err)
	}
	rev, err := changeset.FreezeSource(context.Background(), store, r, changeset.SourceInput{
		Workspace: "W", Change: "C", RequestID: request, Attempt: request, TaskID: "task",
		BaseSHA: base, CaptureSHA: cap.CaptureSHA, Outcome: "completed", Complete: cap.Manifest.Complete})
	if err != nil {
		t.Fatal(err)
	}
	return rev
}

func findFile(t *testing.T, diff Diff, path string) File {
	t.Helper()
	for _, file := range diff.Files {
		if file.Path == path {
			return file
		}
	}
	t.Fatalf("%q absent from %+v", path, diff.Files)
	return File{}
}

func TestSnapshotDiffInterdiffBudgetAndMissingBase(t *testing.T) {
	dir, runner, store, reader := setup(t)
	base := run(t, runner, "rev-parse", "HEAD")
	write(t, dir, "committed.txt", "agent commit\n")
	run(t, runner, "add", "committed.txt")
	run(t, runner, "commit", "-qm", "agent")
	write(t, dir, "uncommitted.txt", "Snapshot content\n")
	r1 := freeze(t, dir, runner, store, base, "A1")
	diff, err := reader.Diff(context.Background(), "W", "C", "repo", r1.Number)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"committed.txt", "uncommitted.txt"} {
		if file := findFile(t, diff, path); len(file.Hunks) == 0 || file.Truncated {
			t.Fatalf("%s: %+v", path, file)
		}
	}
	if _, err := reader.Diff(context.Background(), "W", "C", "foreign", 1); !errors.Is(err, loomgit.NewError(loomgit.RepoSelectionRequired, "", nil)) {
		t.Fatalf("foreign repo: %v", err)
	}
	write(t, dir, "uncommitted.txt", "Snapshot content revised\n")
	write(t, dir, "large.txt", strings.Repeat("long line that changes\n", 65000))
	r2 := freeze(t, dir, runner, store, base, "A2")
	diff, err = reader.Diff(context.Background(), "W", "C", "repo", r2.Number)
	if err != nil {
		t.Fatal(err)
	}
	if diff.InterdiffAgainst != 1 {
		t.Fatalf("interdiff offer: %+v", diff)
	}
	big := findFile(t, diff, "large.txt")
	if !big.Truncated || big.PatchSize <= DefaultFileBudget || big.Patch != "" || len(big.Hunks) != 0 {
		t.Fatalf("large file: %+v", big)
	}
	if small := findFile(t, diff, "uncommitted.txt"); len(small.Hunks) == 0 {
		t.Fatalf("small file: %+v", small)
	}
	between, err := reader.Interdiff(context.Background(), "W", "C", "repo", r2.Number, r1.Number)
	if err != nil {
		t.Fatal(err)
	}
	if file := findFile(t, between, "uncommitted.txt"); len(file.Hunks) == 0 {
		t.Fatalf("interdiff: %+v", file)
	}
	baseRef, _, err := refs(r1)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", dir, "update-ref", "-d", baseRef).CombinedOutput(); err != nil { //nolint:norawexec // Remove one fixture ref to test fail-closed read.
		t.Fatalf("delete fixture ref: %v %s", err, out)
	}
	if _, err := reader.Diff(context.Background(), "W", "C", "repo", r1.Number); !errors.Is(err, loomgit.NewError(loomgit.BaseRefUnresolvable, "", nil)) {
		t.Fatalf("missing base: %v", err)
	}
}

func TestOutputCapStillListsLargeFileAndLaterFiles(t *testing.T) {
	dir, runner, _, reader := setup(t)
	base := run(t, runner, "rev-parse", "HEAD")
	write(t, dir, "a-large.txt", strings.Repeat("changed line\n", 100))
	write(t, dir, "z-small.txt", "small\n")
	run(t, runner, "add", "a-large.txt", "z-small.txt")
	run(t, runner, "commit", "-qm", "change")
	head := run(t, runner, "rev-parse", "HEAD")
	capped, err := gitexec.New(dir, gitexec.Options{ReadOnly: true, OutputCap: 512})
	if err != nil {
		t.Fatal(err)
	}
	reader.FileBudget = 256
	files, err := reader.files(context.Background(), capped, base, head)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Path != "a-large.txt" || !files[0].Truncated || files[0].PatchSize != len(strings.Repeat("changed line\n", 100)) || files[0].Patch != "" || len(files[0].Hunks) != 0 {
		t.Fatalf("large file was dropped or exposed: %+v", files)
	}
	if files[1].Path != "z-small.txt" || files[1].Truncated || len(files[1].Hunks) == 0 {
		t.Fatalf("later file was dropped: %+v", files)
	}
}

func TestDerivedRevisionOffersRangeDiff(t *testing.T) {
	dir, runner, store, reader := setup(t)
	base := run(t, runner, "rev-parse", "HEAD")
	write(t, dir, "change.txt", "source\n")
	run(t, runner, "add", "change.txt")
	run(t, runner, "commit", "-qm", "source")
	write(t, dir, "capture.txt", "saved by Snapshot\n")
	r1 := freeze(t, dir, runner, store, base, "A1")
	run(t, runner, "checkout", "-qB", "derived", base)
	write(t, dir, "change.txt", "replayed\n")
	run(t, runner, "add", "change.txt")
	run(t, runner, "commit", "-qm", "source")
	r2, err := changeset.RecordDerived(context.Background(), store, runner, changeset.DerivedInput{
		Workspace: "W", Change: "C", RequestID: "D1", FromNumber: r1.Number,
		Operation: "restack", BaseSHA: base, HeadSHA: run(t, runner, "rev-parse", "HEAD"), Outcome: "completed"})
	if err != nil {
		t.Fatal(err)
	}
	diff, err := reader.Diff(context.Background(), "W", "C", "repo", r2.Number)
	if err != nil {
		t.Fatal(err)
	}
	if diff.DerivedFromChange != "C" || diff.DerivedFromNumber != 1 || !diff.DerivedRangeDiffAvailable || diff.DerivedRangeDiff == "" {
		t.Fatalf("range-diff missing: %+v", diff)
	}
}
