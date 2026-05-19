package git

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

func TestPRWorkspaceWorktreesUsesDefaultsAndSummarizes(t *testing.T) {
	oldCreatePR := createPRFn
	t.Cleanup(func() { createPRFn = oldCreatePR })

	var calls []struct {
		path   string
		source string
		target string
		remote string
	}
	createPRFn = func(_ *cli.Deps, repoPath, sourceBranch, targetBranch, remote string) (string, error) {
		calls = append(calls, struct {
			path   string
			source string
			target string
			remote string
		}{repoPath, sourceBranch, targetBranch, remote})
		if repoPath == "/repo-web" {
			return "", errors.New("already exists")
		}
		return "https://example.test/pr/1", nil
	}

	prWorkspaceWorktrees(&cli.Deps{}, []cli.WorktreeInfo{
		{Name: "api", Path: "/repo-api", Branch: "feature-api", Repo: &cfgpkg.RepoConfig{DefaultBranch: "develop", Remote: "upstream"}},
		{Name: "web", Path: "/repo-web", Branch: "feature-web", Repo: &cfgpkg.RepoConfig{}},
		{Name: "skip", Path: "/repo-skip"},
	}, "", "")

	if len(calls) != 2 {
		t.Fatalf("createPR calls = %+v, want 2", calls)
	}
	if calls[0].source != "feature-api" || calls[0].target != "develop" || calls[0].remote != "upstream" {
		t.Fatalf("api call = %+v", calls[0])
	}
	if calls[1].source != "feature-web" || calls[1].target != "main" || calls[1].remote != "" {
		t.Fatalf("web call = %+v", calls[1])
	}
}

func TestPullWorkspaceWorktreesUsesDefaultsAndContinuesAfterErrors(t *testing.T) {
	oldPull := pullRepoWorktreeFn
	t.Cleanup(func() { pullRepoWorktreeFn = oldPull })

	var calls []struct {
		path   string
		branch string
		source string
		remote string
	}
	pullRepoWorktreeFn = func(_ *cli.Deps, repoPath, currentBranch, sourceBranch, remote string) error {
		calls = append(calls, struct {
			path   string
			branch string
			source string
			remote string
		}{repoPath, currentBranch, sourceBranch, remote})
		if repoPath == "/repo-web" {
			return errors.New("merge conflict")
		}
		return nil
	}

	pullWorkspaceWorktrees(&cli.Deps{}, []cli.WorktreeInfo{
		{Name: "api", Path: "/repo-api", Branch: "feature-api", Repo: &cfgpkg.RepoConfig{DefaultBranch: "develop", Remote: "upstream"}},
		{Name: "web", Path: "/repo-web", Branch: "feature-web", Repo: &cfgpkg.RepoConfig{}},
		{Name: "skip", Path: "/repo-skip"},
	}, "")

	if len(calls) != 2 {
		t.Fatalf("pull calls = %+v, want 2", calls)
	}
	if calls[0].branch != "feature-api" || calls[0].source != "develop" || calls[0].remote != "upstream" {
		t.Fatalf("api pull call = %+v", calls[0])
	}
	if calls[1].branch != "feature-web" || calls[1].source != "main" || calls[1].remote != "" {
		t.Fatalf("web pull call = %+v", calls[1])
	}
}

func TestWorkspaceRepoCommandsUseResolverRepos(t *testing.T) {
	root := t.TempDir()
	api := filepath.Join(root, "api")
	web := filepath.Join(root, "web")
	for _, dir := range []string{api, web} {
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			t.Fatalf("create fake repo %s: %v", dir, err)
		}
	}
	resolver := &cli.Resolver{
		Mode:      cli.ModeWorkspace,
		Workspace: "WS",
		Config: &cfgpkg.LoomConfig{Workspaces: map[string]cfgpkg.WorkspaceConfig{
			"WS": {
				Path: root,
				Repos: []cfgpkg.RepoConfig{
					{Name: "api", Path: api, DefaultBranch: "develop", Remote: "upstream"},
					{Name: "web", Path: web},
				},
			},
		}},
	}

	oldCreatePR := createPRFn
	oldPush := pushWorkspaceWorktreesFn
	t.Cleanup(func() {
		createPRFn = oldCreatePR
		pushWorkspaceWorktreesFn = oldPush
	})

	var prCalls []string
	createPRFn = func(_ *cli.Deps, repoPath, sourceBranch, targetBranch, remote string) (string, error) {
		prCalls = append(prCalls, strings.Join([]string{repoPath, sourceBranch, targetBranch, remote}, "|"))
		return "https://example.test/pr", nil
	}
	prWorkspaceRepos(&cli.Deps{}, resolver, "feature", "")
	if len(prCalls) != 2 {
		t.Fatalf("pr calls = %#v, want 2", prCalls)
	}
	if !strings.Contains(prCalls[0], api+"|feature|develop|upstream") {
		t.Fatalf("first pr call = %q", prCalls[0])
	}
	if !strings.Contains(prCalls[1], web+"|feature|main|") {
		t.Fatalf("second pr call = %q", prCalls[1])
	}

	var pushed []cli.WorktreeInfo
	pushWorkspaceWorktreesFn = func(_ *cli.Deps, worktrees []cli.WorktreeInfo, sourceBranch, targetBranch string) error {
		pushed = append(pushed, worktrees...)
		if sourceBranch != "feature" || targetBranch != "release" {
			t.Fatalf("push branches = %q -> %q", sourceBranch, targetBranch)
		}
		return errors.New("push failed")
	}
	err := pushWorkspaceRepos(&cli.Deps{}, resolver, "feature", "release")
	if err == nil || !strings.Contains(err.Error(), "push failed") {
		t.Fatalf("pushWorkspaceRepos err = %v", err)
	}
	if len(pushed) != 2 || pushed[0].Name != "api" || pushed[1].Name != "web" {
		t.Fatalf("pushed worktrees = %+v", pushed)
	}
}
