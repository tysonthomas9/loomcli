package gitstate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// git runs a git command in dir and fails the test on error. The whole point of
// this package is real git plumbing (linked-worktree git dirs, MERGE_HEAD,
// stage-2/3 index entries), so mocking git here would test nothing.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-C", dir}, args...)
	out, err := exec.Command("git", full...).CombinedOutput() //nolint:norawexec
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// gitAllowFail runs git and returns success, for commands expected to conflict.
func gitAllowFail(t *testing.T, dir string, args ...string) bool {
	t.Helper()
	full := append([]string{"-C", dir}, args...)
	err := exec.Command("git", full...).Run() //nolint:norawexec
	return err == nil
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// newRepo builds a repo with a base commit on the default branch.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "test")
	git(t, dir, "config", "commit.gpgsign", "false")
	write(t, dir, "a.txt", "base\n")
	write(t, dir, "b.txt", "base\n")
	git(t, dir, "add", "a.txt", "b.txt")
	git(t, dir, "commit", "-q", "-m", "base")
	return dir
}

// conflictingBranch creates a "side" branch that conflicts with main on two
// files and returns its tip sha. HEAD is left on main.
func conflictingBranch(t *testing.T, dir string) string {
	t.Helper()
	git(t, dir, "checkout", "-q", "-b", "side")
	write(t, dir, "a.txt", "side\n")
	write(t, dir, "b.txt", "side\n")
	git(t, dir, "commit", "-q", "-am", "side")
	sha := git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "checkout", "-q", "main")
	write(t, dir, "a.txt", "main\n")
	write(t, dir, "b.txt", "main\n")
	git(t, dir, "commit", "-q", "-am", "main")
	return sha
}

func TestInspectCleanRepo(t *testing.T) {
	dir := newRepo(t)
	st, err := Inspect(dir)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if st.Op != OpNone {
		t.Fatalf("expected clean, got op=%q", st.Op)
	}
}

func TestInspectNotARepoAndMissingPath(t *testing.T) {
	for _, path := range []string{t.TempDir(), filepath.Join(t.TempDir(), "nope"), ""} {
		st, err := Inspect(path)
		if err != nil {
			t.Fatalf("Inspect(%q) returned error: %v", path, err)
		}
		if st.Op != OpNone {
			t.Fatalf("Inspect(%q): expected OpNone, got %q", path, st.Op)
		}
	}
}

func TestInspectConflictedMerge(t *testing.T) {
	dir := newRepo(t)
	sideSHA := conflictingBranch(t, dir)
	if gitAllowFail(t, dir, "merge", "side") {
		t.Fatal("expected the merge to conflict")
	}

	st, err := Inspect(dir)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if st.Op != OpMerge {
		t.Fatalf("op = %q, want merge", st.Op)
	}
	if st.Head != sideSHA {
		t.Fatalf("head = %q, want %q", st.Head, sideSHA)
	}
	if st.Unmerged != 2 {
		t.Fatalf("unmerged = %d, want 2", st.Unmerged)
	}
	if st.Branch != "main" {
		t.Fatalf("branch = %q, want main", st.Branch)
	}
	if !st.AgeKnown() || st.Age() > time.Minute {
		t.Fatalf("age = %v (known=%v), want a fresh timestamp", st.Age(), st.AgeKnown())
	}
	if !strings.Contains(st.String(), "merge in progress") {
		t.Fatalf("String() = %q", st.String())
	}
}

// TestInspectLinkedWorktreeMidMerge is the regression test for the actual
// incident: the poisoned tree was a LINKED worktree, whose MERGE_HEAD lives in
// <common>/worktrees/<name>/, not in <path>/.git. Resolving the git dir by hand
// would make this package detect nothing at all.
func TestInspectLinkedWorktreeMidMerge(t *testing.T) {
	dir := newRepo(t)
	sideSHA := conflictingBranch(t, dir)

	linked := filepath.Join(t.TempDir(), "linked")
	git(t, dir, "worktree", "add", "-q", "-b", "union", linked, "main")
	if gitAllowFail(t, linked, "merge", "side") {
		t.Fatal("expected the merge to conflict in the linked worktree")
	}

	// The state file must NOT be where a naive implementation would look.
	if _, err := os.Stat(filepath.Join(linked, ".git", "MERGE_HEAD")); err == nil {
		t.Fatal("precondition failed: MERGE_HEAD found under <path>/.git")
	}

	st, err := Inspect(linked)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if st.Op != OpMerge {
		t.Fatalf("op = %q, want merge", st.Op)
	}
	if st.Head != sideSHA {
		t.Fatalf("head = %q, want %q", st.Head, sideSHA)
	}
	if st.Unmerged != 2 {
		t.Fatalf("unmerged = %d, want 2", st.Unmerged)
	}
}

