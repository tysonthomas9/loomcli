package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// ============================================================================
// Help Text Regression Tests
// ============================================================================

func TestResetCmd_HelpText_NoHardcodedBranch(t *testing.T) {
	t.Parallel()
	// Regression test: the Long help text should not contain hardcoded branch
	// names as the default. The actual default comes from GetDefaultBranch()
	// which dynamically detects the integration branch.
	longText := strings.ToLower(resetCmd.Long)

	forbidden := []string{
		"default: feature/",
		"default: main",
		"default: master",
		"default: develop",
	}

	for _, pattern := range forbidden {
		if strings.Contains(longText, pattern) {
			t.Errorf("reset command help text contains hardcoded branch name %q — use a dynamic description like 'integration branch' instead", pattern)
		}
	}
}

// ============================================================================
// resetAllWorktrees Per-Repo Branch Tests
// ============================================================================

func TestResetAllWorktrees_PerRepoBranch(t *testing.T) {
	// not parallel: uses global resetForce/resetPush, defaultResolver, mock.Install(), setupWorkspaceConfig
	// In workspace mode with per-repo DefaultBranch values, resetAllWorktrees
	// should use each repo's own DefaultBranch when explicitTarget=false.
	// Uses defaultResolver, DiscoverWorktrees (global deps), and resetForce/resetPush globals.
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

	// GitFetch, GitReset, GitClean all use runGitWithOutputFunc (no push without --push)
	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		// repo1: fetch, reset HEAD, clean, reset origin/develop
		{Dir: repo1Path, Args: []string{"fetch", "origin"}},
		{Dir: repo1Path, Args: []string{"reset", "--hard", "HEAD"}},
		{Dir: repo1Path, Args: []string{"clean", "-fd"}},
		{Dir: repo1Path, Args: []string{"reset", "--hard", "origin/develop"}},
		// repo2: fetch, reset HEAD, clean, reset origin/staging
		{Dir: repo2Path, Args: []string{"fetch", "origin"}},
		{Dir: repo2Path, Args: []string{"reset", "--hard", "HEAD"}},
		{Dir: repo2Path, Args: []string{"clean", "-fd"}},
		{Dir: repo2Path, Args: []string{"reset", "--hard", "origin/staging"}},
	})
	outputMock.Install()

	resetForce = true
	resetPush = false
	defer func() { resetForce = false; resetPush = false }()

	defaultBranch := GetDefaultBranch()
	err := resetAllWorktrees(defaultDeps, defaultBranch, false)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestResetAllWorktrees_ExplicitBranchOverridesPerRepo(t *testing.T) {
	// not parallel: uses global resetForce/resetPush, defaultResolver, mock.Install(), setupWorkspaceConfig
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

	// Both repos should reset to "release" (explicit target overrides per-repo, no push)
	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		// repo1
		{Dir: repo1Path, Args: []string{"fetch", "origin"}},
		{Dir: repo1Path, Args: []string{"reset", "--hard", "HEAD"}},
		{Dir: repo1Path, Args: []string{"clean", "-fd"}},
		{Dir: repo1Path, Args: []string{"reset", "--hard", "origin/release"}},
		// repo2
		{Dir: repo2Path, Args: []string{"fetch", "origin"}},
		{Dir: repo2Path, Args: []string{"reset", "--hard", "HEAD"}},
		{Dir: repo2Path, Args: []string{"clean", "-fd"}},
		{Dir: repo2Path, Args: []string{"reset", "--hard", "origin/release"}},
	})
	outputMock.Install()

	resetForce = true
	resetPush = false
	defer func() { resetForce = false; resetPush = false }()

	err := resetAllWorktrees(defaultDeps, "release", true)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestResetAllWorktrees_MixedDefaultBranch(t *testing.T) {
	// not parallel: uses global resetForce/resetPush, defaultResolver, mock.Install(), setupWorkspaceConfig
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

	// repo1 resets to "develop", repo2 resets to the global default (no push)
	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		// repo1 -> develop
		{Dir: repo1Path, Args: []string{"fetch", "origin"}},
		{Dir: repo1Path, Args: []string{"reset", "--hard", "HEAD"}},
		{Dir: repo1Path, Args: []string{"clean", "-fd"}},
		{Dir: repo1Path, Args: []string{"reset", "--hard", "origin/develop"}},
		// repo2 -> global default
		{Dir: repo2Path, Args: []string{"fetch", "origin"}},
		{Dir: repo2Path, Args: []string{"reset", "--hard", "HEAD"}},
		{Dir: repo2Path, Args: []string{"clean", "-fd"}},
		{Dir: repo2Path, Args: []string{"reset", "--hard", "origin/" + defaultBranch}},
	})
	outputMock.Install()

	resetForce = true
	resetPush = false
	defer func() { resetForce = false; resetPush = false }()

	err := resetAllWorktrees(defaultDeps, defaultBranch, false)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestResetAllWorktrees_LegacyMode_NoPerRepoBranch(t *testing.T) {
	// not parallel: uses t.Setenv, global resetForce/resetPush, defaultResolver, mock.Install(), os.Chdir
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
		// alpha -> main (no push)
		{Dir: wt1, Args: []string{"fetch", "origin"}},
		{Dir: wt1, Args: []string{"reset", "--hard", "HEAD"}},
		{Dir: wt1, Args: []string{"clean", "-fd"}},
		{Dir: wt1, Args: []string{"reset", "--hard", "origin/main"}},
		// beta -> main (no push)
		{Dir: wt2, Args: []string{"fetch", "origin"}},
		{Dir: wt2, Args: []string{"reset", "--hard", "HEAD"}},
		{Dir: wt2, Args: []string{"clean", "-fd"}},
		{Dir: wt2, Args: []string{"reset", "--hard", "origin/main"}},
	})
	outputMock.Install()

	resetForce = true
	resetPush = false
	defer func() { resetForce = false; resetPush = false }()

	err := resetAllWorktrees(defaultDeps, "main", false)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

