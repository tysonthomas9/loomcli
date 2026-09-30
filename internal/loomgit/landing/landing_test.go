package landing

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

type fakeForge struct {
	pull       stackpublish.PR
	pulls      map[int]stackpublish.PR
	associated map[string][]stackpublish.PR
}

func (forge *fakeForge) PullByNumber(_ context.Context, _, _ string, number int) (stackpublish.PR, error) {
	if forge.pulls != nil {
		return forge.pulls[number], nil
	}
	return forge.pull, nil
}

func (forge *fakeForge) PullsForCommit(_ context.Context, _, _, sha string) ([]stackpublish.PR, error) {
	return forge.associated[sha], nil
}

type fixture struct {
	store                   *journal.SQLite
	forge                   *fakeForge
	source, remote, initial string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	remote := filepath.Join(root, "remote.git")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	git(t, source, "init", "-q", "-b", "main")
	git(t, source, "config", "user.name", "Test")
	git(t, source, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(source, "readme"), []byte("initial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, source, "add", "readme")
	git(t, source, "commit", "-qm", "initial")
	initial := git(t, source, "rev-parse", "HEAD")
	git(t, root, "clone", "-q", "--bare", source, remote)
	git(t, source, "remote", "add", "origin", remote)
	store, err := journal.OpenSQLite(filepath.Join(root, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	forge := &fakeForge{pull: stackpublish.PR{Number: 42, Head: "loom/ws/W/change/A", HeadSHA: initial, Base: "main", State: "open"}, associated: map[string][]stackpublish.PR{}}
	return &fixture{store: store, forge: forge, source: source, remote: remote, initial: initial}
}

func (fixture *fixture) publish(t *testing.T, change string) {
	t.Helper()
	ctx := context.Background()
	publication := journal.Publication{Workspace: "W", Change: change, Repo: fixture.source,
		Branch: "loom/ws/W/change/" + change, Trunk: "main", Slug: "owner/repo", Head: fixture.initial}
	if err := fixture.store.BeginPublication(ctx, publication); err != nil {
		t.Fatal(err)
	}
	publication.Phase = "done"
	publication.PRNumber = 42
	if err := fixture.store.AdvancePublication(ctx, publication); err != nil {
		t.Fatal(err)
	}
}

func (fixture *fixture) commit(t *testing.T, message string) string {
	t.Helper()
	path := filepath.Join(fixture.source, "readme")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(message + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	git(t, fixture.source, "add", "readme")
	git(t, fixture.source, "commit", "-qm", message)
	return git(t, fixture.source, "rev-parse", "HEAD")
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...) //nolint:norawexec // Disposable real-Git fixture needs repository setup.
	command.Dir = dir
	command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestMergeRecordLandsWithoutTrailerAndOffersDependent(t *testing.T) {
	fixture := newFixture(t)
	fixture.publish(t, "A")
	ctx := context.Background()
	base := fixture.initial
	dependent, err := fixture.store.ReserveRevision(ctx, loomgit.Revision{Workspace: "W", Change: "B", RequestID: "B-source",
		Kind: "source", Outcome: "completed", BaseSHA: base, TreeHash: base,
		SourceHeadSHA: base, DerivedFromChange: "A", DerivedFromNumber: 1})
	if err != nil {
		t.Fatal(err)
	}
	dependent.HeadSHA = base
	if err := fixture.store.FinishRevision(ctx, dependent); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DriverChange(ctx, "W", "task-B", "repo", "B"); err != nil {
		t.Fatal(err)
	}
	merged := fixture.commit(t, "PR title only")
	git(t, fixture.source, "push", "-q", "origin", "main")
	fixture.forge.pull.Merged = true
	fixture.forge.pull.MergeCommitSHA = merged
	var restackCalls int
	options := Options{Dependents: func(_ context.Context, workspace, change string) ([]Dependent, error) {
		if workspace != "W" || change != "A" {
			t.Fatalf("dependent lookup = %s/%s", workspace, change)
		}
		return []Dependent{{Task: "task-B", Repo: "repo"}}, nil
	}, Restack: func(_ context.Context, offer journal.RestackOffer, _ Forge) (int, error) {
		restackCalls++
		if offer.Change != "B" || offer.Revision != 1 || offer.TrunkSHA != merged {
			t.Fatalf("restack offer = %+v", offer)
		}
		return 2, nil
	}}
	if err := ReconcileWithOptions(ctx, fixture.store, fixture.forge, options); err != nil {
		t.Fatal(err)
	}
	status, err := fixture.store.LandingStatus(ctx, "W", "A")
	if err != nil || status.State != "landed" || status.Rule != "merge_commit" {
		t.Fatalf("landing status = %+v, %v", status, err)
	}
	landed, err := fixture.store.IsLanded(ctx, "W", "A")
	if err != nil || !landed {
		t.Fatalf("Pull landing marker = %v, %v", landed, err)
	}
	offers, err := fixture.store.RestackOffers(ctx, "W", "B")
	if err != nil || len(offers) != 1 || offers[0].TrunkSHA != merged || offers[0].Revision != 1 || offers[0].DerivedRevision != 2 || restackCalls != 1 {
		t.Fatalf("dependent offers = %+v, %v", offers, err)
	}
	dependentStatus, err := fixture.store.LandingStatus(ctx, "W", "B")
	if err != nil || dependentStatus.State != "restack_available" || dependentStatus.Offer == nil || dependentStatus.Offer.TrunkSHA != merged {
		t.Fatalf("dependent status = %+v, %v", dependentStatus, err)
	}
	if err := ReconcileWithOptions(ctx, fixture.store, fixture.forge, options); err != nil || restackCalls != 1 {
		t.Fatalf("idempotent reconcile = %v, restacks = %d", err, restackCalls)
	}
}

func TestLandingWithoutDependentAdapters(t *testing.T) {
	fixture := newFixture(t)
	fixture.publish(t, "A")
	merged := fixture.commit(t, "PR title only")
	git(t, fixture.source, "push", "-q", "origin", "main")
	fixture.forge.pull.Merged = true
	fixture.forge.pull.MergeCommitSHA = merged

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	ctx := context.Background()
	if err := Reconcile(ctx, fixture.store, fixture.forge); err != nil {
		t.Fatal(err)
	}
	status, err := fixture.store.LandingStatus(ctx, "W", "A")
	if err != nil || status.State != "landed" || status.Rule != "merge_commit" {
		t.Fatalf("landing status = %+v, %v", status, err)
	}
	if !strings.Contains(logs.String(), "landing dependent work skipped: adapters not configured") {
		t.Fatalf("missing adapter warning: %s", logs.String())
	}
}

func TestMergedWaitsForFetchedTrunk(t *testing.T) {
	fixture := newFixture(t)
	fixture.publish(t, "A")
	merged := fixture.commit(t, "PR title only")
	fixture.forge.pull.Merged = true
	fixture.forge.pull.MergeCommitSHA = merged
	ctx := context.Background()
	if err := Reconcile(ctx, fixture.store, fixture.forge); err != nil {
		t.Fatal(err)
	}
	status, err := fixture.store.LandingStatus(ctx, "W", "A")
	if err != nil || status.State != "merged" {
		t.Fatalf("before fetch = %+v, %v", status, err)
	}
	git(t, fixture.source, "push", "-q", "origin", "main")
	if err := Reconcile(ctx, fixture.store, fixture.forge); err != nil {
		t.Fatal(err)
	}
	status, err = fixture.store.LandingStatus(ctx, "W", "A")
	if err != nil || status.State != "landed" {
		t.Fatalf("after fetch = %+v, %v", status, err)
	}
}

func TestRestackOfferSurvivesCallbackFailure(t *testing.T) {
	fixture := newFixture(t)
	fixture.publish(t, "A")
	ctx := context.Background()
	if _, err := fixture.store.DriverChange(ctx, "W", "task-B", "repo", "B"); err != nil {
		t.Fatal(err)
	}
	dependent, err := fixture.store.ReserveRevision(ctx, loomgit.Revision{Workspace: "W", Change: "B", RequestID: "B-source",
		Kind: "source", Outcome: "completed", BaseSHA: fixture.initial, TreeHash: fixture.initial,
		SourceHeadSHA: fixture.initial})
	if err != nil {
		t.Fatal(err)
	}
	dependent.HeadSHA = fixture.initial
	if err := fixture.store.FinishRevision(ctx, dependent); err != nil {
		t.Fatal(err)
	}
	fixture.forge.pull.Merged = true
	fixture.forge.pull.MergeCommitSHA = fixture.initial
	options := Options{Dependents: func(context.Context, string, string) ([]Dependent, error) {
		return []Dependent{{Task: "task-B", Repo: "repo"}}, nil
	}, Restack: func(context.Context, journal.RestackOffer, Forge) (int, error) {
		return 0, errors.New("restack unavailable")
	}}
	if err := ReconcileWithOptions(ctx, fixture.store, fixture.forge, options); err == nil {
		t.Fatal("failed restack callback succeeded")
	}
	landed, err := fixture.store.IsLanded(ctx, "W", "A")
	if err != nil || !landed {
		t.Fatalf("landing marker after restack failure = %v, %v", landed, err)
	}
	open, err := fixture.store.OpenRestackOffers(ctx)
	if err != nil || len(open) != 1 {
		t.Fatalf("pending restack offers = %+v, %v", open, err)
	}
	options.Restack = func(context.Context, journal.RestackOffer, Forge) (int, error) { return 2, nil }
	if err := ReconcileWithOptions(ctx, fixture.store, fixture.forge, options); err != nil {
		t.Fatal(err)
	}
	open, err = fixture.store.OpenRestackOffers(ctx)
	if err != nil || len(open) != 0 {
		t.Fatalf("completed restack offers = %+v, %v", open, err)
	}
}

func TestOpenOwnedPRNeverLandsFromCopiedTrailer(t *testing.T) {
	fixture := newFixture(t)
	fixture.publish(t, "A")
	fixture.commit(t, "Loom-Change-Id: A")
	git(t, fixture.source, "push", "-q", "origin", "main")
	if err := Reconcile(context.Background(), fixture.store, fixture.forge); err != nil {
		t.Fatal(err)
	}
	status, err := fixture.store.LandingStatus(context.Background(), "W", "A")
	if err != nil || status.State != "published" {
		t.Fatalf("open PR status = %+v, %v", status, err)
	}
}

func TestSecondaryNeedsOwnedPRAssociation(t *testing.T) {
	fixture := newFixture(t)
	fixture.publish(t, "A")
	commit := fixture.commit(t, "Squashed title\n\nLoom-Change-Id: A")
	git(t, fixture.source, "push", "-q", "origin", "main")
	fixture.forge.pull.Merged = true
	fixture.forge.associated[commit] = []stackpublish.PR{{Number: 99, Merged: true}}
	ctx := context.Background()
	if err := Reconcile(ctx, fixture.store, fixture.forge); err != nil {
		t.Fatal(err)
	}
	status, err := fixture.store.LandingStatus(ctx, "W", "A")
	if err != nil || status.State != "merged" {
		t.Fatalf("unowned association = %+v, %v", status, err)
	}
	fixture.forge.associated[commit] = []stackpublish.PR{{Number: 42, Merged: true}}
	if err := Reconcile(ctx, fixture.store, fixture.forge); err != nil {
		t.Fatal(err)
	}
	status, err = fixture.store.LandingStatus(ctx, "W", "A")
	if err != nil || status.State != "landed" || status.Rule != "associated_commit" {
		t.Fatalf("owned association = %+v, %v", status, err)
	}
}

func TestFetchFailureChangesNoStatus(t *testing.T) {
	fixture := newFixture(t)
	fixture.publish(t, "A")
	publication := journal.Publication{Workspace: "W", Change: "B", Repo: filepath.Join(t.TempDir(), "missing"),
		Branch: "loom/ws/W/change/B", Trunk: "main", Slug: "owner/repo", Head: fixture.initial}
	if err := fixture.store.BeginPublication(context.Background(), publication); err != nil {
		t.Fatal(err)
	}
	publication.Phase = "done"
	publication.PRNumber = 43
	if err := fixture.store.AdvancePublication(context.Background(), publication); err != nil {
		t.Fatal(err)
	}
	fixture.forge.pull.Merged = true
	fixture.forge.pull.MergeCommitSHA = fixture.initial
	if err := Reconcile(context.Background(), fixture.store, fixture.forge); err == nil {
		t.Fatal("missing second trunk fetch succeeded")
	}
	status, err := fixture.store.LandingStatus(context.Background(), "W", "A")
	if err != nil || status.State != "published" {
		t.Fatalf("state after fetch failure = %+v, %v", status, err)
	}
}