func TestInspectRebaseAndCherryPick(t *testing.T) {
	tests := []struct {
		name string
		want Op
		run  func(t *testing.T, dir string)
	}{
		{
			name: "rebase",
			want: OpRebase,
			run: func(t *testing.T, dir string) {
				if gitAllowFail(t, dir, "rebase", "side") {
					t.Fatal("expected the rebase to conflict")
				}
			},
		},
		{
			name: "cherry-pick",
			want: OpCherryPick,
			run: func(t *testing.T, dir string) {
				if gitAllowFail(t, dir, "cherry-pick", "side") {
					t.Fatal("expected the cherry-pick to conflict")
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := newRepo(t)
			conflictingBranch(t, dir)
			tc.run(t, dir)

			st, err := Inspect(dir)
			if err != nil {
				t.Fatalf("Inspect: %v", err)
			}
			if st.Op != tc.want {
				t.Fatalf("op = %q, want %q", st.Op, tc.want)
			}
			if st.Head == "" {
				t.Fatal("head is empty")
			}
		})
	}
}

func TestAbortRestoresACleanTree(t *testing.T) {
	tests := []struct {
		name string
		op   Op
		run  func(t *testing.T, dir string)
	}{
		{"merge", OpMerge, func(t *testing.T, dir string) {
			if gitAllowFail(t, dir, "merge", "side") {
				t.Fatal("expected conflict")
			}
		}},
		{"rebase", OpRebase, func(t *testing.T, dir string) {
			if gitAllowFail(t, dir, "rebase", "side") {
				t.Fatal("expected conflict")
			}
		}},
		{"cherry-pick", OpCherryPick, func(t *testing.T, dir string) {
			if gitAllowFail(t, dir, "cherry-pick", "side") {
				t.Fatal("expected conflict")
			}
		}},
		{"revert", OpRevert, func(t *testing.T, dir string) {
			// Revert the base of the conflicting change so the working tree
			// content disagrees with it.
			if gitAllowFail(t, dir, "revert", "--no-commit", "side") {
				t.Fatal("expected conflict")
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := newRepo(t)
			conflictingBranch(t, dir)
			before := git(t, dir, "rev-parse", "HEAD")
			tc.run(t, dir)

			st, _ := Inspect(dir)
			if st.Op != tc.op {
				t.Fatalf("precondition: op = %q, want %q", st.Op, tc.op)
			}
			if err := Abort(dir, st.Op); err != nil {
				t.Fatalf("Abort: %v", err)
			}
			if after := git(t, dir, "rev-parse", "HEAD"); after != before {
				t.Fatalf("HEAD moved: %s -> %s", before, after)
			}
			if status := git(t, dir, "status", "--porcelain"); status != "" {
				t.Fatalf("tree not clean after abort:\n%s", status)
			}
			post, _ := Inspect(dir)
			if post.Op != OpNone {
				t.Fatalf("still mid-%s after abort", post.Op)
			}
		})
	}
}

func TestAbortRejectsUnknownOp(t *testing.T) {
	if err := Abort(t.TempDir(), OpNone); err == nil {
		t.Fatal("expected an error for an empty op")
	}
}

func TestSnapshotWritesArtifacts(t *testing.T) {
	dir := newRepo(t)
	conflictingBranch(t, dir)
	if gitAllowFail(t, dir, "merge", "side") {
		t.Fatal("expected conflict")
	}

	dest := filepath.Join(t.TempDir(), "snap")
	if err := Snapshot(dir, dest); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	for _, name := range []string{"README.txt", "status.txt", "unmerged.txt", "MERGE_HEAD", "MERGE_MSG"} {
		if _, err := os.Stat(filepath.Join(dest, name)); err != nil {
			t.Fatalf("missing snapshot artifact %s: %v", name, err)
		}
	}
	readme, err := os.ReadFile(filepath.Join(dest, "README.txt"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	if !strings.Contains(string(readme), "op: merge") {
		t.Fatalf("README does not name the op:\n%s", readme)
	}
	unmerged, err := os.ReadFile(filepath.Join(dest, "unmerged.txt"))
	if err != nil {
		t.Fatalf("read unmerged: %v", err)
	}
	if !strings.Contains(string(unmerged), "a.txt") {
		t.Fatalf("unmerged.txt does not list the conflicted file:\n%s", unmerged)
	}
}

// TestSnapshotIsNonFatalOnGitFailure: a directory that is not a repo makes
// every git capture fail. The snapshot must still be written, with the
// failures recorded, so a snapshot problem never blocks an abort a human asked
// for.
func TestSnapshotIsNonFatalOnGitFailure(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "snap")
	if err := Snapshot(t.TempDir(), dest); err != nil {
		t.Fatalf("Snapshot should be best-effort, got: %v", err)
	}
	readme, err := os.ReadFile(filepath.Join(dest, "README.txt"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	if !strings.Contains(string(readme), "incomplete artifacts") {
		t.Fatalf("README does not record the failures:\n%s", readme)
	}
}

func TestAgeUnknownForZeroAndFutureSince(t *testing.T) {
	var zero State
	if zero.AgeKnown() || zero.Age() != 0 {
		t.Fatal("zero Since should be age-unknown")
	}
	future := State{Since: time.Now().Add(time.Hour)}
	if future.AgeKnown() || future.Age() != 0 {
		t.Fatal("future Since should be age-unknown")
	}
	if !strings.Contains(State{Path: "/p", Op: OpMerge}.String(), "age=unknown") {
		t.Fatal("String() should say age=unknown")
	}
}
