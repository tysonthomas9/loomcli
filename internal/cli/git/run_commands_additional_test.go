package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

func TestRunPullAndPRAllCommandsUseFlagValues(t *testing.T) {
	tmp := t.TempDir()
	ws := filepath.Join(tmp, "ws")
	setupWorkspaceConfig(t, &LoomConfig{
		DefaultWorkspace: "ws1",
		Workspaces: map[string]WorkspaceConfig{
			"ws1": {Path: ws},
		},
	})

	t.Run("pull all", func(t *testing.T) {
		deps, _, _, _, _ := NewTestDeps(t)
		cmd := &cobra.Command{}
		cmd.Flags().Bool("all", false, "")
		cmd.Flags().String("workspace", "", "")
		cmd.SetContext(cli.WithDeps(context.Background(), deps))
		if err := cmd.Flags().Set("all", "true"); err != nil {
			t.Fatalf("set all: %v", err)
		}
		if err := runPull(cmd, []string{"main"}); err != nil {
			t.Fatalf("runPull: %v", err)
		}
	})

	t.Run("pr all", func(t *testing.T) {
		deps, _, _, _, _ := NewTestDeps(t)
		cmdMock := NewCommandMock(t, []CommandStub{
			{Name: "gh", Args: []string{"--version"}, Stdout: "gh version 2.0\n"},
		})
		cmdMock.InstallOn(deps)

		cmd := &cobra.Command{}
		cmd.Flags().Bool("all", false, "")
		cmd.Flags().String("workspace", "", "")
		cmd.SetContext(cli.WithDeps(context.Background(), deps))
		if err := cmd.Flags().Set("all", "true"); err != nil {
			t.Fatalf("set all: %v", err)
		}
		if err := runPR(cmd, []string{"main"}); err != nil {
			t.Fatalf("runPR: %v", err)
		}
	})
}

func TestRunPushAndResetAllCommandsUseFlagValues(t *testing.T) {
	tmp := t.TempDir()
	ws := filepath.Join(tmp, "ws")
	setupWorkspaceConfig(t, &LoomConfig{
		DefaultWorkspace: "ws1",
		Workspaces: map[string]WorkspaceConfig{
			"ws1": {Path: ws},
		},
	})

	t.Run("push all", func(t *testing.T) {
		deps, _, _, _, _ := NewTestDeps(t)
		cmd := &cobra.Command{}
		cmd.Flags().Bool("all", false, "")
		cmd.Flags().String("workspace", "", "")
		cmd.SetContext(cli.WithDeps(context.Background(), deps))
		if err := cmd.Flags().Set("all", "true"); err != nil {
			t.Fatalf("set all: %v", err)
		}
		if err := runPush(cmd, []string{"main"}); err != nil {
			t.Fatalf("runPush: %v", err)
		}

		resolver, err := NewResolver()
		if err != nil {
			t.Fatalf("NewResolver: %v", err)
		}
		if err := resolver.SetWorkspace("ws1"); err != nil {
			t.Fatalf("SetWorkspace: %v", err)
		}
		if err := pushWorkspaceRepos(deps, resolver, "feature", "main"); err != nil {
			t.Fatalf("pushWorkspaceRepos with empty workspace: %v", err)
		}
	})

	t.Run("reset all", func(t *testing.T) {
		oldResetAll, oldResetForce := resetAll, resetForce
		t.Cleanup(func() {
			resetAll, resetForce = oldResetAll, oldResetForce
		})
		resetAll = true
		resetForce = true

		deps, _, _, _, _ := NewTestDeps(t)
		cmd := &cobra.Command{}
		cmd.SetContext(cli.WithDeps(context.Background(), deps))
		runReset(cmd, []string{"main"})
	})
}

func TestRunWorkspaceModeCommandsUseResolverAndHooks(t *testing.T) {
	tmp := t.TempDir()
	ws := filepath.Join(tmp, "ws")
	repo := filepath.Join(ws, "api")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	setupWorkspaceConfig(t, &LoomConfig{
		DefaultWorkspace: "ws1",
		Workspaces: map[string]WorkspaceConfig{
			"ws1": {Path: ws, Repos: []RepoConfig{{Name: "api", Path: repo, Remote: "upstream", DefaultBranch: "develop"}}},
		},
	})

	oldPull := pullRepoWorktreeFn
	oldCreatePR := createPRFn
	oldPush := pushWorkspaceWorktreesFn
	t.Cleanup(func() {
		pullRepoWorktreeFn = oldPull
		createPRFn = oldCreatePR
		pushWorkspaceWorktreesFn = oldPush
	})

	deps, _, _, _, _ := NewTestDeps(t)
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature/api\n"},
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature/api\n"},
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature/api\n"},
	})
	cmdMock.Install()

	var pulled bool
	pullRepoWorktreeFn = func(_ *cli.Deps, repoPath, currentBranch, sourceBranch, remote string) error {
		pulled = repoPath == repo && currentBranch == "feature/api" && sourceBranch == "develop" && remote == "upstream"
		return nil
	}
	pullCmd := &cobra.Command{}
	pullCmd.Flags().Bool("all", false, "")
	pullCmd.Flags().String("workspace", "", "")
	pullCmd.SetContext(cli.WithDeps(context.Background(), deps))
	if err := pullCmd.Flags().Set("workspace", "ws1"); err != nil {
		t.Fatalf("set pull workspace: %v", err)
	}
	if err := runPull(pullCmd, []string{"api"}); err != nil {
		t.Fatalf("runPull workspace mode: %v", err)
	}
	if !pulled {
		t.Fatal("runPull did not invoke pullRepoWorktreeFn with resolved repo defaults")
	}

	var prCalled bool
	createPRFn = func(_ *cli.Deps, repoPath, sourceBranch, targetBranch, remote string) (string, error) {
		prCalled = repoPath == repo && sourceBranch == "feature/api" && targetBranch == "develop" && remote == "upstream"
		return "https://example.test/pr/1", nil
	}
	if err := runPRWorkspaceMode(deps, []string{"feature/api"}, false, "ws1"); err != nil {
		t.Fatalf("runPRWorkspaceMode: %v", err)
	}
	if !prCalled {
		t.Fatal("runPRWorkspaceMode did not invoke createPRFn with resolved repo defaults")
	}

	var pushed bool
	pushWorkspaceWorktreesFn = func(_ *cli.Deps, worktrees []cli.WorktreeInfo, sourceBranch, targetBranch string) error {
		pushed = len(worktrees) == 1 && worktrees[0].Path == repo && sourceBranch == "feature/api" && targetBranch == "main"
		return nil
	}
	pushCmd := &cobra.Command{}
	pushCmd.Flags().Bool("all", false, "")
	pushCmd.Flags().String("workspace", "", "")
	pushCmd.SetContext(cli.WithDeps(context.Background(), deps))
	if err := pushCmd.Flags().Set("workspace", "ws1"); err != nil {
		t.Fatalf("set push workspace: %v", err)
	}
	if err := runPush(pushCmd, []string{"feature/api", "main"}); err != nil {
		t.Fatalf("runPush workspace mode: %v", err)
	}
	if !pushed {
		t.Fatal("runPush did not invoke pushWorkspaceWorktreesFn with resolved workspace")
	}
}
