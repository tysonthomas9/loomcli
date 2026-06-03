package backends

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/usage"
)

func TestResolveFlueSandbox(t *testing.T) {
	t.Setenv(envFlueSandbox, "")
	if got := resolveFlueSandbox(); got != "local" {
		t.Errorf("default = %q, want local", got)
	}
	t.Setenv(envFlueSandbox, "Daytona")
	if got := resolveFlueSandbox(); got != "daytona" {
		t.Errorf("got = %q, want daytona", got)
	}
}

func TestRunnerResultHandleLine(t *testing.T) {
	col := usage.NewCollector("flue", "test")
	r := &runnerResult{}
	lines := []string{
		"some unrelated flue log line",
		`LOOMRUNNER {"type":"runner_started","sandbox":"daytona-task"}`,
		`LOOMRUNNER {"type":"sandbox_created","provider":"daytona","sandbox_id":"sb-1","cwd":"/home/daytona/project"}`,
		`LOOMRUNNER {"type":"repo_hydrated","commit":"abc123"}`,
		`LOOMRUNNER {"type":"usage","input_tokens":100,"output_tokens":20}`,
		`LOOMRUNNER {"type":"patch_ready","path":"/tmp/x.patch","files_changed":2}`,
		`LOOMRUNNER not-json`,
		`LOOMRUNNER {"type":"final","status":"completed","sandbox_id":"sb-1"}`,
	}
	for _, l := range lines {
		r.handleLine(l, col)
	}
	if r.sandboxID != "sb-1" || r.remoteCwd != "/home/daytona/project" || r.provider != "daytona" {
		t.Errorf("sandbox fields = %+v", r)
	}
	if r.patchPath != "/tmp/x.patch" {
		t.Errorf("patchPath = %q", r.patchPath)
	}
	if r.status != "completed" {
		t.Errorf("status = %q", r.status)
	}
	if in, out, _, _ := col.Totals(); in != 100 || out != 20 {
		t.Errorf("usage = %d/%d, want 100/20", in, out)
	}
}

func TestRunFlueDaytonaTask_RequiresRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	gitT(t, repo, "init", "-q")
	// No remote configured.
	err := runFlueDaytonaTask(repo, "do work", "agent1", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "git remote") {
		t.Fatalf("expected a 'git remote' error, got %v", err)
	}
}

// TestRunFlueDaytonaTask_SyncsPatchBack is the proposal's fake-runner E2E
// (TestE2E_FlueDaytonaBackend...): with a fake runner, the daytona-task path
// derives the right input, and the runner's patch is synced back into the
// local worktree. No real Daytona/codex needed.
func TestRunFlueDaytonaTask_SyncsPatchBack(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo, bare := newGitRepoWithRemoteT(t)
	baseRef := gitT(t, repo, "rev-parse", "HEAD")
	branch := gitT(t, repo, "rev-parse", "--abbrev-ref", "HEAD")

	// A real patch that adds created.txt (worktree reset back to base afterward).
	patch := makeAddFilePatchT(t, repo, "created.txt", "daytona task ok\n")

	// Swap the runner exec with a fake that writes the patch and reports success.
	var gotInput runnerInput
	orig := flueRunnerExec
	t.Cleanup(func() { flueRunnerExec = orig })
	t.Cleanup(ClearLastRuntimeMetadata)
	flueRunnerExec = func(_ context.Context, _, _ string, in runnerInput, _ <-chan struct{}, _ *usage.Collector) (*runnerResult, error) {
		gotInput = in
		if err := os.WriteFile(in.PatchOut, patch, 0o600); err != nil {
			return nil, err
		}
		return &runnerResult{status: "completed", sandboxID: "sb-test", remoteCwd: "/home/daytona/project", patchPath: in.PatchOut, cleanup: "deleted"}, nil
	}

	if err := runFlueDaytonaTask(repo, "make a change", "nova", nil, nil); err != nil {
		t.Fatalf("runFlueDaytonaTask: %v", err)
	}

	// Sandbox runtime metadata was captured for the session finalizer
	// (proposal: session metadata records provider daytona, sandbox ID, base ref,
	// and cleanup outcome).
	rt := GetLastRuntimeMetadata()
	if rt == nil {
		t.Fatal("runtime metadata was not captured")
	}
	if rt.Provider != "daytona" || rt.SandboxID != "sb-test" || rt.RemoteCwd != "/home/daytona/project" {
		t.Errorf("runtime metadata = %+v", rt)
	}
	if rt.BaseRef != baseRef {
		t.Errorf("runtime base_ref = %q, want %q", rt.BaseRef, baseRef)
	}
	if rt.SyncStrategy != "patch-back" {
		t.Errorf("runtime sync_strategy = %q, want patch-back", rt.SyncStrategy)
	}
	if rt.Cleanup != "deleted" {
		t.Errorf("runtime cleanup = %q, want deleted", rt.Cleanup)
	}
	if gotInput.SyncStrategy != "patch-back" {
		t.Errorf("runner input sync_strategy = %q, want patch-back", gotInput.SyncStrategy)
	}

	// Patch synced back into the local worktree.
	data, err := os.ReadFile(filepath.Join(repo, "created.txt"))
	if err != nil {
		t.Fatalf("patch was not synced back: %v", err)
	}
	if !strings.Contains(string(data), "daytona task ok") {
		t.Fatalf("created.txt = %q", data)
	}

	// "Back to loom": the work was committed locally (HEAD advanced past base)...
	newHead := gitT(t, repo, "rev-parse", "HEAD")
	if newHead == baseRef {
		t.Error("daytona work was not committed (HEAD did not advance)")
	}
	if tracked := gitT(t, repo, "ls-files", "created.txt"); tracked == "" {
		t.Error("created.txt is not tracked after commit")
	}
	// ...and pushed to the origin remote.
	if pushed := gitT(t, bare, "rev-parse", branch); pushed != newHead {
		t.Errorf("push did not land in origin: bare %s != local %s", pushed, newHead)
	}

	// Runner received correctly-derived input.
	if gotInput.Sandbox != "daytona-task" {
		t.Errorf("sandbox = %q", gotInput.Sandbox)
	}
	if gotInput.RepoRemoteURL != bare {
		t.Errorf("remote = %q, want %q", gotInput.RepoRemoteURL, bare)
	}
	if gotInput.BaseRef != baseRef {
		t.Errorf("base_ref = %q, want %q", gotInput.BaseRef, baseRef)
	}
	if gotInput.RepoBranch != branch {
		t.Errorf("branch = %q, want %q", gotInput.RepoBranch, branch)
	}
	if gotInput.PatchOut == "" {
		t.Error("patch_out was empty")
	}
}

