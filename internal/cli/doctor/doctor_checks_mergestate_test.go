package doctor

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/gitstate"
	"github.com/tysonthomas9/loomcli/internal/cli/integration"
)

// stubMergeSources replaces the check's four seams for one test and restores
// them afterwards, the way doctor_checks_transcripts_test.go stubs getSignalDir.
func stubMergeSources(t *testing.T, shared []integration.SharedWorktree, local []candidate, states map[string]gitstate.State) {
	t.Helper()
	origShared, origLocal, origInspect, origRoot := sharedWorktreeSource, localWorktreeSource, inspectWorktree, snapshotRoot
	t.Cleanup(func() {
		sharedWorktreeSource, localWorktreeSource, inspectWorktree, snapshotRoot = origShared, origLocal, origInspect, origRoot
	})
	sharedWorktreeSource = func() ([]integration.SharedWorktree, error) { return shared, nil }
	localWorktreeSource = func() []candidate { return local }
	inspectWorktree = func(path string) (gitstate.State, error) {
		if st, ok := states[path]; ok {
			return st, nil
		}
		return gitstate.State{Path: path}, nil
	}
	snapshotRoot = func() string { return filepath.Join(t.TempDir(), "rescue") }
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
		Since: time.Now().Add(-age), Branch: "local/union",
	}
}

func TestMergeInProgressNoCandidatesIsSkipped(t *testing.T) {
	stubMergeSources(t, nil, nil, nil)
	if got := checkMergeInProgress(); got.Name != "" {
		t.Fatalf("expected a skipped result, got %+v", got)
	}
}

func TestMergeInProgressAllCleanPasses(t *testing.T) {
	stubMergeSources(t,
		[]integration.SharedWorktree{{Repo: "loomcli", Path: "/union/loomcli", Branch: "local/union"}},
		[]candidate{{label: "worker", path: "/wt/worker"}},
		nil)
	got := checkMergeInProgress()
	if got.Status != StatusPass {
		t.Fatalf("status = %v, want pass (%+v)", got.Status, got)
	}
	if !strings.Contains(got.Summary, "2 worktree(s) checked") {
		t.Fatalf("summary = %q", got.Summary)
	}
}

// The incident: a shared union worktree stuck mid-merge for hours. It must fail,
// not warn — it blocks every later union merge fleet-wide.
func TestMergeInProgressStalledSharedWorktreeFails(t *testing.T) {
	stubMergeSources(t,
		[]integration.SharedWorktree{{Repo: "loomcli", Path: "/union/loomcli", Branch: "local/union"}},
		nil,
		map[string]gitstate.State{"/union/loomcli": stalledState("/union/loomcli", gitstate.OpMerge, 4*time.Hour)})

	got := checkMergeInProgress()
	if got.Status != StatusFail {
		t.Fatalf("status = %v, want fail", got.Status)
	}
	for _, want := range []string{"[shared]", "loomcli (local/union)", "/union/loomcli", "merge", "abc1234", "unmerged=38"} {
		if !strings.Contains(got.Detail, want) {
			t.Fatalf("detail missing %q:\n%s", want, got.Detail)
		}
	}
}

// A live integrator mid-merge is normal. Reporting it would make the check
// noise, and a noisy check gets ignored.
func TestMergeInProgressYoungMergeIsSkipped(t *testing.T) {
	stubMergeSources(t,
		[]integration.SharedWorktree{{Repo: "loomcli", Path: "/union/loomcli"}},
		nil,
		map[string]gitstate.State{"/union/loomcli": stalledState("/union/loomcli", gitstate.OpMerge, 30*time.Second)})

	if got := checkMergeInProgress(); got.Status != StatusPass {
		t.Fatalf("status = %v, want pass (%+v)", got.Status, got)
	}
}

