package git

// reproducepush_test.go is the Phase 1 repro harness for loomcli-nfuq9.
//
// It drives pushBranchInRepo against a fully sandboxed
// bare-origin + clone + extra-worktree layout and snapshots
// <clone>/.git/config after every git subcommand to identify which
// command writes core.bare=true.
//
// Gated by LOOM_REPRO_CORE_BARE=1 so CI/devs never pay the cost by default.
// Never touches the real repo; never writes to the host's git config.

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

func TestRepro_LoomMergeSetsCoreBare(t *testing.T) {
	if os.Getenv("LOOM_REPRO_CORE_BARE") != "1" {
		t.Skip("set LOOM_REPRO_CORE_BARE=1 to run the core.bare repro harness")
	}
	clitest.ClearGitEnvVars(t)

	tmp := t.TempDir()
	origin := filepath.Join(tmp, "origin")
	repo := filepath.Join(tmp, "repo")
	wtFeature := filepath.Join(tmp, "wt-feature")

	// Some older git versions don't accept -b on `init --bare`; use default
	// branch via config if needed. Modern git (2.28+) supports -b.
	mustGit(t, "", "init", "--bare", "-b", "main", origin)

	// Clone into a working copy (main is checked out here)
	mustGit(t, tmp, "clone", origin, repo)
	writeFile(t, filepath.Join(repo, "README.md"), "hello\n")
	mustGit(t, repo, "add", ".")
	mustGit(t, repo, "commit", "-m", "seed main")
	mustGit(t, repo, "push", "origin", "main")

	// Create a sibling worktree on a feature branch
	mustGit(t, repo, "worktree", "add", "-b", "feature", wtFeature)
	writeFile(t, filepath.Join(wtFeature, "feature.txt"), "feature work\n")
	mustGit(t, wtFeature, "add", ".")
	mustGit(t, wtFeature, "commit", "-m", "feature work")

	// Divergent commit on main so push triggers detached merge path
	writeFile(t, filepath.Join(repo, "main.txt"), "main work\n")
	mustGit(t, repo, "add", ".")
	mustGit(t, repo, "commit", "-m", "divergent main")
	mustGit(t, repo, "push", "origin", "main")

	// Sanity: config must not already have core.bare=true
	configPath := filepath.Join(repo, ".git", "config")
	preHash, preBody, preBare := snapshotGitConfig(t, configPath)
	if preBare {
		t.Fatalf("pre-condition violated: core.bare=true already set before harness ran:\nhash=%s\n%s", preHash, preBody)
	}
	t.Logf("starting config hash=%s (core.bare absent)", preHash)

	// Install a snapshotting GitRunner on a fresh Deps. All production
	// push code paths use deps.Git, so every git invocation is observed.
	deps := cli.DefaultDeps()
	deps.Logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	deps.Agent = &noopAgent{}
	snap := newSnapshottingGitRunner(t, configPath, tmp)
	deps.Git = snap

	// Drive the production flow on the feature worktree twice so we capture
	// both the top-level path (which engages the detached fallback because
	// main is checked out in <repo>) and the detached path directly (which
	// the design fingers as the likely writer of core.bare=true).
	//
	// After the GitExecError fix, RunWithOutput wraps stderr into the
	// returned error, so isWorktreeConflictErr now matches and Phase A
	// exercises the full top-level push → detached fallback chain.
	t.Log("--- phase A: pushBranchInRepo (top-level path; should engage detached fallback) ---")
	startA := time.Now()
	errA := pushBranchInRepo(deps, wtFeature, "feature", "main", "")
	elapsedA := time.Since(startA)
	if errA != nil {
		t.Logf("pushBranchInRepo returned err (non-fatal for harness): %v", errA)
	}
	t.Logf("pushBranchInRepo completed in %s", elapsedA)
	if !sawCheckoutDetach(snap.calls) {
		t.Errorf("phase A: expected `git checkout --detach` to be invoked via the detached fallback, but it was not\nTrace:\n%s", snap.Trace())
	}

	// Reset back to feature so we know the worktree is on a branch, not
	// mid-detached state, before running the direct detached drill.
	mustGit(t, wtFeature, "checkout", "feature")

	t.Log("--- phase B: pushBranchInRepoDetached directly (the actual bug path) ---")
	startB := time.Now()
	errB := pushBranchInRepoDetached(deps, wtFeature, "feature", "main", "")
	elapsedB := time.Since(startB)
	if errB != nil {
		t.Logf("pushBranchInRepoDetached returned err (non-fatal for harness): %v", errB)
	}
	t.Logf("pushBranchInRepoDetached completed in %s", elapsedB)

	// Emit the full command trace.
	t.Logf("\n--- git command trace (%d calls) ---\n%s", len(snap.calls), snap.Trace())

	_, postBody, postBare := snapshotGitConfig(t, configPath)
	if postBare {
		offender := snap.FirstBareWriter()
		t.Fatalf("core.bare=true was written into %s\nFirst offender: %s\nFinal config:\n%s",
			configPath, offender, postBody)
	}
	t.Logf("PASS: core.bare=true was NOT observed in %s after push (host=%s)", configPath, runtime.GOOS)
}

