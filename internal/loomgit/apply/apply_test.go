package apply

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/changeset"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/pool"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

type fixture struct {
	dir          string
	runner       *gitexec.Runner
	store        *journal.SQLite
	service      *Service
	base, source string
	dbPath       string
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
	dbPath := filepath.Join(t.TempDir(), "journal.sqlite")
	store, err := journal.OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repo, err := pool.New(store, options).Admit(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{dir: dir, runner: runner, store: store, service: New(store, repo, runner), dbPath: dbPath}
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

func (f *fixture) apply(t *testing.T) (Result, error) {
	t.Helper()
	return f.service.Apply(context.Background(), Request{Workspace: "W", Lead: "L", Change: "C1", Revision: 1, RequestID: "apply-1"})
}

func TestApplyFastForwardPreservesUnrelatedEditAndLogsLayer(t *testing.T) {
	f := newFixture(t)
	f.write(t, "base", "user edit\n")
	got, err := f.apply(t)
	if err != nil {
		t.Fatal(err)
	}
	if got.HeadSHA != f.source || got.Derived.Number != 0 || f.git(t, "rev-parse", "HEAD") != f.source {
		t.Fatalf("fast forward: %+v", got)
	}
	data, err := os.ReadFile(filepath.Join(f.dir, "base"))
	if err != nil || string(data) != "user edit\n" {
		t.Fatalf("user edit: %q, %v", data, err)
	}
	log, err := f.service.AppliedLog(context.Background(), "W", "L")
	if err != nil || len(log) != 1 || len(log[0].Commits) != 1 || log[0].Commits[0] != f.source {
		t.Fatalf("applied log: %+v, %v", log, err)
	}
	if len(log[0].CommitDetails) != 1 || log[0].CommitDetails[0].Task != "T1" ||
		log[0].CommitDetails[0].Change != "C1" || log[0].CommitDetails[0].Revision != "1" {
		t.Fatalf("commit attribution: %+v", log[0].CommitDetails)
	}
}

func TestApplyEventIsDurableOnlyAfterCompletion(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	before, err := f.store.PendingEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.apply(t)
	if err != nil {
		t.Fatal(err)
	}
	events, err := f.store.PendingEvents(ctx)
	if err != nil || len(events) != len(before)+1 || events[len(before)].Kind != "git.integrated" ||
		!strings.Contains(string(events[len(before)].Payload), result.HeadSHA) {
		t.Fatalf("completed apply events: %+v, %v", events, err)
	}
	if err := f.service.Reconcile(ctx, "W", "L"); err != nil {
		t.Fatal(err)
	}
	again, err := f.store.PendingEvents(ctx)
	if err != nil || len(again) != len(events) || again[len(before)].ID != events[len(before)].ID {
		t.Fatalf("reconciled events: %+v, %v", again, err)
	}
}

func TestApplyWIPBasedRevisionLeavesUserEditsUncommitted(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.git(t, "checkout", "-q", "-b", "wip", f.base)
	wip := f.commit(t, "user.txt", "user work\n", "workspace WIP")
	agent := f.commit(t, "agent.txt", "agent work\n", "agent change")
	f.git(t, "checkout", "-q", "loom/ws/W/interactive/L")
	f.write(t, "user.txt", "user work\n")
	revision, err := f.store.ReserveRevision(ctx, loomgit.Revision{
		Workspace: "W", Change: "C2", RequestID: "wip-source", Kind: "source", Operation: "snapshot",
		Outcome: "completed", BaseSHA: wip, TreeHash: f.git(t, "rev-parse", agent+"^{tree}"), SourceHeadSHA: agent,
	})
	if err != nil {
		t.Fatal(err)
	}
	revision.HeadSHA = agent
	if err := f.store.FinishRevision(ctx, revision); err != nil {
		t.Fatal(err)
	}
	if _, err := review.Submit(ctx, f.store, "W", "C2", revision.Number, agent, "approve", "", review.Actor{Kind: "human", ID: "reviewer"}); err != nil {
		t.Fatal(err)
	}
	result, err := f.service.Apply(ctx, Request{Workspace: "W", Lead: "L", Change: "C2", Revision: revision.Number, RequestID: "apply-wip"})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.git(t, "show", "HEAD:agent.txt"); got != "agent work" {
		t.Fatalf("applied agent file = %q", got)
	}
	if got := f.git(t, "ls-tree", "HEAD", "user.txt"); got != "" {
		t.Fatalf("WIP file entered applied layer: %q", got)
	}
	if got := f.git(t, "status", "--porcelain"); got != "?? user.txt" {
		t.Fatalf("user edit status = %q", got)
	}
	if result.HeadSHA == agent || f.git(t, "rev-parse", "HEAD^1") != f.base {
		t.Fatalf("agent layer did not replay onto lead tip: %+v", result)
	}
}

func TestApplyReplayRecordsDerivedAndCarriesApproval(t *testing.T) {
	f := newFixture(t)
	target := f.commit(t, "target", "target\n", "target")
	got, err := f.apply(t)
	if err != nil {
		t.Fatal(err)
	}
	if got.Derived.Number != 2 || got.Derived.DerivedFromNumber != 1 || got.HeadSHA == f.source ||
		f.git(t, "rev-parse", got.HeadSHA+"^1") != target || f.git(t, "rev-parse", "HEAD") != got.HeadSHA {
		t.Fatalf("derived: %+v", got)
	}
	if f.git(t, "show", "-s", "--format=%B", got.HeadSHA) != f.git(t, "show", "-s", "--format=%B", f.source) {
		t.Fatal("source message and trailers changed")
	}
	if err := review.RequireVerdict(context.Background(), f.store, "W", "C1", 2, got.HeadSHA, "apply", "L"); err != nil {
		t.Fatalf("derived approval: %v", err)
	}
}

func TestApplyDerivedRequestIDTracksReplayBase(t *testing.T) {
	fixture := newFixture(t)
	firstBase := fixture.commit(t, "first", "first\n", "first")
	first, err := changeset.RecordDerived(context.Background(), fixture.store, fixture.runner, changeset.DerivedInput{
		Workspace: "W", Change: "C1", RequestID: "approval:1:derived", FromNumber: 1,
		Operation: "apply", BaseSHA: firstBase, HeadSHA: firstBase, Outcome: "completed",
	})
	if err != nil {
		t.Fatal(err)
	}
	secondBase := fixture.commit(t, "second", "second\n", "second")
	second, err := fixture.service.Apply(context.Background(), Request{Workspace: "W", Lead: "L", Change: "C1", Revision: 1, RequestID: "approval:1"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Derived.Number == first.Number || second.Derived.BaseSHA != secondBase ||
		second.Derived.RequestID == first.RequestID {
		t.Fatalf("base move reused derived revision: first=%+v second=%+v", first, second.Derived)
	}
	again, err := changeset.RecordDerived(context.Background(), fixture.store, fixture.runner, changeset.DerivedInput{
		Workspace: "W", Change: "C1", RequestID: second.Derived.RequestID, FromNumber: 1,
		Operation: "apply", BaseSHA: secondBase, HeadSHA: second.Derived.SourceHeadSHA, Outcome: "completed",
	})
	if err != nil || again.Number != second.Derived.Number {
		t.Fatalf("same-base retry created a revision: %+v, %v", again, err)
	}
}

func TestApplyPendingAndConflictLeaveCheckoutUntouched(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*testing.T, *fixture)
		code loomgit.Code
	}{
		{"pending", func(t *testing.T, f *fixture) { f.write(t, "change", "user\n") }, loomgit.ApplyPending},
		{"conflict", func(t *testing.T, f *fixture) { f.commit(t, "change", "other\n", "other") }, loomgit.Conflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.edit(t, f)
			before := f.git(t, "rev-parse", "HEAD")
			index := f.git(t, "ls-files", "--stage")
			got, err := f.apply(t)
			if !errors.Is(err, loomgit.NewError(tc.code, "", nil)) || len(got.Paths) != 1 || got.Paths[0] != "change" {
				t.Fatalf("result: %+v, %v", got, err)
			}
			if f.git(t, "rev-parse", "HEAD") != before || f.git(t, "ls-files", "--stage") != index {
				t.Fatal("checkout changed")
			}
		})
	}
}

func TestApplyPreservesStagedUnrelatedEdit(t *testing.T) {
	f := newFixture(t)
	f.write(t, "base", "staged\n")
	f.git(t, "add", "base")
	before := f.git(t, "ls-files", "--stage")
	if _, err := f.apply(t); err != nil {
		t.Fatal(err)
	}
	after := f.git(t, "ls-files", "--stage")
	if !strings.Contains(after, strings.Fields(before)[1]) {
		t.Fatalf("staged edit lost: before=%q after=%q", before, after)
	}
}

func TestApplyRecomputesAfterTerminalCommitBeforeIndexLock(t *testing.T) {
	f := newFixture(t)
	var terminal string
	f.service.beforeIndexLock = func() {
		f.service.beforeIndexLock = nil
		terminal = f.commit(t, "terminal", "terminal\n", "terminal")
	}
	got, err := f.apply(t)
	if err != nil {
		t.Fatal(err)
	}
	if got.Derived.Number != 2 || f.git(t, "rev-parse", got.HeadSHA+"^1") != terminal {
		t.Fatalf("did not replay onto terminal commit: %+v", got)
	}
}

func TestApplyIndexLockBlocksTerminalGitCommit(t *testing.T) {
	f := newFixture(t)
	f.write(t, "base", "staged\n")
	f.git(t, "add", "base")
	locked := make(chan struct{})
	release := make(chan struct{})
	f.service.onIndexLocked = func() { close(locked); <-release }
	finished := make(chan error, 1)
	go func() { _, err := f.apply(t); finished <- err }()
	<-locked
	cmd := exec.Command("git", "-C", f.dir, "-c", "user.name=Terminal", "-c", "user.email=terminal@example.com", "commit", "-qm", "terminal") //nolint:norawexec // Real local Git contention is the behavior under test.
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "index.lock") {
		close(release)
		t.Fatalf("terminal commit was not blocked by index lock: %q, %v", out, err)
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if f.git(t, "rev-parse", "HEAD") != f.source || f.git(t, "show", "HEAD:change") != "change" {
		t.Fatal("swap produced wrong branch tree")
	}
}

func TestApplyDropsAlreadyPresentCommitWithoutCarriedVerdict(t *testing.T) {
	f := newFixture(t)
	f.commit(t, "change", "change\n", "same patch")
	got, err := f.apply(t)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.DroppedCommits) != 1 || got.DroppedCommits[0] != f.source || got.Derived.Number != 2 {
		t.Fatalf("dropped commit: %+v", got)
	}
	if err := review.RequireVerdict(context.Background(), f.store, "W", "C1", 2, got.HeadSHA, "apply", "L"); !errors.Is(err, loomgit.NewError(loomgit.ReviewRequired, "", nil)) {
		t.Fatalf("dropped commit carried approval: %v", err)
	}
}

