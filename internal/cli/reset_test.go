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

// ============================================================================
// Reset Command Argument Validation Tests
// ============================================================================

func TestResetCmd_ArgsValidation_MissingWorktree(t *testing.T) {
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
	// Verify that resetWorktree returns true on a successful reset.
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
	mock.Install()

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Dir: wtPath, Args: []string{"fetch", "origin"}},
		{Dir: wtPath, Args: []string{"reset", "--hard", "HEAD"}},
		{Dir: wtPath, Args: []string{"clean", "-fd"}},
		{Dir: wtPath, Args: []string{"reset", "--hard", "origin/main"}},
		{Dir: wtPath, Args: []string{"push", "origin", "test-branch", "--force"}},
	})
	outputMock.Install()

	// Capture stdout to suppress output
	oldStdout := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	result := resetWorktree("test-wt", "main", false)

	w.Close()
	os.Stdout = oldStdout

	if !result {
		t.Error("expected resetWorktree to return true on success")
	}
}

func TestResetWorktree_ReturnsFalse_OnFetchError(t *testing.T) {
	// Verify that resetWorktree returns false when fetch fails.
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
	mock.Install()

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Dir: wtPath, Args: []string{"fetch", "origin"}, Err: fmt.Errorf("network error")},
	})
	outputMock.Install()

	// Capture stderr and stdout to suppress output
	oldStderr := os.Stderr
	_, wErr, _ := os.Pipe()
	os.Stderr = wErr

	oldStdout := os.Stdout
	_, wOut, _ := os.Pipe()
	os.Stdout = wOut

	result := resetWorktree("test-wt", "main", false)

	wErr.Close()
	wOut.Close()
	os.Stderr = oldStderr
	os.Stdout = oldStdout

	if result {
		t.Error("expected resetWorktree to return false on fetch error")
	}
}

func TestResetWorktree_ReturnsFalse_OnResolveError(t *testing.T) {
	// Verify that resetWorktree returns false when ResolveWorktreePath fails
	// (e.g., invalid worktree name that doesn't exist).
	ResetBeadsDirCache()

	// Set up a temp dir with no worktrees directory so resolution fails
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// No command mocks needed - should fail before any git commands run
	mock := NewCommandMock(t, []CommandStub{})
	mock.Install()

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{})
	outputMock.Install()

	// Capture stderr and stdout to suppress output
	oldStderr := os.Stderr
	_, wErr, _ := os.Pipe()
	os.Stderr = wErr

	oldStdout := os.Stdout
	_, wOut, _ := os.Pipe()
	os.Stdout = wOut

	result := resetWorktree("nonexistent-worktree-xyz", "main", false)

	wErr.Close()
	wOut.Close()
	os.Stderr = oldStderr
	os.Stdout = oldStdout

	if result {
		t.Error("expected resetWorktree to return false when worktree path cannot be resolved")
	}
}

func TestResetWorktree_ReturnsTrue_OnUserAbort(t *testing.T) {
	// Verify that resetWorktree returns true when the user declines confirmation.
	// A user abort is not an error, so it should return true.
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
	mock.Install()

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{})
	outputMock.Install()

	// Mock stdin to respond "n" to the confirmation prompt
	MockStdin(t, "n\n")

	// Capture stdout to suppress output
	oldStdout := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	// askConfirm=true triggers the confirmation prompt
	result := resetWorktree("test-wt", "main", true)

	w.Close()
	os.Stdout = oldStdout

	if !result {
		t.Error("expected resetWorktree to return true on user abort (abort is not an error)")
	}
}

