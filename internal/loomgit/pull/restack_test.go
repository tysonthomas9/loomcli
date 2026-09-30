package pull

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

func addRestackLayer(t *testing.T, f *fixture, number int, base, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), fmt.Sprintf("source-%d", number))
	f.git(t, "worktree", "add", "-q", "--detach", path, base)
	if err := os.WriteFile(filepath.Join(path, name), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	trunkGit(t, path, "add", name)
	trunkGit(t, path, "commit", "-qm", fmt.Sprintf("layer %d", number))
	head := trunkGit(t, path, "rev-parse", "HEAD")
	change := fmt.Sprintf("C%d", number)
	ctx := context.Background()
	revision, err := f.store.ReserveRevision(ctx, loomgit.Revision{Workspace: "W", Change: change,
		RequestID: fmt.Sprintf("source-%d", number), Kind: "source", Operation: "snapshot", Outcome: "completed",
		BaseSHA: base, TreeHash: f.git(t, "rev-parse", head+"^{tree}"), SourceHeadSHA: head})
	if err != nil {
		t.Fatal(err)
	}
	revision.HeadSHA = head
	if err := f.store.FinishRevision(ctx, revision); err != nil {
		t.Fatal(err)
	}
	if _, err := review.Submit(ctx, f.store, "W", change, revision.Number, head, "approve", "", review.Actor{Kind: "human", ID: "reviewer"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.applier.Apply(ctx, apply.Request{Workspace: "W", Lead: "L", Change: change,
		Revision: revision.Number, RequestID: fmt.Sprintf("apply-%d", number)}); err != nil {
		t.Fatal(err)
	}
	return head
}

func TestRestackFourLayersConflictChangesNoRefs(t *testing.T) {
	f := newFixture(t)
	trunk := preparePull(t, f)
	if _, err := f.apply(t); err != nil {
		t.Fatal(err)
	}
	base := f.source
	for number := 2; number <= 4; number++ {
		name := fmt.Sprintf("file-%d", number)
		body := fmt.Sprintf("layer-%d\n", number)
		base = addRestackLayer(t, f, number, base, name, body)
	}
	if err := os.WriteFile(filepath.Join(trunk, "file-3"), []byte("trunk\n"), 0600); err != nil {
		t.Fatal(err)
	}
	trunkGit(t, trunk, "add", "file-3")
	trunkGit(t, trunk, "commit", "-qm", "conflict")
	old := f.git(t, "rev-parse", "HEAD")
	refs := f.git(t, "for-each-ref", "--format=%(refname) %(objectname)", "refs/loom")
	result, err := f.service.Restack(context.Background(), RestackRequest{Workspace: "W", Lead: "L", Repo: "repo",
		RequestID: "restack-conflict", BaseSHA: f.git(t, "rev-parse", "main"),
		Order: []string{"C1", "C2", "C3", "C4"}})
	var loomErr *loomgit.Error
	if !errors.As(err, &loomErr) || loomErr.Code() != string(loomgit.Conflict) || !strings.Contains(strings.Join(result.Paths, ","), "file-3") {
		t.Fatalf("expected layer-3 conflict, got %+v, %v", result, err)
	}
	if f.git(t, "rev-parse", "HEAD") != old || f.git(t, "for-each-ref", "--format=%(refname) %(objectname)", "refs/loom") != refs {
		t.Fatal("conflict changed checkout or revision refs")
	}
}

func TestRestackCarriesVerdictsAndReorders(t *testing.T) {
	f := newFixture(t)
	trunk := preparePull(t, f)
	if _, err := f.apply(t); err != nil {
		t.Fatal(err)
	}
	addRestackLayer(t, f, 2, f.source, "second", "two\n")
	base := advanceTrunk(t, f, trunk, "trunk advance")
	result, err := f.service.Restack(context.Background(), RestackRequest{Workspace: "W", Lead: "L", Repo: "repo",
		RequestID: "restack-clean", BaseSHA: base, Order: []string{"C1", "C2"}})
	if err != nil || result.HeadSHA != f.git(t, "rev-parse", "HEAD") {
		t.Fatalf("restack: %+v, %v", result, err)
	}
	for _, change := range []string{"C1", "C2"} {
		revision, err := f.store.GetRevision(context.Background(), "W", change, 2)
		if err != nil || revision.Operation != "restack" {
			t.Fatalf("revision %s: %+v, %v", change, revision, err)
		}
		verdict, err := f.store.LatestVerdict(context.Background(), revision)
		if err != nil || verdict.Kind != "carried" {
			t.Fatalf("verdict %s: %+v, %v", change, verdict, err)
		}
		if err := review.RequireVerdict(context.Background(), f.store, "W", change, revision.Number, revision.HeadSHA, "publish", ""); err != nil {
			t.Fatalf("publish gate %s: %v", change, err)
		}
	}
	result, err = f.service.Restack(context.Background(), RestackRequest{Workspace: "W", Lead: "L", Repo: "repo",
		RequestID: "restack-reorder", BaseSHA: base, Order: []string{"C2", "C1"}})
	if err != nil || result.HeadSHA != f.git(t, "rev-parse", "HEAD") {
		t.Fatalf("reorder: %+v, %v", result, err)
	}
	for _, change := range []string{"C1", "C2"} {
		revision, err := f.store.GetRevision(context.Background(), "W", change, 3)
		if err != nil || revision.Operation != "reorder" {
			t.Fatalf("reorder revision %s: %+v, %v", change, revision, err)
		}
		verdict, err := f.store.LatestVerdict(context.Background(), revision)
		if err != nil || verdict.Kind != "carried" {
			t.Fatalf("reorder verdict %s: %+v, %v", change, verdict, err)
		}
	}
}

func TestRestackScratchCreationFailureLeavesRefsUntouched(t *testing.T) {
	f := newFixture(t)
	preparePull(t, f)
	if _, err := f.apply(t); err != nil {
		t.Fatal(err)
	}
	f.service.scratchParent = filepath.Join(t.TempDir(), "missing")
	old := f.git(t, "rev-parse", "HEAD")
	refs := f.git(t, "for-each-ref", "--format=%(refname) %(objectname)", "refs/loom")
	_, err := f.service.Restack(context.Background(), RestackRequest{Workspace: "W", Lead: "L", Repo: "repo",
		RequestID: "restack-no-scratch", BaseSHA: f.base, Order: []string{"C1"}})
	if err == nil || !strings.Contains(err.Error(), "create restack scratch") {
		t.Fatalf("expected scratch error, got %v", err)
	}
	if f.git(t, "rev-parse", "HEAD") != old || f.git(t, "for-each-ref", "--format=%(refname) %(objectname)", "refs/loom") != refs {
		t.Fatal("scratch failure changed checkout or revision refs")
	}
}

func TestRestackChangedPatchRequiresReview(t *testing.T) {
	f := fixtureWithSource(t, func(t *testing.T, f *fixture) {
		f.commit(t, "base", "source\nbase\n", "source context")
	})
	trunk := preparePull(t, f)
	if _, err := f.apply(t); err != nil {
		t.Fatal(err)
	}
	addRestackLayer(t, f, 2, f.source, "second", "two\n")
	if err := os.WriteFile(filepath.Join(trunk, "base"), []byte("base\ntrunk\n"), 0600); err != nil {
		t.Fatal(err)
	}
	trunkGit(t, trunk, "add", "base")
	trunkGit(t, trunk, "commit", "-qm", "trunk context")
	result, err := f.service.Restack(context.Background(), RestackRequest{Workspace: "W", Lead: "L", Repo: "repo",
		RequestID: "restack-changed", BaseSHA: f.git(t, "rev-parse", "main")})
	if err != nil {
		t.Fatalf("restack: %+v, %v", result, err)
	}
	revision, err := f.store.GetRevision(context.Background(), "W", "C1", 2)
	if err != nil || revision.Operation != "restack" {
		t.Fatalf("derived: %+v, %v", revision, err)
	}
	if err := review.RequireVerdict(context.Background(), f.store, "W", "C1", revision.Number, revision.HeadSHA, "publish", ""); err == nil || !strings.Contains(err.Error(), string(loomgit.ReviewRequired)) {
		t.Fatalf("expected review_required, got %v", err)
	}
	other, err := f.store.GetRevision(context.Background(), "W", "C2", 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := review.RequireVerdict(context.Background(), f.store, "W", "C2", other.Number, other.HeadSHA, "publish", ""); err != nil {
		t.Fatalf("unchanged layer lost verdict: %v", err)
	}
}
