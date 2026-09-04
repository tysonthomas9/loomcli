package supervisor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// --- helpers ---

// newRepo creates a temp git repo with one commit and returns its path.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	initGitRepo(t, dir)
	return dir
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", full, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

// commitFile writes rel and commits it, so later edits are TRACKED changes.
func commitFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	writeFile(t, dir, rel, content)
	runGit(t, dir, "add", "--", rel)
	runGit(t, dir, "commit", "-q", "-m", "add "+rel)
}

// resolved mirrors diffSourceSet.add's normalization so tests can compare
// paths (t.TempDir() hands back /var/... which resolves to /private/var/...).
func resolved(t *testing.T, p string) string {
	t.Helper()
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatalf("abs %s: %v", p, err)
	}
	if r, err := filepath.EvalSymlinks(filepath.Clean(abs)); err == nil {
		return r
	}
	return filepath.Clean(abs)
}

// stubDiscovery replaces both discovery seams for the duration of the test.
func stubDiscovery(t *testing.T, agentWts, repoWts []cli.WorktreeInfo, err error) {
	t.Helper()
	origAgent, origRepo := discoverAgentWorktreesFn, discoverWorktreesFn
	discoverAgentWorktreesFn = func() ([]cli.WorktreeInfo, error) { return agentWts, err }
	discoverWorktreesFn = func() ([]cli.WorktreeInfo, error) { return repoWts, err }
	t.Cleanup(func() {
		discoverAgentWorktreesFn, discoverWorktreesFn = origAgent, origRepo
	})
}

// --- per-source capture ---

func TestCaptureRepoDiff_TrackedOnly(t *testing.T) {
	repo := newRepo(t)
	commitFile(t, repo, "tracked.go", "package main\n")
	writeFile(t, repo, "tracked.go", "package main\n\nfunc main() {}\n")

	got := captureRepoDiff(repo, config.MaxDiffBytes)
	if !strings.Contains(got, "tracked.go") || !strings.Contains(got, "func main()") {
		t.Fatalf("tracked modification missing from diff:\n%s", got)
	}
}

func TestCaptureRepoDiff_UntrackedFileIncluded(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "newfile.go", "package brandnew\n")

	got := captureRepoDiff(repo, config.MaxDiffBytes)
	if !strings.Contains(got, "newfile.go") {
		t.Fatalf("untracked filename missing:\n%s", got)
	}
	if !strings.Contains(got, "package brandnew") {
		t.Fatalf("untracked file CONTENTS missing:\n%s", got)
	}
}

func TestCaptureRepoDiff_TrackedAndUntracked(t *testing.T) {
	repo := newRepo(t)
	commitFile(t, repo, "tracked.go", "package main\n")
	writeFile(t, repo, "tracked.go", "package main // edited\n")
	writeFile(t, repo, "untracked.go", "package fresh\n")

	got := captureRepoDiff(repo, config.MaxDiffBytes)
	for _, want := range []string{"tracked.go", "edited", "untracked.go", "package fresh"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in combined diff:\n%s", want, got)
		}
	}
}

func TestCaptureRepoDiff_ExcludesLoomRuntimeFiles(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, cli.LockFileName, `{"pid":1}`)
	writeFile(t, repo, cli.LockFileName+".flock", "")
	writeFile(t, repo, cli.LockFileName+".tmp", `{"pid":1}`)
	writeFile(t, repo, config.CheckpointFileName, `{"task_id":"X"}`)
	writeFile(t, repo, config.CheckpointFileName+".tmp", `{"task_id":"X"}`)
	writeFile(t, repo, YieldFileName, `{"reason":"preempt"}`)
	writeFile(t, repo, ".claude/settings.json", "{}")
	writeFile(t, repo, ".loom/state.json", "{}")

	if got := captureRepoDiff(repo, config.MaxDiffBytes); got != "" {
		t.Fatalf("loom runtime files leaked into checkpoint diff:\n%s", got)
	}
}

func TestCaptureRepoDiff_RespectsGitignore(t *testing.T) {
	repo := newRepo(t)
	commitFile(t, repo, ".gitignore", "ignored.txt\n")
	writeFile(t, repo, "ignored.txt", "SHOULD NOT APPEAR")
	writeFile(t, repo, "seen.txt", "visible")

	got := captureRepoDiff(repo, config.MaxDiffBytes)
	if strings.Contains(got, "SHOULD NOT APPEAR") || strings.Contains(got, "ignored.txt") {
		t.Fatalf("gitignored file reported:\n%s", got)
	}
	if !strings.Contains(got, "seen.txt") {
		t.Fatalf("non-ignored file missing:\n%s", got)
	}
}

