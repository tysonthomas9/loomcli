package gitbranch

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectBranchStates(t *testing.T) {
	repo := initBranchTestRepo(t)
	corruptBranchRef(t, repo, "broken")

	healthy, err := Inspect(repo, "main")
	if err != nil {
		t.Fatalf("Inspect healthy: %v", err)
	}
	if healthy.State != StateHealthy || healthy.BaseSHA == "" {
		t.Fatalf("healthy = %+v, want healthy with sha", healthy)
	}
	missing, err := Inspect(repo, "missing")
	if err != nil {
		t.Fatalf("Inspect missing: %v", err)
	}
	if missing.State != StateMissing {
		t.Fatalf("missing state = %q, want missing", missing.State)
	}
	broken, err := Inspect(repo, "broken")
	if err != nil {
		t.Fatalf("Inspect broken: %v", err)
	}
	if broken.State != StateBroken {
		t.Fatalf("broken state = %q, want broken", broken.State)
	}
}

func TestRecoverBranchPrefersReflogAndClearsCorruptRef(t *testing.T) {
	repo := initBranchTestRepo(t)
	workerSHA := commitBranchChange(t, repo, "worker", "worker.txt", "worker\n")
	git(t, repo, "checkout", "main")
	corruptBranchRef(t, repo, "worker")

	info, err := Inspect(repo, "worker")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	recovery, err := Recover(repo, "worker", "main", info)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if recovery.Base != "reflog" || recovery.BaseSHA != workerSHA {
		t.Fatalf("recovery = %+v, want reflog %s", recovery, workerSHA)
	}
	assertLooseRefAbsent(t, repo, "worker")
}

func TestRecoverBranchFallsBackToDefaultBranch(t *testing.T) {
	repo := initBranchTestRepo(t)
	mainSHA := gitOutput(t, repo, "rev-parse", "main")
	corruptBranchRef(t, repo, "worker")

	info, err := Inspect(repo, "worker")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	recovery, err := Recover(repo, "worker", "main", info)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if recovery.Base != "default branch main" || recovery.BaseSHA != mainSHA {
		t.Fatalf("recovery = %+v, want default main %s", recovery, mainSHA)
	}
	assertLooseRefAbsent(t, repo, "worker")
}

func TestRecoverBranchFallsBackToHEAD(t *testing.T) {
	repo := initBranchTestRepo(t)
	headSHA := gitOutput(t, repo, "rev-parse", "HEAD")
	corruptBranchRef(t, repo, "worker")

	info, err := Inspect(repo, "worker")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	recovery, err := Recover(repo, "worker", "does-not-exist", info)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if recovery.Base != "HEAD" || recovery.BaseSHA != headSHA {
		t.Fatalf("recovery = %+v, want HEAD %s", recovery, headSHA)
	}
	assertLooseRefAbsent(t, repo, "worker")
}

func initBranchTestRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	git(t, repo, "init", "-b", "main")
	git(t, repo, "config", "user.email", "test@example.com")
	git(t, repo, "config", "user.name", "Test User")
	writeBranchTestFile(t, filepath.Join(repo, "README.md"), "base\n")
	git(t, repo, "add", "README.md")
	git(t, repo, "commit", "-m", "base")
	return repo
}

func commitBranchChange(t *testing.T, repo, branch, name, content string) string {
	t.Helper()
	git(t, repo, "checkout", "-b", branch)
	writeBranchTestFile(t, filepath.Join(repo, name), content)
	git(t, repo, "add", name)
	git(t, repo, "commit", "-m", branch+" change")
	return gitOutput(t, repo, "rev-parse", "HEAD")
}

func corruptBranchRef(t *testing.T, repo, branch string) {
	t.Helper()
	refPath := filepath.Join(branchTestCommonDir(t, repo), "refs", "heads", filepath.FromSlash(branch))
	if err := os.MkdirAll(filepath.Dir(refPath), 0o755); err != nil {
		t.Fatalf("mkdir ref parent: %v", err)
	}
	if err := os.WriteFile(refPath, nil, 0o644); err != nil {
		t.Fatalf("write corrupt ref: %v", err)
	}
}

func assertLooseRefAbsent(t *testing.T, repo, branch string) {
	t.Helper()
	refPath := filepath.Join(branchTestCommonDir(t, repo), "refs", "heads", filepath.FromSlash(branch))
	if _, err := os.Lstat(refPath); !os.IsNotExist(err) {
		t.Fatalf("loose ref still exists or stat failed: %v", err)
	}
}

func branchTestCommonDir(t *testing.T, repo string) string {
	t.Helper()
	common, err := CommonDir(repo)
	if err != nil {
		t.Fatalf("CommonDir: %v", err)
	}
	return common
}

func writeBranchTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec,gosec // Test helper creates real git repos.
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec,gosec // Test helper creates real git repos.
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out))
}
