package cli

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestMergeCmd_ArgsValidation(t *testing.T) {
	// Save and restore the global flag
	origMergeAll := mergeAll
	defer func() { mergeAll = origMergeAll }()

	tests := []struct {
		name      string
		args      []string
		allFlag   bool
		wantError bool
		errorMsg  string
	}{
		// Without --all flag: requires exactly 2 args (source, target)
		{
			name:      "without --all, no args",
			args:      []string{},
			allFlag:   false,
			wantError: true,
			errorMsg:  "requires exactly 2 arguments",
		},
		{
			name:      "without --all, one arg",
			args:      []string{"feature/branch"},
			allFlag:   false,
			wantError: true,
			errorMsg:  "requires exactly 2 arguments",
		},
		{
			name:      "without --all, two args (success)",
			args:      []string{"feature/branch", "main"},
			allFlag:   false,
			wantError: false,
		},
		{
			name:      "without --all, three args",
			args:      []string{"feature/branch", "main", "extra"},
			allFlag:   false,
			wantError: true,
			errorMsg:  "requires exactly 2 arguments",
		},

		// With --all flag: requires exactly 1 arg (target)
		{
			name:      "with --all, no args",
			args:      []string{},
			allFlag:   true,
			wantError: true,
			errorMsg:  "--all flag requires exactly 1 argument",
		},
		{
			name:      "with --all, one arg (success)",
			args:      []string{"main"},
			allFlag:   true,
			wantError: false,
		},
		{
			name:      "with --all, two args",
			args:      []string{"main", "extra"},
			allFlag:   true,
			wantError: true,
			errorMsg:  "--all flag requires exactly 1 argument",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Set the flag state
			mergeAll = tc.allFlag

			// Call the Args validation function directly
			err := mergeCmd.Args(mergeCmd, tc.args)

			if tc.wantError {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tc.errorMsg)
					return
				}
				if tc.errorMsg != "" && !strings.Contains(err.Error(), tc.errorMsg) {
					t.Errorf("expected error containing %q, got %q", tc.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			}
		})
	}
}

