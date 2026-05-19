package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

func testResolver(cfg *LoomConfig, ws string) *cli.Resolver {
	return &cli.Resolver{Mode: cli.ModeWorkspace, Config: cfg, Workspace: ws}
}

func TestResolveWorkspaceTarget_WorkspaceName(t *testing.T) {
	tmpDir := t.TempDir()
	resolver := testResolver(&LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"myws": {Path: tmpDir},
		},
	}, "myws")

	target, err := resolveWorkspaceTarget(resolver, "myws", "")
	if err != nil {
		t.Fatalf("resolveWorkspaceTarget() error = %v", err)
	}
	if target.WorkDir != tmpDir || target.AgentName != "myws" {
		t.Fatalf("target = %+v, want workspace root/name", target)
	}
}

func TestResolveWorkspaceTarget_RepoName(t *testing.T) {
	tmpDir := t.TempDir()
	repoPath := filepath.Join(tmpDir, "repo1")
	createGitRepo(t, repoPath)
	resolver := testResolver(&LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"myws": {
				Path:  tmpDir,
				Repos: []RepoConfig{{Name: "repo1", Path: repoPath}},
			},
		},
	}, "myws")

	target, err := resolveWorkspaceTarget(resolver, "repo1", "")
	if err != nil {
		t.Fatalf("resolveWorkspaceTarget() error = %v", err)
	}
	if target.WorkDir != repoPath || target.AgentName != "repo1" {
		t.Fatalf("target = %+v, want repo path/name", target)
	}
}

func TestResolveWorkspaceTarget_PerRepoWorktree(t *testing.T) {
	tmpDir := t.TempDir()
	repoPath := filepath.Join(tmpDir, "repo1")
	createGitRepo(t, repoPath)
	resolver := testResolver(&LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"myws": {
				Path:  tmpDir,
				Repos: []RepoConfig{{Name: "repo1", Path: repoPath}},
			},
		},
	}, "myws")

	target, err := resolveWorkspaceTarget(resolver, "nova", "repo1")
	if err != nil {
		t.Fatalf("resolveWorkspaceTarget per repo: %v", err)
	}
	wantPath := filepath.Join(tmpDir, "worktrees", "repo1", "nova")
	if target.WorkDir != wantPath || target.AgentName != "nova" || target.Repo != "repo1" {
		t.Fatalf("target = %+v, want workdir %q repo1/nova", target, wantPath)
	}
	if _, err := os.Stat(filepath.Join(wantPath, ".git")); err != nil {
		t.Fatalf("repo worktree .git missing: %v", err)
	}
	if got := GetWorktreeName(wantPath); got != "nova" {
		t.Fatalf("GetWorktreeName = %q", got)
	}
	if err := EnsureRepoWorktree(repoPath, wantPath, "nova"); err != nil {
		t.Fatalf("EnsureRepoWorktree existing: %v", err)
	}
}

func TestResolveWorkspaceTarget_NoArgUsesWorkspaceRoot(t *testing.T) {
	tmpDir := t.TempDir()
	resolver := testResolver(&LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"myws": {Path: tmpDir},
		},
	}, "myws")

	target, err := resolveWorkspaceTarget(resolver, "", "")
	if err != nil {
		t.Fatalf("resolveWorkspaceTarget() error = %v", err)
	}
	if target.WorkDir != tmpDir || target.AgentName != "myws" {
		t.Fatalf("target = %+v, want workspace root/default agent name", target)
	}
}

func TestResolveWorkspaceTarget_UnknownName(t *testing.T) {
	resolver := testResolver(&LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"myws": {Path: t.TempDir()},
		},
	}, "myws")

	_, err := resolveWorkspaceTarget(resolver, "missing", "")
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("error = %v, want unknown target error mentioning name", err)
	}
}

func TestResolveAgentTarget_AbsolutePath(t *testing.T) {
	dir := t.TempDir()

	target, err := ResolveAgentTarget(dir, "")
	if err != nil {
		t.Fatalf("ResolveAgentTarget() error = %v", err)
	}
	if target.WorkDir != dir || target.AgentName != filepath.Base(dir) {
		t.Fatalf("target = %+v, want absolute path target", target)
	}
}

func TestResolveAgentTarget_AbsolutePathMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")

	_, err := ResolveAgentTarget(missing, "")
	if err == nil {
		t.Fatal("ResolveAgentTarget() error = nil, want missing path error")
	}
	if !os.IsNotExist(err) && !strings.Contains(err.Error(), "path does not exist") {
		t.Fatalf("error = %v, want missing path error", err)
	}
}