func TestApplyReplaysThreeTaskCommitsInOrder(t *testing.T) {
	f := fixtureWithSource(t, func(t *testing.T, f *fixture) {
		for _, step := range []struct{ path, subject string }{{"second", "second"}, {"capture", "loom: uncommitted work from task T1"}} {
			f.commit(t, step.path, step.path+"\n", step.subject+"\n\nLoom-Change-Id: C1\nLoom-Revision: 1\nLoom-Task: T1\nLoom-Attempt: A1")
		}
	})
	target := f.commit(t, "target", "target\n", "target")
	got, err := f.apply(t)
	if err != nil {
		t.Fatal(err)
	}
	commits := strings.Fields(f.git(t, "rev-list", "--reverse", target+".."+got.HeadSHA))
	if len(commits) != 3 || got.Derived.Number != 2 {
		t.Fatalf("commits=%v derived=%+v", commits, got.Derived)
	}
	log, err := f.service.AppliedLog(context.Background(), "W", "L")
	if err != nil || len(log) != 1 || len(log[0].CommitDetails) != 3 {
		t.Fatalf("log=%+v err=%v", log, err)
	}
	for i, sha := range commits {
		if log[0].CommitDetails[i].SHA != sha || log[0].CommitDetails[i].Task != "T1" ||
			f.git(t, "show", "-s", "--format=%B", sha) != f.git(t, "show", "-s", "--format=%B", strings.Fields(f.git(t, "rev-list", "--reverse", f.base+".."+f.source))[i]) {
			t.Fatalf("commit %d attribution or message differs", i)
		}
	}
}

