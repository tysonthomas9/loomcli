package pull

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/pool"
	"github.com/tysonthomas9/loomcli/internal/loomgit/reconcile"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

type fixture struct {
	dir          string
	runner       *gitexec.Runner
	store        *journal.SQLite
	service      *Service
	applier      *apply.Service
	base, source string
}

func newFixture(t *testing.T) *fixture { return fixtureWithSource(t, nil) }

func fixtureWithSource(t *testing.T, extend func(*testing.T, *fixture)) *fixture {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", "-b", "main", dir).CombinedOutput(); err != nil { //nolint:norawexec // Temporary real-Git fixture, no network.
		t.Fatalf("git init: %s: %v", out, err)
	}
	options := gitexec.Options{GlobalConfig: os.DevNull, SystemConfig: os.DevNull,
		FallbackIdentity: gitexec.Identity{Name: "Test", Email: "test@example.com"}}
	runner, err := gitexec.New(dir, options)
	if err != nil {
		t.Fatal(err)
	}
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	if err := os.MkdirAll(filepath.Join(configDir, "loomgit"), 0700); err != nil {
		t.Fatal(err)
	}
	store, err := journal.OpenSQLite(filepath.Join(configDir, "loomgit", "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repo, err := pool.New(store, options).Admit(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{dir: dir, runner: runner, store: store, service: New(store, repo, runner), applier: apply.New(store, repo, runner)}
	f.commit(t, "base", "base\n", "base")
	f.base = f.git(t, "rev-parse", "HEAD")
	f.git(t, "checkout", "-q", "-b", "source")
	f.source = f.commit(t, "change", "change\n", "change\n\nLoom-Change-Id: C1\nLoom-Revision: 1\nLoom-Task: T1\nLoom-Attempt: A1")
	if extend != nil {
		extend(t, f)
		f.source = f.git(t, "rev-parse", "HEAD")
	}
	f.git(t, "checkout", "-q", "-b", "loom/ws/W/interactive/L", f.base)
	r, err := store.ReserveRevision(context.Background(), loomgit.Revision{
		Workspace: "W", Change: "C1", RequestID: "source", Kind: "source", Operation: "snapshot",
		Outcome: "completed", BaseSHA: f.base, TreeHash: f.git(t, "rev-parse", f.source+"^{tree}"), SourceHeadSHA: f.source,
	})
	if err != nil {
		t.Fatal(err)
	}
	r.HeadSHA = f.source
	if err := store.FinishRevision(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if _, err := review.Submit(context.Background(), store, "W", "C1", 1, f.source, "approve", "", review.Actor{Kind: "human", ID: "reviewer"}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) git(t *testing.T, args ...string) string {
	t.Helper()
	out, err := f.runner.Run(context.Background(), args...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func (f *fixture) write(t *testing.T, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) commit(t *testing.T, name, body, message string) string {
	t.Helper()
	f.write(t, name, body)
	f.git(t, "add", name)
	f.git(t, "commit", "-qm", message)
	return f.git(t, "rev-parse", "HEAD")
}

func (f *fixture) apply(t *testing.T) (apply.Result, error) {
	t.Helper()
	return f.applier.Apply(context.Background(), apply.Request{Workspace: "W", Lead: "L", Change: "C1", Revision: 1, RequestID: "apply-1"})
}

func preparePull(t *testing.T, fixture *fixture) string {
	t.Helper()
	ctx := context.Background()
	entry, _, err := fixture.store.Begin(ctx, "workspace-create:W", "ensure_workspace")
	if err != nil {
		t.Fatal(err)
	}
	entry, err = fixture.store.Advance(ctx, entry, "checkouts_added", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	entry, err = fixture.store.Advance(ctx, entry, "rows_written", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.CommitWorkspace(ctx, entry, []loomgit.WorkspaceRepo{{Workspace: "W", Repo: "repo", Trunk: "main", BaseSHA: fixture.base}}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.SaveWorkingAreas(ctx, []journal.WorkingArea{{Workspace: "W", Lead: "L", Repo: "repo",
		Path: fixture.dir, Branch: "loom/ws/W/interactive/L", BaseSHA: fixture.base, Mode: "worktree"}}); err != nil {
		t.Fatal(err)
	}
	trunk := filepath.Join(t.TempDir(), "trunk")
	fixture.git(t, "worktree", "add", "-q", trunk, "main")
	fixture.git(t, "remote", "add", "origin", fixture.dir)
	return trunk
}

func advanceTrunk(t *testing.T, fixture *fixture, trunk, name string) string {
	t.Helper()
	trunkGit(t, trunk, "commit", "--allow-empty", "-qm", name)
	return fixture.git(t, "rev-parse", "main")
}

func trunkGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=Test", "-c", "user.email=test@example.com"}, args...)...) //nolint:norawexec // Temporary real-Git fixture, no network.
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("trunk git %v: %s: %v", args, out, err)
	}
	return strings.TrimSpace(string(out))
}

func TestPullRestacksAndCarriesVerdictWithoutPush(t *testing.T) {
	fixture := newFixture(t)
	trunk := preparePull(t, fixture)
	if _, err := fixture.apply(t); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(t.TempDir(), "second")
	fixture.git(t, "worktree", "add", "-q", "-b", "source2", second, fixture.source)
	if err := os.WriteFile(filepath.Join(second, "second"), []byte("second\n"), 0600); err != nil {
		t.Fatal(err)
	}
	trunkGit(t, second, "add", "second")
	trunkGit(t, second, "commit", "-qm", "second")
	secondHead := fixture.git(t, "rev-parse", "source2")
	ctx := context.Background()
	revision, err := fixture.store.ReserveRevision(ctx, loomgit.Revision{Workspace: "W", Change: "C2",
		RequestID: "source2", Kind: "source", Operation: "snapshot", Outcome: "completed",
		BaseSHA: fixture.source, TreeHash: fixture.git(t, "rev-parse", secondHead+"^{tree}"), SourceHeadSHA: secondHead})
	if err != nil {
		t.Fatal(err)
	}
	revision.HeadSHA = secondHead
	if err := fixture.store.FinishRevision(ctx, revision); err != nil {
		t.Fatal(err)
	}
	if _, err := review.Submit(ctx, fixture.store, "W", "C2", revision.Number, secondHead, "approve", "", review.Actor{Kind: "human", ID: "reviewer"}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.applier.Apply(ctx, apply.Request{Workspace: "W", Lead: "L", Change: "C2", Revision: revision.Number, RequestID: "apply-2"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"trunk-1", "trunk-2", "trunk-3"} {
		advanceTrunk(t, fixture, trunk, name)
	}
	trunkHead := fixture.git(t, "rev-parse", "main")
	fixture.write(t, "unrelated", "dirty\n")
	result, err := fixture.service.Pull(context.Background(), PullRequest{Workspace: "W", Lead: "L", Repo: "repo", RequestID: "pull-1"})
	if err != nil {
		t.Fatal(err)
	}
	if result.HeadSHA != fixture.git(t, "rev-parse", "HEAD") || fixture.git(t, "rev-parse", "HEAD~2") != trunkHead {
		t.Fatalf("unexpected restacked head: %+v", result)
	}
	if got := fixture.git(t, "rev-parse", "main"); got != trunkHead {
		t.Fatalf("trunk was pushed: %s", got)
	}
	if got := fixture.git(t, "status", "--porcelain"); !strings.Contains(got, "unrelated") {
		t.Fatalf("uncommitted edit lost: %s", got)
	}
	log, err := fixture.service.appliedLog(context.Background(), "W", "L", fixture.git(t, "rev-parse", "HEAD"))
	if err != nil || len(log) != 2 || log[0].Change != "C1" || log[1].Change != "C2" {
		t.Fatalf("restacked log = %+v, %v", log, err)
	}
	for _, layer := range log {
		derived, err := fixture.store.GetRevision(ctx, "W", layer.Change, layer.Revision)
		if err != nil || derived.Operation != "pull" {
			t.Fatalf("derived = %+v, %v", derived, err)
		}
		verdict, err := fixture.store.LatestVerdict(ctx, derived)
		if err != nil || verdict.Kind != "carried" {
			t.Fatalf("verdict = %+v, %v", verdict, err)
		}
	}
}

func TestPullLandedLayerIsDropped(t *testing.T) {
	fixture := newFixture(t)
	trunk := preparePull(t, fixture)
	if _, err := fixture.apply(t); err != nil {
		t.Fatal(err)
	}
	trunkHead := advanceTrunk(t, fixture, trunk, "trunk")
	if err := fixture.store.MarkLanded(context.Background(), "W", "C1"); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.service.Pull(context.Background(), PullRequest{Workspace: "W", Lead: "L", Repo: "repo", RequestID: "pull-landed"})
	if err != nil || result.HeadSHA != trunkHead || fixture.git(t, "rev-parse", "HEAD") != trunkHead {
		t.Fatalf("landed pull = %+v, %v", result, err)
	}
}

func TestPullConflictLeavesCheckoutAndRevisionRefs(t *testing.T) {
	fixture := newFixture(t)
	trunk := preparePull(t, fixture)
	if _, err := fixture.apply(t); err != nil {
		t.Fatal(err)
	}
	fixture.commit(t, "owned", "lead\n", "lead-owned")
	if err := os.WriteFile(filepath.Join(trunk, "owned"), []byte("other\n"), 0600); err != nil {
		t.Fatal(err)
	}
	trunkGit(t, trunk, "add", "owned")
	trunkGit(t, trunk, "commit", "-qm", "trunk")
	before := fixture.git(t, "rev-parse", "HEAD")
	refs := fixture.git(t, "for-each-ref", "--format=%(refname)", "refs/loom")
	result, err := fixture.service.Pull(context.Background(), PullRequest{Workspace: "W", Lead: "L", Repo: "repo", RequestID: "pull-conflict"})
	var pullError *loomgit.Error
	if !errors.As(err, &pullError) || pullError.Code() != string(loomgit.Conflict) || len(result.Paths) == 0 {
		t.Fatalf("expected conflict paths, got %+v, %v", result, err)
	}
	if fixture.git(t, "rev-parse", "HEAD") != before {
		t.Fatal("conflict moved branch")
	}
	if fixture.git(t, "for-each-ref", "--format=%(refname)", "refs/loom") != refs {
		t.Fatal("conflict changed revision refs")
	}
}

func TestPullOverlapHoldsSwap(t *testing.T) {
	fixture := newFixture(t)
	trunk := preparePull(t, fixture)
	if _, err := fixture.apply(t); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(trunk, "base"), []byte("upstream\n"), 0600); err != nil {
		t.Fatal(err)
	}
	trunkGit(t, trunk, "add", "base")
	trunkGit(t, trunk, "commit", "-qm", "trunk")
	fixture.write(t, "base", "dirty\n")
	before := fixture.git(t, "rev-parse", "HEAD")
	result, err := fixture.service.Pull(context.Background(), PullRequest{Workspace: "W", Lead: "L", Repo: "repo", RequestID: "pull-held"})
	var pullError *loomgit.Error
	if !errors.As(err, &pullError) || pullError.Code() != string(loomgit.SwapHeld) || len(result.Paths) != 1 || result.Paths[0] != "base" {
		t.Fatalf("expected swap held, got %+v, %v", result, err)
	}
	if fixture.git(t, "rev-parse", "HEAD") != before {
		t.Fatal("held swap moved branch")
	}
}

func TestPullReconcileCompletesLayerLogAfterSwap(t *testing.T) {
	fixture := newFixture(t)
	trunk := preparePull(t, fixture)
	if err := fixture.store.SaveWorkingAreas(context.Background(), []journal.WorkingArea{{Workspace: "W", Lead: "L", Repo: "other",
		Path: filepath.Join(t.TempDir(), "other"), Branch: "loom/ws/W/interactive/L", BaseSHA: fixture.base, Mode: "clone"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.apply(t); err != nil {
		t.Fatal(err)
	}
	advanceTrunk(t, fixture, trunk, "trunk")
	fixture.service.beforeCompletePull = func() error { return errors.New("interrupted after swap") }
	_, err := fixture.service.Pull(context.Background(), PullRequest{Workspace: "W", Lead: "L", Repo: "repo", RequestID: "pull-recover"})
	if err == nil {
		t.Fatal("expected injected interruption")
	}
	fixture.service.beforeCompletePull = nil
	if err := reconcile.RunOnce(context.Background(), reconcile.Handlers{Apply: reconcile.RecoverFunc(Recover)}); err != nil {
		t.Fatal(err)
	}
	log, err := fixture.service.appliedLog(context.Background(), "W", "L", fixture.git(t, "rev-parse", "HEAD"))
	if err != nil || len(log) != 1 || log[0].Change != "C1" {
		t.Fatalf("recovered layer log = %+v, %v", log, err)
	}
}

func TestPullRunOnceRecoversKilledProcess(t *testing.T) {
	if os.Getenv("P29_PULL_CHILD") == "1" {
		runInterruptedPullChild(t)
		return
	}
	fixture := newFixture(t)
	trunk := preparePull(t, fixture)
	if _, err := fixture.apply(t); err != nil {
		t.Fatal(err)
	}
	advanceTrunk(t, fixture, trunk, "trunk")
	command := exec.Command(os.Args[0], "-test.run=^TestPullRunOnceRecoversKilledProcess$") //nolint:norawexec // Real subprocess proves recovery after abrupt exit.
	command.Env = append(os.Environ(), "P29_PULL_CHILD=1", "P29_PULL_AREA="+fixture.dir)
	output, err := command.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 77 {
		t.Fatalf("child exit = %v, output = %s", err, output)
	}
	ctx := context.Background()
	if err := reconcile.RunOnce(ctx, reconcile.Handlers{Apply: reconcile.RecoverFunc(Recover)}); err != nil {
		t.Fatal(err)
	}
	plans, err := fixture.store.PendingPullPlans(ctx, "W", "L")
	if err != nil || len(plans) != 0 {
		t.Fatalf("pending plans = %+v, %v", plans, err)
	}
	log, err := fixture.service.appliedLog(ctx, "W", "L", fixture.git(t, "rev-parse", "HEAD"))
	if err != nil || len(log) != 1 || log[0].Change != "C1" {
		t.Fatalf("recovered layers = %+v, %v", log, err)
	}
}

func runInterruptedPullChild(t *testing.T) {
	ctx := context.Background()
	path := os.Getenv("P29_PULL_AREA")
	store, err := journal.OpenSQLite(filepath.Join(os.Getenv("LOOM_CONFIG_DIR"), "loomgit", "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	options := gitexec.Options{GlobalConfig: os.DevNull, SystemConfig: os.DevNull,
		FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}}
	repo, err := pool.New(store, options).Admit(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := gitexec.New(path, options)
	if err != nil {
		t.Fatal(err)
	}
	service := New(store, repo, runner)
	service.beforeCompletePull = func() error { os.Exit(77); return nil }
	if _, err := service.Pull(ctx, PullRequest{Workspace: "W", Lead: "L", Repo: "repo",
		RequestID: "pull-killed"}); err != nil {
		t.Fatal(err)
	}
	t.Fatal("pull returned without interruption")
}

func TestPullRecoveryFailureHoldsBackOnlyItsLead(t *testing.T) {
	fixture := newFixture(t)
	ctx := context.Background()
	trunk := preparePull(t, fixture)
	if _, err := fixture.apply(t); err != nil {
		t.Fatal(err)
	}
	advanceTrunk(t, fixture, trunk, "trunk")
	// W1/L's plan comes first and its working area is gone.
	if err := fixture.store.SavePullPlan(ctx, journal.PullPlan{RequestID: "w1-pull", Workspace: "W1", Lead: "L",
		Repo: "repo", BaseSHA: fixture.base}); err != nil {
		t.Fatal(err)
	}
	fixture.service.beforeCompletePull = func() error { return errors.New("interrupted after swap") }
	if _, err := fixture.service.Pull(ctx, PullRequest{Workspace: "W", Lead: "L", Repo: "repo", RequestID: "pull-recover"}); err == nil {
		t.Fatal("expected injected interruption")
	}
	fixture.service.beforeCompletePull = nil
	err := Recover(ctx)
	var failed *PlanError
	if !errors.As(err, &failed) || failed.Workspace != "W1" || failed.Lead != "L" {
		t.Fatalf("pull recovery = %v", err)
	}
	log, err := fixture.service.appliedLog(ctx, "W", "L", fixture.git(t, "rev-parse", "HEAD"))
	if err != nil || len(log) != 1 || log[0].Change != "C1" {
		t.Fatalf("other lead's recovered layers = %+v, %v", log, err)
	}
	if plans, err := fixture.store.PendingPullPlans(ctx, "W1", "L"); err != nil || len(plans) != 1 {
		t.Fatalf("failed lead's plan = %+v, %v", plans, err)
	}
}