func TestMergeInProgressThresholdEnvOverride(t *testing.T) {
	stubMergeSources(t,
		[]integration.SharedWorktree{{Repo: "loomcli", Path: "/union/loomcli"}},
		nil,
		map[string]gitstate.State{"/union/loomcli": stalledState("/union/loomcli", gitstate.OpMerge, 30*time.Second)})

	t.Setenv("LOOM_DOCTOR_MERGE_STALE", "1s")
	if got := checkMergeInProgress(); got.Status != StatusFail {
		t.Fatalf("with a 1s threshold, status = %v, want fail", got.Status)
	}
	// An unparseable value must fall back to the default, not fail the check.
	t.Setenv("LOOM_DOCTOR_MERGE_STALE", "not-a-duration")
	if got := mergeStaleThreshold(); got != defaultMergeStaleThreshold {
		t.Fatalf("threshold = %v, want the default", got)
	}
}

// An age that cannot be determined is unknown, not young: report it.
func TestMergeInProgressUnknownAgeIsReported(t *testing.T) {
	st := gitstate.State{Path: "/union/loomcli", Op: gitstate.OpMerge, Head: "abc1234"}
	stubMergeSources(t,
		[]integration.SharedWorktree{{Repo: "loomcli", Path: "/union/loomcli"}},
		nil,
		map[string]gitstate.State{"/union/loomcli": st})

	got := checkMergeInProgress()
	if got.Status != StatusFail {
		t.Fatalf("status = %v, want fail", got.Status)
	}
	if !strings.Contains(got.Detail, "age=unknown") {
		t.Fatalf("detail = %q", got.Detail)
	}
}

// One agent worktree mid-merge costs one agent a cycle; it is not a fleet-wide
// blocker, so it warns.
func TestMergeInProgressAgentWorktreeOnlyWarns(t *testing.T) {
	stubMergeSources(t, nil,
		[]candidate{{label: "worker", path: "/wt/worker"}},
		map[string]gitstate.State{"/wt/worker": stalledState("/wt/worker", gitstate.OpRebase, time.Hour)})

	got := checkMergeInProgress()
	if got.Status != StatusWarn {
		t.Fatalf("status = %v, want warn", got.Status)
	}
	if !strings.Contains(got.Detail, "[local]") {
		t.Fatalf("detail = %q", got.Detail)
	}
}

// Shared entries win over local ones for the same path: they carry the higher
// severity, and reporting the same tree twice at two severities is worse than
// either.
func TestMergeInProgressDedupesSharedAndLocalPaths(t *testing.T) {
	stubMergeSources(t,
		[]integration.SharedWorktree{{Repo: "loomcli", Path: "/union/loomcli"}},
		[]candidate{{label: "union-clone", path: "/union/loomcli"}},
		map[string]gitstate.State{"/union/loomcli": stalledState("/union/loomcli", gitstate.OpMerge, time.Hour)})

	got := checkMergeInProgress()
	if !strings.Contains(got.Summary, "1 worktree(s) stuck") {
		t.Fatalf("summary = %q", got.Summary)
	}
	if strings.Contains(got.Detail, "[local]") {
		t.Fatalf("the local duplicate should have been dropped:\n%s", got.Detail)
	}
}

// Shared offenders sort ahead of local ones, so the fleet-wide blocker is the
// first line an operator reads.
func TestMergeInProgressSharedSortsFirst(t *testing.T) {
	stubMergeSources(t,
		[]integration.SharedWorktree{{Repo: "zzz-repo", Path: "/union/zzz"}},
		[]candidate{{label: "aaa-agent", path: "/wt/aaa"}},
		map[string]gitstate.State{
			"/union/zzz": stalledState("/union/zzz", gitstate.OpMerge, time.Hour),
			"/wt/aaa":    stalledState("/wt/aaa", gitstate.OpMerge, time.Hour),
		})

	got := checkMergeInProgress()
	if !strings.HasPrefix(got.Detail, "[shared]") {
		t.Fatalf("shared should come first:\n%s", got.Detail)
	}
}