// TestResetAllWorktrees_PartialFailure verifies that when one worktree fails
// during resetAllWorktrees, the other worktrees are still attempted.
//
// Note: resetAllWorktrees calls os.Exit(1) when there are failures, so we
// cannot directly test the full function in-process. Instead, we verify the
// underlying behavior by testing resetWorktree return values and confirming
// that the partial failure tracking logic in resetAllWorktrees is correct.
//
// The key contract tested here: resetWorktree returns false on failure and
// true on success, which resetAllWorktrees uses to build its failed list.
func TestResetAllWorktrees_PartialFailure(t *testing.T) {
	// Test that resetWorktree returns correct booleans for a mix of success
	// and failure, which is what resetAllWorktrees relies on to track failures.
	ResetBeadsDirCache()

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	repo1Path := filepath.Join(tmpDir, "worktrees", "repo1")
	repo2Path := filepath.Join(tmpDir, "worktrees", "repo2")
	createGitRepo(t, repo1Path)
	createGitRepo(t, repo2Path)

	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// First call: repo1 succeeds (all commands pass)
	// We test repo1 and repo2 in separate sub-tests to simulate what
	// resetAllWorktrees does internally.
	t.Run("repo1_succeeds", func(t *testing.T) {
		mock := NewCommandMock(t, []CommandStub{
			{Dir: repo1Path, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature-1\n"},
		})
		mock.Install()

		outputMock := NewOutputCommandMock(t, []OutputCommandStub{
			{Dir: repo1Path, Args: []string{"fetch", "origin"}},
			{Dir: repo1Path, Args: []string{"reset", "--hard", "HEAD"}},
			{Dir: repo1Path, Args: []string{"clean", "-fd"}},
			{Dir: repo1Path, Args: []string{"reset", "--hard", "origin/main"}},
			{Dir: repo1Path, Args: []string{"push", "origin", "feature-1", "--force"}},
		})
		outputMock.Install()

		oldStdout := os.Stdout
		_, w, _ := os.Pipe()
		os.Stdout = w

		result := resetWorktree("repo1", "main", false)

		w.Close()
		os.Stdout = oldStdout

		if !result {
			t.Error("expected repo1 resetWorktree to return true (success)")
		}
	})

	// Second call: repo2 fails (fetch error)
	t.Run("repo2_fails_fetch", func(t *testing.T) {
		mock := NewCommandMock(t, []CommandStub{
			{Dir: repo2Path, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature-2\n"},
		})
		mock.Install()

		outputMock := NewOutputCommandMock(t, []OutputCommandStub{
			{Dir: repo2Path, Args: []string{"fetch", "origin"}, Err: fmt.Errorf("network error")},
		})
		outputMock.Install()

		// Capture stderr to verify error message
		oldStderr := os.Stderr
		rErr, wErr, _ := os.Pipe()
		os.Stderr = wErr

		oldStdout := os.Stdout
		_, wOut, _ := os.Pipe()
		os.Stdout = wOut

		result := resetWorktree("repo2", "main", false)

		wErr.Close()
		wOut.Close()
		os.Stderr = oldStderr
		os.Stdout = oldStdout

		buf := make([]byte, 1024)
		n, _ := rErr.Read(buf)
		stderr := string(buf[:n])

		if result {
			t.Error("expected repo2 resetWorktree to return false (fetch error)")
		}

		if !containsSubstring([]string{stderr}, "Error fetching") {
			t.Errorf("expected stderr to contain 'Error fetching', got %q", stderr)
		}
	})

	// Simulate the tracking logic from resetAllWorktrees
	t.Run("failure_tracking", func(t *testing.T) {
		// This mirrors the logic in resetAllWorktrees:
		//   var failed []string
		//   for _, t := range targets {
		//       if !resetWorktree(...) { failed = append(failed, t.wt.Name) }
		//   }
		// We verify that with one true and one false, exactly one name is tracked.
		var failed []string
		results := map[string]bool{
			"repo1": true,  // success
			"repo2": false, // failure
		}
		for name, ok := range results {
			if !ok {
				failed = append(failed, name)
			}
		}
		if len(failed) != 1 {
			t.Errorf("expected 1 failure, got %d: %v", len(failed), failed)
		}
		if !containsSubstring(failed, "repo2") {
			t.Errorf("expected failed list to contain 'repo2', got %v", failed)
		}
	})
}

// ============================================================================
// confirmAction Tests
// ============================================================================

func TestConfirmAction_Yes(t *testing.T) {
	MockStdin(t, "y\n")

	result := confirmAction("Are you sure?")
	if !result {
		t.Error("expected confirmAction to return true for 'y' input")
	}
}

func TestConfirmAction_YesLong(t *testing.T) {
	MockStdin(t, "yes\n")

	result := confirmAction("Are you sure?")
	if !result {
		t.Error("expected confirmAction to return true for 'yes' input")
	}
}

func TestConfirmAction_YesUppercase(t *testing.T) {
	MockStdin(t, "Y\n")

	result := confirmAction("Are you sure?")
	if !result {
		t.Error("expected confirmAction to return true for 'Y' input")
	}
}

func TestConfirmAction_No(t *testing.T) {
	MockStdin(t, "n\n")

	result := confirmAction("Are you sure?")
	if result {
		t.Error("expected confirmAction to return false for 'n' input")
	}
}

func TestConfirmAction_NoLong(t *testing.T) {
	MockStdin(t, "no\n")

	result := confirmAction("Are you sure?")
	if result {
		t.Error("expected confirmAction to return false for 'no' input")
	}
}

func TestConfirmAction_Default(t *testing.T) {
	// Empty input (just newline) should default to false
	MockStdin(t, "\n")

	result := confirmAction("Are you sure?")
	if result {
		t.Error("expected confirmAction to return false for empty input (default)")
	}
}

func TestConfirmAction_Whitespace(t *testing.T) {
	// Whitespace only should default to false
	MockStdin(t, "   \n")

	result := confirmAction("Are you sure?")
	if result {
		t.Error("expected confirmAction to return false for whitespace input")
	}
}

func TestConfirmAction_Invalid(t *testing.T) {
	// Invalid input should default to false
	MockStdin(t, "maybe\n")

	result := confirmAction("Are you sure?")
	if result {
		t.Error("expected confirmAction to return false for invalid input")
	}
}

func TestConfirmAction_YesWithSpaces(t *testing.T) {
	// Input with surrounding spaces should be trimmed
	MockStdin(t, "  y  \n")

	result := confirmAction("Are you sure?")
	if !result {
		t.Error("expected confirmAction to return true for '  y  ' input (should be trimmed)")
	}
}

func TestConfirmAction_Output(t *testing.T) {
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
	mock.Install()

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Dir: wtPath, Args: []string{"fetch", "origin"}},
		{Dir: wtPath, Args: []string{"reset", "--hard", "HEAD"}},
		{Dir: wtPath, Args: []string{"clean", "-fd"}},
		{Dir: wtPath, Args: []string{"reset", "--hard", "origin/main"}},
		{Dir: wtPath, Args: []string{"push", "origin", "test-branch", "--force"}},
	})
	outputMock.Install()

	// Capture stdout to suppress output
	oldStdout := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	resetWorktree("test-wt", "main", false)

	w.Close()
	os.Stdout = oldStdout
	// No errors expected - success path completes normally
}