// ============================================================================
// Reset Command Argument Validation Tests
// ============================================================================

func TestResetCmd_ArgsValidation_MissingWorktree(t *testing.T) {
	// not parallel: uses global resetAll
	// The reset command's Args function requires either --all flag or a worktree argument
	// Test that it returns an error when neither is provided
	resetAll = false
	defer func() { resetAll = false }()

	// Create a minimal cobra command to test Args validation
	cmd := &cobra.Command{}
	err := resetCmd.Args(cmd, []string{})
	if err == nil {
		t.Error("expected error when worktree argument is missing and --all not set")
	}
	if err.Error() != "requires worktree argument (or use --all)" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestResetCmd_ArgsValidation_AllWithTooManyArgs(t *testing.T) {
	// not parallel: uses global resetAll
	// When --all is set, at most 1 argument (target branch) is allowed
	resetAll = true
	defer func() { resetAll = false }()

	cmd := &cobra.Command{}
	// Two args with --all should fail
	err := resetCmd.Args(cmd, []string{"branch1", "branch2"})
	if err == nil {
		t.Error("expected error when --all is set with more than 1 argument")
	}
	if err.Error() != "--all flag accepts at most 1 argument (target branch)" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestResetCmd_ArgsValidation_AllWithOneArg(t *testing.T) {
	// not parallel: uses global resetAll
	// --all with one arg (target branch) should be valid
	resetAll = true
	defer func() { resetAll = false }()

	cmd := &cobra.Command{}
	err := resetCmd.Args(cmd, []string{"main"})
	if err != nil {
		t.Errorf("--all with one arg should be valid, got error: %v", err)
	}
}

func TestResetCmd_ArgsValidation_AllWithNoArgs(t *testing.T) {
	// not parallel: uses global resetAll
	// --all with no args should be valid (uses default branch)
	resetAll = true
	defer func() { resetAll = false }()

	cmd := &cobra.Command{}
	err := resetCmd.Args(cmd, []string{})
	if err != nil {
		t.Errorf("--all with no args should be valid, got error: %v", err)
	}
}

func TestResetCmd_ArgsValidation_WorktreeProvided(t *testing.T) {
	// not parallel: uses global resetAll
	// When worktree is provided and --all is not set, should be valid
	resetAll = false
	defer func() { resetAll = false }()

	cmd := &cobra.Command{}
	err := resetCmd.Args(cmd, []string{"falcon"})
	if err != nil {
		t.Errorf("worktree arg without --all should be valid, got error: %v", err)
	}
}

func TestResetCmd_ArgsValidation_WorktreeAndBranch(t *testing.T) {
	// not parallel: uses global resetAll
	// When worktree and branch are provided (no --all), should be valid
	resetAll = false
	defer func() { resetAll = false }()

	cmd := &cobra.Command{}
	err := resetCmd.Args(cmd, []string{"falcon", "main"})
	if err != nil {
		t.Errorf("worktree and branch args should be valid, got error: %v", err)
	}
}

// ============================================================================
// resetWorktree Return Value Tests
// ============================================================================

func TestResetWorktree_ReturnsTrue_OnSuccess(t *testing.T) {
	// not parallel: uses os.Chdir, os.Stdout capture, global resetPush
	// Verify that resetWorktree returns true on a successful reset (local only, no push).
	deps, _, _, _, _ := NewTestDeps(t)
	ResetBeadsDirCache()

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	wtPath := filepath.Join(tmpDir, "worktrees", "test-wt")
	createGitRepo(t, wtPath)

	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	mock := NewCommandMock(t, []CommandStub{
		{Dir: wtPath, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "test-branch\n"},
	})
	mock.InstallOn(deps)

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Dir: wtPath, Args: []string{"fetch", "origin"}},
		{Dir: wtPath, Args: []string{"reset", "--hard", "HEAD"}},
		{Dir: wtPath, Args: []string{"clean", "-fd"}},
		{Dir: wtPath, Args: []string{"reset", "--hard", "origin/main"}},
	})
	outputMock.InstallOn(deps)

	resetPush = false
	defer func() { resetPush = false }()

	// Capture stdout to suppress output
	oldStdout := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	result := resetWorktree(deps, "test-wt", "main", false)

	w.Close()
	os.Stdout = oldStdout

	if !result {
		t.Error("expected resetWorktree to return true on success")
	}
}