func TestMergeInProgressContractErrorWarns(t *testing.T) {
	stubMergeSources(t, nil, nil, nil)
	sharedWorktreeSource = func() ([]integration.SharedWorktree, error) {
		return nil, errors.New("boom")
	}
	got := checkMergeInProgress()
	if got.Status != StatusWarn || !strings.Contains(got.Detail, "boom") {
		t.Fatalf("unexpected result: %+v", got)
	}
}

// --- fix path (real repos: the abort and the snapshot must actually happen) ---

func TestMergeInProgressFixAbortsSharedWorktree(t *testing.T) {
	dir := newConflictedRepo(t)
	rescue := filepath.Join(t.TempDir(), "rescue")

	stubMergeSources(t,
		[]integration.SharedWorktree{{Repo: "loomcli", Path: dir, Branch: "local/union"}},
		nil, nil)
	inspectWorktree = gitstate.Inspect
	snapshotRoot = func() string { return rescue }
	setDoctorFix(t, true)
	t.Setenv("LOOM_DOCTOR_MERGE_STALE", "0s")

	got := checkMergeInProgress()
	if got.Status != StatusWarn {
		t.Fatalf("status = %v, want warn after a successful abort (%+v)", got.Status, got)
	}
	if !strings.Contains(got.Summary, "1 aborted") {
		t.Fatalf("summary = %q", got.Summary)
	}
	if st, _ := gitstate.Inspect(dir); st.Op != gitstate.OpNone {
		t.Fatalf("worktree still mid-%s", st.Op)
	}
	entries, err := os.ReadDir(rescue)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one snapshot dir in %s: %v %v", rescue, entries, err)
	}
	snap := filepath.Join(rescue, entries[0].Name())
	for _, name := range []string{"README.txt", "unmerged.txt", "worktree.diff"} {
		if _, statErr := os.Stat(filepath.Join(snap, name)); statErr != nil {
			t.Fatalf("snapshot missing %s: %v", name, statErr)
		}
	}
}

// --fix must never touch an agent worktree: a live agent may be mid-run in it
// and no lock covers that decision.
func TestMergeInProgressFixLeavesAgentWorktreeAlone(t *testing.T) {
	dir := newConflictedRepo(t)
	stubMergeSources(t, nil, []candidate{{label: "worker", path: dir}}, nil)
	inspectWorktree = gitstate.Inspect
	setDoctorFix(t, true)
	t.Setenv("LOOM_DOCTOR_MERGE_STALE", "0s")

	got := checkMergeInProgress()
	if got.Status != StatusWarn {
		t.Fatalf("status = %v, want warn", got.Status)
	}
	if !strings.Contains(got.Detail, "not fixed: agent/repo worktree") {
		t.Fatalf("detail does not explain the refusal:\n%s", got.Detail)
	}
	if st, _ := gitstate.Inspect(dir); st.Op != gitstate.OpMerge {
		t.Fatalf("the agent worktree was aborted; op = %q", st.Op)
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

func TestKindLabel(t *testing.T) {
	if kindLabel(kindShared) != "[shared]" || kindLabel(kindLocal) != "[local]" {
		t.Fatal("unexpected kind labels")
	}
}

func TestSamePathFoldsCase(t *testing.T) {
	// EvalSymlinks does not fold case on macOS: /...&/PUPPET and /...&/puppet
	// resolve to two different strings for the same directory.
	if !samePath("/Users/x/workspaces/PUPPET", "/Users/x/workspaces/puppet") {
		t.Fatal("samePath should fold case")
	}
	if samePath("/a/one", "/a/two") {
		t.Fatal("distinct paths compared equal")
	}
}

// runGitForTest shells out to real git. The fix path aborts a real merge and
// snapshots a real index, so a mocked runner would leave the destructive half
// of this check untested.
func runGitForTest(dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	out, err := exec.Command("git", full...).CombinedOutput() //nolint:norawexec
	return strings.TrimSpace(string(out)), err
}