func TestCaptureRepoDiff_SkipsLargeAndBinary(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "huge.txt", strings.Repeat("x", maxUntrackedFileBytes+1))
	writeFile(t, repo, "blob.bin", "\x00\x01\x02binary\x00payload")

	got := captureRepoDiff(repo, config.MaxDiffBytes)
	if !strings.Contains(got, "# skipped huge.txt") {
		t.Fatalf("oversized file should be a marker, not content:\n%s", got)
	}
	if strings.Contains(got, strings.Repeat("x", 200)) {
		t.Fatalf("oversized file contents leaked:\n%s", got)
	}
	if !strings.Contains(got, "blob.bin") {
		t.Fatalf("binary file not reported at all:\n%s", got)
	}
}

func TestCaptureRepoDiff_UntrackedFilenameWithSpaces(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "my new file.txt", "spaces are legal\n")

	got := captureRepoDiff(repo, config.MaxDiffBytes)
	if !strings.Contains(got, "my new file.txt") || !strings.Contains(got, "spaces are legal") {
		t.Fatalf("NUL-split filename handling broken:\n%s", got)
	}
}

func TestCaptureRepoDiff_NoCommitsFallsBackToUntracked(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@t")
	runGit(t, dir, "config", "user.name", "t")
	writeFile(t, dir, "first.go", "package first\n")

	got := captureRepoDiff(dir, config.MaxDiffBytes)
	if !strings.Contains(got, "first.go") {
		t.Fatalf("repo with no HEAD should still report untracked work:\n%s", got)
	}
}