func TestResetWorktree_ReturnsFalse_OnFetchError(t *testing.T) {
	// not parallel: uses os.Chdir, os.Stdout/os.Stderr capture
	// Verify that resetWorktree returns false when fetch fails.
	deps, _, _, _, _ := NewTestDeps(t)
	ResetBeadsDirCache()

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	wtPath := filepath.Join(tmpDir, "worktrees", "test-wt")
	createGitRepo(t, wtPath)

	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	mock := NewCommandMock(t, []CommandStub{
		{Dir: wtPath, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "test-branch\n"},
	})
	mock.InstallOn(deps)

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Dir: wtPath, Args: []string{"fetch", "origin"}, Err: fmt.Errorf("network error")},
	})
	outputMock.InstallOn(deps)

	// Capture stderr and stdout to suppress output
	oldStderr := os.Stderr
	_, wErr, _ := os.Pipe()
	os.Stderr = wErr

	oldStdout := os.Stdout
	_, wOut, _ := os.Pipe()
	os.Stdout = wOut

	result := resetWorktree(deps, "test-wt", "main", false)

	wErr.Close()
	wOut.Close()
	os.Stderr = oldStderr
	os.Stdout = oldStdout

	if result {
		t.Error("expected resetWorktree to return false on fetch error")
	}
}

func TestResetWorktree_ReturnsFalse_OnResolveError(t *testing.T) {
	// not parallel: uses os.Chdir, os.Stdout/os.Stderr capture
	// (e.g., invalid worktree name that doesn't exist).
	deps, _, _, _, _ := NewTestDeps(t)
	ResetBeadsDirCache()

	// Set up a temp dir with no worktrees directory so resolution fails
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// No command mocks needed - should fail before any git commands run
	mock := NewCommandMock(t, []CommandStub{})
	mock.InstallOn(deps)

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{})
	outputMock.InstallOn(deps)

	// Capture stderr and stdout to suppress output
	oldStderr := os.Stderr
	_, wErr, _ := os.Pipe()
	os.Stderr = wErr

	oldStdout := os.Stdout
	_, wOut, _ := os.Pipe()
	os.Stdout = wOut

	result := resetWorktree(deps, "nonexistent-worktree-xyz", "main", false)

	wErr.Close()
	wOut.Close()
	os.Stderr = oldStderr
	os.Stdout = oldStdout

	if result {
		t.Error("expected resetWorktree to return false when worktree path cannot be resolved")
	}
}

func TestResetWorktree_ReturnsTrue_OnUserAbort(t *testing.T) {
	// not parallel: uses os.Chdir, os.Stdout capture, MockStdin
	// A user abort is not an error, so it should return true.
	deps, _, _, _, _ := NewTestDeps(t)
	ResetBeadsDirCache()

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	wtPath := filepath.Join(tmpDir, "worktrees", "test-wt")
	createGitRepo(t, wtPath)

	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// No git commands should be called since the user aborts before any git ops
	mock := NewCommandMock(t, []CommandStub{})
	mock.InstallOn(deps)

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{})
	outputMock.InstallOn(deps)

	// Mock stdin to respond "n" to the confirmation prompt
	MockStdin(t, "n\n")

	// Capture stdout to suppress output
	oldStdout := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	// askConfirm=true triggers the confirmation prompt
	result := resetWorktree(deps, "test-wt", "main", true)

	w.Close()
	os.Stdout = oldStdout

	if !result {
		t.Error("expected resetWorktree to return true on user abort (abort is not an error)")
	}
}

// TestResetAllWorktrees_PartialFailure_ReturnsError verifies that when one
// worktree fails during resetAllWorktrees, the function returns an error
// and the failure summary is printed to stderr.
func TestResetAllWorktrees_PartialFailure_ReturnsError(t *testing.T) {
	// not parallel: uses global resetForce/resetPush, defaultResolver, mock.Install(), setupWorkspaceConfig, os.Stdout/os.Stderr capture
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
					{Name: "repo1", Path: repo1Path, DefaultBranch: "main"},
					{Name: "repo2", Path: repo2Path, DefaultBranch: "main"},
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
		{Dir: repo1Path, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature-1\n"},
		{Dir: repo2Path, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature-2\n"},
		// resetWorktree(repo1): GetCurrentBranch - succeeds
		{Dir: repo1Path, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature-1\n"},
		// resetWorktree(repo2): GetCurrentBranch - succeeds (fetch will fail)
		{Dir: repo2Path, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature-2\n"},
	})
	mock.Install()

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		// repo1: full success sequence (no push)
		{Dir: repo1Path, Args: []string{"fetch", "origin"}},
		{Dir: repo1Path, Args: []string{"reset", "--hard", "HEAD"}},
		{Dir: repo1Path, Args: []string{"clean", "-fd"}},
		{Dir: repo1Path, Args: []string{"reset", "--hard", "origin/main"}},
		// repo2: fetch fails
		{Dir: repo2Path, Args: []string{"fetch", "origin"}, Err: fmt.Errorf("network error")},
	})
	outputMock.Install()

	resetForce = true
	resetPush = false
	defer func() { resetForce = false; resetPush = false }()

	// Capture stderr to verify failure summary
	oldStderr := os.Stderr
	rErr, wErr, _ := os.Pipe()
	os.Stderr = wErr

	oldStdout := os.Stdout
	_, wOut, _ := os.Pipe()
	os.Stdout = wOut

	err := resetAllWorktrees(defaultDeps, "main", false)

	wErr.Close()
	wOut.Close()
	os.Stderr = oldStderr
	os.Stdout = oldStdout

	if err == nil {
		t.Fatal("expected error when one worktree fails, got nil")
	}
	if !strings.Contains(err.Error(), "1 worktree(s)") {
		t.Errorf("expected error to mention 1 failed worktree, got: %v", err)
	}

	buf := make([]byte, 4096)
	n, _ := rErr.Read(buf)
	stderr := string(buf[:n])

	if !strings.Contains(stderr, "Failed to reset 1 worktree(s)") {
		t.Errorf("expected stderr to contain failure summary, got: %q", stderr)
	}
}

