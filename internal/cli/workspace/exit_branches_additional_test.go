package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

type workspaceExitCode int

func expectWorkspaceExit(t *testing.T, want int, fn func()) {
	t.Helper()
	testingSetExitProcess(t, func(code int) {
		panic(workspaceExitCode(code))
	})
	defer func() {
		t.Helper()
		got := recover()
		if got == nil {
			t.Fatalf("expected exitProcess(%d)", want)
		}
		code, ok := got.(workspaceExitCode)
		if !ok {
			panic(got)
		}
		if int(code) != want {
			t.Fatalf("exit code = %d, want %d", code, want)
		}
	}()
	fn()
}

func TestWorkspaceExitBranchesWithInjectedExit(t *testing.T) {
	oldRepos, oldBranch, oldForce := wsCreateRepos, wsCreateBranch, wsRemoveForce
	t.Cleanup(func() {
		wsCreateRepos, wsCreateBranch, wsRemoveForce = oldRepos, oldBranch, oldForce
	})

	expectWorkspaceExit(t, 1, func() { _ = validateCreateInputs("bad/name") })

	wsCreateBranch = "-bad"
	expectWorkspaceExit(t, 1, func() { _ = validateCreateInputs("good") })

	wsCreateBranch = ""
	if got := validateCreateInputs("feature_1"); got != "feature_1" {
		t.Fatalf("branch = %q, want feature_1", got)
	}

	wsCreateRepos = ""
	expectWorkspaceExit(t, 1, func() { _ = parseRepoPaths() })
	wsCreateRepos = "api,web"
	if got := parseRepoPaths(); len(got) != 2 || got[0] != "api" || got[1] != "web" {
		t.Fatalf("repo paths = %+v", got)
	}

	repoDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoDir, ".agent.lock"), []byte("locked"), 0o600); err != nil {
		t.Fatalf("write lock: %v", err)
	}
	ws := config.WorkspaceConfig{Repos: []config.RepoConfig{{Name: "api", Path: repoDir}}}
	wsRemoveForce = false
	expectWorkspaceExit(t, 1, func() { checkRunningAgentsOrExit(ws) })
	wsRemoveForce = true
	checkRunningAgentsOrExit(ws)
}

func TestWorkspaceWorktreeRemovalBranches(t *testing.T) {
	oldForce := wsRemoveForce
	t.Cleanup(func() { wsRemoveForce = oldForce })

	wsDir := t.TempDir()
	repoDir := filepath.Join(wsDir, "api")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir git dir: %v", err)
	}
	removeWorktrees(nil, config.WorkspaceConfig{Path: wsDir, Repos: []config.RepoConfig{{Name: "api", Path: "api"}}})
	if _, err := os.Stat(wsDir); !os.IsNotExist(err) {
		t.Fatalf("workspace dir still exists or stat error: %v", err)
	}

	mainRepo := t.TempDir()
	wtDir := t.TempDir()
	gitFile := filepath.Join(wtDir, ".git")
	if err := os.WriteFile(gitFile, []byte("gitdir: "+filepath.Join(mainRepo, ".git", "worktrees", "api")), 0o600); err != nil {
		t.Fatalf("write git file: %v", err)
	}
	if got := findMainRepoPath(wtDir); got != mainRepo {
		t.Fatalf("findMainRepoPath = %q, want %q", got, mainRepo)
	}
	if got := findMainRepoPath(t.TempDir()); got != "" {
		t.Fatalf("missing git file path = %q, want empty", got)
	}
	if got := findMainRepoPath(writeWorkspaceGitFile(t, "not a gitdir")); got != "" {
		t.Fatalf("invalid git file path = %q, want empty", got)
	}
}

func writeWorkspaceGitFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte(strings.TrimSpace(content)), 0o600); err != nil {
		t.Fatalf("write git file: %v", err)
	}
	return dir
}
