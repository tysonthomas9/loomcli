package localworkspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

func TestPathHelpers(t *testing.T) {
	local := bootstrap.WorkspaceLocalState{
		Path:  "/workspace",
		Repos: map[string]string{"api": "/custom/api"},
	}
	if got := RepoPath(local, "api"); got != "/custom/api" {
		t.Fatalf("RepoPath custom = %q", got)
	}
	if got := RepoPath(local, "web"); got != filepath.Join("/workspace", "web") {
		t.Fatalf("RepoPath fallback = %q", got)
	}
	if got := RepoPath(bootstrap.WorkspaceLocalState{}, "web"); got != "" {
		t.Fatalf("RepoPath empty = %q", got)
	}
	if got := AgentWorktreePath("/workspace", "api", "nova"); got != filepath.Join("/workspace", "worktrees", "api", "nova") {
		t.Fatalf("AgentWorktreePath = %q", got)
	}
	if !PathContains("/workspace", "/workspace/api") || !PathContains("/workspace", "/workspace") {
		t.Fatal("PathContains should accept root and child")
	}
	if PathContains("/workspace", "/workspace-other") || PathContains("/workspace", "/tmp") {
		t.Fatal("PathContains accepted escaping path")
	}
	if PathContains("\x00", "/tmp") {
		t.Fatal("PathContains accepted invalid root")
	}
}

func TestRepoCheckoutPathValidation(t *testing.T) {
	root := t.TempDir()
	got, err := RepoCheckoutPath(root, "api")
	if err != nil {
		t.Fatalf("RepoCheckoutPath valid error = %v", err)
	}
	if got != filepath.Join(root, "api") {
		t.Fatalf("RepoCheckoutPath = %q", got)
	}
	for _, tt := range []struct {
		name string
		root string
		repo string
	}{
		{name: "empty root", repo: "api"},
		{name: "empty repo", root: root},
		{name: "absolute repo", root: root, repo: filepath.Join(root, "api")},
		{name: "nested repo", root: root, repo: filepath.Join("nested", "api")},
		{name: "slash repo", root: root, repo: "a/b"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := RepoCheckoutPath(tt.root, tt.repo); err == nil {
				t.Fatal("RepoCheckoutPath error = nil")
			}
		})
	}
}

func TestSelectAgentRepos(t *testing.T) {
	repos := []Repo{
		{Name: "api", Groups: []string{"backend"}},
		{Name: "web", Groups: []string{"frontend"}},
		{Name: "docs", Groups: []string{"docs"}},
	}
	if got, err := SelectAgentRepos(nil, domain.Agent{}); err != nil || got != nil {
		t.Fatalf("SelectAgentRepos empty = %#v err=%v", got, err)
	}
	if got, err := SelectAgentRepos(repos, domain.Agent{CrossRepo: true}); err != nil || len(got) != len(repos) {
		t.Fatalf("SelectAgentRepos cross = %#v err=%v", got, err)
	}
	if got, err := SelectAgentRepos(repos, domain.Agent{Repos: []string{"web"}}); err != nil || len(got) != 1 || got[0].Name != "web" {
		t.Fatalf("SelectAgentRepos explicit = %#v err=%v", got, err)
	}
	if got, err := SelectAgentRepos(repos, domain.Agent{RepoGroups: []string{"backend", "docs"}}); err != nil || len(got) != 2 || got[0].Name != "api" || got[1].Name != "docs" {
		t.Fatalf("SelectAgentRepos groups = %#v err=%v", got, err)
	}
	if got, err := SelectAgentRepos(repos, domain.Agent{}); err != nil || len(got) != 1 || got[0].Name != "api" {
		t.Fatalf("SelectAgentRepos default = %#v err=%v", got, err)
	}
	if _, err := SelectAgentRepos(repos, domain.Agent{Repos: []string{"missing"}}); err == nil || !strings.Contains(err.Error(), "api, docs, web") {
		t.Fatalf("SelectAgentRepos missing err=%v", err)
	}
}

func TestStateHelpersAndBranchDetection(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	if got := FirstWorktreePath(nil); got != "" {
		t.Fatalf("FirstWorktreePath empty = %q", got)
	}
	if got := FirstWorktreePath(map[string]string{"z": "/z", "a": "/a"}); got != "/a" {
		t.Fatalf("FirstWorktreePath = %q", got)
	}
	if !branchAlreadyExists("fatal: a branch named 'worker' already exists", nil) {
		t.Fatal("branchAlreadyExists did not match branch message")
	}
	if !branchAlreadyExists("", os.ErrExist) {
		t.Fatal("branchAlreadyExists did not inspect error text")
	}
	if branchAlreadyExists("different failure", nil) {
		t.Fatal("branchAlreadyExists matched unrelated output")
	}

	if err := RememberAgentWorktree("WS", "nova", "/workspace/worktrees/api/nova"); err != nil {
		t.Fatalf("RememberAgentWorktree: %v", err)
	}
	if err := RememberRepoPath("WS", "api", "/workspace/api"); err != nil {
		t.Fatalf("RememberRepoPath: %v", err)
	}
	state, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("LoadStateCache: %v", err)
	}
	local := state.Workspaces["WS"]
	if local.Agents["nova"].Worktree != "/workspace/worktrees/api/nova" || local.Repos["api"] != "/workspace/api" {
		t.Fatalf("local state = %#v", local)
	}
}