// TestResetAllWorktrees_AllFail_ReturnsError verifies that when all worktrees
// fail, the error includes all worktree names.
func TestResetAllWorktrees_AllFail_ReturnsError(t *testing.T) {
	// not parallel: uses global resetForce/resetPush, defaultResolver, mock.Install(), setupWorkspaceConfig, os.Stdout/os.Stderr capture
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
					{Name: "repo1", Path: repo1Path, DefaultBranch: "main"},
					{Name: "repo2", Path: repo2Path, DefaultBranch: "main"},
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
		{Dir: repo1Path, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature-1\n"},
		{Dir: repo2Path, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature-2\n"},
		// resetWorktree(repo1): GetCurrentBranch
		{Dir: repo1Path, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature-1\n"},
		// resetWorktree(repo2): GetCurrentBranch
		{Dir: repo2Path, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature-2\n"},
	})
	mock.Install()

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		// repo1: fetch fails
		{Dir: repo1Path, Args: []string{"fetch", "origin"}, Err: fmt.Errorf("network error")},
		// repo2: fetch fails
		{Dir: repo2Path, Args: []string{"fetch", "origin"}, Err: fmt.Errorf("network error")},
	})
	outputMock.Install()

	resetForce = true
	resetPush = false
	defer func() { resetForce = false; resetPush = false }()

	// Capture stderr
	oldStderr := os.Stderr
	rErr, wErr, _ := os.Pipe()
	os.Stderr = wErr

	oldStdout := os.Stdout
	_, wOut, _ := os.Pipe()
	os.Stdout = wOut

	err := resetAllWorktrees(defaultDeps, "main", false)

	wErr.Close()
	wOut.Close()
	os.Stderr = oldStderr
	os.Stdout = oldStdout

	if err == nil {
		t.Fatal("expected error when all worktrees fail, got nil")
	}
	if !strings.Contains(err.Error(), "2 worktree(s)") {
		t.Errorf("expected error to mention 2 failed worktrees, got: %v", err)
	}

	buf := make([]byte, 4096)
	n, _ := rErr.Read(buf)
	stderr := string(buf[:n])

	if !strings.Contains(stderr, "Failed to reset 2 worktree(s)") {
		t.Errorf("expected stderr to contain failure summary, got: %q", stderr)
	}
}

// ============================================================================
// confirmAction Tests
// ============================================================================

func TestConfirmAction_Yes(t *testing.T) {
	// not parallel: uses MockStdin
	MockStdin(t, "y\n")

	result := confirmAction("Are you sure?")
	if !result {
		t.Error("expected confirmAction to return true for 'y' input")
	}
}

func TestConfirmAction_YesLong(t *testing.T) {
	// not parallel: uses MockStdin
	MockStdin(t, "yes\n")

	result := confirmAction("Are you sure?")
	if !result {
		t.Error("expected confirmAction to return true for 'yes' input")
	}
}

func TestConfirmAction_YesUppercase(t *testing.T) {
	// not parallel: uses MockStdin
	MockStdin(t, "Y\n")

	result := confirmAction("Are you sure?")
	if !result {
		t.Error("expected confirmAction to return true for 'Y' input")
	}
}

func TestConfirmAction_No(t *testing.T) {
	// not parallel: uses MockStdin
	MockStdin(t, "n\n")

	result := confirmAction("Are you sure?")
	if result {
		t.Error("expected confirmAction to return false for 'n' input")
	}
}

func TestConfirmAction_NoLong(t *testing.T) {
	// not parallel: uses MockStdin
	MockStdin(t, "no\n")

	result := confirmAction("Are you sure?")
	if result {
		t.Error("expected confirmAction to return false for 'no' input")
	}
}

func TestConfirmAction_Default(t *testing.T) {
	// not parallel: uses MockStdin
	// Empty input (just newline) should default to false
	MockStdin(t, "\n")

	result := confirmAction("Are you sure?")
	if result {
		t.Error("expected confirmAction to return false for empty input (default)")
	}
}

func TestConfirmAction_Whitespace(t *testing.T) {
	// not parallel: uses MockStdin
	// Whitespace only should default to false
	MockStdin(t, "   \n")

	result := confirmAction("Are you sure?")
	if result {
		t.Error("expected confirmAction to return false for whitespace input")
	}
}

