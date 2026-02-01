package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// ============================================================================
// resetAllWorktrees Per-Repo Branch Tests
// ============================================================================

func TestResetAllWorktrees_PerRepoBranch(t *testing.T) {
	// In workspace mode with per-repo DefaultBranch values, resetAllWorktrees
	// should use each repo's own DefaultBranch when explicitTarget=false.
	ResetBeadsDirCache()

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	repo1Path := filepath.Join(tmpDir, "repo1")
	repo2Path := filepath.Join(tmpDir, "repo2")
	createGitRepo(t, repo1Path)
	createGitRepo(t, repo2Path)

	cfg := &LoomConfig{
		DefaultWorkspace: "testws",
		Workspaces: map[string]WorkspaceConfig{
			"testws": {
				Path: tmpDir,
				Repos: []RepoConfig{
					{Name: "repo1", Path: repo1Path, DefaultBranch: "develop"},
					{Name: "repo2", Path: repo2Path, DefaultBranch: "staging"},
				},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	// DiscoverWorktrees calls GetCurrentBranch for each repo (via execCommand),
	// then resetWorktree calls GetCurrentBranch again for each.
	mock := NewCommandMock(t, []CommandStub{
		// DiscoverWorktrees: GetCurrentBranch for repo1
		{Dir: repo1Path, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature-1\n"},
		// DiscoverWorktrees: GetCurrentBranch for repo2
		{Dir: repo2Path, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature-2\n"},
		// resetWorktree(repo1): GetCurrentBranch
		{Dir: repo1Path, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature-1\n"},
		// resetWorktree(repo2): GetCurrentBranch
		{Dir: repo2Path, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature-2\n"},
	})
	mock.Install()

	// GitFetch, GitReset, GitClean, GitPushForce all use runGitWithOutputFunc
	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		// repo1: fetch, reset HEAD, clean, reset origin/develop, push
		{Dir: repo1Path, Args: []string{"fetch", "origin"}},
		{Dir: repo1Path, Args: []string{"reset", "--hard", "HEAD"}},
		{Dir: repo1Path, Args: []string{"clean", "-fd"}},
		{Dir: repo1Path, Args: []string{"reset", "--hard", "origin/develop"}},
		{Dir: repo1Path, Args: []string{"push", "origin", "feature-1", "--force"}},
		// repo2: fetch, reset HEAD, clean, reset origin/staging, push
		{Dir: repo2Path, Args: []string{"fetch", "origin"}},
		{Dir: repo2Path, Args: []string{"reset", "--hard", "HEAD"}},
		{Dir: repo2Path, Args: []string{"clean", "-fd"}},
		{Dir: repo2Path, Args: []string{"reset", "--hard", "origin/staging"}},
		{Dir: repo2Path, Args: []string{"push", "origin", "feature-2", "--force"}},
	})
	outputMock.Install()

	resetForce = true
	defer func() { resetForce = false }()

	defaultBranch := GetDefaultBranch()
	resetAllWorktrees(defaultBranch, false)
}

func TestResetAllWorktrees_ExplicitBranchOverridesPerRepo(t *testing.T) {
	// When explicitTarget=true, all repos should use the given branch,
	// ignoring per-repo DefaultBranch settings.
	ResetBeadsDirCache()

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	repo1Path := filepath.Join(tmpDir, "repo1")
	repo2Path := filepath.Join(tmpDir, "repo2")
	createGitRepo(t, repo1Path)
	createGitRepo(t, repo2Path)

	cfg := &LoomConfig{
		DefaultWorkspace: "testws",
		Workspaces: map[string]WorkspaceConfig{
			"testws": {
				Path: tmpDir,
				Repos: []RepoConfig{
					{Name: "repo1", Path: repo1Path, DefaultBranch: "develop"},
					{Name: "repo2", Path: repo2Path, DefaultBranch: "staging"},
				},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	mock := NewCommandMock(t, []CommandStub{
		// DiscoverWorktrees: GetCurrentBranch for each
		{Dir: repo1Path, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "main\n"},
		{Dir: repo2Path, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "main\n"},
		// resetWorktree: GetCurrentBranch for each
		{Dir: repo1Path, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "main\n"},
		{Dir: repo2Path, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "main\n"},
	})
	mock.Install()

	// Both repos should reset to "release" (explicit target overrides per-repo)
	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		// repo1
		{Dir: repo1Path, Args: []string{"fetch", "origin"}},
		{Dir: repo1Path, Args: []string{"reset", "--hard", "HEAD"}},
		{Dir: repo1Path, Args: []string{"clean", "-fd"}},
		{Dir: repo1Path, Args: []string{"reset", "--hard", "origin/release"}},
		{Dir: repo1Path, Args: []string{"push", "origin", "main", "--force"}},
		// repo2
		{Dir: repo2Path, Args: []string{"fetch", "origin"}},
		{Dir: repo2Path, Args: []string{"reset", "--hard", "HEAD"}},
		{Dir: repo2Path, Args: []string{"clean", "-fd"}},
		{Dir: repo2Path, Args: []string{"reset", "--hard", "origin/release"}},
		{Dir: repo2Path, Args: []string{"push", "origin", "main", "--force"}},
	})
	outputMock.Install()

	resetForce = true
	defer func() { resetForce = false }()

	resetAllWorktrees("release", true)
}

func TestResetAllWorktrees_MixedDefaultBranch(t *testing.T) {
	// One repo has a custom DefaultBranch, the other has none.
	// The repo without DefaultBranch should use the global default.
	ResetBeadsDirCache()

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	repo1Path := filepath.Join(tmpDir, "repo1")
	repo2Path := filepath.Join(tmpDir, "repo2")
	createGitRepo(t, repo1Path)
	createGitRepo(t, repo2Path)

	cfg := &LoomConfig{
		DefaultWorkspace: "testws",
		Workspaces: map[string]WorkspaceConfig{
			"testws": {
				Path: tmpDir,
				Repos: []RepoConfig{
					{Name: "repo1", Path: repo1Path, DefaultBranch: "develop"},
					{Name: "repo2", Path: repo2Path}, // no DefaultBranch
				},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	defaultBranch := GetDefaultBranch()

	mock := NewCommandMock(t, []CommandStub{
		// DiscoverWorktrees
		{Dir: repo1Path, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feat\n"},
		{Dir: repo2Path, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feat\n"},
		// resetWorktree for each
		{Dir: repo1Path, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feat\n"},
		{Dir: repo2Path, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feat\n"},
	})
	mock.Install()

	// repo1 resets to "develop", repo2 resets to the global default
	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		// repo1 -> develop
		{Dir: repo1Path, Args: []string{"fetch", "origin"}},
		{Dir: repo1Path, Args: []string{"reset", "--hard", "HEAD"}},
		{Dir: repo1Path, Args: []string{"clean", "-fd"}},
		{Dir: repo1Path, Args: []string{"reset", "--hard", "origin/develop"}},
		{Dir: repo1Path, Args: []string{"push", "origin", "feat", "--force"}},
		// repo2 -> global default
		{Dir: repo2Path, Args: []string{"fetch", "origin"}},
		{Dir: repo2Path, Args: []string{"reset", "--hard", "HEAD"}},
		{Dir: repo2Path, Args: []string{"clean", "-fd"}},
		{Dir: repo2Path, Args: []string{"reset", "--hard", "origin/" + defaultBranch}},
		{Dir: repo2Path, Args: []string{"push", "origin", "feat", "--force"}},
	})
	outputMock.Install()

	resetForce = true
	defer func() { resetForce = false }()

	resetAllWorktrees(defaultBranch, false)
}

func TestResetAllWorktrees_LegacyMode_NoPerRepoBranch(t *testing.T) {
	// In legacy mode (no workspace config), all repos use the same target branch
	// because WorktreeInfo.Repo is nil.
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ResetBeadsDirCache()

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(tmpDir)

	wt1 := filepath.Join(tmpDir, "worktrees", "alpha")
	wt2 := filepath.Join(tmpDir, "worktrees", "beta")
	createGitRepo(t, wt1)
	createGitRepo(t, wt2)

	mock := NewCommandMock(t, []CommandStub{
		// DiscoverWorktrees: GetCurrentBranch for each
		{Dir: wt1, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "main\n"},
		{Dir: wt2, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "main\n"},
		// resetWorktree for each
		{Dir: wt1, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "main\n"},
		{Dir: wt2, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "main\n"},
	})
	mock.Install()

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		// alpha -> main
		{Dir: wt1, Args: []string{"fetch", "origin"}},
		{Dir: wt1, Args: []string{"reset", "--hard", "HEAD"}},
		{Dir: wt1, Args: []string{"clean", "-fd"}},
		{Dir: wt1, Args: []string{"reset", "--hard", "origin/main"}},
		{Dir: wt1, Args: []string{"push", "origin", "main", "--force"}},
		// beta -> main
		{Dir: wt2, Args: []string{"fetch", "origin"}},
		{Dir: wt2, Args: []string{"reset", "--hard", "HEAD"}},
		{Dir: wt2, Args: []string{"clean", "-fd"}},
		{Dir: wt2, Args: []string{"reset", "--hard", "origin/main"}},
		{Dir: wt2, Args: []string{"push", "origin", "main", "--force"}},
	})
	outputMock.Install()

	resetForce = true
	defer func() { resetForce = false }()

	resetAllWorktrees("main", false)
}
