package pull

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

// replaceFixture is lead L's working area with layers C1 and C2 applied, and
// an approved fix-up revision 2 of C1 made on C1's head.
func replaceFixture(t *testing.T, files map[string]string) (*fixture, loomgit.Revision) {
	t.Helper()
	f := newFixture(t)
	preparePull(t, f)
	if _, err := f.apply(t); err != nil {
		t.Fatal(err)
	}
	addRestackLayer(t, f, 2, f.source, "second", "two\n")
	path := filepath.Join(t.TempDir(), "fixup")
	f.git(t, "worktree", "add", "-q", "--detach", path, f.source)
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(path, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		trunkGit(t, path, "add", name)
	}
	trunkGit(t, path, "commit", "-qm", "fix-up")
	head := trunkGit(t, path, "rev-parse", "HEAD")
	ctx := context.Background()
	revision, err := f.store.ReserveRevision(ctx, loomgit.Revision{Workspace: "W", Change: "C1",
		RequestID: "fixup-source", Kind: "source", Operation: "snapshot", Outcome: "completed",
		BaseSHA: f.source, TreeHash: f.git(t, "rev-parse", head+"^{tree}"), SourceHeadSHA: head})
	if err != nil {
		t.Fatal(err)
	}
	revision.HeadSHA = head
	if err := f.store.FinishRevision(ctx, revision); err != nil {
		t.Fatal(err)
	}
	if _, err := review.Submit(ctx, f.store, "W", "C1", revision.Number, head, "approve", "", review.Actor{Kind: "human", ID: "reviewer"}); err != nil {
		t.Fatal(err)
	}
	return f, revision
}

// crashedCleanupStore stands in for a process that died mid-restack: the
// in-process cleanup of the revisions it recorded never happens.
type crashedCleanupStore struct{ Store }

func (crashedCleanupStore) AbortRestack(context.Context, string, string, string, []string) error {
	return errors.New("process gone")
}

func replaceRequest(base string, revision loomgit.Revision, requestID string) RestackRequest {
	return RestackRequest{Workspace: "W", Lead: "L", Repo: "repo", BaseSHA: base, RequestID: requestID,
		ReplaceChange: "C1", ReplaceRevision: revision.Number}
}

func derivedCount(t *testing.T, f *fixture, change string) int {
	t.Helper()
	count := 0
	for number := 1; ; number++ {
		revision, err := f.store.GetRevision(context.Background(), "W", change, number)
		if errors.Is(err, journal.ErrNotFound) {
			return count
		}
		if err != nil {
			t.Fatal(err)
		}
		if revision.Kind == "derived" {
			count++
		}
	}
}

func TestApplyOfNewerRevisionReplacesLayerAndReplaysAbove(t *testing.T) {
	f, fix := replaceFixture(t, map[string]string{"change": "fixed\n"})
	ctx := context.Background()
	request := apply.Request{Workspace: "W", Lead: "L", Change: "C1", Revision: fix.Number, RequestID: "approval:fix"}
	if _, err := f.applier.Apply(ctx, request); err != nil {
		t.Fatal(err)
	}
	head := f.git(t, "rev-parse", "HEAD")
	layers, err := f.service.appliedLog(ctx, "W", "L", head)
	if err != nil || len(layers) != 2 || layers[0].Change != "C1" || layers[1].Change != "C2" {
		t.Fatalf("layers = %+v, %v", layers, err)
	}
	if layers[0].NewTip != fix.HeadSHA || layers[0].OldTip != f.base || f.git(t, "rev-parse", head+"^") != fix.HeadSHA {
		t.Fatalf("C1 layer %+v is not the fix-up under C2 (HEAD %s)", layers[0], head)
	}
	if f.git(t, "show", "HEAD:change") != "fixed" || f.git(t, "show", "HEAD:second") != "two" {
		t.Fatal("working area lost the fix-up or the layer above")
	}
	replaced, err := f.store.GetRevision(ctx, "W", "C1", layers[0].Revision)
	if err != nil || replaced.Kind != "derived" || replaced.DerivedFromNumber != fix.Number || replaced.BaseSHA != f.base {
		t.Fatalf("replaced layer revision = %+v, %v", replaced, err)
	}
	if err := review.RequireVerdict(ctx, f.store, "W", "C1", replaced.Number, replaced.HeadSHA, "publish", ""); err != nil {
		t.Fatalf("replaced layer is not publishable: %v", err)
	}
	upper, err := f.store.GetRevision(ctx, "W", "C2", layers[1].Revision)
	if err != nil || upper.Operation != "restack" {
		t.Fatalf("C2 revision = %+v, %v", upper, err)
	}
	// Applying the same revision again holds still.
	revisions := derivedCount(t, f, "C1")
	if _, err := f.applier.Apply(ctx, request); err != nil {
		t.Fatal(err)
	}
	if f.git(t, "rev-parse", "HEAD") != head || derivedCount(t, f, "C1") != revisions {
		t.Fatal("re-applying the replaced revision changed the working area")
	}
}

func TestReplaceConflictWithLayerAboveInstallsNothing(t *testing.T) {
	f, fix := replaceFixture(t, map[string]string{"second": "clash\n"})
	old := f.git(t, "rev-parse", "HEAD")
	refs := f.git(t, "for-each-ref", "--format=%(refname) %(objectname)", "refs/loom")
	result, err := f.applier.Apply(context.Background(), apply.Request{Workspace: "W", Lead: "L", Change: "C1",
		Revision: fix.Number, RequestID: "approval:clash"})
	var coded *loomgit.Error
	if !errors.As(err, &coded) || coded.Kind != loomgit.Conflict || len(result.Paths) != 1 || result.Paths[0] != "second" {
		t.Fatalf("conflict = %+v, %v", result, err)
	}
	if f.git(t, "rev-parse", "HEAD") != old || f.git(t, "for-each-ref", "--format=%(refname) %(objectname)", "refs/loom") != refs {
		t.Fatal("a conflicting replace changed the working area or revision refs")
	}
}