func TestConfirmAction_Invalid(t *testing.T) {
	// not parallel: uses MockStdin
	// Invalid input should default to false
	MockStdin(t, "maybe\n")

	result := confirmAction("Are you sure?")
	if result {
		t.Error("expected confirmAction to return false for invalid input")
	}
}

func TestConfirmAction_YesWithSpaces(t *testing.T) {
	// not parallel: uses MockStdin
	// Input with surrounding spaces should be trimmed
	MockStdin(t, "  y  \n")

	result := confirmAction("Are you sure?")
	if !result {
		t.Error("expected confirmAction to return true for '  y  ' input (should be trimmed)")
	}
}

func TestConfirmAction_Output(t *testing.T) {
	// not parallel: uses MockStdin, os.Stdout capture
	// Test that the prompt is displayed correctly
	// This is a basic test - confirmAction writes to stdout
	MockStdin(t, "n\n")

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	confirmAction("Test prompt?")

	w.Close()
	os.Stdout = oldStdout

	// Read output
	buf := make([]byte, 256)
	n, _ := r.Read(buf)
	output := string(buf[:n])

	if output != "Test prompt? (y/N) " {
		t.Errorf("expected 'Test prompt? (y/N) ', got %q", output)
	}
}

// ============================================================================
// resetWorktree Tests
// ============================================================================

func TestResetWorktree_Success(t *testing.T) {
	// not parallel: uses os.Chdir, os.Stdout capture, global resetPush
	deps, _, _, _, _ := NewTestDeps(t)
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	wtPath := filepath.Join(tmpDir, "worktrees", "test-wt")
	createGitRepo(t, wtPath)

	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	mock := NewCommandMock(t, []CommandStub{
		{Dir: wtPath, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "test-branch\n"},
	})
	mock.InstallOn(deps)

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Dir: wtPath, Args: []string{"fetch", "origin"}},
		{Dir: wtPath, Args: []string{"reset", "--hard", "HEAD"}},
		{Dir: wtPath, Args: []string{"clean", "-fd"}},
		{Dir: wtPath, Args: []string{"reset", "--hard", "origin/main"}},
	})
	outputMock.InstallOn(deps)

	resetPush = false
	defer func() { resetPush = false }()

	// Capture stdout to suppress output
	oldStdout := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	resetWorktree(deps, "test-wt", "main", false)

	w.Close()
	os.Stdout = oldStdout
	// No errors expected - success path completes normally
}

func TestResetWorktree_FetchError(t *testing.T) {
	// not parallel: uses os.Chdir, os.Stdout/os.Stderr capture
	deps, _, _, _, _ := NewTestDeps(t)
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	wtPath := filepath.Join(tmpDir, "worktrees", "test-wt")
	createGitRepo(t, wtPath)

	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	mock := NewCommandMock(t, []CommandStub{
		{Dir: wtPath, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "test-branch\n"},
	})
	mock.InstallOn(deps)

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Dir: wtPath, Args: []string{"fetch", "origin"}, Err: fmt.Errorf("network error")},
	})
	outputMock.InstallOn(deps)

	// Capture stderr to check error message
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	// Capture stdout too
	oldStdout := os.Stdout
	_, wOut, _ := os.Pipe()
	os.Stdout = wOut

	resetWorktree(deps, "test-wt", "main", false)

	w.Close()
	wOut.Close()
	os.Stderr = oldStderr
	os.Stdout = oldStdout

	buf := make([]byte, 1024)
	n, _ := r.Read(buf)
	stderr := string(buf[:n])

	if stderr == "" || !containsSubstring([]string{stderr}, "Error fetching") {
		t.Errorf("expected 'Error fetching' in stderr, got %q", stderr)
	}
}

// ============================================================================
// resetWorktree Lock Safety Tests
// ============================================================================

func TestResetWorktree_RefusesWithActiveLock(t *testing.T) {
	// not parallel: uses os.Chdir, os.Stdout/os.Stderr capture, global resetForce
	deps, _, _, _, _ := NewTestDeps(t)
	ResetBeadsDirCache()

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	wtPath := filepath.Join(tmpDir, "worktrees", "test-wt")
	createGitRepo(t, wtPath)

	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Create an active lock (current process PID so IsProcessRunning returns true)
	err := AcquireLock(wtPath, "task", "falcon")
	if err != nil {
		t.Fatalf("failed to acquire lock: %v", err)
	}
	defer ReleaseLock(wtPath)

	// Ensure --force is NOT set
	resetForce = false
	defer func() { resetForce = false }()

	// No git commands should be called - reset should abort before any git ops
	mock := NewCommandMock(t, []CommandStub{})
	mock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, []OutputCommandStub{})
	outputMock.InstallOn(deps)

	// Capture stderr
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	// Capture stdout
	oldStdout := os.Stdout
	_, wOut, _ := os.Pipe()
	os.Stdout = wOut

	result := resetWorktree(deps, "test-wt", "main", false)

	w.Close()
	wOut.Close()
	os.Stderr = oldStderr
	os.Stdout = oldStdout

	buf := make([]byte, 2048)
	n, _ := r.Read(buf)
	stderr := string(buf[:n])

	if result {
		t.Error("expected resetWorktree to return false when lock is active")
	}
	if !strings.Contains(stderr, "is actively working") {
		t.Errorf("expected 'is actively working' warning, got: %q", stderr)
	}
	if !strings.Contains(stderr, "Use --force") {
		t.Errorf("expected 'Use --force' hint, got: %q", stderr)
	}
}