// TestPushWorktreeBack covers the loom-side git proxy: it commits the synced
// work, tolerates a failed push (best-effort), and is a no-op when clean.
func TestPushWorktreeBack(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	gitT(t, repo, "init", "-q")
	gitT(t, repo, "config", "user.email", "t@example.com")
	gitT(t, repo, "config", "user.name", "Test")
	writeFileT(t, filepath.Join(repo, "base.txt"), "base\n")
	gitT(t, repo, "add", ".")
	gitT(t, repo, "commit", "-qm", "base")
	// Origin points at a path with no repo, so push fails fast offline.
	gitT(t, repo, "remote", "add", "origin", filepath.Join(t.TempDir(), "missing-bare"))
	baseHead := gitT(t, repo, "rev-parse", "HEAD")

	// A pending change in the worktree (as if a patch were just applied).
	writeFileT(t, filepath.Join(repo, "x.txt"), "hi\n")

	// Push fails (bad remote) but the commit must still succeed → returns nil.
	if err := pushWorktreeBack(repo, "nova", "sb-1"); err != nil {
		t.Fatalf("pushWorktreeBack should be best-effort on push failure, got: %v", err)
	}
	committed := gitT(t, repo, "rev-parse", "HEAD")
	if committed == baseHead {
		t.Fatal("work was not committed despite push failure")
	}

	// Second call with a clean tree is a no-op (no empty commit).
	if err := pushWorktreeBack(repo, "nova", "sb-1"); err != nil {
		t.Fatalf("pushWorktreeBack (clean): %v", err)
	}
	if h := gitT(t, repo, "rev-parse", "HEAD"); h != committed {
		t.Error("pushWorktreeBack created an empty commit on a clean tree")
	}
}

// TestFlueNonInteractive_DispatchesToDaytona confirms LOOM_FLUE_SANDBOX=daytona
// routes the non-interactive path to the daytona runner.
func TestFlueNonInteractive_DispatchesToDaytona(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	gitT(t, repo, "init", "-q")
	gitT(t, repo, "config", "user.email", "t@example.com")
	gitT(t, repo, "config", "user.name", "Test")
	writeFileT(t, filepath.Join(repo, "base.txt"), "base\n")
	gitT(t, repo, "add", ".")
	gitT(t, repo, "commit", "-qm", "base")
	gitT(t, repo, "remote", "add", "origin", "https://example.com/repo.git")

	t.Setenv(envFlueSandbox, "daytona")
	orig := flueRunnerExec
	t.Cleanup(func() { flueRunnerExec = orig })
	t.Cleanup(ClearLastRuntimeMetadata)
	called := false
	flueRunnerExec = func(_ context.Context, _, _ string, in runnerInput, _ <-chan struct{}, _ *usage.Collector) (*runnerResult, error) {
		called = true
		return &runnerResult{status: "completed", sandboxID: "sb"}, nil
	}

	if err := defaultFlueNonInteractiveInvoker(repo, "p", "ag", nil, nil); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !called {
		t.Fatal("LOOM_FLUE_SANDBOX=daytona did not route to the daytona runner")
	}
}

