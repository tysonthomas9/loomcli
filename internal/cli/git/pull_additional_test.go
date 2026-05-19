package git

import (
	"errors"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

var errFakeGitOutput = errors.New("fake git output failure")

func TestPullCommandArgsValidation(t *testing.T) {
	origPullAll := pullAll
	t.Cleanup(func() { pullAll = origPullAll })

	pullAll = false
	if err := pullCmd.Args(pullCmd, nil); err == nil || !strings.Contains(err.Error(), "requires 1-2 arguments") {
		t.Fatalf("no-arg pull error = %v", err)
	}
	if err := pullCmd.Args(pullCmd, []string{"worker", "main", "extra"}); err == nil {
		t.Fatal("pull accepted too many worktree args")
	}
	if err := pullCmd.Args(pullCmd, []string{"worker"}); err != nil {
		t.Fatalf("pull worktree args rejected: %v", err)
	}

	pullAll = true
	if err := pullCmd.Args(pullCmd, []string{"main", "extra"}); err == nil || !strings.Contains(err.Error(), "--all flag accepts") {
		t.Fatalf("pull --all error = %v", err)
	}
	if err := pullCmd.Args(pullCmd, []string{"main"}); err != nil {
		t.Fatalf("pull --all source branch rejected: %v", err)
	}
	if sourceBranchDisplay("") != "(per-repo default)" || sourceBranchDisplay("develop") != "develop" {
		t.Fatalf("sourceBranchDisplay returned unexpected values")
	}
}

func TestPullRepoWorktreeSuccessAndMergeFailure(t *testing.T) {
	deps, gitRunner, _, _, _ := NewTestDeps(t)
	if err := pullRepoWorktree(deps, "/repo", "feature", "main", ""); err != nil {
		t.Fatalf("pullRepoWorktree success path: %v", err)
	}

	gitRunner.WithOutput = errFakeGitOutput
	if err := pullRepoWorktree(deps, "/repo", "feature", "main", ""); err == nil || !strings.Contains(err.Error(), "fetching") {
		t.Fatalf("pullRepoWorktree fetch failure = %v", err)
	}
}

func TestPullWorkspaceWorktreesSkipsMissingRepoAndDefaultsBranch(t *testing.T) {
	deps, gitRunner, _, _, _ := NewTestDeps(t)
	pullWorkspaceWorktrees(deps, []cli.WorktreeInfo{
		{Name: "skip", Path: "/skip"},
		{Name: "api", Path: "/repo/api", Branch: "feature", Repo: &RepoConfig{Name: "api", Remote: "upstream", DefaultBranch: "develop"}},
		{Name: "web", Path: "/repo/web", Branch: "feature", Repo: &RepoConfig{Name: "web"}},
	}, "")
	var commands []string
	for _, call := range gitRunner.RunCalls {
		commands = append(commands, strings.Join(call.Args, " "))
	}
	joined := strings.Join(commands, "\n")
	for _, want := range []string{
		"fetch upstream",
		"merge upstream/develop",
		"push upstream feature",
		"fetch origin",
		"merge origin/main",
		"push origin feature",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("commands missing %q:\n%s", want, joined)
		}
	}
}