func TestResetWorktree_RefusesWithActiveLock_ShowsTaskID(t *testing.T) {
	// not parallel: uses os.Chdir, os.Stdout/os.Stderr capture, global resetForce
	deps, _, _, _, _ := NewTestDeps(t)
	ResetBeadsDirCache()

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	wtPath := filepath.Join(tmpDir, "worktrees", "test-wt")
	createGitRepo(t, wtPath)

	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Create a lock with task ID
	err := AcquireLock(wtPath, "task", "falcon")
	if err != nil {
		t.Fatalf("failed to acquire lock: %v", err)
	}
	defer ReleaseLock(wtPath)
	UpdateLockTask(wtPath, "loomcli-abc", "Fix the bug")

	resetForce = false
	defer func() { resetForce = false }()

	mock := NewCommandMock(t, []CommandStub{})
	mock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, []OutputCommandStub{})
	outputMock.InstallOn(deps)

	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	oldStdout := os.Stdout
	_, wOut, _ := os.Pipe()
	os.Stdout = wOut

	resetWorktree(deps, "test-wt", "main", false)

	w.Close()
	wOut.Close()
	os.Stderr = oldStderr
	os.Stdout = oldStdout

	buf := make([]byte, 2048)
	n, _ := r.Read(buf)
	stderr := string(buf[:n])

	if !strings.Contains(stderr, "on task loomcli-abc") {
		t.Errorf("expected task ID in warning, got: %q", stderr)
	}
}

func TestResetWorktree_ForceOverridesLock(t *testing.T) {
	// not parallel: uses os.Chdir, os.Stdout/os.Stderr capture, global resetForce/resetPush
	deps, _, _, _, _ := NewTestDeps(t)
	ResetBeadsDirCache()

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	wtPath := filepath.Join(tmpDir, "worktrees", "test-wt")
	createGitRepo(t, wtPath)

	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Create an active lock
	err := AcquireLock(wtPath, "task", "falcon")
	if err != nil {
		t.Fatalf("failed to acquire lock: %v", err)
	}
	defer ReleaseLock(wtPath)

	// Set --force (no --push, so no force-push expected)
	resetForce = true
	resetPush = false
	defer func() { resetForce = false; resetPush = false }()

	// Set up mocks for the git operations (should proceed with --force, no push)
	mock := NewCommandMock(t, []CommandStub{
		{Dir: wtPath, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "test-branch\n"},
	})
	mock.InstallOn(deps)

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Dir: wtPath, Args: []string{"fetch", "origin"}},
		{Dir: wtPath, Args: []string{"reset", "--hard", "HEAD"}},
		{Dir: wtPath, Args: []string{"clean", "-fd"}},
		{Dir: wtPath, Args: []string{"reset", "--hard", "origin/main"}},
	})
	outputMock.InstallOn(deps)

	// Capture stderr to verify warning
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	oldStdout := os.Stdout
	_, wOut, _ := os.Pipe()
	os.Stdout = wOut

	result := resetWorktree(deps, "test-wt", "main", false)

	w.Close()
	wOut.Close()
	os.Stderr = oldStderr
	os.Stdout = oldStdout

	buf := make([]byte, 2048)
	n, _ := r.Read(buf)
	stderr := string(buf[:n])

	if !result {
		t.Error("expected resetWorktree to return true with --force despite active lock")
	}
	if !strings.Contains(stderr, "Proceeding with --force") {
		t.Errorf("expected 'Proceeding with --force' message, got: %q", stderr)
	}
}

func TestResetWorktree_ProceedsWithStaleLock(t *testing.T) {
	// not parallel: uses os.Chdir, os.Stdout capture, global resetForce/resetPush
	deps, _, _, _, _ := NewTestDeps(t)
	ResetBeadsDirCache()

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	wtPath := filepath.Join(tmpDir, "worktrees", "test-wt")
	createGitRepo(t, wtPath)

	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Create a stale lock (non-existent PID)
	staleLock := LockInfo{
		PID:       999999999,
		Command:   "task",
		AgentName: "dead-agent",
		StartedAt: time.Now().Add(-1 * time.Hour),
	}
	data, _ := json.Marshal(staleLock)
	lockPath := filepath.Join(wtPath, LockFileName)
	if err := os.WriteFile(lockPath, data, 0644); err != nil {
		t.Fatalf("failed to write stale lock: %v", err)
	}

	resetForce = false
	resetPush = false
	defer func() { resetForce = false; resetPush = false }()

	// Set up mocks - should proceed normally (stale lock does not block, no push)
	mock := NewCommandMock(t, []CommandStub{
		{Dir: wtPath, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "test-branch\n"},
	})
	mock.InstallOn(deps)

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Dir: wtPath, Args: []string{"fetch", "origin"}},
		{Dir: wtPath, Args: []string{"reset", "--hard", "HEAD"}},
		{Dir: wtPath, Args: []string{"clean", "-fd"}},
		{Dir: wtPath, Args: []string{"reset", "--hard", "origin/main"}},
	})
	outputMock.InstallOn(deps)

	// Capture output
	oldStdout := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	result := resetWorktree(deps, "test-wt", "main", false)

	w.Close()
	os.Stdout = oldStdout

	if !result {
		t.Error("expected resetWorktree to proceed with stale lock")
	}
}

