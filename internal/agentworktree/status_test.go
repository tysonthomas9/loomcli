package agentworktree

import (
	"context"
	"errors"
	"go/build"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomagent"
)

func portSetup(t *testing.T) (loomagent.Workspace, *Worktrees, string) {
	t.Helper()
	w, repo := setup(t)
	return Port{W: w}, w, repo
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceStatusFingerprint(t *testing.T) {
	ctx := context.Background()
	ws, _, repo := portSetup(t)
	s := loomagent.WorkspaceSpec{Key: "agt_s", Repo: repo, BaseRef: "main", Branch: "loom/agent/agt_s"}
	wc, err := ws.Ensure(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	status := func() loomagent.WorkspaceStatus {
		t.Helper()
		st, err := ws.Status(ctx, s)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}

	clean := status()
	if len(clean.Uncommitted) != 0 || clean.Branch != s.Branch || clean.HEAD != wc.HEAD || clean.Fingerprint == "" {
		t.Fatalf("clean Status = %+v, want no paths on %s at %s", clean, s.Branch, wc.HEAD)
	}

	seen := map[string]string{clean.Fingerprint: "clean"}
	step := func(name string, change func(), wantPaths ...string) {
		t.Helper()
		change()
		st := status()
		if !slices.Equal(st.Uncommitted, wantPaths) {
			t.Fatalf("%s: paths = %q, want %q", name, st.Uncommitted, wantPaths)
		}
		if prev, ok := seen[st.Fingerprint]; ok {
			t.Fatalf("%s: fingerprint unchanged from %s", name, prev)
		}
		seen[st.Fingerprint] = name
		if again := status(); again.Fingerprint != st.Fingerprint {
			t.Fatalf("%s: fingerprint not stable: %s then %s", name, st.Fingerprint, again.Fingerprint)
		}
	}
	base := filepath.Join(wc.Path, "base.txt")
	step("modified", func() { write(t, base, "edit one") }, "base.txt")
	step("same path, new content", func() { write(t, base, "edit two") }, "base.txt")
	step("untracked in subdir", func() {
		if err := os.MkdirAll(filepath.Join(wc.Path, "dir"), 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(wc.Path, "dir", "new file.txt"), "new")
	}, "base.txt", "dir/new file.txt")
	step("untracked content", func() { write(t, filepath.Join(wc.Path, "dir", "new file.txt"), "newer") }, "base.txt", "dir/new file.txt")
	step("staged", func() { run(t, wc.Path, "add", "base.txt") }, "base.txt", "dir/new file.txt")
	step("deleted", func() {
		run(t, wc.Path, "reset", "-q", "--hard")
		if err := os.Remove(base); err != nil {
			t.Fatal(err)
		}
	}, "base.txt", "dir/new file.txt")

	run(t, wc.Path, "add", "-A")
	run(t, wc.Path, "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "-qm", "agent work")
	st := status()
	if len(st.Uncommitted) != 0 || st.HEAD == wc.HEAD || st.HEAD != run(t, wc.Path, "rev-parse", "HEAD") {
		t.Fatalf("after commit Status = %+v, want no paths and the new head", st)
	}
}

func TestWorkspaceStatusDetachedPRRef(t *testing.T) {
	ctx := context.Background()
	ws, _, repo := portSetup(t)
	run(t, repo, "checkout", "-q", "-b", "pr-src")
	head := commit(t, repo, "pr.txt", "pr change")
	run(t, repo, "checkout", "-q", "main")
	run(t, repo, "update-ref", "refs/pull/7/head", head)
	run(t, repo, "branch", "-D", "pr-src")

	s := loomagent.WorkspaceSpec{Key: "rev_pr7", Repo: repo, BaseRef: "refs/pull/7/head", Detached: true}
	if _, err := ws.Ensure(ctx, s); err != nil {
		t.Fatal(err)
	}
	st, err := ws.Status(ctx, s)
	if err != nil || st.Branch != "" || st.HEAD != head || len(st.Uncommitted) != 0 {
		t.Fatalf("Status = %+v, %v; want detached at %s", st, err, head)
	}
	if err := ws.Remove(ctx, s); err != nil {
		t.Fatal(err)
	}
	if got := run(t, repo, "rev-parse", "refs/pull/7/head"); got != head {
		t.Fatalf("PR ref moved to %s", got)
	}
}

// refs lists every ref and the commit it points at, so a test can show that
// branches and history are untouched.
func refs(t *testing.T, repo string) string {
	t.Helper()
	return run(t, repo, "for-each-ref", "--format=%(refname) %(objectname)")
}

func TestWorkspaceStatusMissingIsAbsent(t *testing.T) {
	ctx := context.Background()
	ws, _, repo := portSetup(t)
	s := loomagent.WorkspaceSpec{Key: "agt_m", Repo: repo, BaseRef: "main", Branch: "loom/agent/agt_m"}
	if st, err := ws.Status(ctx, s); err != nil || len(st.Uncommitted) != 0 {
		t.Fatalf("never-made Status = %+v, %v; want absent", st, err)
	}
	wc, err := ws.Ensure(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	commit(t, wc.Path, "mine.txt", "agent history")
	other := loomagent.WorkspaceSpec{Key: "agt_n", Repo: repo, BaseRef: "main", Branch: "loom/agent/agt_n"}
	oc, err := ws.Ensure(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	commit(t, oc.Path, "theirs.txt", "other history")
	write(t, filepath.Join(oc.Path, "wip.txt"), "other agent's work")
	before := refs(t, repo)

	for i := 0; i < 2; i++ { // the removal, then a retry after it
		if err := ws.Remove(ctx, s); err != nil {
			t.Fatalf("Remove %d: %v", i, err)
		}
		if st, err := ws.Status(ctx, s); err != nil || len(st.Uncommitted) != 0 {
			t.Fatalf("removed Status = %+v, %v; want absent", st, err)
		}
	}
	if after := refs(t, repo); after != before {
		t.Fatalf("refs changed:\n%s\nwant\n%s", after, before)
	}
	if b, err := os.ReadFile(filepath.Join(oc.Path, "wip.txt")); err != nil || string(b) != "other agent's work" {
		t.Fatalf("other agent's worktree touched: %q, %v", b, err)
	}
	if st, err := ws.Status(ctx, other); err != nil || !slices.Equal(st.Uncommitted, []string{"wip.txt"}) {
		t.Fatalf("other Status = %+v, %v", st, err)
	}
}

func TestWorkspaceStatusAbsentOnlyWhenMissing(t *testing.T) {
	ctx := context.Background()
	ws, w, repo := portSetup(t)
	s := loomagent.WorkspaceSpec{Key: "agt_p", Repo: repo, BaseRef: "main", Branch: "loom/agent/agt_p"}
	wc, err := ws.Ensure(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(wc.Path)
	if err := os.Chmod(parent, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })
	if _, err := os.Lstat(wc.Path); err == nil || os.IsNotExist(err) {
		t.Skipf("cannot make an unreadable parent here (running as root?): %v", err)
	}
	if _, err := ws.Status(ctx, s); err == nil || errors.Is(err, ErrNotOwned) {
		t.Fatalf("Status on unreadable path = %v; want a stat error, not absent", err)
	}
	if err := ws.Remove(ctx, s); err == nil {
		t.Fatal("Remove on unreadable path = nil; want a stat error")
	}
	if err := os.Chmod(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wc.Path, "base.txt")); err != nil {
		t.Fatalf("worktree touched: %v", err)
	}

	plain := loomagent.WorkspaceSpec{Key: "agt_q", Repo: repo, BaseRef: "main", Branch: "loom/agent/agt_q"}
	ppath, _ := w.Path(Spec(plain))
	if err := os.MkdirAll(ppath, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = ws.Status(ctx, plain)
	requireNotOwned(t, err)
}

func TestWorkspaceStatusRefusesForeign(t *testing.T) {
	ctx := context.Background()
	ws, _, repo := portSetup(t)
	s := loomagent.WorkspaceSpec{Key: "agt_f", Repo: repo, BaseRef: "main", Branch: "loom/agent/agt_f"}
	if _, err := ws.Ensure(ctx, s); err != nil {
		t.Fatal(err)
	}
	s.Branch = "loom/agent/other"
	_, err := ws.Status(ctx, s)
	requireNotOwned(t, err)
}

func TestWorkspaceRemoveOwned(t *testing.T) {
	ctx := context.Background()

	t.Run("removes clean worktree and keeps branch", func(t *testing.T) {
		ws, _, repo := portSetup(t)
		s := loomagent.WorkspaceSpec{Key: "agt_r", Repo: repo, BaseRef: "main", Branch: "loom/agent/agt_r"}
		other := loomagent.WorkspaceSpec{Key: "agt_o", Repo: repo, BaseRef: "main", Branch: "loom/agent/agt_o"}
		wc, err := ws.Ensure(ctx, s)
		if err != nil {
			t.Fatal(err)
		}
		oc, err := ws.Ensure(ctx, other)
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(oc.Path, "wip.txt"), "other agent's work")
		write(t, filepath.Join(wc.Path, "agent.txt"), "done")
		run(t, wc.Path, "add", "agent.txt")
		run(t, wc.Path, "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "-qm", "done")
		tip := run(t, wc.Path, "rev-parse", "HEAD")
		before := refs(t, repo)

		if err := ws.Remove(ctx, s); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(wc.Path); !os.IsNotExist(err) {
			t.Fatalf("worktree still exists: %v", err)
		}
		if got := run(t, repo, "rev-parse", "refs/heads/"+s.Branch); got != tip {
			t.Fatalf("branch at %s, want kept tip %s", got, tip)
		}
		if strings.Contains(run(t, repo, "worktree", "list", "--porcelain"), wc.Path) {
			t.Fatal("worktree still registered")
		}
		if b, err := os.ReadFile(filepath.Join(oc.Path, "wip.txt")); err != nil || string(b) != "other agent's work" {
			t.Fatalf("other agent's worktree touched: %q, %v", b, err)
		}
		if err := ws.Remove(ctx, s); err != nil {
			t.Fatalf("second Remove = %v, want nil", err)
		}
		if after := refs(t, repo); after != before {
			t.Fatalf("refs changed:\n%s\nwant\n%s", after, before)
		}
		again, err := ws.Ensure(ctx, loomagent.WorkspaceSpec{Key: s.Key, Repo: repo, Branch: s.Branch})
		if err != nil || again.HEAD != tip {
			t.Fatalf("re-Ensure = %+v, %v; want kept tip %s", again, err, tip)
		}
	})

	t.Run("refuses dirty", func(t *testing.T) {
		for name, dirty := range map[string]func(dir string){
			"modified":  func(dir string) { write(t, filepath.Join(dir, "base.txt"), "edit") },
			"untracked": func(dir string) { write(t, filepath.Join(dir, "new.txt"), "new") },
			"staged": func(dir string) {
				write(t, filepath.Join(dir, "base.txt"), "staged")
				run(t, dir, "add", "base.txt")
			},
		} {
			ws, _, repo := portSetup(t)
			s := loomagent.WorkspaceSpec{Key: "agt_d", Repo: repo, BaseRef: "main", Branch: "loom/agent/agt_d"}
			wc, err := ws.Ensure(ctx, s)
			if err != nil {
				t.Fatal(err)
			}
			dirty(wc.Path)
			if err := ws.Remove(ctx, s); !errors.Is(err, ErrDirty) {
				t.Fatalf("%s: Remove = %v, want ErrDirty", name, err)
			}
			if _, err := os.Stat(filepath.Join(wc.Path, "base.txt")); err != nil {
				t.Fatalf("%s: worktree changed: %v", name, err)
			}
		}
	})

	t.Run("refuses foreign", func(t *testing.T) {
		ws, w, repo := portSetup(t)
		s := loomagent.WorkspaceSpec{Key: "agt_x", Repo: repo, BaseRef: "main", Branch: "loom/agent/agt_x"}
		wc, err := ws.Ensure(ctx, s)
		if err != nil {
			t.Fatal(err)
		}
		wrongBranch := s
		wrongBranch.Branch = "loom/agent/someone-else"
		requireNotOwned(t, ws.Remove(ctx, wrongBranch))
		asReviewer := loomagent.WorkspaceSpec{Key: s.Key, Repo: repo, BaseRef: "main", Detached: true}
		requireNotOwned(t, ws.Remove(ctx, asReviewer))
		if _, err := os.Stat(wc.Path); err != nil {
			t.Fatalf("owned worktree removed by a foreign spec: %v", err)
		}

		other := newRepo(t, filepath.Join(t.TempDir(), "other"))
		fs := loomagent.WorkspaceSpec{Key: "agt_y", Repo: repo, BaseRef: "main", Branch: "loom/agent/agt_y"}
		path, _ := w.Path(Spec(fs))
		run(t, other, "worktree", "add", "-q", "-b", fs.Branch, path)
		requireNotOwned(t, ws.Remove(ctx, fs))

		plain := loomagent.WorkspaceSpec{Key: "agt_z", Repo: repo, BaseRef: "main", Branch: "loom/agent/agt_z"}
		ppath, _ := w.Path(Spec(plain))
		if err := os.MkdirAll(ppath, 0o755); err != nil {
			t.Fatal(err)
		}
		requireNotOwned(t, ws.Remove(ctx, plain))
		for _, p := range []string{path, ppath} {
			if _, err := os.Stat(p); err != nil {
				t.Fatalf("foreign path %s touched: %v", p, err)
			}
		}
	})

	t.Run("loomagent runs no git", func(t *testing.T) {
		pkg, err := build.Import("github.com/tysonthomas9/loomcli/internal/loomagent", ".", 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range pkg.Imports {
			if imp == "os/exec" || strings.HasSuffix(imp, "/internal/gitrunner") || strings.HasSuffix(imp, "/internal/agentworktree") {
				t.Fatalf("loomagent imports %s", imp)
			}
		}
	})
}

func TestWorkspaceRemoveConfirmedFingerprint(t *testing.T) {
	ctx := context.Background()
	ws, _, repo := portSetup(t)
	s := loomagent.WorkspaceSpec{Key: "agt_c", Repo: repo, BaseRef: "main", Branch: "loom/agent/agt_c"}
	wc, err := ws.Ensure(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(wc.Path, "base.txt"), "edit")
	write(t, filepath.Join(wc.Path, "new.txt"), "untracked")
	seen, err := ws.Status(ctx, s)
	if err != nil || len(seen.Uncommitted) != 2 {
		t.Fatalf("Status = %+v, %v", seen, err)
	}

	if err := ws.Remove(ctx, s); !errors.Is(err, ErrDirty) {
		t.Fatalf("unconfirmed Remove = %v, want ErrDirty", err)
	}
	write(t, filepath.Join(wc.Path, "new.txt"), "edited after the user confirmed")
	stale := s
	stale.Confirm = seen.Fingerprint
	if err := ws.Remove(ctx, stale); !errors.Is(err, ErrDirty) {
		t.Fatalf("stale Remove = %v, want ErrDirty", err)
	}
	if b, err := os.ReadFile(filepath.Join(wc.Path, "new.txt")); err != nil || string(b) != "edited after the user confirmed" {
		t.Fatalf("stale Remove touched work: %q, %v", b, err)
	}

	now, err := ws.Status(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	// Confirm waives only the dirty-work check, never ownership.
	wrongBranch := s
	wrongBranch.Branch, wrongBranch.Confirm = "loom/agent/someone-else", now.Fingerprint
	requireNotOwned(t, ws.Remove(ctx, wrongBranch))
	asReviewer := loomagent.WorkspaceSpec{Key: s.Key, Repo: repo, BaseRef: "main", Detached: true, Confirm: now.Fingerprint}
	requireNotOwned(t, ws.Remove(ctx, asReviewer))
	sameName := newRepo(t, filepath.Join(t.TempDir(), filepath.Base(repo))) // same <root>/<repo name>/<key> path
	otherRepo := s
	otherRepo.Repo, otherRepo.Confirm = sameName, now.Fingerprint
	requireNotOwned(t, ws.Remove(ctx, otherRepo))
	if again, err := ws.Status(ctx, s); err != nil || again.Fingerprint != now.Fingerprint {
		t.Fatalf("foreign confirmed Remove touched the work: %+v, %v", again, err)
	}

	before := refs(t, repo)
	confirmed := s
	confirmed.Confirm = now.Fingerprint
	if err := ws.Remove(ctx, confirmed); err != nil {
		t.Fatalf("confirmed Remove = %v", err)
	}
	if err := ws.Remove(ctx, confirmed); err != nil {
		t.Fatalf("retried confirmed Remove = %v", err)
	}
	if after := refs(t, repo); after != before {
		t.Fatalf("refs changed:\n%s\nwant\n%s", after, before)
	}
	if _, err := os.Stat(wc.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists: %v", err)
	}
	if got := run(t, repo, "rev-parse", "refs/heads/"+s.Branch); got != wc.HEAD {
		t.Fatalf("branch at %s, want kept %s", got, wc.HEAD)
	}
	if st, err := ws.Status(ctx, s); err != nil || len(st.Uncommitted) != 0 {
		t.Fatalf("Status after delete = %+v, %v; want absent", st, err)
	}
}
