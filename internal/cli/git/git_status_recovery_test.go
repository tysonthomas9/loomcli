package git

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitStatusSummaryRejectsEachFailedRead(t *testing.T) {
	for _, failed := range []string{"branch --show-current", "status --porcelain", "diff --name-only --diff-filter=U", "rev-list --left-right --count HEAD...refs/remotes/origin/main", "stash list"} {
		t.Run(failed, func(t *testing.T) {
			sentinel := errors.New("git read failed")
			runner := func(_ string, args ...string) (string, error) {
				command := strings.Join(args, " ")
				if command == failed {
					return "", sentinel
				}
				if args[0] == "branch" {
					return "feature\n", nil
				}
				if args[0] == "rev-list" {
					return "0\t0\n", nil
				}
				return "", nil
			}
			result, err := readGitStatusSummary("repo", "main", runner)
			if result != nil || !errors.Is(err, sentinel) {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestGitStatusSummaryRejectsMalformedComparison(t *testing.T) {
	for _, counts := range []string{"", "0", "0 0 0", "bad 0", "0 bad", "-1 0", "0 -1", "999999999999999999999999999999 0"} {
		t.Run(counts, func(t *testing.T) {
			runner := func(_ string, args ...string) (string, error) {
				if args[0] == "branch" {
					return "main", nil
				}
				if args[0] == "rev-list" {
					return counts, nil
				}
				return "", nil
			}
			if result, err := readGitStatusSummary("repo", "main", runner); err == nil || result != nil {
				t.Fatalf("malformed comparison acknowledged: %+v", result)
			}
		})
	}
}

func TestGitStatusSummaryRealCleanDetachedAndMissingTarget(t *testing.T) {
	dir := t.TempDir()
	gitRun := func(dir string, args ...string) (string, error) {
		command := exec.Command("git", args...) //nolint:norawexec // Fixed Git fixture commands run only in the test temporary repository.
		command.Dir = dir
		output, err := command.CombinedOutput()
		return string(output), err
	}
	setup := func(args ...string) {
		t.Helper()
		if output, err := gitRun(dir, args...); err != nil {
			t.Fatalf("git %v: %v %s", args, err, output)
		}
	}
	setup("init", "-b", "main")
	setup("-c", "user.name=Recovery Proof", "-c", "user.email=proof@example.invalid", "commit", "--allow-empty", "-m", "initial")
	setup("update-ref", "refs/remotes/origin/main", "HEAD")
	for _, detached := range []bool{false, true} {
		if detached {
			setup("checkout", "--detach", "HEAD")
		}
		result, err := readGitStatusSummary(dir, "main", gitRun)
		if err != nil {
			t.Fatal(err)
		}
		wantBranch := "main"
		if detached {
			wantBranch = "(detached)"
		}
		if result.Branch != wantBranch || !result.IsClean || result.Ahead != 0 || result.Behind != 0 || result.StashCount != 0 || result.ChangedFiles == nil || result.ConflictedFiles == nil {
			t.Fatalf("unexpected clean result %+v", result)
		}
	}
	// Git permits Unicode and @ in ref names. The fixed revision prefix keeps
	// this one argument from becoming an option without imposing a new grammar.
	for _, target := range []string{"feature/日本語", "feature/review@home"} {
		setup("update-ref", "refs/remotes/origin/"+target, "HEAD")
		result, err := readGitStatusSummary(dir, target, gitRun)
		if err != nil || result == nil || result.Ahead != 0 || result.Behind != 0 {
			t.Fatalf("valid Git target %q rejected: result=%+v err=%v", target, result, err)
		}
	}
	if result, err := readGitStatusSummary(dir, "missing", gitRun); err == nil || result != nil {
		t.Fatalf("missing upstream acknowledged %+v", result)
	}
	if result, err := readGitStatusSummary(filepath.Join(dir, "missing"), "main", gitRun); err == nil || result != nil {
		t.Fatalf("missing repository acknowledged %+v", result)
	}
}