func TestReplaceHeldByLeadEditsKeepsThem(t *testing.T) {
	f, fix := replaceFixture(t, map[string]string{"change": "fixed\n"})
	f.write(t, "change", "lead edit\n")
	old := f.git(t, "rev-parse", "HEAD")
	request := apply.Request{Workspace: "W", Lead: "L", Change: "C1", Revision: fix.Number, RequestID: "approval:held"}
	result, err := f.applier.Apply(context.Background(), request)
	var coded *loomgit.Error
	if !errors.As(err, &coded) || coded.Kind != loomgit.ApplyPending || len(result.Paths) != 1 {
		t.Fatalf("held replace = %+v, %v", result, err)
	}
	if body, _ := os.ReadFile(filepath.Join(f.dir, "change")); string(body) != "lead edit\n" || f.git(t, "rev-parse", "HEAD") != old {
		t.Fatal("held replace touched the lead's edit or HEAD")
	}
	f.git(t, "checkout", "--", "change")
	if _, err := f.applier.Apply(context.Background(), request); err != nil {
		t.Fatalf("retry after the edit moved: %v", err)
	}
	if f.git(t, "show", "HEAD:change") != "fixed" {
		t.Fatal("retry did not install the fix-up")
	}
}

func TestReplaceInterruptedBeforePlanRetriesCleanly(t *testing.T) {
	f, fix := replaceFixture(t, map[string]string{"change": "fixed\n"})
	ctx := context.Background()
	old := f.git(t, "rev-parse", "HEAD")
	// The process dies before the plan is saved, so its own cleanup never runs.
	f.service.store = crashedCleanupStore{Store: f.store}
	f.service.beforeSavePullPlan = func() error { return errors.New("killed before the plan was saved") }
	if _, err := f.service.Restack(ctx, replaceRequest(f.base, fix, "replace:crash")); err == nil {
		t.Fatal("expected the injected interruption")
	}
	f.service.store, f.service.beforeSavePullPlan = f.store, nil
	if f.git(t, "rev-parse", "HEAD") != old {
		t.Fatal("interrupted replace moved the working area")
	}
	before := derivedCount(t, f, "C1")
	if _, err := f.service.Restack(ctx, replaceRequest(f.base, fix, "replace:crash")); err != nil {
		t.Fatalf("retry after the interruption: %v", err)
	}
	if got := derivedCount(t, f, "C1"); got != 1 || before != 1 {
		t.Fatalf("C1 derived revisions: %d before retry, %d after (want one, no orphan)", before, got)
	}
	if f.git(t, "show", "HEAD:change") != "fixed" {
		t.Fatal("retry did not install the fix-up")
	}
}

func TestReplaceInterruptedAfterSwapIsCompletedNotRedone(t *testing.T) {
	f, fix := replaceFixture(t, map[string]string{"change": "fixed\n"})
	ctx := context.Background()
	f.service.beforeCompletePull = func() error { return errors.New("killed after the swap") }
	if _, err := f.service.Restack(ctx, replaceRequest(f.base, fix, "replace:approval:swap")); err == nil {
		t.Fatal("expected the injected interruption")
	}
	f.service.beforeCompletePull = nil
	swapped := f.git(t, "rev-parse", "HEAD")
	revisions := derivedCount(t, f, "C1")
	// The follow retries the same approval: it finishes the swap's journal and
	// does not replace the layer a second time.
	if _, err := f.applier.Apply(ctx, apply.Request{Workspace: "W", Lead: "L", Change: "C1",
		Revision: fix.Number, RequestID: "approval:swap"}); err != nil {
		t.Fatal(err)
	}
	layers, err := f.service.appliedLog(ctx, "W", "L", f.git(t, "rev-parse", "HEAD"))
	if err != nil || len(layers) != 2 || layers[0].Change != "C1" || layers[1].Change != "C2" {
		t.Fatalf("recovered layers = %+v, %v", layers, err)
	}
	if f.git(t, "rev-parse", "HEAD") != swapped || derivedCount(t, f, "C1") != revisions {
		t.Fatal("recovery replaced the layer again")
	}
}

func TestReplaceRefusesIncompleteRevision(t *testing.T) {
	f, fix := replaceFixture(t, map[string]string{"change": "fixed\n"})
	ctx := context.Background()
	partial, err := f.store.ReserveRevision(ctx, loomgit.Revision{Workspace: "W", Change: "C1",
		RequestID: "fixup-partial", Kind: "source", Operation: "snapshot", Outcome: "completed", Incomplete: true,
		BaseSHA: fix.BaseSHA, TreeHash: fix.TreeHash, SourceHeadSHA: fix.HeadSHA})
	if err != nil {
		t.Fatal(err)
	}
	partial.HeadSHA = fix.HeadSHA
	if err := f.store.FinishRevision(ctx, partial); err != nil {
		t.Fatal(err)
	}
	old := f.git(t, "rev-parse", "HEAD")
	_, err = f.service.Restack(ctx, replaceRequest(f.base, partial, "replace:partial"))
	var coded *loomgit.Error
	if !errors.As(err, &coded) || coded.Kind != loomgit.CaptureIncomplete {
		t.Fatalf("incomplete replace = %v", err)
	}
	if f.git(t, "rev-parse", "HEAD") != old || derivedCount(t, f, "C1") != 0 {
		t.Fatal("an incomplete revision replaced the layer")
	}
}
