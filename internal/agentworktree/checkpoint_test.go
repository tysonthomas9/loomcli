package agentworktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomagent"
)

const turn1 = "refs/loom/checkpoints/agt_1/turn/1"

// agentTree makes agt_1's worktree on its own branch and returns its spec and path.
func agentTree(t *testing.T) (*Worktrees, Spec, string) {
	t.Helper()
	w, repo := setup(t)
	s := Spec{Key: "agt_1", Repo: repo, BaseRef: "main", Branch: "loom/agent/agt_1"}
	wt, err := w.Ensure(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	return w, s, wt.Path
}

// tree lists ref's files with their content, "path=content" sorted by path.
func tree(t *testing.T, dir, ref string) string {
	t.Helper()
	var out []string
	for _, p := range strings.Split(run(t, dir, "ls-tree", "-r", "--name-only", ref), "\n") {
		out = append(out, p+"="+run(t, dir, "show", ref+":"+p))
	}
	return strings.Join(out, " ")
}

// gitState is what a capture must leave alone: the index file, the
// branch, its log and the status.
func gitState(t *testing.T, dir string) string {
	t.Helper()
	idx, err := os.ReadFile(run(t, dir, "rev-parse", "--path-format=absolute", "--git-path", "index"))
	if err != nil {
		t.Fatal(err)
	}
	return string(idx) + "\x00" + run(t, dir, "symbolic-ref", "HEAD") + "\x00" + run(t, dir, "log", "--format=%H %s") +
		"\x00" + run(t, dir, "status", "--porcelain=v2", "--untracked-files=all")
}

// TestCheckpointLeavesIndexUntouched: the capture holds tracked, staged and
// untracked files as on disk, on top of HEAD, while the linked worktree's own
// index (found with --git-path), branch, log and status are unchanged.
func TestCheckpointLeavesIndexUntouched(t *testing.T) {
	ctx := context.Background()
	w, s, path := agentTree(t)
	write(t, filepath.Join(path, "base.txt"), "edited")
	write(t, filepath.Join(path, "staged.txt"), "staged")
	run(t, path, "add", "staged.txt")
	write(t, filepath.Join(path, "staged.txt"), "staged then edited")
	write(t, filepath.Join(path, "new.txt"), "untracked")
	before := gitState(t, path)
	if err := (Port{W: w}).Checkpoint(ctx, loomagent.WorkspaceSpec(s), turn1); err != nil {
		t.Fatal(err)
	}
	if after := gitState(t, path); after != before {
		t.Fatalf("capture changed the worktree's git state:\n%q\nwant\n%q", after, before)
	}
	if got, want := tree(t, path, turn1), "base.txt=edited new.txt=untracked staged.txt=staged then edited"; got != want {
		t.Fatalf("turn/1 = %q, want %q", got, want)
	}
	if parent, head := run(t, path, "rev-parse", turn1+"^"), run(t, path, "rev-parse", "HEAD"); parent != head {
		t.Fatalf("turn/1 parent %s, want HEAD %s", parent, head)
	}
}

// TestCheckpointSparseAndNestedRepo: in a sparse checkout a file outside the
// cone keeps its committed content instead of reading as deleted; nested
// repositories, with or without a commit, are left out and named in the
// commit message.
func TestCheckpointSparseAndNestedRepo(t *testing.T) {
	ctx := context.Background()
	w, repo := setup(t)
	if err := os.MkdirAll(filepath.Join(repo, "out"), 0o750); err != nil {
		t.Fatal(err)
	}
	commit(t, repo, "out/kept.txt", "outside the cone")
	s := Spec{Key: "agt_1", Repo: repo, BaseRef: "main", Branch: "loom/agent/agt_1"}
	wt, err := w.Ensure(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	run(t, wt.Path, "sparse-checkout", "set", "in")
	if _, err := os.Stat(filepath.Join(wt.Path, "out", "kept.txt")); !os.IsNotExist(err) {
		t.Fatalf("sparse checkout left out/kept.txt: %v", err)
	}
	for _, d := range []string{"in/empty", "in/full"} {
		run(t, "", "init", "-q", filepath.Join(wt.Path, d))
	}
	commit(t, filepath.Join(wt.Path, "in/full"), "x.txt", "nested")
	write(t, filepath.Join(wt.Path, "in", "work.txt"), "work")
	if err := w.Checkpoint(ctx, s, turn1); err != nil {
		t.Fatal(err)
	}
	if got, want := tree(t, wt.Path, turn1), "base.txt=base in/work.txt=work out/kept.txt=outside the cone"; got != want {
		t.Fatalf("turn/1 = %q, want %q", got, want)
	}
	msg := run(t, wt.Path, "log", "-1", "--format=%B", turn1)
	for _, d := range []string{"in/empty/", "in/full/"} {
		if !strings.Contains(msg, "skipped nested repository "+d) {
			t.Fatalf("message %q does not record %s", msg, d)
		}
	}
}

// TestCheckpointTempIndexCleaned: the private index, and a lock a crash
// left on it, are gone after a capture.
func TestCheckpointTempIndexCleaned(t *testing.T) {
	w, s, path := agentTree(t)
	idx := run(t, path, "rev-parse", "--path-format=absolute", "--git-path", "loom-checkpoint.index")
	write(t, idx+".lock", "left by a crash")
	if err := w.Checkpoint(context.Background(), s, turn1); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{idx, idx + ".lock"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s left behind: %v", p, err)
		}
	}
}

// crashCheckpointAt makes the next Checkpoint crash at point; the returned
// func runs f and reports whether it crashed there.
func crashCheckpointAt(t *testing.T, point string) func(func()) bool {
	t.Helper()
	type crash struct{}
	checkpointCrash = func(p string) {
		if p == point {
			panic(crash{})
		}
	}
	t.Cleanup(func() { checkpointCrash = func(string) {} })
	return func(f func()) (crashed bool) {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(crash); !ok {
					panic(r)
				}
				crashed = true
				checkpointCrash = func(string) {}
			}
		}()
		f()
		return false
	}
}