func TestIsLoomRuntimePath(t *testing.T) {
	runtimePaths := []string{
		cli.LockFileName,
		cli.LockFileName + ".flock",
		cli.LockFileName + ".tmp",
		config.CheckpointFileName,
		config.CheckpointFileName + ".tmp",
		YieldFileName,
		".claude/settings.json",
		".loom/state.json",
	}
	for _, p := range runtimePaths {
		if !isLoomRuntimePath(p) {
			t.Errorf("isLoomRuntimePath(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"main.go", "internal/cli/x.go", ".gitignore", "agent.go", ".claudefile"} {
		if isLoomRuntimePath(p) {
			t.Errorf("isLoomRuntimePath(%q) = true, want false", p)
		}
	}
}

// --- source collection and orchestration ---

func TestCaptureGitDiff_IncludesOwnWorktreeInWorkspaceMode(t *testing.T) {
	own := newRepo(t)
	writeFile(t, own, "wip.go", "package wip\n")
	clean := newRepo(t)

	// The pre-fix code discarded worktreePath entirely and scanned only these.
	stubDiscovery(t, nil, []cli.WorktreeInfo{{Name: "clean-clone", Path: clean}}, nil)

	got, scanned := captureGitDiff(own, "worker", config.MaxDiffBytes)
	if !strings.Contains(got, "wip.go") {
		t.Fatalf("agent's own worktree was not diffed:\n%s", got)
	}
	if len(scanned) == 0 || scanned[0] != resolved(t, own) {
		t.Fatalf("own worktree must be scanned FIRST, got %v", scanned)
	}
}

func TestCaptureGitDiff_IncludesSiblingAgentWorktree(t *testing.T) {
	own := newRepo(t)
	sibling := newRepo(t)
	writeFile(t, sibling, "sibling.go", "package sibling\n")
	otherAgent := newRepo(t)
	writeFile(t, otherAgent, "notmine.go", "package notmine\n")

	stubDiscovery(t, []cli.WorktreeInfo{
		{Name: "worker", Path: sibling},
		{Name: "critic", Path: otherAgent},
	}, nil, nil)

	got, _ := captureGitDiff(own, "worker", config.MaxDiffBytes)
	if !strings.Contains(got, "sibling.go") {
		t.Fatalf("same-named sibling worktree missing:\n%s", got)
	}
	if strings.Contains(got, "notmine.go") {
		t.Fatalf("another agent's worktree leaked into the checkpoint:\n%s", got)
	}
}

func TestCaptureGitDiff_Dedup(t *testing.T) {
	own := newRepo(t)
	writeFile(t, own, "wip.go", "package wip\n")

	stubDiscovery(t,
		[]cli.WorktreeInfo{{Name: "worker", Path: own}},
		[]cli.WorktreeInfo{{Name: "repo", Path: own}}, nil)

	got, scanned := captureGitDiff(own, "worker", config.MaxDiffBytes)
	if len(scanned) != 1 {
		t.Fatalf("same path reached by three sources should be scanned once, got %v", scanned)
	}
	if n := strings.Count(got, "wip.go"); n == 0 {
		t.Fatalf("dedup dropped the only source:\n%s", got)
	}
}

func TestCaptureGitDiff_SkipsNonGitPaths(t *testing.T) {
	own := newRepo(t)
	writeFile(t, own, "wip.go", "package wip\n")
	notARepo := t.TempDir()

	stubDiscovery(t, nil, []cli.WorktreeInfo{
		{Name: "not-a-repo", Path: notARepo},
		{Name: "missing", Path: filepath.Join(notARepo, "nope")},
	}, nil)

	_, scanned := captureGitDiff(own, "worker", config.MaxDiffBytes)
	if len(scanned) != 1 || scanned[0] != resolved(t, own) {
		t.Fatalf("non-git paths must not be listed as scanned, got %v", scanned)
	}
}

func TestCaptureGitDiff_BudgetOrdering(t *testing.T) {
	own := newRepo(t)
	writeFile(t, own, "big.txt", strings.Repeat("own-worktree-content\n", 400))
	clone := newRepo(t)
	writeFile(t, clone, "small.txt", "clone-content\n")

	stubDiscovery(t, nil, []cli.WorktreeInfo{{Name: "clone", Path: clone}}, nil)

	got, _ := captureGitDiff(own, "worker", 2048)
	if !strings.Contains(got, "own-worktree-content") {
		t.Fatalf("own worktree starved by the budget:\n%s", got)
	}
	if !strings.Contains(got, "budget exhausted") && !strings.Contains(got, "truncated") {
		t.Fatalf("omission was silent; expected a marker:\n%s", got)
	}
	if len(got) > 2048 {
		t.Fatalf("diff exceeded budget: %d bytes", len(got))
	}
}

func TestCaptureGitDiff_ScannedPathsListsVisitedTrees(t *testing.T) {
	own := newRepo(t)
	sibling := newRepo(t)
	clone := newRepo(t)

	stubDiscovery(t,
		[]cli.WorktreeInfo{{Name: "worker", Path: sibling}},
		[]cli.WorktreeInfo{{Name: "clone", Path: clone}}, nil)

	_, scanned := captureGitDiff(own, "worker", config.MaxDiffBytes)
	want := []string{resolved(t, own), resolved(t, sibling), resolved(t, clone)}
	if len(scanned) != len(want) {
		t.Fatalf("scanned = %v, want %v", scanned, want)
	}
	for i := range want {
		if scanned[i] != want[i] {
			t.Fatalf("scanned[%d] = %q, want %q (full: %v)", i, scanned[i], want[i], scanned)
		}
	}
}

func TestCaptureGitDiff_NonWorkspaceMode(t *testing.T) {
	own := newRepo(t)
	writeFile(t, own, "wip.go", "package wip\n")

	// DiscoverAgentWorktrees errors by contract outside workspace mode.
	stubDiscovery(t, nil, nil, errors.New("agent worktree discovery requires workspace mode"))

	got, scanned := captureGitDiff(own, "worker", config.MaxDiffBytes)
	if !strings.Contains(got, "wip.go") {
		t.Fatalf("non-workspace mode must degrade to single-repo capture:\n%s", got)
	}
	if len(scanned) != 1 {
		t.Fatalf("scanned = %v, want just the own worktree", scanned)
	}
}

func TestCheckpointDiffSources_ExtraPathsEnv(t *testing.T) {
	own := newRepo(t)
	extra := newRepo(t)
	stubDiscovery(t, nil, nil, errors.New("no workspace"))
	t.Setenv(checkpointExtraPathsEnv, extra)

	sources := checkpointDiffSources(own, "worker")
	if len(sources) != 2 || sources[1].Path != resolved(t, extra) {
		t.Fatalf("LOOM_CHECKPOINT_EXTRA_PATHS not honored: %+v", sources)
	}
}
