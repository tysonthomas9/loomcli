package doctor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/gitstate"
)

// stubMergeSources replaces the check's seams for one test and restores them
// afterwards, the way doctor_checks_transcripts_test.go stubs getSignalDir.
func stubMergeSources(t *testing.T, local []candidate, states map[string]gitstate.State) {
	t.Helper()
	origLocal, origInspect := localWorktreeSource, inspectWorktree
	t.Cleanup(func() {
		localWorktreeSource, inspectWorktree = origLocal, origInspect
	})
	localWorktreeSource = func() []candidate { return local }
	inspectWorktree = func(path string) (gitstate.State, error) {
		if st, ok := states[path]; ok {
			return st, nil
		}
		return gitstate.State{Path: path}, nil
	}
}

func setDoctorFix(t *testing.T, v bool) {
	t.Helper()
	orig := doctorFix
	t.Cleanup(func() { doctorFix = orig })
	doctorFix = v
}

func stalledState(path string, op gitstate.Op, age time.Duration) gitstate.State {
	return gitstate.State{
		Path: path, Op: op, Head: "abc1234", Unmerged: 38,
		Since: time.Now().Add(-age), Branch: "feature",
	}
}

func TestMergeInProgressNoCandidatesIsSkipped(t *testing.T) {
	stubMergeSources(t, nil, nil)
	if got := checkMergeInProgress(); got.Name != "" {
		t.Fatalf("expected a skipped result, got %+v", got)
	}
}

func TestMergeInProgressAllCleanPasses(t *testing.T) {
	stubMergeSources(t,
		[]candidate{{label: "repo", path: "/ws/repo"}, {label: "worker", path: "/wt/worker"}},
		nil)
	got := checkMergeInProgress()
	if got.Status != StatusPass {
		t.Fatalf("status = %v, want pass (%+v)", got.Status, got)
	}
	if !strings.Contains(got.Summary, "2 worktree(s) checked") {
		t.Fatalf("summary = %q", got.Summary)
	}
}

// A worktree stuck mid-operation costs whoever works in it next a cycle. It
// warns and names the worktree, the operation and how far it got.
func TestMergeInProgressStalledWorktreeWarns(t *testing.T) {
	stubMergeSources(t,
		[]candidate{{label: "worker", path: "/wt/worker"}},
		map[string]gitstate.State{"/wt/worker": stalledState("/wt/worker", gitstate.OpRebase, time.Hour)})

	got := checkMergeInProgress()
	if got.Status != StatusWarn {
		t.Fatalf("status = %v, want warn", got.Status)
	}
	for _, want := range []string{"worker", "/wt/worker", "rebase", "abc1234", "unmerged=38"} {
		if !strings.Contains(got.Detail, want) {
			t.Fatalf("detail missing %q:\n%s", want, got.Detail)
		}
	}
}

// Someone resolving a merge right now is normal. Reporting it would make the
// check noise, and a noisy check gets ignored.
func TestMergeInProgressYoungMergeIsSkipped(t *testing.T) {
	stubMergeSources(t,
		[]candidate{{label: "repo", path: "/ws/repo"}},
		map[string]gitstate.State{"/ws/repo": stalledState("/ws/repo", gitstate.OpMerge, 30*time.Second)})

	if got := checkMergeInProgress(); got.Status != StatusPass {
		t.Fatalf("status = %v, want pass (%+v)", got.Status, got)
	}
}

func TestMergeInProgressThresholdEnvOverride(t *testing.T) {
	stubMergeSources(t,
		[]candidate{{label: "repo", path: "/ws/repo"}},
		map[string]gitstate.State{"/ws/repo": stalledState("/ws/repo", gitstate.OpMerge, 30*time.Second)})

	t.Setenv("LOOM_DOCTOR_MERGE_STALE", "1s")
	if got := checkMergeInProgress(); got.Status != StatusWarn {
		t.Fatalf("with a 1s threshold, status = %v, want warn", got.Status)
	}
	// An unparseable value must fall back to the default, not fail the check.
	t.Setenv("LOOM_DOCTOR_MERGE_STALE", "not-a-duration")
	if got := mergeStaleThreshold(); got != defaultMergeStaleThreshold {
		t.Fatalf("threshold = %v, want the default", got)
	}
}

// An age that cannot be determined is unknown, not young: report it.
func TestMergeInProgressUnknownAgeIsReported(t *testing.T) {
	st := gitstate.State{Path: "/ws/repo", Op: gitstate.OpMerge, Head: "abc1234"}
	stubMergeSources(t,
		[]candidate{{label: "repo", path: "/ws/repo"}},
		map[string]gitstate.State{"/ws/repo": st})

	got := checkMergeInProgress()
	if got.Status != StatusWarn {
		t.Fatalf("status = %v, want warn", got.Status)
	}
	if !strings.Contains(got.Detail, "age=unknown") {
		t.Fatalf("detail = %q", got.Detail)
	}
}

func TestMergeInProgressSortsByLabel(t *testing.T) {
	stubMergeSources(t,
		[]candidate{{label: "zzz-agent", path: "/wt/zzz"}, {label: "aaa-agent", path: "/wt/aaa"}},
		map[string]gitstate.State{
			"/wt/zzz": stalledState("/wt/zzz", gitstate.OpMerge, time.Hour),
			"/wt/aaa": stalledState("/wt/aaa", gitstate.OpMerge, time.Hour),
		})

	got := checkMergeInProgress()
	if !strings.HasPrefix(got.Detail, "aaa-agent") {
		t.Fatalf("offenders not sorted by label:\n%s", got.Detail)
	}
}

// The check never repairs, `--fix` included: a live agent may be mid-run in
// the worktree and no lock covers that decision. Real git, so the assertion is
// about the repo, not a stub.
func TestMergeInProgressNeverAborts(t *testing.T) {
	dir := newConflictedRepo(t)
	stubMergeSources(t, []candidate{{label: "worker", path: dir}}, nil)
	inspectWorktree = gitstate.Inspect
	setDoctorFix(t, true)
	t.Setenv("LOOM_DOCTOR_MERGE_STALE", "0s")

	got := checkMergeInProgress()
	if got.Status != StatusWarn {
		t.Fatalf("status = %v, want warn", got.Status)
	}
	if st, _ := gitstate.Inspect(dir); st.Op != gitstate.OpMerge {
		t.Fatalf("the worktree was aborted; op = %q", st.Op)
	}
}

// newConflictedRepo builds a real repo sitting in a conflicted merge.
func newConflictedRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		out, err := runGitForTest(dir, args...)
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	writeFile(t, dir, "a.txt", "base\n")
	run("add", "a.txt")
	run("commit", "-q", "-m", "base")
	run("checkout", "-q", "-b", "side")
	writeFile(t, dir, "a.txt", "side\n")
	run("commit", "-q", "-am", "side")
	run("checkout", "-q", "main")
	writeFile(t, dir, "a.txt", "main\n")
	run("commit", "-q", "-am", "main")
	if _, err := runGitForTest(dir, "merge", "side"); err == nil {
		t.Fatal("expected the merge to conflict")
	}
	return dir
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// runGitForTest shells out to real git, so the never-aborts assertion is made
// against a real index rather than a mocked runner.
func runGitForTest(dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	out, err := exec.Command("git", full...).CombinedOutput() //nolint:norawexec
	return strings.TrimSpace(string(out)), err
}