func TestMergeBranch(t *testing.T) {
	tests := []struct {
		name           string
		sourceBranch   string
		targetBranch   string
		outputStubs    []OutputCommandStub // for GitFetch, GitCheckout, GitPull, GitMergeOrigin, GitPush
		commandStubs   []CommandStub       // for IsCleanWorkingTree, HasCommitsBetween, GetConflictedFiles
		claudeCalled   bool                // whether claude should be invoked
		claudeErr      error               // error from claude invocation
	}{
		{
			name:         "successful merge no conflicts",
			sourceBranch: "feature/test",
			targetBranch: "main",
			outputStubs: []OutputCommandStub{
				{Args: []string{"fetch", "origin"}, Err: nil},                                                                            // GitFetch
				{Args: []string{"checkout", "main"}, Err: nil},                                                                           // GitCheckout
				{Args: []string{"pull", "origin", "main"}, Err: nil},                                                                     // GitPull
				{Args: []string{"merge", "origin/feature/test", "-m", "Merge feature/test into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil}, // GitMergeOrigin
				{Args: []string{"push", "origin", "main"}, Err: nil},                                                                     // GitPush
			},
			commandStubs: []CommandStub{
				{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""},                                             // IsCleanWorkingTree (clean, no stash)
				{Name: "git", Args: []string{"log", "main..origin/feature/test", "--oneline"}, Stdout: "abc123 some commit\n"}, // HasCommitsBetween
			},
		},
		{
			name:         "already up to date",
			sourceBranch: "feature/test",
			targetBranch: "main",
			outputStubs: []OutputCommandStub{
				{Args: []string{"fetch", "origin"}, Err: nil},
				{Args: []string{"checkout", "main"}, Err: nil},
				{Args: []string{"pull", "origin", "main"}, Err: nil},
				// No merge or push since already up to date
			},
			commandStubs: []CommandStub{
				{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""},                                 // IsCleanWorkingTree
				{Name: "git", Args: []string{"log", "main..origin/feature/test", "--oneline"}, Stdout: ""}, // no commits
			},
		},
		{
			name:         "fetch fails",
			sourceBranch: "feature/test",
			targetBranch: "main",
			outputStubs: []OutputCommandStub{
				{Args: []string{"fetch", "origin"}, Err: errors.New("network error")},
			},
			commandStubs: []CommandStub{},
		},
		{
			name:         "checkout fails",
			sourceBranch: "feature/test",
			targetBranch: "main",
			outputStubs: []OutputCommandStub{
				{Args: []string{"fetch", "origin"}, Err: nil},
				{Args: []string{"checkout", "main"}, Err: errors.New("checkout failed")},
			},
			commandStubs: []CommandStub{
				{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""}, // IsCleanWorkingTree
			},
		},
		{
			name:         "pull fails",
			sourceBranch: "feature/test",
			targetBranch: "main",
			outputStubs: []OutputCommandStub{
				{Args: []string{"fetch", "origin"}, Err: nil},
				{Args: []string{"checkout", "main"}, Err: nil},
				{Args: []string{"pull", "origin", "main"}, Err: errors.New("pull failed")},
			},
			commandStubs: []CommandStub{
				{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""}, // IsCleanWorkingTree
			},
		},
		{
			name:         "merge with conflicts invokes claude",
			sourceBranch: "feature/test",
			targetBranch: "main",
			outputStubs: []OutputCommandStub{
				{Args: []string{"fetch", "origin"}, Err: nil},
				{Args: []string{"checkout", "main"}, Err: nil},
				{Args: []string{"pull", "origin", "main"}, Err: nil},
				{Args: []string{"merge", "origin/feature/test", "-m", "Merge feature/test into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: errors.New("CONFLICT")},
				// No push after conflicts
			},
			commandStubs: []CommandStub{
				{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""},                                    // IsCleanWorkingTree
				{Name: "git", Args: []string{"log", "main..origin/feature/test", "--oneline"}, Stdout: "abc123 commit\n"},
				{Name: "git", Args: []string{"diff", "--name-only", "--diff-filter=U"}, Stdout: "file1.go\nfile2.go\n"},
			},
			claudeCalled: true,
		},
		{
			name:         "merge fails no conflicts returns error",
			sourceBranch: "feature/test",
			targetBranch: "main",
			outputStubs: []OutputCommandStub{
				{Args: []string{"fetch", "origin"}, Err: nil},
				{Args: []string{"checkout", "main"}, Err: nil},
				{Args: []string{"pull", "origin", "main"}, Err: nil},
				{Args: []string{"merge", "origin/feature/test", "-m", "Merge feature/test into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: errors.New("merge failed")},
			},
			commandStubs: []CommandStub{
				{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""},                                    // IsCleanWorkingTree
				{Name: "git", Args: []string{"log", "main..origin/feature/test", "--oneline"}, Stdout: "abc123 commit\n"},
				{Name: "git", Args: []string{"diff", "--name-only", "--diff-filter=U"}, Stdout: ""}, // no conflict files
			},
		},
		{
			name:         "push fails after successful merge",
			sourceBranch: "feature/test",
			targetBranch: "main",
			outputStubs: []OutputCommandStub{
				{Args: []string{"fetch", "origin"}, Err: nil},
				{Args: []string{"checkout", "main"}, Err: nil},
				{Args: []string{"pull", "origin", "main"}, Err: nil},
				{Args: []string{"merge", "origin/feature/test", "-m", "Merge feature/test into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
				{Args: []string{"push", "origin", "main"}, Err: errors.New("push rejected")},
			},
			commandStubs: []CommandStub{
				{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""},                                    // IsCleanWorkingTree
				{Name: "git", Args: []string{"log", "main..origin/feature/test", "--oneline"}, Stdout: "abc123 commit\n"},
			},
		},
		{
			name:         "merge conflicts with claude error",
			sourceBranch: "feature/test",
			targetBranch: "main",
			outputStubs: []OutputCommandStub{
				{Args: []string{"fetch", "origin"}, Err: nil},
				{Args: []string{"checkout", "main"}, Err: nil},
				{Args: []string{"pull", "origin", "main"}, Err: nil},
				{Args: []string{"merge", "origin/feature/test", "-m", "Merge feature/test into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: errors.New("CONFLICT")},
			},
			commandStubs: []CommandStub{
				{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""},                                    // IsCleanWorkingTree
				{Name: "git", Args: []string{"log", "main..origin/feature/test", "--oneline"}, Stdout: "abc123 commit\n"},
				{Name: "git", Args: []string{"diff", "--name-only", "--diff-filter=U"}, Stdout: "file1.go\n"},
			},
			claudeCalled: true,
			claudeErr:    errors.New("claude failed"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Install output command mock (streaming git commands)
			outputMock := NewOutputCommandMock(t, tc.outputStubs)
			outputMock.Install()

			// Install command mock (captured git commands)
			if len(tc.commandStubs) > 0 {
				cmdMock := NewCommandMock(t, tc.commandStubs)
				cmdMock.Install()
			}

			// Mock claude invoker
			claudeCalled := false
			origClaude := claudeInvoker
			claudeInvoker = func(workDir, prompt, agentName string) error {
				claudeCalled = true
				return tc.claudeErr
			}
			t.Cleanup(func() { claudeInvoker = origClaude })

			// Call the function under test
			mergeBranch(tc.sourceBranch, tc.targetBranch)

			if tc.claudeCalled && !claudeCalled {
				t.Error("expected claude to be invoked, but it was not")
			}
			if !tc.claudeCalled && claudeCalled {
				t.Error("expected claude NOT to be invoked, but it was")
			}
		})
	}
}

func TestMergeAllWorktrees(t *testing.T) {
	tests := []struct {
		name         string
		targetBranch string
		worktrees    []WorktreeInfo
		outputStubs  []OutputCommandStub
		commandStubs []CommandStub
	}{
		{
			name:         "multiple worktrees",
			targetBranch: "main",
			worktrees: []WorktreeInfo{
				{Name: "alpha", Path: "/worktrees/alpha", Branch: "alpha-branch"},
				{Name: "beta", Path: "/worktrees/beta", Branch: "beta-branch"},
			},
			outputStubs: []OutputCommandStub{
				// First worktree merge: alpha-branch -> main
				{Args: []string{"fetch", "origin"}, Err: nil},
				{Args: []string{"checkout", "main"}, Err: nil},
				{Args: []string{"pull", "origin", "main"}, Err: nil},
				{Args: []string{"merge", "origin/alpha-branch", "-m", "Merge alpha-branch into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
				{Args: []string{"push", "origin", "main"}, Err: nil},
				// Second worktree merge: beta-branch -> main
				{Args: []string{"fetch", "origin"}, Err: nil},
				{Args: []string{"checkout", "main"}, Err: nil},
				{Args: []string{"pull", "origin", "main"}, Err: nil},
				{Args: []string{"merge", "origin/beta-branch", "-m", "Merge beta-branch into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
				{Args: []string{"push", "origin", "main"}, Err: nil},
			},
			commandStubs: []CommandStub{
				{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""},                                    // IsCleanWorkingTree (alpha)
				{Name: "git", Args: []string{"log", "main..origin/alpha-branch", "--oneline"}, Stdout: "abc commit\n"},
				{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""},                                    // IsCleanWorkingTree (beta)
				{Name: "git", Args: []string{"log", "main..origin/beta-branch", "--oneline"}, Stdout: "def commit\n"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Set up a temp dir with worktree directories
			tmpDir := t.TempDir()

			// Create worktrees/<name>/.git for each test worktree
			for _, wt := range tc.worktrees {
				wtPath := tmpDir + "/worktrees/" + wt.Name + "/.git"
				if err := os.MkdirAll(wtPath, 0755); err != nil {
					t.Fatalf("failed to create worktree dir: %v", err)
				}
			}

			// Set LOOM_WORKTREES_DIR to point to our temp worktrees
			SetupTestEnv(t, map[string]string{
				"LOOM_WORKTREES_DIR": tmpDir + "/worktrees",
			})

			// Install output command mock
			outputMock := NewOutputCommandMock(t, tc.outputStubs)
			outputMock.Install()

			// Install command mock - DiscoverWorktrees calls GetCurrentBranch for all,
			// then mergeBranch calls HasCommitsBetween for each
			var allCmdStubs []CommandStub
			// First: GetCurrentBranch for each worktree (during DiscoverWorktrees)
			for _, wt := range tc.worktrees {
				allCmdStubs = append(allCmdStubs, CommandStub{
					Name:   "git",
					Args:   []string{"branch", "--show-current"},
					Stdout: wt.Branch + "\n",
				})
			}
			// Then: HasCommitsBetween for each mergeBranch call
			allCmdStubs = append(allCmdStubs, tc.commandStubs...)
			cmdMock := NewCommandMock(t, allCmdStubs)
			cmdMock.Install()

			// Mock claude (shouldn't be called in this test)
			origClaude := claudeInvoker
			claudeInvoker = func(workDir, prompt, agentName string) error {
				t.Error("unexpected claude invocation")
				return nil
			}
			t.Cleanup(func() { claudeInvoker = origClaude })

			mergeAllWorktrees(tc.targetBranch)
		})
	}
}

func TestTargetBranchDisplay(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{"empty returns per-repo default", "", "(per-repo default)"},
		{"non-empty returns as-is", "main", "main"},
		{"feature branch", "feature/web-ui", "feature/web-ui"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := targetBranchDisplay(tc.input)
			if got != tc.expect {
				t.Errorf("targetBranchDisplay(%q) = %q, want %q", tc.input, got, tc.expect)
			}
		})
	}
}

func TestMergeWorkspaceWorktrees_IteratesAllRepos(t *testing.T) {
	worktrees := []WorktreeInfo{
		{
			Name:   "repo-a",
			Path:   "/ws/repo-a",
			Branch: "feat-a",
			Repo:   &RepoConfig{Name: "repo-a", DefaultBranch: "main", Remote: ""},
		},
		{
			Name:   "repo-b",
			Path:   "/ws/repo-b",
			Branch: "feat-b",
			Repo:   &RepoConfig{Name: "repo-b", DefaultBranch: "main", Remote: ""},
		},
	}

	outputStubs := []OutputCommandStub{
		// repo-a: fetch, checkout, pull, merge, push
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"checkout", "main"}, Err: nil},
		{Args: []string{"pull", "origin", "main"}, Err: nil},
		{Args: []string{"merge", "origin/feat-a", "-m", "Merge feat-a into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "main"}, Err: nil},
		// repo-b: fetch, checkout, pull, merge, push
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"checkout", "main"}, Err: nil},
		{Args: []string{"pull", "origin", "main"}, Err: nil},
		{Args: []string{"merge", "origin/feat-b", "-m", "Merge feat-b into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "main"}, Err: nil},
	}

	commandStubs := []CommandStub{
		// IsCleanWorkingTree + HasCommitsBetweenRemote for repo-a
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""},
		{Name: "git", Args: []string{"log", "main..origin/feat-a", "--oneline"}, Stdout: "abc commit\n"},
		// IsCleanWorkingTree + HasCommitsBetweenRemote for repo-b
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""},
		{Name: "git", Args: []string{"log", "main..origin/feat-b", "--oneline"}, Stdout: "def commit\n"},
	}

	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.Install()
	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.Install()

	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	// sourceBranch="" means use wt.Branch for each; targetBranch="main" is explicit
	mergeWorkspaceWorktrees(worktrees, "", "main")
}

func TestMergeWorkspaceWorktrees_UsesPerRepoDefaultBranch(t *testing.T) {
	worktrees := []WorktreeInfo{
		{
			Name:   "repo-a",
			Path:   "/ws/repo-a",
			Branch: "feat-a",
			Repo:   &RepoConfig{Name: "repo-a", DefaultBranch: "develop", Remote: ""},
		},
		{
			Name:   "repo-b",
			Path:   "/ws/repo-b",
			Branch: "feat-b",
			Repo:   &RepoConfig{Name: "repo-b", DefaultBranch: "staging", Remote: ""},
		},
	}

	outputStubs := []OutputCommandStub{
		// repo-a merges into "develop"
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"checkout", "develop"}, Err: nil},
		{Args: []string{"pull", "origin", "develop"}, Err: nil},
		{Args: []string{"merge", "origin/feat-a", "-m", "Merge feat-a into develop\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "develop"}, Err: nil},
		// repo-b merges into "staging"
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"checkout", "staging"}, Err: nil},
		{Args: []string{"pull", "origin", "staging"}, Err: nil},
		{Args: []string{"merge", "origin/feat-b", "-m", "Merge feat-b into staging\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "staging"}, Err: nil},
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""},
		{Name: "git", Args: []string{"log", "develop..origin/feat-a", "--oneline"}, Stdout: "abc commit\n"},
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""},
		{Name: "git", Args: []string{"log", "staging..origin/feat-b", "--oneline"}, Stdout: "def commit\n"},
	}

	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.Install()
	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.Install()

	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	// targetBranch="" means use per-repo DefaultBranch
	mergeWorkspaceWorktrees(worktrees, "", "")
}

func TestMergeWorkspaceWorktrees_CLIArgOverridesConfig(t *testing.T) {
	worktrees := []WorktreeInfo{
		{
			Name:   "repo-a",
			Path:   "/ws/repo-a",
			Branch: "feat-a",
			Repo:   &RepoConfig{Name: "repo-a", DefaultBranch: "develop", Remote: ""},
		},
	}

	// CLI target "release" overrides per-repo "develop"
	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"checkout", "release"}, Err: nil},
		{Args: []string{"pull", "origin", "release"}, Err: nil},
		{Args: []string{"merge", "origin/feat-a", "-m", "Merge feat-a into release\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "release"}, Err: nil},
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""},
		{Name: "git", Args: []string{"log", "release..origin/feat-a", "--oneline"}, Stdout: "abc commit\n"},
	}

	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.Install()
	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.Install()

	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	mergeWorkspaceWorktrees(worktrees, "", "release")
}

func TestMergeWorkspaceWorktrees_CustomRemote(t *testing.T) {
	worktrees := []WorktreeInfo{
		{
			Name:   "repo-a",
			Path:   "/ws/repo-a",
			Branch: "feat-a",
			Repo:   &RepoConfig{Name: "repo-a", DefaultBranch: "main", Remote: "upstream"},
		},
	}

	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "upstream"}, Err: nil},
		{Args: []string{"checkout", "main"}, Err: nil},
		{Args: []string{"pull", "upstream", "main"}, Err: nil},
		{Args: []string{"merge", "upstream/feat-a", "-m", "Merge feat-a into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "upstream", "main"}, Err: nil},
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""},
		{Name: "git", Args: []string{"log", "main..upstream/feat-a", "--oneline"}, Stdout: "abc commit\n"},
	}

	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.Install()
	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.Install()

	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	mergeWorkspaceWorktrees(worktrees, "", "main")
}

func TestRunMerge_LegacyMode(t *testing.T) {
	// When no workspace config exists, runMerge falls through to legacy mergeBranch.
	// We test the legacy path by calling mergeBranch directly, same as existing TestMergeBranch.
	// This test verifies the same git command sequence with hardcoded "origin".

	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"checkout", "main"}, Err: nil},
		{Args: []string{"pull", "origin", "main"}, Err: nil},
		{Args: []string{"merge", "origin/feature/test", "-m", "Merge feature/test into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "main"}, Err: nil},
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""},
		{Name: "git", Args: []string{"log", "main..origin/feature/test", "--oneline"}, Stdout: "abc123 commit\n"},
	}

	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.Install()
	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.Install()

	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	mergeBranch("feature/test", "main")
}

func TestMergeWorkspaceWorktrees_SkipsNilRepo(t *testing.T) {
	worktrees := []WorktreeInfo{
		{
			Name:   "repo-a",
			Path:   "/ws/repo-a",
			Branch: "feat-a",
			Repo:   nil, // should be skipped
		},
		{
			Name:   "repo-b",
			Path:   "/ws/repo-b",
			Branch: "feat-b",
			Repo:   &RepoConfig{Name: "repo-b", DefaultBranch: "main", Remote: ""},
		},
	}

	// Only repo-b should be processed
	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"checkout", "main"}, Err: nil},
		{Args: []string{"pull", "origin", "main"}, Err: nil},
		{Args: []string{"merge", "origin/feat-b", "-m", "Merge feat-b into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "main"}, Err: nil},
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""},
		{Name: "git", Args: []string{"log", "main..origin/feat-b", "--oneline"}, Stdout: "def commit\n"},
	}

	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.Install()
	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.Install()

	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	mergeWorkspaceWorktrees(worktrees, "", "main")
}

func TestMergeBranch_DirtyWorkingTree_StashesAndPops(t *testing.T) {
	// Test that when working tree is dirty, mergeBranch stashes before checkout
	// and pops stash after merge completes successfully
	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"stash"}, Err: nil},                      // GitStash
		{Args: []string{"checkout", "main"}, Err: nil},
		{Args: []string{"pull", "origin", "main"}, Err: nil},
		{Args: []string{"merge", "origin/feature/test", "-m", "Merge feature/test into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "main"}, Err: nil},
		{Args: []string{"stash", "pop"}, Err: nil},               // GitStashPop (via defer)
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: " M dirty.go\n"}, // IsCleanWorkingTree returns false (dirty)
		{Name: "git", Args: []string{"log", "main..origin/feature/test", "--oneline"}, Stdout: "abc123 commit\n"},
	}

	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.Install()
	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.Install()

	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	mergeBranch("feature/test", "main")
}

