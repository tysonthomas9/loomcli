package git

import (
	"errors"
	"os"
	"path/filepath"
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

func TestPullRepoWorktreeConflictAndPushErrors(t *testing.T) {
	t.Run("merge conflict invokes agent", func(t *testing.T) {
		deps, _, execRunner, _, _ := NewTestDeps(t)
		execRunner.RunFunc = func(_ string, name string, args ...string) cli.CommandResult {
			if name == "git" && strings.Join(args, " ") == "diff --name-only --diff-filter=U" {
				return cli.CommandResult{Stdout: "a.go\nb.go\n"}
			}
			return cli.CommandResult{}
		}
		outputMock := NewOutputCommandMock(t, []OutputCommandStub{
			{Err: nil},
			{Err: errors.New("merge conflict")},
		})
		outputMock.InstallOn(deps)

		var prompt string
		deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, gotPrompt, agentName string) error {
			if workDir != "/repo" || agentName != "" {
				t.Fatalf("agent invocation workDir=%q agent=%q", workDir, agentName)
			}
			prompt = gotPrompt
			return nil
		}}

		if err := pullRepoWorktree(deps, "/repo", "feature", "main", "origin"); err != nil {
			t.Fatalf("pullRepoWorktree conflict path: %v", err)
		}
		if !strings.Contains(prompt, "a.go") || !strings.Contains(prompt, "b.go") {
			t.Fatalf("conflict prompt = %q", prompt)
		}
	})

	t.Run("merge conflict without conflicted files returns merge error", func(t *testing.T) {
		deps, _, execRunner, _, _ := NewTestDeps(t)
		execRunner.RunFunc = func(_ string, name string, args ...string) cli.CommandResult {
			if name == "git" && strings.Join(args, " ") == "diff --name-only --diff-filter=U" {
				return cli.CommandResult{}
			}
			return cli.CommandResult{}
		}
		outputMock := NewOutputCommandMock(t, []OutputCommandStub{
			{Err: nil},
			{Err: errors.New("merge failed")},
		})
		outputMock.InstallOn(deps)

		if err := pullRepoWorktree(deps, "/repo", "feature", "main", "origin"); err == nil || !strings.Contains(err.Error(), "merge failed") {
			t.Fatalf("pullRepoWorktree no-conflict-files error = %v", err)
		}
	})

	t.Run("push failure", func(t *testing.T) {
		deps, _, _, _, _ := NewTestDeps(t)
		outputMock := NewOutputCommandMock(t, []OutputCommandStub{
			{Err: nil},
			{Err: nil},
			{Err: errors.New("push rejected")},
		})
		outputMock.InstallOn(deps)

		if err := pullRepoWorktree(deps, "/repo", "feature", "main", "origin"); err == nil || !strings.Contains(err.Error(), "pushing") {
			t.Fatalf("pullRepoWorktree push failure = %v", err)
		}
	})
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

func TestPullAllWorkspacesUsesConfiguredRepos(t *testing.T) {
	tmp := t.TempDir()
	ws1 := filepath.Join(tmp, "ws1")
	ws2 := filepath.Join(tmp, "ws2")
	repo := filepath.Join(ws1, "api")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	if err := os.MkdirAll(ws2, 0755); err != nil {
		t.Fatalf("mkdir ws2: %v", err)
	}
	setupWorkspaceConfig(t, &LoomConfig{
		DefaultWorkspace: "ws1",
		Workspaces: map[string]WorkspaceConfig{
			"ws1": {Path: ws1, Repos: []RepoConfig{{Name: "api", Path: repo, DefaultBranch: "develop"}}},
			"ws2": {Path: ws2},
		},
	})

	deps, _, _, _, _ := NewTestDeps(t)
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature\n"},
	})
	cmdMock.Install()
	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"fetch", "origin"}},
		{Args: []string{"merge", "origin/develop", "-m", "Pull from develop\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}},
		{Args: []string{"push", "origin", "feature"}},
	})
	outputMock.InstallOn(deps)
	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Fatalf("unexpected agent invocation in %s: %s", workDir, prompt)
		return nil
	}}

	pullAllWorkspaces(deps, "")
}

func TestPushAllWorkspacesUsesConfiguredRepos(t *testing.T) {
	tmp := t.TempDir()
	ws1 := filepath.Join(tmp, "ws1")
	ws2 := filepath.Join(tmp, "ws2")
	repo := filepath.Join(ws1, "api")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	if err := os.MkdirAll(ws2, 0755); err != nil {
		t.Fatalf("mkdir ws2: %v", err)
	}
	setupWorkspaceConfig(t, &LoomConfig{
		DefaultWorkspace: "ws1",
		Workspaces: map[string]WorkspaceConfig{
			"ws1": {Path: ws1, Repos: []RepoConfig{{Name: "api", Path: repo}}},
			"ws2": {Path: ws2},
		},
	})

	deps, _, _, _, _ := NewTestDeps(t)
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature\n"},
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature\n"},
		{Name: "git", Args: []string{"log", "origin/main..feature", "--oneline"}, Stdout: "abc commit\n"},
	})
	cmdMock.Install()
	deps.Exec = cmdMock
	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"fetch", "origin"}},
		{Args: []string{"stash"}},
		{Args: []string{"checkout", "main"}},
		{Args: []string{"pull", "origin", "main"}},
		{Args: []string{"merge", "-m", "Merge feature into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", "--", "feature"}},
		{Args: []string{"push", "origin", "main"}},
		{Args: []string{"checkout", "feature"}},
	})
	outputMock.InstallOn(deps)
	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Fatalf("unexpected agent invocation in %s: %s", workDir, prompt)
		return nil
	}}

	if err := pushAllWorkspaces(deps, ""); err != nil {
		t.Fatalf("pushAllWorkspaces: %v", err)
	}
}

func TestPullWorkspaceRepoUsesRepoDefaultBranch(t *testing.T) {
	tmp := t.TempDir()
	ws := filepath.Join(tmp, "ws")
	repo := filepath.Join(ws, "api")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	setupWorkspaceConfig(t, &LoomConfig{
		DefaultWorkspace: "ws1",
		Workspaces: map[string]WorkspaceConfig{
			"ws1": {Path: ws, Repos: []RepoConfig{{Name: "api", Path: repo, DefaultBranch: "develop", Remote: "upstream"}}},
		},
	})

	resolver, err := NewResolver()
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if err := resolver.SetWorkspace("ws1"); err != nil {
		t.Fatalf("SetWorkspace: %v", err)
	}
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature\n"},
	})
	cmdMock.Install()
	deps, _, _, _, _ := NewTestDeps(t)
	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"fetch", "upstream"}},
		{Args: []string{"merge", "upstream/develop", "-m", "Pull from develop\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}},
		{Args: []string{"push", "upstream", "feature"}},
	})
	outputMock.InstallOn(deps)

	pullWorkspaceRepo(deps, resolver, "api", "")
}