func TestApplySecondCommitConflictKeepsRefsAndFiles(t *testing.T) {
	var second string
	f := fixtureWithSource(t, func(t *testing.T, f *fixture) {
		second = f.commit(t, "base", "source\n", "second")
		f.commit(t, "third", "third\n", "third")
	})
	target := f.commit(t, "base", "target\n", "target")
	refs := f.git(t, "for-each-ref", "--format=%(refname) %(objectname)")
	index := f.git(t, "ls-files", "--stage")
	got, err := f.apply(t)
	if !errors.Is(err, loomgit.NewError(loomgit.Conflict, "", nil)) || got.ConflictCommit != second ||
		len(got.Paths) != 1 || got.Paths[0] != "base" {
		t.Fatalf("conflict: %+v, %v", got, err)
	}
	if f.git(t, "rev-parse", "HEAD") != target || f.git(t, "for-each-ref", "--format=%(refname) %(objectname)") != refs ||
		f.git(t, "ls-files", "--stage") != index || f.git(t, "show", "HEAD:base") != "target" {
		t.Fatal("conflict changed checkout or revision refs")
	}
}

func TestApplyMergeCommitBecomesSingleParentLayer(t *testing.T) {
	f := fixtureWithSource(t, func(t *testing.T, f *fixture) {
		f.git(t, "checkout", "-q", "-b", "side", f.base)
		f.commit(t, "side-file", "side\n", "side")
		f.git(t, "checkout", "-q", "source")
		f.git(t, "merge", "--no-ff", "-qm", "merge side", "side")
	})
	target := f.commit(t, "target", "target\n", "target")
	got, err := f.apply(t)
	if err != nil {
		t.Fatal(err)
	}
	if f.git(t, "rev-list", "--count", target+".."+got.HeadSHA) != "2" ||
		f.git(t, "show", "-s", "--format=%p", got.HeadSHA) == "" ||
		len(strings.Fields(f.git(t, "show", "-s", "--format=%p", got.HeadSHA))) != 1 ||
		f.git(t, "show", "-s", "--format=%s", got.HeadSHA) != "merge side" {
		t.Fatalf("merge layer not linear: %+v", got)
	}
}