func TestResetWorktree_ProceedsWithNoLock(t *testing.T) {
	// not parallel: uses os.Chdir, os.Stdout capture, global resetForce/resetPush
	deps, _, _, _, _ := NewTestDeps(t)
	ResetBeadsDirCache()

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	wtPath := filepath.Join(tmpDir, "worktrees", "test-wt")
	createGitRepo(t, wtPath)

	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// No lock file created

	resetForce = false
	resetPush = false
	defer func() { resetForce = false; resetPush = false }()

	mock := NewCommandMock(t, []CommandStub{
		{Dir: wtPath, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "test-branch\n"},
	})
	mock.InstallOn(deps)

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Dir: wtPath, Args: []string{"fetch", "origin"}},
		{Dir: wtPath, Args: []string{"reset", "--hard", "HEAD"}},
		{Dir: wtPath, Args: []string{"clean", "-fd"}},
		{Dir: wtPath, Args: []string{"reset", "--hard", "origin/main"}},
	})
	outputMock.InstallOn(deps)

	oldStdout := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	result := resetWorktree(deps, "test-wt", "main", false)

	w.Close()
	os.Stdout = oldStdout

	if !result {
		t.Error("expected resetWorktree to proceed with no lock")
	}
}

// ============================================================================
// isProtectedBranch Tests
// ============================================================================

func TestIsProtectedBranch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		branch string
		want   bool
	}{
		{"main", true},
		{"master", true},
		{"feature-x", false},
		{"main-feature", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("branch=%q", tt.branch), func(t *testing.T) {
			got := isProtectedBranch(tt.branch)
			if got != tt.want {
				t.Errorf("isProtectedBranch(%q) = %v, want %v", tt.branch, got, tt.want)
			}
		})
	}
}

// ============================================================================
// resetWorktree Protected Branch Tests
// ============================================================================

func TestResetWorktree_ProtectedBranch_Blocked(t *testing.T) {
	// not parallel: uses os.Chdir, os.Stdout/os.Stderr capture, global resetForce/resetPush
	// When current branch is "main", --push is set but --force is not,
	// resetWorktree should return false and NOT call GitPushForce.
	deps, _, _, _, _ := NewTestDeps(t)
	ResetBeadsDirCache()

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	wtPath := filepath.Join(tmpDir, "worktrees", "test-wt")
	createGitRepo(t, wtPath)

	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	resetForce = false
	resetPush = true
	defer func() { resetForce = false; resetPush = false }()

	mock := NewCommandMock(t, []CommandStub{
		{Dir: wtPath, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "main\n"},
	})
	mock.InstallOn(deps)

	// No push step - should be blocked before GitPushForce
	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Dir: wtPath, Args: []string{"fetch", "origin"}},
		{Dir: wtPath, Args: []string{"reset", "--hard", "HEAD"}},
		{Dir: wtPath, Args: []string{"clean", "-fd"}},
		{Dir: wtPath, Args: []string{"reset", "--hard", "origin/main"}},
	})
	outputMock.InstallOn(deps)

	// Capture stderr
	oldStderr := os.Stderr
	rErr, wErr, _ := os.Pipe()
	os.Stderr = wErr

	// Capture stdout
	oldStdout := os.Stdout
	_, wOut, _ := os.Pipe()
	os.Stdout = wOut

	result := resetWorktree(deps, "test-wt", "main", false)

	wErr.Close()
	wOut.Close()
	os.Stderr = oldStderr
	os.Stdout = oldStdout

	buf := make([]byte, 2048)
	n, _ := rErr.Read(buf)
	stderr := string(buf[:n])

	if result {
		t.Error("expected resetWorktree to return false when force-pushing to protected branch 'main'")
	}
	if !strings.Contains(stderr, "refusing to force-push to protected branch 'main'") {
		t.Errorf("expected stderr to contain refusing message, got: %q", stderr)
	}
}