func TestMergeBranch_StashPopConflicts_WarnsButSucceeds(t *testing.T) {
	// Test that when stash pop fails due to conflicts, a warning is printed
	// but the merge itself succeeds (no error returned to caller)
	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"stash"}, Err: nil},
		{Args: []string{"checkout", "main"}, Err: nil},
		{Args: []string{"pull", "origin", "main"}, Err: nil},
		{Args: []string{"merge", "origin/feature/test", "-m", "Merge feature/test into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "main"}, Err: nil},
		{Args: []string{"stash", "pop"}, Err: errors.New("conflict during stash pop")}, // stash pop fails
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: " M dirty.go\n"},
		{Name: "git", Args: []string{"log", "main..origin/feature/test", "--oneline"}, Stdout: "abc123 commit\n"},
		{Name: "git", Args: []string{"diff", "--name-only", "--diff-filter=U"}, Stdout: "dirty.go\n"}, // HasUnmergedFiles returns true
	}

	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.Install()
	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.Install()

	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	// mergeBranch doesn't return an error, it just prints to stderr
	// The test verifies the correct sequence of commands is called
	mergeBranch("feature/test", "main")
}

func TestMergeBranch_StashFails_ReturnsEarly(t *testing.T) {
	// Test that when GitStash fails, mergeBranch returns early without proceeding to checkout
	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"stash"}, Err: errors.New("stash failed: no local changes")}, // GitStash fails
		// No checkout, pull, merge, push - should return early
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: " M dirty.go\n"}, // dirty working tree
	}

	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.Install()
	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.Install()

	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	// mergeBranch prints error and returns early - no panic
	mergeBranch("feature/test", "main")
}