func TestApplyRequiresApprovalBeforeAnyCheckoutChange(t *testing.T) {
	f := newFixture(t)
	if _, err := f.store.RecordVerdict(context.Background(), loomgit.Verdict{
		Workspace: "W", Change: "C1", Number: 1, HeadSHA: f.source,
		Kind: "reject", ActorKind: "human", ActorID: "reviewer",
	}); err != nil {
		t.Fatal(err)
	}
	before := f.git(t, "for-each-ref", "--format=%(refname) %(objectname)")
	_, err := f.apply(t)
	if !errors.Is(err, loomgit.NewError(loomgit.ReviewRequired, "", nil)) {
		t.Fatalf("approval gate: %v", err)
	}
	if f.git(t, "for-each-ref", "--format=%(refname) %(objectname)") != before || f.git(t, "rev-parse", "HEAD") != f.base {
		t.Fatal("rejected revision changed checkout")
	}
}

func TestApplyDoesNotPushBareRemote(t *testing.T) {
	f := newFixture(t)
	remote := filepath.Join(t.TempDir(), "origin.git")
	if out, err := exec.Command("git", "init", "--bare", "-q", remote).CombinedOutput(); err != nil { //nolint:norawexec // Temporary bare remote for a local integration test.
		t.Fatalf("init bare remote: %s: %v", out, err)
	}
	if out, err := exec.Command("git", "-C", f.dir, "remote", "add", "origin", remote).CombinedOutput(); err != nil { //nolint:norawexec // Temporary real-Git fixture, no network.
		t.Fatalf("add remote: %s: %v", out, err)
	}
	if out, err := exec.Command("git", "-C", f.dir, "push", "origin", f.base+":refs/heads/main").CombinedOutput(); err != nil { //nolint:norawexec // Pushes only the test fixture to its temporary bare remote.
		t.Fatalf("seed remote: %s: %v", out, err)
	}
	if _, err := f.apply(t); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "--git-dir="+remote, "rev-parse", "refs/heads/main").CombinedOutput() //nolint:norawexec // Reads the temporary bare remote fixture.
	if err != nil || strings.TrimSpace(string(out)) != f.base {
		t.Fatalf("remote ref moved: %s: %v", out, err)
	}
}