func TestEnsureGitWorktreeReturnsWhenTargetAlreadyExists(t *testing.T) {
	target := filepath.Join(t.TempDir(), "worktree")
	if err := os.MkdirAll(filepath.Join(target, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := EnsureGitWorktree("/missing/repo", target, "branch"); err != nil {
		t.Fatalf("EnsureGitWorktree existing .git error = %v", err)
	}
}

func TestCloneRepoToCreatesParentAndReportsFailure(t *testing.T) {
	target := filepath.Join(t.TempDir(), "nested", "repo")
	err := CloneRepoTo(t.Context(), "definitely-not-a-repo", target)
	if err == nil || !strings.Contains(err.Error(), "git clone failed") {
		t.Fatalf("CloneRepoTo error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Dir(target)); statErr != nil {
		t.Fatalf("clone parent was not created: %v", statErr)
	}
}

func TestResolveFreshBaseRefWithoutDefaultBranch(t *testing.T) {
	base, err := resolveFreshBaseRef("/missing/repo", "", "")
	if err != nil || base != "" {
		t.Fatalf("resolveFreshBaseRef empty = %q err=%v", base, err)
	}
}

func TestRunGitFailureIncludesCommand(t *testing.T) {
	if _, err := runGit(t.TempDir(), "not-a-real-subcommand"); err == nil || !strings.Contains(err.Error(), "git not-a-real-subcommand") {
		t.Fatalf("runGit error = %v", err)
	}
}

func TestEnsureGitWorktreeFromBranchUsesFetchedDefaultBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	repo := filepath.Join(root, "repo")
	target := filepath.Join(root, "worktrees", "worker")

	git(t, "", "init", "--bare", remote)
	git(t, "", "init", seed)
	git(t, seed, "checkout", "-b", "main")
	git(t, seed, "config", "user.name", "Test User")
	git(t, seed, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(seed, "base.txt"), "v1\n")
	git(t, seed, "add", "base.txt")
	git(t, seed, "commit", "-m", "base")
	git(t, seed, "remote", "add", "origin", remote)
	git(t, seed, "push", "origin", "main")

	git(t, "", "clone", remote, repo)
	git(t, repo, "checkout", "main")

	writeFile(t, filepath.Join(seed, "base.txt"), "v2\n")
	git(t, seed, "add", "base.txt")
	git(t, seed, "commit", "-m", "advance")
	git(t, seed, "push", "origin", "main")

	if err := EnsureGitWorktreeFromBranch(repo, target, "worker", "origin", "main"); err != nil {
		t.Fatalf("EnsureGitWorktreeFromBranch() error = %v", err)
	}

	gotBytes, err := os.ReadFile(filepath.Join(target, "base.txt"))
	if err != nil {
		t.Fatalf("read target file: %v", err)
	}
	if got := string(gotBytes); got != "v2\n" {
		t.Fatalf("target base.txt = %q, want fetched v2", got)
	}
}

func TestEnsureGitWorktreeFromBranchFallsBackToLocalDefaultBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	repo := filepath.Join(root, "repo")
	target := filepath.Join(root, "worktrees", "worker")

	git(t, "", "init", "--bare", remote)
	git(t, "", "init", seed)
	git(t, seed, "checkout", "-b", "main")
	git(t, seed, "config", "user.name", "Test User")
	git(t, seed, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(seed, "base.txt"), "main\n")
	git(t, seed, "add", "base.txt")
	git(t, seed, "commit", "-m", "base")
	git(t, seed, "remote", "add", "origin", remote)
	git(t, seed, "push", "origin", "main")

	git(t, "", "clone", remote, repo)
	git(t, repo, "checkout", "-b", "browser-e2e")
	writeFile(t, filepath.Join(repo, "base.txt"), "local branch\n")
	git(t, repo, "add", "base.txt")
	git(t, repo, "commit", "-m", "local branch")

	if err := EnsureGitWorktreeFromBranch(repo, target, "worker", "origin", "browser-e2e"); err != nil {
		t.Fatalf("EnsureGitWorktreeFromBranch() error = %v", err)
	}

	gotBytes, err := os.ReadFile(filepath.Join(target, "base.txt"))
	if err != nil {
		t.Fatalf("read target file: %v", err)
	}
	if got := string(gotBytes); got != "local branch\n" {
		t.Fatalf("target base.txt = %q, want local branch content", got)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec,gosec // fixed test helper commands.
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
