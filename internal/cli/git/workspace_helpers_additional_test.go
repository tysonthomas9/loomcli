package git

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

func TestPullAndPRWorkspaceWorktreesAdditionalBranches(t *testing.T) {
	deps, _, _ := installWrapperCoverageDeps(t)
	worktrees := []cli.WorktreeInfo{
		{Name: "skip", Path: "/skip", Branch: "skip-branch"},
		{Name: "api", Path: "/api", Branch: "feature/api", Repo: &RepoConfig{Name: "api", Remote: "origin"}},
		{Name: "ui", Path: "/ui", Branch: "feature/ui", Repo: &RepoConfig{Name: "ui", Remote: "upstream", DefaultBranch: "trunk"}},
	}

	oldPull := pullRepoWorktreeFn
	oldCreatePR := createPRFn
	t.Cleanup(func() {
		pullRepoWorktreeFn = oldPull
		createPRFn = oldCreatePR
	})

	var pulls []string
	pullRepoWorktreeFn = func(_ *cli.Deps, repoPath, currentBranch, sourceBranch, remote string) error {
		pulls = append(pulls, strings.Join([]string{repoPath, currentBranch, sourceBranch, remote}, "|"))
		if repoPath == "/ui" {
			return errors.New("pull failed")
		}
		return nil
	}
	pullOut := captureGitStdout(t, func() {
		pullWorkspaceWorktrees(deps, worktrees, "")
	})
	if len(pulls) != 2 || pulls[0] != "/api|feature/api|main|origin" || pulls[1] != "/ui|feature/ui|trunk|upstream" {
		t.Fatalf("pull calls = %#v", pulls)
	}
	if !strings.Contains(pullOut, "api") || !strings.Contains(pullOut, "pull failed") {
		t.Fatalf("pull summary = %q", pullOut)
	}

	var prs []string
	createPRFn = func(_ *cli.Deps, repoPath, sourceBranch, targetBranch, remote string) (string, error) {
		prs = append(prs, strings.Join([]string{repoPath, sourceBranch, targetBranch, remote}, "|"))
		if repoPath == "/ui" {
			return "", errors.New("pr failed")
		}
		return "https://github.example.test/pr/1", nil
	}
	prOut := captureGitStdout(t, func() {
		prWorkspaceWorktrees(deps, worktrees, "", "")
	})
	if len(prs) != 2 || prs[0] != "/api|feature/api|main|origin" || prs[1] != "/ui|feature/ui|trunk|upstream" {
		t.Fatalf("pr calls = %#v", prs)
	}
	if !strings.Contains(prOut, "https://github.example.test/pr/1") || !strings.Contains(prOut, "pr failed") {
		t.Fatalf("pr summary = %q", prOut)
	}
}

func TestGeneratePRInfoFallbackBranches(t *testing.T) {
	_, gitRunner, _ := installWrapperCoverageDeps(t)
	gitRunner.RunFunc = func(_ string, args ...string) cli.CommandResult {
		joined := strings.Join(args, " ")
		switch joined {
		case "log origin/main..origin/feature --format=%s --reverse":
			return cli.CommandResult{Err: errors.New("missing remote branch")}
		case "log origin/main..feature --format=%s --reverse":
			return cli.CommandResult{Stdout: ""}
		default:
			return cli.CommandResult{Err: errors.New("unexpected " + joined)}
		}
	}
	title, body := generatePRInfo(gitRunnerDeps(t), "/repo", "origin", "main", "feature")
	if title != "feature" || body != "" {
		t.Fatalf("empty fallback title/body = %q/%q", title, body)
	}

	gitRunner.RunFunc = func(_ string, args ...string) cli.CommandResult {
		return cli.CommandResult{Err: errors.New("git log failed")}
	}
	title, body = generatePRInfo(gitRunnerDeps(t), "/repo", "origin", "main", "feature")
	if title != "feature" || body != "" {
		t.Fatalf("failed fallback title/body = %q/%q", title, body)
	}
}

func gitRunnerDeps(t *testing.T) *cli.Deps {
	t.Helper()
	return defaultDeps
}

func captureGitStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	fn()
	_ = w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("copy stdout: %v", err)
	}
	_ = r.Close()
	return buf.String()
}