func TestResetWorktree_ProtectedBranch_Master_Blocked(t *testing.T) {
	// not parallel: uses os.Chdir, os.Stdout/os.Stderr capture, global resetForce/resetPush
	// Same as above but with "master" as current branch.
	deps, _, _, _, _ := NewTestDeps(t)
	ResetBeadsDirCache()

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	wtPath := filepath.Join(tmpDir, "worktrees", "test-wt")
	createGitRepo(t, wtPath)

	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	resetForce = false
	resetPush = true
	defer func() { resetForce = false; resetPush = false }()

	mock := NewCommandMock(t, []CommandStub{
		{Dir: wtPath, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "master\n"},
	})
	mock.InstallOn(deps)

	// No push step - should be blocked before GitPushForce
	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Dir: wtPath, Args: []string{"fetch", "origin"}},
		{Dir: wtPath, Args: []string{"reset", "--hard", "HEAD"}},
		{Dir: wtPath, Args: []string{"clean", "-fd"}},
		{Dir: wtPath, Args: []string{"reset", "--hard", "origin/main"}},
	})
	outputMock.InstallOn(deps)

	// Capture stderr
	oldStderr := os.Stderr
	rErr, wErr, _ := os.Pipe()
	os.Stderr = wErr

	// Capture stdout
	oldStdout := os.Stdout
	_, wOut, _ := os.Pipe()
	os.Stdout = wOut

	result := resetWorktree(deps, "test-wt", "main", false)

	wErr.Close()
	wOut.Close()
	os.Stderr = oldStderr
	os.Stdout = oldStdout

	buf := make([]byte, 2048)
	n, _ := rErr.Read(buf)
	stderr := string(buf[:n])

	if result {
		t.Error("expected resetWorktree to return false when force-pushing to protected branch 'master'")
	}
	if !strings.Contains(stderr, "refusing to force-push to protected branch 'master'") {
		t.Errorf("expected stderr to contain refusing message, got: %q", stderr)
	}
}

func TestResetWorktree_ProtectedBranch_ForceOverride(t *testing.T) {
	// not parallel: uses os.Chdir, os.Stdout/os.Stderr capture, global resetForce/resetPush
	// When current branch is "main" AND both --push and --force are set,
	// force push should proceed with a warning.
	deps, _, _, _, _ := NewTestDeps(t)
	ResetBeadsDirCache()

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	wtPath := filepath.Join(tmpDir, "worktrees", "test-wt")
	createGitRepo(t, wtPath)

	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	resetForce = true
	resetPush = true
	defer func() { resetForce = false; resetPush = false }()

	mock := NewCommandMock(t, []CommandStub{
		{Dir: wtPath, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "main\n"},
	})
	mock.InstallOn(deps)

	// Full command sequence including push (should proceed)
	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Dir: wtPath, Args: []string{"fetch", "origin"}},
		{Dir: wtPath, Args: []string{"reset", "--hard", "HEAD"}},
		{Dir: wtPath, Args: []string{"clean", "-fd"}},
		{Dir: wtPath, Args: []string{"reset", "--hard", "origin/main"}},
		{Dir: wtPath, Args: []string{"push", "origin", "main", "--force"}},
	})
	outputMock.InstallOn(deps)

	// Capture stderr to verify warning
	oldStderr := os.Stderr
	rErr, wErr, _ := os.Pipe()
	os.Stderr = wErr

	// Capture stdout
	oldStdout := os.Stdout
	_, wOut, _ := os.Pipe()
	os.Stdout = wOut

	result := resetWorktree(deps, "test-wt", "main", false)

	wErr.Close()
	wOut.Close()
	os.Stderr = oldStderr
	os.Stdout = oldStdout

	buf := make([]byte, 2048)
	n, _ := rErr.Read(buf)
	stderr := string(buf[:n])

	if !result {
		t.Error("expected resetWorktree to return true when --force overrides protected branch guard")
	}
	if !strings.Contains(stderr, "force-pushing to protected branch 'main'") {
		t.Errorf("expected warning about force-pushing to protected branch, got: %q", stderr)
	}
}

func TestResetWorktree_NonProtectedBranch_Allowed(t *testing.T) {
	// not parallel: uses os.Chdir, os.Stdout/os.Stderr capture, global resetForce/resetPush
	// A non-protected branch like "feature-x" should push without warnings
	// or blocks when --push is set (even without --force).
	deps, _, _, _, _ := NewTestDeps(t)
	ResetBeadsDirCache()

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	wtPath := filepath.Join(tmpDir, "worktrees", "test-wt")
	createGitRepo(t, wtPath)

	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	resetForce = false
	resetPush = true
	defer func() { resetForce = false; resetPush = false }()

	mock := NewCommandMock(t, []CommandStub{
		{Dir: wtPath, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature-x\n"},
	})
	mock.InstallOn(deps)

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Dir: wtPath, Args: []string{"fetch", "origin"}},
		{Dir: wtPath, Args: []string{"reset", "--hard", "HEAD"}},
		{Dir: wtPath, Args: []string{"clean", "-fd"}},
		{Dir: wtPath, Args: []string{"reset", "--hard", "origin/main"}},
		{Dir: wtPath, Args: []string{"push", "origin", "feature-x", "--force"}},
	})
	outputMock.InstallOn(deps)

	// Capture stderr to verify no protected branch warning
	oldStderr := os.Stderr
	rErr, wErr, _ := os.Pipe()
	os.Stderr = wErr

	// Capture stdout
	oldStdout := os.Stdout
	_, wOut, _ := os.Pipe()
	os.Stdout = wOut

	result := resetWorktree(deps, "test-wt", "main", false)

	wErr.Close()
	wOut.Close()
	os.Stderr = oldStderr
	os.Stdout = oldStdout

	buf := make([]byte, 2048)
	n, _ := rErr.Read(buf)
	stderr := string(buf[:n])

	if !result {
		t.Error("expected resetWorktree to return true for non-protected branch")
	}
	if strings.Contains(stderr, "protected branch") {
		t.Errorf("expected no protected branch warning for 'feature-x', got: %q", stderr)
	}
}
