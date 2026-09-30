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
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/reconcile"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

type failingRestackStore struct {
	Store
	step string
}

func (store failingRestackStore) FinishRevision(ctx context.Context, revision loomgit.Revision) error {
	if store.step == "revision" && revision.Operation == "restack" {
		return errors.New("injected revision failure")
	}
	return store.Store.FinishRevision(ctx, revision)
}

func (store failingRestackStore) SavePullPlan(ctx context.Context, plan journal.PullPlan) error {
	if store.step == "plan" {
		return errors.New("injected plan failure")
	}
	return store.Store.SavePullPlan(ctx, plan)
}

func (store failingRestackStore) CompletePull(ctx context.Context, requestID, workspace, lead, repo, base string, layers []loomgit.AppliedLayer) error {
	if store.step == "complete" {
		return errors.New("injected completion failure")
	}
	return store.Store.CompletePull(ctx, requestID, workspace, lead, repo, base, layers)
}

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

func TestUnapplyRebuildsUpperLayerAndKeepsEdits(t *testing.T) {
	f := newFixture(t)
	preparePull(t, f)
	if _, err := f.apply(t); err != nil {
		t.Fatal(err)
	}
	second := addRestackLayer(t, f, 2, f.source, "second", "two\n")
	addRestackLayer(t, f, 3, second, "third", "three\n")
	if err := os.WriteFile(filepath.Join(f.dir, "personal"), []byte("keep\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := f.service.Restack(context.Background(), RestackRequest{Workspace: "W", Lead: "L", Repo: "repo",
		RequestID: "unapply-2", BaseSHA: f.base, RemoveChange: "C2"})
	if err != nil {
		t.Fatalf("unapply: %+v, %v", result, err)
	}
	if result.HeadSHA != f.git(t, "rev-parse", "HEAD") || f.git(t, "show", "HEAD:third") != "three" {
		t.Fatalf("working area did not follow rebuilt leaf: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "second")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed layer still in working area: %v", err)
	}
	if body, err := os.ReadFile(filepath.Join(f.dir, "personal")); err != nil || string(body) != "keep\n" {
		t.Fatalf("uncommitted edit changed: %q, %v", body, err)
	}
	revision, err := f.store.GetRevision(context.Background(), "W", "C3", 2)
	if err != nil || revision.Operation != "unapply" {
		t.Fatalf("derived revision: %+v, %v", revision, err)
	}
	verdict, err := f.store.LatestVerdict(context.Background(), revision)
	if err != nil || verdict.Kind != "carried" {
		t.Fatalf("derived verdict: %+v, %v", verdict, err)
	}
	if _, err := f.store.GetRevision(context.Background(), "W", "C1", 2); !errors.Is(err, journal.ErrNotFound) {
		t.Fatalf("lower layer should retain its revision: %v", err)
	}
	if active, err := f.store.PredecessorApplied(context.Background(), "W", "L", "C2"); err != nil || active {
		t.Fatalf("removed layer is still active: %t, %v", active, err)
	}
}