func TestMergeBranchInRepo_DirtyWorkingTree_StashesAndPops(t *testing.T) {
	// Test that mergeBranchInRepo stashes dirty changes and pops after merge
	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"stash"}, Err: nil},
		{Args: []string{"checkout", "main"}, Err: nil},
		{Args: []string{"pull", "origin", "main"}, Err: nil},
		{Args: []string{"merge", "origin/feature", "-m", "Merge feature into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "main"}, Err: nil},
		{Args: []string{"stash", "pop"}, Err: nil},
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: " M file.go\n"},
		{Name: "git", Args: []string{"log", "main..origin/feature", "--oneline"}, Stdout: "abc commit\n"},
	}

	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.Install()
	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.Install()

	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	err := mergeBranchInRepo("/repo", "feature", "main", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestMergeBranchInRepo_StashFails_ReturnsError(t *testing.T) {
	// Test that when GitStash fails, mergeBranchInRepo returns the error
	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"stash"}, Err: errors.New("stash failed")},
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: " M file.go\n"},
	}

	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.Install()
	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.Install()

	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	err := mergeBranchInRepo("/repo", "feature", "main", "")
	if err == nil {
		t.Error("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "stashing") {
		t.Errorf("expected error to mention stashing, got: %v", err)
	}
}

func TestMergeBranchInRepo_StashPopConflicts_WarnsButSucceeds(t *testing.T) {
	// Test that stash pop conflicts produce warning but don't fail the merge
	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"stash"}, Err: nil},
		{Args: []string{"checkout", "main"}, Err: nil},
		{Args: []string{"pull", "origin", "main"}, Err: nil},
		{Args: []string{"merge", "origin/feature", "-m", "Merge feature into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "main"}, Err: nil},
		{Args: []string{"stash", "pop"}, Err: errors.New("conflict")},
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: " M file.go\n"},
		{Name: "git", Args: []string{"log", "main..origin/feature", "--oneline"}, Stdout: "abc commit\n"},
		{Name: "git", Args: []string{"diff", "--name-only", "--diff-filter=U"}, Stdout: "file.go\n"}, // HasUnmergedFiles
	}

	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.Install()
	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.Install()

	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	// Should succeed despite stash pop conflict
	err := mergeBranchInRepo("/repo", "feature", "main", "")
	if err != nil {
		t.Errorf("expected success despite stash pop conflict, got: %v", err)
	}
}

func TestMergeBranchInRepo_CleanWorkingTree_NoStash(t *testing.T) {
	// Test that clean working tree skips stash entirely
	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		// No stash or stash pop - clean working tree
		{Args: []string{"checkout", "main"}, Err: nil},
		{Args: []string{"pull", "origin", "main"}, Err: nil},
		{Args: []string{"merge", "origin/feature", "-m", "Merge feature into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "main"}, Err: nil},
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""}, // clean
		{Name: "git", Args: []string{"log", "main..origin/feature", "--oneline"}, Stdout: "abc commit\n"},
	}

	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.Install()
	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.Install()

	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	err := mergeBranchInRepo("/repo", "feature", "main", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
