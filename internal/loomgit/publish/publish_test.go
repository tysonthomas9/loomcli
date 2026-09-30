package publish

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
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...) //nolint:norawexec // Disposable Git repositories are the test subject.
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

type fixture struct {
	repo, remote, base string
	store              *journal.SQLite
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	root := t.TempDir()
	repo, remote := filepath.Join(root, "repo"), filepath.Join(root, "remote.git")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "config", "user.name", "Publisher")
	git(t, repo, "config", "user.email", "publisher@example.test")
	if err := os.WriteFile(filepath.Join(repo, "file"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "file")
	git(t, repo, "commit", "-qm", "base")
	base := git(t, repo, "rev-parse", "HEAD")
	git(t, root, "init", "-q", "--bare", remote)
	git(t, repo, "remote", "add", "origin", remote)
	store, err := journal.OpenSQLite(filepath.Join(root, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return fixture{repo, remote, base, store}
}

func (f fixture) revision(t *testing.T, number int, parent, body, kind string) loomgit.Revision {
	t.Helper()
	git(t, f.repo, "reset", "-q", "--hard", parent)
	if err := os.WriteFile(filepath.Join(f.repo, "file"), []byte(body+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, f.repo, "add", "file")
	git(t, f.repo, "commit", "-qm", "layer\n\nLoom-Change-Id: C\nLoom-Revision: "+strconv.Itoa(number))
	head := git(t, f.repo, "rev-parse", "HEAD")
	ctx := context.Background()
	r, err := f.store.ReserveRevision(ctx, loomgit.Revision{Workspace: "W", Change: "C", RequestID: "r" + strconv.Itoa(number),
		Kind: kind, Operation: "apply", Outcome: "completed", BaseSHA: parent, TreeHash: head, SourceHeadSHA: head})
	if err != nil {
		t.Fatal(err)
	}
	r.HeadSHA = head
	if err := f.store.FinishRevision(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SaveApplied(ctx, loomgit.AppliedLayer{RequestID: "apply-" + strconv.Itoa(number), Workspace: "W", Lead: "L", Change: "C", Revision: r.Number, OldTip: parent, NewTip: head, Commits: []string{head}}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.AdvanceApplied(ctx, "apply-"+strconv.Itoa(number), "prepared", "done"); err != nil {
		t.Fatal(err)
	}
	ref, err := refname.RevisionHead("W", "C", strconv.Itoa(r.Number))
	if err != nil {
		t.Fatal(err)
	}
	git(t, f.repo, "update-ref", ref, head)
	r.Ready = true
	return r
}

func (f fixture) approve(t *testing.T, r loomgit.Revision) {
	t.Helper()
	if _, err := review.Submit(context.Background(), f.store, "W", "C", r.Number, r.HeadSHA, "approve", "", review.Actor{Kind: "human", ID: "reviewer"}); err != nil {
		t.Fatal(err)
	}
}

func (f fixture) request() Request {
	return Request{Workspace: "W", Lead: "L", Change: "C", Repo: f.repo, WorkingArea: f.repo, BaseSHA: f.base}
}

func (f fixture) published(t *testing.T) string {
	t.Helper()
	ref, err := refname.Publication("W", "C")
	if err != nil {
		t.Fatal(err)
	}
	return git(t, f.repo, "rev-parse", ref)
}

func (f fixture) remoteHead(t *testing.T) string {
	t.Helper()
	return git(t, f.remote, "rev-parse", "refs/heads/loom/ws/W/change/C")
}

func codeIs(t *testing.T, err error, code loomgit.Code) {
	t.Helper()
	var coded *loomgit.Error
	if !errors.As(err, &coded) || coded.Kind != code {
		t.Fatalf("error = %v, want %s", err, code)
	}
}

func TestPublishCreatesAndLeasedReplacesLayer(t *testing.T) {
	f := newFixture(t)
	r1 := f.revision(t, 1, f.base, "one", "source")
	f.approve(t, r1)
	got, err := Publish(context.Background(), f.store, f.request())
	if err != nil || got.HeadSHA != r1.HeadSHA || f.remoteHead(t) != r1.HeadSHA || f.published(t) != r1.HeadSHA {
		t.Fatalf("first publish: revision=%+v err=%v", got, err)
	}
	r2 := f.revision(t, 2, f.base, "source two", "source")
	r3 := f.revision(t, 3, f.base, "derived three", "derived")
	f.approve(t, r3)
	got, err = Publish(context.Background(), f.store, f.request())
	if err != nil || got.Number != r3.Number || got.HeadSHA != r3.HeadSHA || f.remoteHead(t) != r3.HeadSHA || f.published(t) != r3.HeadSHA {
		t.Fatalf("derived publish: revision=%+v err=%v, source=%s", got, err, r2.HeadSHA)
	}
}

func TestPublishRefusesForeignRemoteMove(t *testing.T) {
	f := newFixture(t)
	r1 := f.revision(t, 1, f.base, "one", "source")
	f.approve(t, r1)
	if _, err := Publish(context.Background(), f.store, f.request()); err != nil {
		t.Fatal(err)
	}
	foreign := f.revision(t, 2, f.base, "reviewer", "source")
	git(t, f.repo, "push", "--force-with-lease=refs/heads/loom/ws/W/change/C:"+r1.HeadSHA,
		"origin", foreign.HeadSHA+":refs/heads/loom/ws/W/change/C")
	r3 := f.revision(t, 3, f.base, "three", "derived")
	f.approve(t, r3)
	_, err := Publish(context.Background(), f.store, f.request())
	codeIs(t, err, loomgit.Diverged)
	if f.remoteHead(t) != foreign.HeadSHA || f.published(t) != r1.HeadSHA {
		t.Fatal("foreign move or publication tracking changed")
	}
}

func TestPublishRefusesUnknownFirstBranchAndUnownedTarget(t *testing.T) {
	f := newFixture(t)
	r := f.revision(t, 1, f.base, "one", "source")
	f.approve(t, r)
	foreign := git(t, f.repo, "rev-parse", f.base)
	git(t, f.repo, "push", "origin", foreign+":refs/heads/loom/ws/W/change/C")
	_, err := Publish(context.Background(), f.store, f.request())
	codeIs(t, err, loomgit.Diverged)
	if f.remoteHead(t) != foreign {
		t.Fatal("unknown branch changed")
	}
	req := f.request()
	req.Branch = "main"
	_, err = Publish(context.Background(), f.store, req)
	codeIs(t, err, loomgit.Protected)
}

func TestPublishRequiresApprovedCurrentLayer(t *testing.T) {
	f := newFixture(t)
	f.revision(t, 1, f.base, "one", "source")
	_, err := Publish(context.Background(), f.store, f.request())
	codeIs(t, err, loomgit.ReviewRequired)
	if got := git(t, f.remote, "for-each-ref", "--format=%(refname)", "refs/heads"); got != "" {
		t.Fatalf("unexpected remote branch: %s", got)
	}
}

func TestPublishFindsLayerBelowAnotherChange(t *testing.T) {
	f := newFixture(t)
	r := f.revision(t, 1, f.base, "one", "source")
	f.approve(t, r)
	if err := os.WriteFile(filepath.Join(f.repo, "other"), []byte("second layer\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, f.repo, "add", "other")
	git(t, f.repo, "commit", "-qm", "next layer\n\nLoom-Change-Id: D\nLoom-Revision: 1")
	got, err := Publish(context.Background(), f.store, f.request())
	if err != nil || got.HeadSHA != r.HeadSHA || f.remoteHead(t) != r.HeadSHA {
		t.Fatalf("stacked layer: revision=%+v err=%v", got, err)
	}
}

func TestPublishSelectsAppliedLogInsteadOfCommitTrailer(t *testing.T) {
	f := newFixture(t)
	r := f.revision(t, 1, f.base, "one", "source")
	f.approve(t, r)
	if err := os.WriteFile(filepath.Join(f.repo, "other"), []byte("unlogged\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, f.repo, "add", "other")
	git(t, f.repo, "commit", "-qm", "unlogged\n\nLoom-Change-Id: C")
	got, err := Publish(context.Background(), f.store, f.request())
	if err != nil || got.HeadSHA != r.HeadSHA || f.remoteHead(t) != r.HeadSHA {
		t.Fatalf("selected unlogged trailer: revision=%+v err=%v", got, err)
	}
}

type probePusher struct {
	actual, ref, head, expected string
}

func (p *probePusher) RemoteSHA(_ context.Context, _, _ string) (string, error) {
	return p.actual, nil
}

func (p *probePusher) Push(_ context.Context, _, ref, head, expected string) error {
	p.ref, p.head, p.expected = ref, head, expected
	return nil
}

func TestPublishPassesOneExactLeaseToMirrorPusher(t *testing.T) {
	f := newFixture(t)
	r := f.revision(t, 1, f.base, "one", "source")
	f.approve(t, r)
	runner, err := gitexec.New(f.repo, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
	if err != nil {
		t.Fatal(err)
	}
	p := &probePusher{}
	if err := push(context.Background(), runner, p, "W", "C", "loom/ws/W/change/C", r.HeadSHA); err != nil {
		t.Fatal(err)
	}
	if p.ref != "refs/heads/loom/ws/W/change/C" || p.head != r.HeadSHA || p.expected != "" {
		t.Fatalf("first push args: %+v", p)
	}
	r2 := f.revision(t, 2, f.base, "two", "derived")
	p = &probePusher{actual: r.HeadSHA}
	if err := push(context.Background(), runner, p, "W", "C", "loom/ws/W/change/C", r2.HeadSHA); err != nil {
		t.Fatal(err)
	}
	if p.ref != "refs/heads/loom/ws/W/change/C" || p.head != r2.HeadSHA || p.expected != r.HeadSHA {
		t.Fatalf("replacement push args: %+v", p)
	}
}
