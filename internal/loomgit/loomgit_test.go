package loomgit

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec,gosec // fixed test helper commands.
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func newRepo(t *testing.T, dir string) string {
	t.Helper()
	run(t, "", "init", "-b", "main", dir)
	run(t, dir, "config", "user.name", "Test")
	run(t, dir, "config", "user.email", "test@example.test")
	commit(t, dir, "base.txt", "base")
	return dir
}

func commit(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "add", name)
	run(t, dir, "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "-m", body)
	return run(t, dir, "rev-parse", "HEAD")
}

func setup(t *testing.T) (*Worktrees, string) {
	t.Helper()
	tmp := t.TempDir()
	w, err := New(filepath.Join(tmp, "worktrees"), TargetLocal)
	if err != nil {
		t.Fatal(err)
	}
	return w, newRepo(t, filepath.Join(tmp, "repo"))
}

func requireNotOwned(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrNotOwned) {
		t.Fatalf("err = %v, want ErrNotOwned", err)
	}
}

func TestEnsureBranchFromLocalBaseIsIdempotent(t *testing.T) {
	w, repo := setup(t)
	run(t, repo, "checkout", "-b", "local-only")
	base := commit(t, repo, "local.txt", "local")
	run(t, repo, "checkout", "main")
	s := Spec{Key: "agt_1", Repo: repo, BaseRef: "local-only", Branch: "loom/agent/agt_1"}

	first, err := w.Ensure(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if first.HEAD != base || first.Branch != s.Branch {
		t.Fatalf("got %+v, want HEAD %s on %s", first, base, s.Branch)
	}
	if err := os.WriteFile(filepath.Join(first.Path, "work.txt"), []byte("wip"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := w.Ensure(context.Background(), s)
	if err != nil || second != first {
		t.Fatalf("second Ensure = %+v, %v; want %+v", second, err, first)
	}
	if _, err := os.Stat(filepath.Join(first.Path, "work.txt")); err != nil {
		t.Fatalf("agent work lost: %v", err)
	}
}

func TestEnsureBranchFromRemoteBase(t *testing.T) {
	tmp := t.TempDir()
	upstream := newRepo(t, filepath.Join(tmp, "upstream"))
	repo := filepath.Join(tmp, "repo")
	run(t, "", "clone", upstream, repo)
	tip := commit(t, upstream, "new.txt", "upstream advance")
	w, err := New(filepath.Join(tmp, "worktrees"), TargetLocal)
	if err != nil {
		t.Fatal(err)
	}

	got, err := w.Ensure(context.Background(), Spec{Key: "agt_r", Repo: repo, BaseRef: "main", Branch: "loom/agent/agt_r"})
	if err != nil {
		t.Fatal(err)
	}
	if got.HEAD != tip {
		t.Fatalf("HEAD = %s, want fetched remote tip %s", got.HEAD, tip)
	}
}

func TestEnsureRecreatesRemovedWorktreeFromKeptBranch(t *testing.T) {
	w, repo := setup(t)
	s := Spec{Key: "agt_2", Repo: repo, BaseRef: "main", Branch: "loom/agent/agt_2"}
	wt, err := w.Ensure(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	work := commit(t, wt.Path, "agent.txt", "agent work")
	if err := os.RemoveAll(wt.Path); err != nil { // a cleaned-up worktree; the registration goes stale
		t.Fatal(err)
	}

	again, err := w.Ensure(context.Background(), Spec{Key: s.Key, Repo: repo, Branch: s.Branch})
	if err != nil {
		t.Fatal(err)
	}
	if again.HEAD != work {
		t.Fatalf("HEAD = %s, want kept branch tip %s", again.HEAD, work)
	}
}

func TestEnsureNewBranchNeedsBaseRef(t *testing.T) {
	w, repo := setup(t)
	if _, err := w.Ensure(context.Background(), Spec{Key: "agt_3", Repo: repo, Branch: "loom/agent/agt_3"}); err == nil {
		t.Fatal("want error for new branch without BaseRef")
	}
}

func TestEnsureDetachedAtSHA(t *testing.T) {
	w, repo := setup(t)
	sha := run(t, repo, "rev-parse", "HEAD")
	commit(t, repo, "later.txt", "later")
	s := Spec{Key: "rev_1", Repo: repo, BaseRef: sha, Detached: true}

	got, err := w.Ensure(context.Background(), s)
	if err != nil || got.HEAD != sha || got.Branch != "" {
		t.Fatalf("Ensure = %+v, %v; want detached at %s", got, err, sha)
	}
	if again, err := w.Ensure(context.Background(), s); err != nil || again != got {
		t.Fatalf("second Ensure = %+v, %v", again, err)
	}
}

func TestWorktreeOwnershipRefusesWrongRepo(t *testing.T) {
	w, repo := setup(t)
	other := newRepo(t, filepath.Join(t.TempDir(), "other"))
	s := Spec{Key: "agt_4", Repo: repo, BaseRef: "main", Branch: "loom/agent/agt_4"}
	path, _ := w.Path(s)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, other, "worktree", "add", "-b", s.Branch, path)

	_, err := w.Ensure(context.Background(), s)
	requireNotOwned(t, err)
	if got := run(t, path, "rev-parse", "--path-format=absolute", "--git-common-dir"); !samePath(got, filepath.Join(other, ".git")) {
		t.Fatalf("other repo's worktree was replaced: %s", got)
	}
}

func TestWorktreeOwnershipRefusesWrongBranch(t *testing.T) {
	w, repo := setup(t)
	s := Spec{Key: "agt_5", Repo: repo, BaseRef: "main", Branch: "loom/agent/agt_5"}
	wt, err := w.Ensure(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	s.Branch = "loom/agent/someone-else"
	_, err = w.Ensure(context.Background(), s)
	requireNotOwned(t, err)
	if got := run(t, wt.Path, "branch", "--show-current"); got != "loom/agent/agt_5" {
		t.Fatalf("branch changed to %q", got)
	}
}

func TestWorktreeOwnershipRefusesDirtyOrMovedReviewer(t *testing.T) {
	w, repo := setup(t)
	sha := run(t, repo, "rev-parse", "HEAD")
	s := Spec{Key: "rev_2", Repo: repo, BaseRef: sha, Detached: true}
	wt, err := w.Ensure(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	dirty := filepath.Join(wt.Path, "notes.txt")
	if err := os.WriteFile(dirty, []byte("review notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = w.Ensure(context.Background(), s)
	requireNotOwned(t, err)
	if _, err := os.Stat(dirty); err != nil {
		t.Fatalf("dirty file not preserved: %v", err)
	}

	s.BaseRef = commit(t, repo, "next.txt", "next")
	_, err = w.Ensure(context.Background(), s)
	requireNotOwned(t, err)
}

func TestWorktreeOwnershipRefusesPlainFolder(t *testing.T) {
	w, repo := setup(t)
	s := Spec{Key: "agt_6", Repo: repo, BaseRef: "main", Branch: "loom/agent/agt_6"}
	path, _ := w.Path(s)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(path, "keep.txt")
	if err := os.WriteFile(keep, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := w.Ensure(context.Background(), s)
	requireNotOwned(t, err)
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("folder contents not preserved: %v", err)
	}
}

func TestEnsureRejectsBadSpecAndTarget(t *testing.T) {
	if _, err := New(t.TempDir(), Target("remote")); err == nil {
		t.Fatal("want error for non-local target")
	}
	w, repo := setup(t)
	for _, s := range []Spec{
		{Key: "../x", Repo: repo, BaseRef: "main", Branch: "b"},
		{Key: "k", Repo: repo, BaseRef: "main"},
		{Key: "k", Repo: repo, Branch: "b", Detached: true},
		{Key: "k", Repo: repo, Detached: true},
	} {
		if _, err := w.Ensure(context.Background(), s); err == nil {
			t.Fatalf("want error for %+v", s)
		}
	}
}