// checkpointRefs lists the repo's checkpoint refs.
func checkpointRefs(t *testing.T, dir string) string {
	t.Helper()
	return run(t, dir, "for-each-ref", "--format=%(refname) %(objectname)", "refs/loom/checkpoints/")
}

// TestCheckpointRestartBeforeUpdateRef: a crash after the commit, before
// update-ref, leaves no ref; the retry makes exactly one, and a repeat
// changes nothing.
func TestCheckpointRestartBeforeUpdateRef(t *testing.T) {
	ctx := context.Background()
	w, s, path := agentTree(t)
	write(t, filepath.Join(path, "a.txt"), "a")
	if !crashCheckpointAt(t, "update-ref")(func() { _ = w.Checkpoint(ctx, s, turn1) }) {
		t.Fatal("no crash before update-ref")
	}
	if refs := checkpointRefs(t, path); refs != "" {
		t.Fatalf("refs after the crash = %q", refs)
	}
	for range 2 {
		if err := w.Checkpoint(ctx, s, turn1); err != nil {
			t.Fatal(err)
		}
	}
	if refs := checkpointRefs(t, path); strings.Count(refs, "\n") != 0 || !strings.HasPrefix(refs, turn1+" ") {
		t.Fatalf("refs = %q, want turn/1 once", refs)
	}
	if got := tree(t, path, turn1); got != "a.txt=a base.txt=base" {
		t.Fatalf("turn/1 = %q", got)
	}
}

// TestCheckpointRestartAfterUpdateRef: once the ref exists a repeat, even
// after the worktree changed, keeps the first capture.
func TestCheckpointRestartAfterUpdateRef(t *testing.T) {
	ctx := context.Background()
	w, s, path := agentTree(t)
	write(t, filepath.Join(path, "a.txt"), "turn 1")
	if err := w.Checkpoint(ctx, s, turn1); err != nil {
		t.Fatal(err)
	}
	first := checkpointRefs(t, path)
	write(t, filepath.Join(path, "a.txt"), "the next turn")
	if err := w.Checkpoint(ctx, s, turn1); err != nil {
		t.Fatal(err)
	}
	if again := checkpointRefs(t, path); again != first {
		t.Fatalf("repeat moved the ref: %q, was %q", again, first)
	}
	if got := run(t, path, "show", turn1+":a.txt"); got != "turn 1" {
		t.Fatalf("turn/1 a.txt = %q", got)
	}
}