// TestRunFlueDaytonaTask_ConcurrentTasksAreIsolated is the "at scale" check:
// N tasks run concurrently (as the daemon would fan them out), each against its
// own repo + its own sandbox, and each runner's patch must land only in that
// task's worktree — no cross-talk. This exercises the per-task isolation of the
// loom-owned Go path (Daytona-per-task; proposal Phase 4 fan-out).
func TestRunFlueDaytonaTask_ConcurrentTasksAreIsolated(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	const n = 6

	type task struct {
		repo  string
		name  string
		patch []byte
	}
	tasks := make([]task, n)
	patches := make(map[string][]byte, n)
	for i := range tasks {
		name := fmt.Sprintf("nova%d", i)
		repo := newGitRepoT(t)
		tasks[i] = task{repo: repo, name: name, patch: makeAddFilePatchT(t, repo, name+".txt", name+" was here\n")}
		patches[name] = tasks[i].patch
	}

	// Fake runner: each concurrent call writes the patch for its own task only.
	// patches is read-only after setup, so concurrent reads are race-free.
	orig := flueRunnerExec
	t.Cleanup(func() { flueRunnerExec = orig })
	t.Cleanup(ClearLastRuntimeMetadata)
	flueRunnerExec = func(_ context.Context, _, agentName string, in runnerInput, _ <-chan struct{}, _ *usage.Collector) (*runnerResult, error) {
		if err := os.WriteFile(in.PatchOut, patches[agentName], 0o600); err != nil {
			return nil, err
		}
		return &runnerResult{status: "completed", sandboxID: "sb-" + agentName, patchPath: in.PatchOut}, nil
	}

	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range tasks {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = runFlueDaytonaTask(tasks[i].repo, "make a change", tasks[i].name, nil, nil)
		}(i)
	}
	wg.Wait()

	for i, tk := range tasks {
		if errs[i] != nil {
			t.Errorf("task %d (%s): %v", i, tk.name, errs[i])
			continue
		}
		// Its own file landed.
		if !fileExistsT(filepath.Join(tk.repo, tk.name+".txt")) {
			t.Errorf("task %d (%s): own patch not applied", i, tk.name)
		}
		// No other task's file leaked into this repo.
		for j, other := range tasks {
			if j == i {
				continue
			}
			if fileExistsT(filepath.Join(tk.repo, other.name+".txt")) {
				t.Errorf("cross-talk: repo %s contains %s's file", tk.name, other.name)
			}
		}
	}
}

// ── test git helpers (real git is required to build a repo + a valid patch) ──

// newGitRepoT initializes a committed git repo with a local (bare) origin so
// the commit+push-back path works hermetically (no network).
func newGitRepoT(t *testing.T) string {
	t.Helper()
	repo, _ := newGitRepoWithRemoteT(t)
	return repo
}

// newGitRepoWithRemoteT returns a committed git repo plus the path to its bare
// origin remote, so tests can assert the push landed.
func newGitRepoWithRemoteT(t *testing.T) (repo, bare string) {
	t.Helper()
	repo = t.TempDir()
	gitT(t, repo, "init", "-q")
	gitT(t, repo, "config", "user.email", "t@example.com")
	gitT(t, repo, "config", "user.name", "Test")
	writeFileT(t, filepath.Join(repo, "base.txt"), "base\n")
	gitT(t, repo, "add", ".")
	gitT(t, repo, "commit", "-qm", "base")
	bare = t.TempDir()
	gitT(t, bare, "init", "--bare", "-q")
	gitT(t, repo, "remote", "add", "origin", bare)
	return repo, bare
}

// makeAddFilePatchT returns a git-generated patch that adds one new file, then
// restores the worktree to its committed base so the file isn't already present.
func makeAddFilePatchT(t *testing.T, repo, relPath, content string) []byte {
	t.Helper()
	writeFileT(t, filepath.Join(repo, relPath), content)
	gitT(t, repo, "add", "-N", relPath)
	patch := gitRawT(t, repo, "diff", "--binary", "--full-index")
	gitT(t, repo, "reset", "-q")
	_ = os.Remove(filepath.Join(repo, relPath))
	return patch
}

func fileExistsT(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput() //nolint:norawexec // test needs real git
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func gitRawT(t *testing.T, dir string, args ...string) []byte {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output() //nolint:norawexec // test needs real git
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

func writeFileT(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