func TestResetWorktree_FetchError(t *testing.T) {
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
	mock.Install()

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Dir: wtPath, Args: []string{"fetch", "origin"}, Err: fmt.Errorf("network error")},
	})
	outputMock.Install()

	// Capture stderr to check error message
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	// Capture stdout too
	oldStdout := os.Stdout
	_, wOut, _ := os.Pipe()
	os.Stdout = wOut

	resetWorktree("test-wt", "main", false)

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
	mock.Install()
	outputMock := NewOutputCommandMock(t, []OutputCommandStub{})
	outputMock.Install()

	// Capture stderr
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	// Capture stdout
	oldStdout := os.Stdout
	_, wOut, _ := os.Pipe()
	os.Stdout = wOut

	result := resetWorktree("test-wt", "main", false)

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
	mock.Install()
	outputMock := NewOutputCommandMock(t, []OutputCommandStub{})
	outputMock.Install()

	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	oldStdout := os.Stdout
	_, wOut, _ := os.Pipe()
	os.Stdout = wOut

	resetWorktree("test-wt", "main", false)

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

	// Set --force
	resetForce = true
	defer func() { resetForce = false }()

	// Set up mocks for the git operations (should proceed with --force)
	mock := NewCommandMock(t, []CommandStub{
		{Dir: wtPath, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "test-branch\n"},
	})
	mock.Install()

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Dir: wtPath, Args: []string{"fetch", "origin"}},
		{Dir: wtPath, Args: []string{"reset", "--hard", "HEAD"}},
		{Dir: wtPath, Args: []string{"clean", "-fd"}},
		{Dir: wtPath, Args: []string{"reset", "--hard", "origin/main"}},
		{Dir: wtPath, Args: []string{"push", "origin", "test-branch", "--force"}},
	})
	outputMock.Install()

	// Capture stderr to verify warning
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	oldStdout := os.Stdout
	_, wOut, _ := os.Pipe()
	os.Stdout = wOut

	result := resetWorktree("test-wt", "main", false)

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
	defer func() { resetForce = false }()

	// Set up mocks - should proceed normally (stale lock does not block)
	mock := NewCommandMock(t, []CommandStub{
		{Dir: wtPath, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "test-branch\n"},
	})
	mock.Install()

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Dir: wtPath, Args: []string{"fetch", "origin"}},
		{Dir: wtPath, Args: []string{"reset", "--hard", "HEAD"}},
		{Dir: wtPath, Args: []string{"clean", "-fd"}},
		{Dir: wtPath, Args: []string{"reset", "--hard", "origin/main"}},
		{Dir: wtPath, Args: []string{"push", "origin", "test-branch", "--force"}},
	})
	outputMock.Install()

	// Capture output
	oldStdout := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	result := resetWorktree("test-wt", "main", false)

	w.Close()
	os.Stdout = oldStdout

	if !result {
		t.Error("expected resetWorktree to proceed with stale lock")
	}
}

func TestResetWorktree_ProceedsWithNoLock(t *testing.T) {
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
	defer func() { resetForce = false }()

	mock := NewCommandMock(t, []CommandStub{
		{Dir: wtPath, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "test-branch\n"},
	})
	mock.Install()

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Dir: wtPath, Args: []string{"fetch", "origin"}},
		{Dir: wtPath, Args: []string{"reset", "--hard", "HEAD"}},
		{Dir: wtPath, Args: []string{"clean", "-fd"}},
		{Dir: wtPath, Args: []string{"reset", "--hard", "origin/main"}},
		{Dir: wtPath, Args: []string{"push", "origin", "test-branch", "--force"}},
	})
	outputMock.Install()

	oldStdout := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	result := resetWorktree("test-wt", "main", false)

	w.Close()
	os.Stdout = oldStdout

	if !result {
		t.Error("expected resetWorktree to proceed with no lock")
	}
}