// --- helpers ---

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec,gosec // test harness
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = clitest.GitSafeEnv(
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("setup: git %v (dir=%s) failed: %v\n%s", args, dir, err, out)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// snapshottingGitRunner executes real git commands under gitSafeEnv and
// snapshots <configPath> before and after each call. It implements
// cli.GitRunner so it can be dropped into deps.Git.
type snapshottingGitRunner struct {
	t          *testing.T
	configPath string
	sandboxDir string
	calls      []gitCall
}

type gitCall struct {
	dir           string
	args          []string
	preHash       string
	preBody       string
	postHash      string
	postBody      string
	hasBareBefore bool
	hasBareAfter  bool
	stderrTail    string
	err           error
}

func newSnapshottingGitRunner(t *testing.T, configPath, sandboxDir string) *snapshottingGitRunner {
	return &snapshottingGitRunner{t: t, configPath: configPath, sandboxDir: sandboxDir}
}

func (s *snapshottingGitRunner) Run(dir string, args ...string) cli.CommandResult {
	pre, body, preBare := snapshotGitConfig(s.t, s.configPath)
	cmd := exec.Command("git", args...) //nolint:norawexec,gosec // test harness
	cmd.Dir = dir
	cmd.Env = clitest.GitSafeEnv(
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	post, postBody, postBare := snapshotGitConfig(s.t, s.configPath)
	s.calls = append(s.calls, gitCall{
		dir: dir, args: append([]string(nil), args...),
		preHash: pre, preBody: body, hasBareBefore: preBare,
		postHash: post, postBody: postBody, hasBareAfter: postBare,
		stderrTail: tail(stderr.String(), 240), err: err,
	})
	return cli.CommandResult{Stdout: stdout.String(), Stderr: stderr.String(), Err: err}
}

func (s *snapshottingGitRunner) RunWithOutput(dir string, args ...string) error {
	pre, body, preBare := snapshotGitConfig(s.t, s.configPath)
	cmd := exec.Command("git", args...) //nolint:norawexec,gosec // test harness
	cmd.Dir = dir
	cmd.Env = clitest.GitSafeEnv(
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	// Discard stdout for output-streaming calls; the push code reads status from err only.
	err := cmd.Run()
	post, postBody, postBare := snapshotGitConfig(s.t, s.configPath)
	s.calls = append(s.calls, gitCall{
		dir: dir, args: append([]string(nil), args...),
		preHash: pre, preBody: body, hasBareBefore: preBare,
		postHash: post, postBody: postBody, hasBareAfter: postBare,
		stderrTail: tail(stderr.String(), 240), err: err,
	})
	if err != nil {
		// Mirror production wrapping so substring matchers like
		// isWorktreeConflictErr engage in the harness exactly as they do
		// in production.
		return &cli.GitExecError{
			Args:   append([]string(nil), args...),
			Stderr: strings.TrimRight(stderr.String(), "\n"),
			Err:    err,
		}
	}
	return nil
}

func sawCheckoutDetach(calls []gitCall) bool {
	for _, c := range calls {
		if len(c.args) >= 2 && c.args[0] == "checkout" && c.args[1] == "--detach" {
			return true
		}
	}
	return false
}

func (s *snapshottingGitRunner) Trace() string {
	var b strings.Builder
	for i, c := range s.calls {
		dir := strings.TrimPrefix(c.dir, s.sandboxDir)
		flag := ""
		if c.postHash != c.preHash {
			flag = " [CFG-CHANGED]"
		}
		if !c.hasBareBefore && c.hasBareAfter {
			flag = " <<<CORE.BARE=TRUE WRITTEN HERE>>>"
		}
		fmt.Fprintf(&b, "%3d | git %s (dir=%s) pre=%s post=%s%s", i+1, strings.Join(c.args, " "), dir, c.preHash, c.postHash, flag)
		if c.err != nil {
			fmt.Fprintf(&b, " err=%q", c.err.Error())
		}
		if c.stderrTail != "" {
			fmt.Fprintf(&b, " stderr=%q", c.stderrTail)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func (s *snapshottingGitRunner) FirstBareWriter() string {
	for i, c := range s.calls {
		if !c.hasBareBefore && c.hasBareAfter {
			return fmt.Sprintf("call #%d: git %s (dir=%s)\n--- pre config ---\n%s\n--- post config ---\n%s",
				i+1, strings.Join(c.args, " "), c.dir, c.preBody, c.postBody)
		}
	}
	return "<none observed>"
}

func tail(s string, n int) string {
	s = strings.TrimRight(s, "\n\r ")
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}

// noopAgent is a safe stand-in for cli.AgentInvoker so merge-conflict paths
// don't actually shell out to Claude during the harness.
type noopAgent struct{}

func (noopAgent) InvokeInteractive(workDir, prompt, agentName string) error {
	return fmt.Errorf("repro harness: agent not available (conflict path)")
}

func (noopAgent) InvokeNonInteractive(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error {
	return fmt.Errorf("repro harness: agent not available (conflict path)")
}