func TestUnapplyConflictLeavesLeafAndRevisionRefsUntouched(t *testing.T) {
	f := newFixture(t)
	preparePull(t, f)
	if _, err := f.apply(t); err != nil {
		t.Fatal(err)
	}
	second := addRestackLayer(t, f, 2, f.source, "change", "two\n")
	addRestackLayer(t, f, 3, second, "change", "three\n")
	old := f.git(t, "rev-parse", "HEAD")
	refs := f.git(t, "for-each-ref", "--format=%(refname) %(objectname)", "refs/loom")
	result, err := f.service.Restack(context.Background(), RestackRequest{Workspace: "W", Lead: "L", Repo: "repo",
		RequestID: "unapply-conflict", BaseSHA: f.base, RemoveChange: "C2"})
	var coded *loomgit.Error
	if !errors.As(err, &coded) || coded.Kind != loomgit.Conflict || len(result.Paths) == 0 {
		t.Fatalf("expected conflict paths: %+v, %v", result, err)
	}
	if head := f.git(t, "rev-parse", "HEAD"); head != old {
		t.Fatalf("working area moved to %s", head)
	}
	if after := f.git(t, "for-each-ref", "--format=%(refname) %(objectname)", "refs/loom"); after != refs {
		t.Fatal("conflict changed revision refs")
	}
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

func TestRestackLocalEntryUsesRegisteredWorkingArea(t *testing.T) {
	f := newFixture(t)
	trunk := preparePull(t, f)
	if _, err := f.apply(t); err != nil {
		t.Fatal(err)
	}
	base := advanceTrunk(t, f, trunk, "trunk advance")
	result, err := RestackLocal(context.Background(), f.dir, base, nil, "restack-local")
	if err != nil || result.HeadSHA != f.git(t, "rev-parse", "HEAD") {
		t.Fatalf("local restack: %+v, %v", result, err)
	}
	revision, err := f.store.GetRevision(context.Background(), "W", "C1", 2)
	if err != nil || revision.Operation != "restack" || revision.DerivedFromChange != "C1" || revision.DerivedFromNumber != 1 {
		t.Fatalf("local derived revision: %+v, %v", revision, err)
	}
}

func TestRestackOfferDerivesRevisionOnTrunk(t *testing.T) {
	fixture := newFixture(t)
	trunk := preparePull(t, fixture)
	if _, err := fixture.apply(t); err != nil {
		t.Fatal(err)
	}
	base := advanceTrunk(t, fixture, trunk, "landed predecessor")
	offer := journal.RestackOffer{Workspace: "W", Change: "C1", Predecessor: "X", Repo: "repo", Revision: 1, TrunkSHA: base}
	revision, err := RestackOffer(context.Background(), offer)
	if err != nil || revision != 2 {
		t.Fatalf("restack offer = %d, %v", revision, err)
	}
	derived, err := fixture.store.GetRevision(context.Background(), "W", "C1", revision)
	if err != nil || derived.Operation != "restack" || derived.BaseSHA != base {
		t.Fatalf("derived revision = %+v, %v", derived, err)
	}
	again, err := RestackOffer(context.Background(), offer)
	if err != nil || again != revision {
		t.Fatalf("repeated offer = %d, %v", again, err)
	}
}

func TestRestackPreSwapFailuresLeaveNoRefsOrPlan(t *testing.T) {
	for _, step := range []string{"revision", "plan", "swap", "held"} {
		t.Run(step, func(t *testing.T) {
			f := newFixture(t)
			trunk := preparePull(t, f)
			if _, err := f.apply(t); err != nil {
				t.Fatal(err)
			}
			base := advanceTrunk(t, f, trunk, "trunk advance")
			if step == "held" {
				if err := os.WriteFile(filepath.Join(trunk, "base"), []byte("trunk\n"), 0600); err != nil {
					t.Fatal(err)
				}
				trunkGit(t, trunk, "add", "base")
				trunkGit(t, trunk, "commit", "-qm", "trunk file change")
				base = f.git(t, "rev-parse", "main")
				f.write(t, "base", "dirty\n")
			}
			old := f.git(t, "rev-parse", "HEAD")
			refs := f.git(t, "for-each-ref", "--format=%(refname) %(objectname)", "refs/loom")
			f.service.store = failingRestackStore{Store: f.store, step: step}
			if step == "swap" {
				f.service.beforeRestackSwap = func() error { return errors.New("injected pre-swap failure") }
			}
			_, err := f.service.Restack(context.Background(), RestackRequest{Workspace: "W", Lead: "L", Repo: "repo",
				RequestID: "restack-" + step, BaseSHA: base})
			if err == nil {
				t.Fatal("expected injected failure")
			}
			if got := f.git(t, "rev-parse", "HEAD"); got != old {
				t.Fatalf("HEAD moved: %s != %s", got, old)
			}
			if step == "held" {
				body, readErr := os.ReadFile(filepath.Join(f.dir, "base"))
				if readErr != nil || string(body) != "dirty\n" {
					t.Fatalf("dirty working file changed: %q, %v", body, readErr)
				}
			}
			if got := f.git(t, "for-each-ref", "--format=%(refname) %(objectname)", "refs/loom"); got != refs {
				t.Fatalf("revision refs changed:\n%s\n%s", refs, got)
			}
			plans, err := f.store.PendingPullPlans(context.Background(), "W", "L")
			if err != nil || len(plans) != 0 {
				t.Fatalf("pending plan: %+v, %v", plans, err)
			}
			if _, err := f.store.GetRevision(context.Background(), "W", "C1", 2); err == nil {
				t.Fatal("aborted derived revision persisted")
			}
		})
	}
}

func TestRestackPostSwapFailureCompletesForward(t *testing.T) {
	for _, step := range []string{"after-swap", "complete"} {
		t.Run(step, func(t *testing.T) {
			f := newFixture(t)
			trunk := preparePull(t, f)
			if _, err := f.apply(t); err != nil {
				t.Fatal(err)
			}
			base := advanceTrunk(t, f, trunk, "trunk advance")
			old := f.git(t, "rev-parse", "HEAD")
			if step == "after-swap" {
				f.service.beforeCompletePull = func() error { return errors.New("injected post-swap failure") }
			} else {
				f.service.store = failingRestackStore{Store: f.store, step: step}
			}
			result, err := f.service.Restack(context.Background(), RestackRequest{Workspace: "W", Lead: "L", Repo: "repo",
				RequestID: "restack-recover", BaseSHA: base})
			if err == nil || result.HeadSHA == old || f.git(t, "rev-parse", "HEAD") != result.HeadSHA {
				t.Fatalf("post-swap state: %+v, %v", result, err)
			}
			if err := reconcile.RunOnce(context.Background(), reconcile.Handlers{Apply: reconcile.RecoverFunc(Recover)}); err != nil {
				t.Fatal(err)
			}
			plans, err := f.store.PendingPullPlans(context.Background(), "W", "L")
			if err != nil || len(plans) != 0 {
				t.Fatalf("pending plan: %+v, %v", plans, err)
			}
			layers, err := f.service.appliedLog(context.Background(), "W", "L", result.HeadSHA)
			if err != nil || len(layers) != 1 || layers[0].Change != "C1" || layers[0].NewTip != result.HeadSHA {
				t.Fatalf("recovered layers: %+v, %v", layers, err)
			}
		})
	}
}

func TestRestackPreSwapFailureRemovesNewOwnRevision(t *testing.T) {
	f := newFixture(t)
	trunk := preparePull(t, f)
	if _, err := f.apply(t); err != nil {
		t.Fatal(err)
	}
	f.commit(t, "own", "lead work\n", "lead change")
	base := advanceTrunk(t, f, trunk, "trunk advance")
	old := f.git(t, "rev-parse", "HEAD")
	refs := f.git(t, "for-each-ref", "--format=%(refname) %(objectname)", "refs/loom")
	f.service.beforeRestackSwap = func() error { return errors.New("injected pre-swap failure") }
	_, err := f.service.Restack(context.Background(), RestackRequest{Workspace: "W", Lead: "L", Repo: "repo",
		RequestID: "restack-own-abort", BaseSHA: base})
	if err == nil {
		t.Fatal("expected injected failure")
	}
	if f.git(t, "rev-parse", "HEAD") != old || f.git(t, "for-each-ref", "--format=%(refname) %(objectname)", "refs/loom") != refs {
		t.Fatal("own-layer failure changed working area or revision refs")
	}
	if _, err := f.store.RevisionByRequest(context.Background(), "own:W:L:"+old); err == nil {
		t.Fatal("aborted own revision persisted")
	}
}
