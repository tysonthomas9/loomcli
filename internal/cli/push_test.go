package cli

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestPushCmd_ArgsValidation(t *testing.T) {
	// Save and restore the global flag
	origPushAll := pushAll
	defer func() { pushAll = origPushAll }()

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
			pushAll = tc.allFlag

			// Call the Args validation function directly
			err := pushCmd.Args(pushCmd, tc.args)

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

func TestPushBranch(t *testing.T) {
	tests := []struct {
		name         string
		sourceBranch string
		targetBranch string
		outputStubs  []OutputCommandStub // for GitFetch, GitCheckout, GitPull, GitMergeOrigin, GitPush
		commandStubs []CommandStub       // for IsCleanWorkingTree, HasCommitsBetween, GetConflictedFiles
		claudeCalled bool                // whether claude should be invoked
		claudeErr    error               // error from claude invocation
	}{
		{
			name:         "successful push no conflicts",
			sourceBranch: "feature/test",
			targetBranch: "main",
			outputStubs: []OutputCommandStub{
				{Args: []string{"fetch", "origin"}, Err: nil},                                                                                                             // GitFetch
				{Args: []string{"stash"}, Err: nil},                                                                                                                       // GitStash (no-op)
				{Args: []string{"checkout", "main"}, Err: nil},                                                                                                            // GitCheckout
				{Args: []string{"pull", "origin", "main"}, Err: nil},                                                                                                      // GitPull
				{Args: []string{"merge", "origin/feature/test", "-m", "Merge feature/test into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil}, // GitMergeOrigin
				{Args: []string{"push", "origin", "main"}, Err: nil},                                                                                                      // GitPush
			},
			commandStubs: []CommandStub{
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                                     // getStashCount (before)
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                                     // getStashCount (after, same = not stashed)
				{Name: "git", Args: []string{"worktree", "list", "--porcelain"}, Stdout: ""},                                   // IsRefCheckedOutInWorktree
				{Name: "git", Args: []string{"log", "main..origin/feature/test", "--oneline"}, Stdout: "abc123 some commit\n"}, // HasCommitsBetween
			},
		},
		{
			name:         "already up to date",
			sourceBranch: "feature/test",
			targetBranch: "main",
			outputStubs: []OutputCommandStub{
				{Args: []string{"fetch", "origin"}, Err: nil},
				{Args: []string{"stash"}, Err: nil},
				{Args: []string{"checkout", "main"}, Err: nil},
				{Args: []string{"pull", "origin", "main"}, Err: nil},
				// No merge or push since already up to date
			},
			commandStubs: []CommandStub{
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                  // getStashCount (before)
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                  // getStashCount (after)
				{Name: "git", Args: []string{"worktree", "list", "--porcelain"}, Stdout: ""},                // IsRefCheckedOutInWorktree
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
				{Args: []string{"stash"}, Err: nil},
				{Args: []string{"checkout", "main"}, Err: errors.New("checkout failed")},
			},
			commandStubs: []CommandStub{
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                   // getStashCount (before)
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                   // getStashCount (after)
				{Name: "git", Args: []string{"worktree", "list", "--porcelain"}, Stdout: ""}, // IsRefCheckedOutInWorktree
			},
		},
		{
			name:         "pull fails",
			sourceBranch: "feature/test",
			targetBranch: "main",
			outputStubs: []OutputCommandStub{
				{Args: []string{"fetch", "origin"}, Err: nil},
				{Args: []string{"stash"}, Err: nil},
				{Args: []string{"checkout", "main"}, Err: nil},
				{Args: []string{"pull", "origin", "main"}, Err: errors.New("pull failed")},
			},
			commandStubs: []CommandStub{
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                   // getStashCount (before)
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                   // getStashCount (after)
				{Name: "git", Args: []string{"worktree", "list", "--porcelain"}, Stdout: ""}, // IsRefCheckedOutInWorktree
			},
		},
		{
			name:         "merge with conflicts invokes claude",
			sourceBranch: "feature/test",
			targetBranch: "main",
			outputStubs: []OutputCommandStub{
				{Args: []string{"fetch", "origin"}, Err: nil},
				{Args: []string{"stash"}, Err: nil},
				{Args: []string{"checkout", "main"}, Err: nil},
				{Args: []string{"pull", "origin", "main"}, Err: nil},
				{Args: []string{"merge", "origin/feature/test", "-m", "Merge feature/test into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: errors.New("CONFLICT")},
				// No push after conflicts
			},
			commandStubs: []CommandStub{
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                             // getStashCount (before)
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                             // getStashCount (after)
				{Name: "git", Args: []string{"worktree", "list", "--porcelain"}, Stdout: ""},                           // IsRefCheckedOutInWorktree
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
				{Args: []string{"stash"}, Err: nil},
				{Args: []string{"checkout", "main"}, Err: nil},
				{Args: []string{"pull", "origin", "main"}, Err: nil},
				{Args: []string{"merge", "origin/feature/test", "-m", "Merge feature/test into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: errors.New("merge failed")},
			},
			commandStubs: []CommandStub{
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                             // getStashCount (before)
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                             // getStashCount (after)
				{Name: "git", Args: []string{"worktree", "list", "--porcelain"}, Stdout: ""},                           // IsRefCheckedOutInWorktree
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
				{Args: []string{"stash"}, Err: nil},
				{Args: []string{"checkout", "main"}, Err: nil},
				{Args: []string{"pull", "origin", "main"}, Err: nil},
				{Args: []string{"merge", "origin/feature/test", "-m", "Merge feature/test into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
				{Args: []string{"push", "origin", "main"}, Err: errors.New("push rejected")},
			},
			commandStubs: []CommandStub{
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                             // getStashCount (before)
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                             // getStashCount (after)
				{Name: "git", Args: []string{"worktree", "list", "--porcelain"}, Stdout: ""},                           // IsRefCheckedOutInWorktree
				{Name: "git", Args: []string{"log", "main..origin/feature/test", "--oneline"}, Stdout: "abc123 commit\n"},
			},
		},
		{
			name:         "merge conflicts with claude error",
			sourceBranch: "feature/test",
			targetBranch: "main",
			outputStubs: []OutputCommandStub{
				{Args: []string{"fetch", "origin"}, Err: nil},
				{Args: []string{"stash"}, Err: nil},
				{Args: []string{"checkout", "main"}, Err: nil},
				{Args: []string{"pull", "origin", "main"}, Err: nil},
				{Args: []string{"merge", "origin/feature/test", "-m", "Merge feature/test into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: errors.New("CONFLICT")},
			},
			commandStubs: []CommandStub{
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                             // getStashCount (before)
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                             // getStashCount (after)
				{Name: "git", Args: []string{"worktree", "list", "--porcelain"}, Stdout: ""},                           // IsRefCheckedOutInWorktree
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
			pushBranch(tc.sourceBranch, tc.targetBranch)

			if tc.claudeCalled && !claudeCalled {
				t.Error("expected claude to be invoked, but it was not")
			}
			if !tc.claudeCalled && claudeCalled {
				t.Error("expected claude NOT to be invoked, but it was")
			}
		})
	}
}

func TestPushAllWorktrees(t *testing.T) {
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
				// First worktree push: alpha-branch -> main
				{Args: []string{"fetch", "origin"}, Err: nil},
				{Args: []string{"stash"}, Err: nil},
				{Args: []string{"checkout", "main"}, Err: nil},
				{Args: []string{"pull", "origin", "main"}, Err: nil},
				{Args: []string{"merge", "origin/alpha-branch", "-m", "Merge alpha-branch into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
				{Args: []string{"push", "origin", "main"}, Err: nil},
				// Second worktree push: beta-branch -> main
				{Args: []string{"fetch", "origin"}, Err: nil},
				{Args: []string{"stash"}, Err: nil},
				{Args: []string{"checkout", "main"}, Err: nil},
				{Args: []string{"pull", "origin", "main"}, Err: nil},
				{Args: []string{"merge", "origin/beta-branch", "-m", "Merge beta-branch into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
				{Args: []string{"push", "origin", "main"}, Err: nil},
			},
			commandStubs: []CommandStub{
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                             // getStashCount before (alpha)
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                             // getStashCount after (alpha)
				{Name: "git", Args: []string{"worktree", "list", "--porcelain"}, Stdout: ""},                           // IsRefCheckedOutInWorktree (alpha)
				{Name: "git", Args: []string{"log", "main..origin/alpha-branch", "--oneline"}, Stdout: "abc commit\n"},
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                             // getStashCount before (beta)
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                             // getStashCount after (beta)
				{Name: "git", Args: []string{"worktree", "list", "--porcelain"}, Stdout: ""},                           // IsRefCheckedOutInWorktree (beta)
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
			// then pushBranch calls HasCommitsBetween for each
			var allCmdStubs []CommandStub
			// First: GetCurrentBranch for each worktree (during DiscoverWorktrees)
			for _, wt := range tc.worktrees {
				allCmdStubs = append(allCmdStubs, CommandStub{
					Name:   "git",
					Args:   []string{"branch", "--show-current"},
					Stdout: wt.Branch + "\n",
				})
			}
			// Then: HasCommitsBetween for each pushBranch call
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

			pushAllWorktrees(tc.targetBranch)
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

func TestPushWorkspaceWorktrees_IteratesAllRepos(t *testing.T) {
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
		// repo-a: fetch, stash, checkout, pull, merge, push
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"stash"}, Err: nil},
		{Args: []string{"checkout", "main"}, Err: nil},
		{Args: []string{"pull", "origin", "main"}, Err: nil},
		{Args: []string{"merge", "origin/feat-a", "-m", "Merge feat-a into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "main"}, Err: nil},
		// repo-b: fetch, stash, checkout, pull, merge, push
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"stash"}, Err: nil},
		{Args: []string{"checkout", "main"}, Err: nil},
		{Args: []string{"pull", "origin", "main"}, Err: nil},
		{Args: []string{"merge", "origin/feat-b", "-m", "Merge feat-b into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "main"}, Err: nil},
	}

	commandStubs := []CommandStub{
		// getStashCount + IsRefCheckedOutInWorktree + HasCommitsBetweenRemote for repo-a
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"worktree", "list", "--porcelain"}, Stdout: ""},
		{Name: "git", Args: []string{"log", "main..origin/feat-a", "--oneline"}, Stdout: "abc commit\n"},
		// getStashCount + IsRefCheckedOutInWorktree + HasCommitsBetweenRemote for repo-b
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"worktree", "list", "--porcelain"}, Stdout: ""},
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
	pushWorkspaceWorktrees(worktrees, "", "main")
}

func TestPushWorkspaceWorktrees_UsesPerRepoDefaultBranch(t *testing.T) {
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
		// repo-a pushes into "develop"
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"stash"}, Err: nil},
		{Args: []string{"checkout", "develop"}, Err: nil},
		{Args: []string{"pull", "origin", "develop"}, Err: nil},
		{Args: []string{"merge", "origin/feat-a", "-m", "Merge feat-a into develop\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "develop"}, Err: nil},
		// repo-b pushes into "staging"
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"stash"}, Err: nil},
		{Args: []string{"checkout", "staging"}, Err: nil},
		{Args: []string{"pull", "origin", "staging"}, Err: nil},
		{Args: []string{"merge", "origin/feat-b", "-m", "Merge feat-b into staging\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "staging"}, Err: nil},
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"worktree", "list", "--porcelain"}, Stdout: ""},
		{Name: "git", Args: []string{"log", "develop..origin/feat-a", "--oneline"}, Stdout: "abc commit\n"},
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"worktree", "list", "--porcelain"}, Stdout: ""},
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
	pushWorkspaceWorktrees(worktrees, "", "")
}

func TestPushWorkspaceWorktrees_CLIArgOverridesConfig(t *testing.T) {
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
		{Args: []string{"stash"}, Err: nil},
		{Args: []string{"checkout", "release"}, Err: nil},
		{Args: []string{"pull", "origin", "release"}, Err: nil},
		{Args: []string{"merge", "origin/feat-a", "-m", "Merge feat-a into release\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "release"}, Err: nil},
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"worktree", "list", "--porcelain"}, Stdout: ""},
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

	pushWorkspaceWorktrees(worktrees, "", "release")
}

func TestPushWorkspaceWorktrees_CustomRemote(t *testing.T) {
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
		{Args: []string{"stash"}, Err: nil},
		{Args: []string{"checkout", "main"}, Err: nil},
		{Args: []string{"pull", "upstream", "main"}, Err: nil},
		{Args: []string{"merge", "upstream/feat-a", "-m", "Merge feat-a into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "upstream", "main"}, Err: nil},
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"worktree", "list", "--porcelain"}, Stdout: ""},
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

	pushWorkspaceWorktrees(worktrees, "", "main")
}

func TestRunPush_LegacyMode(t *testing.T) {
	// When no workspace config exists, runPush falls through to legacy pushBranch.
	// We test the legacy path by calling pushBranch directly.
	// This test verifies the same git command sequence with hardcoded "origin".

	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"stash"}, Err: nil},
		{Args: []string{"checkout", "main"}, Err: nil},
		{Args: []string{"pull", "origin", "main"}, Err: nil},
		{Args: []string{"merge", "origin/feature/test", "-m", "Merge feature/test into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "main"}, Err: nil},
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"worktree", "list", "--porcelain"}, Stdout: ""},
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

	pushBranch("feature/test", "main")
}

func TestPushWorkspaceWorktrees_SkipsNilRepo(t *testing.T) {
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
		{Args: []string{"stash"}, Err: nil},
		{Args: []string{"checkout", "main"}, Err: nil},
		{Args: []string{"pull", "origin", "main"}, Err: nil},
		{Args: []string{"merge", "origin/feat-b", "-m", "Merge feat-b into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "main"}, Err: nil},
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"worktree", "list", "--porcelain"}, Stdout: ""},
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

	pushWorkspaceWorktrees(worktrees, "", "main")
}

func TestPushBranch_DirtyWorkingTree_StashesAndPops(t *testing.T) {
	// Test that when working tree is dirty, pushBranch stashes before checkout
	// and pops stash after merge completes successfully
	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"stash"}, Err: nil}, // GitStash
		{Args: []string{"checkout", "main"}, Err: nil},
		{Args: []string{"pull", "origin", "main"}, Err: nil},
		{Args: []string{"merge", "origin/feature/test", "-m", "Merge feature/test into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "main"}, Err: nil},
		{Args: []string{"stash", "pop"}, Err: nil}, // GitStashPop (via defer)
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                    // getStashCount (before, 0)
		{Name: "git", Args: []string{"stash", "list"}, Stdout: "stash@{0}: WIP on main: abc1234\n"}, // getStashCount (after, 1 = stashed)
		{Name: "git", Args: []string{"worktree", "list", "--porcelain"}, Stdout: ""},                  // IsRefCheckedOutInWorktree
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

	pushBranch("feature/test", "main")
}

func TestPushBranch_StashPopConflicts_WarnsButSucceeds(t *testing.T) {
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
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                    // getStashCount (before)
		{Name: "git", Args: []string{"stash", "list"}, Stdout: "stash@{0}: WIP on main: abc1234\n"}, // getStashCount (after, stashed)
		{Name: "git", Args: []string{"worktree", "list", "--porcelain"}, Stdout: ""},                  // IsRefCheckedOutInWorktree
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

	// pushBranch doesn't return an error, it just prints to stderr
	// The test verifies the correct sequence of commands is called
	pushBranch("feature/test", "main")
}

func TestPushBranch_StashFails_ReturnsEarly(t *testing.T) {
	// Test that when GitStash fails, pushBranch returns early without proceeding to checkout
	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"stash"}, Err: errors.New("stash failed: no local changes")}, // GitStash fails
		// No checkout, pull, merge, push - should return early
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""}, // getStashCount (before)
		// No second stash list - git stash command fails before it
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

	// pushBranch prints error and returns early - no panic
	pushBranch("feature/test", "main")
}

func TestPushBranchInRepo_DirtyWorkingTree_StashesAndPops(t *testing.T) {
	// Test that pushBranchInRepo stashes dirty changes and pops after merge
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
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                    // getStashCount (before)
		{Name: "git", Args: []string{"stash", "list"}, Stdout: "stash@{0}: WIP on main: abc1234\n"}, // getStashCount (after, stashed)
		{Name: "git", Args: []string{"worktree", "list", "--porcelain"}, Stdout: ""},
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

	err := pushBranchInRepo("/repo", "feature", "main", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPushBranchInRepo_StashFails_ReturnsError(t *testing.T) {
	// Test that when GitStash fails, pushBranchInRepo returns the error
	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"stash"}, Err: errors.New("stash failed")},
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""}, // getStashCount (before)
		// No second stash list - git stash command fails
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

	err := pushBranchInRepo("/repo", "feature", "main", "")
	if err == nil {
		t.Error("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "stashing") {
		t.Errorf("expected error to mention stashing, got: %v", err)
	}
}

func TestPushBranchInRepo_StashPopConflicts_WarnsButSucceeds(t *testing.T) {
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
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                    // getStashCount (before)
		{Name: "git", Args: []string{"stash", "list"}, Stdout: "stash@{0}: WIP on main: abc1234\n"}, // getStashCount (after, stashed)
		{Name: "git", Args: []string{"worktree", "list", "--porcelain"}, Stdout: ""},
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
	err := pushBranchInRepo("/repo", "feature", "main", "")
	if err != nil {
		t.Errorf("expected success despite stash pop conflict, got: %v", err)
	}
}

func TestPushBranchInRepo_CleanWorkingTree_NoStash(t *testing.T) {
	// Test that clean working tree skips stash pop (stash count unchanged)
	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"stash"}, Err: nil}, // git stash runs but is no-op
		{Args: []string{"checkout", "main"}, Err: nil},
		{Args: []string{"pull", "origin", "main"}, Err: nil},
		{Args: []string{"merge", "origin/feature", "-m", "Merge feature into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "main"}, Err: nil},
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                   // getStashCount (before)
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                   // getStashCount (after, same = not stashed)
		{Name: "git", Args: []string{"worktree", "list", "--porcelain"}, Stdout: ""},
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

	err := pushBranchInRepo("/repo", "feature", "main", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPushCmd_MergeAlias(t *testing.T) {
	// Test that "merge" is listed as an alias for the push command
	aliases := pushCmd.Aliases
	found := false
	for _, a := range aliases {
		if a == "merge" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'merge' to be an alias for push command, got aliases: %v", aliases)
	}
}

func TestPushCmd_MergeAliasIsFirst(t *testing.T) {
	// Test that "merge" alias is the first (primary) alias
	aliases := pushCmd.Aliases
	if len(aliases) == 0 {
		t.Fatal("push command has no aliases")
	}
	if aliases[0] != "merge" {
		t.Errorf("expected first alias to be 'merge', got %q", aliases[0])
	}
}

func TestPushCmd_GroupID(t *testing.T) {
	// Verify command is in the "git" group
	if pushCmd.GroupID != "git" {
		t.Errorf("expected push command to be in 'git' group, got %q", pushCmd.GroupID)
	}
}

func TestPushCmd_Flags(t *testing.T) {
	// Verify flags are registered
	if allFlag := pushCmd.Flags().Lookup("all"); allFlag == nil {
		t.Error("expected --all flag to be registered")
	}
	if wsFlag := pushCmd.Flags().Lookup("workspace"); wsFlag == nil {
		t.Error("expected --workspace flag to be registered")
	}

	// Verify shorthand flags
	if allFlag := pushCmd.Flags().ShorthandLookup("a"); allFlag == nil {
		t.Error("expected -a shorthand flag to be registered")
	}
	if wsFlag := pushCmd.Flags().ShorthandLookup("W"); wsFlag == nil {
		t.Error("expected -W shorthand flag to be registered")
	}
}

func TestPushAllWorktrees_EmptyList(t *testing.T) {
	tmpDir := t.TempDir()

	// Create empty worktrees directory
	if err := os.MkdirAll(tmpDir+"/worktrees", 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	SetupTestEnv(t, map[string]string{
		"LOOM_WORKTREES_DIR": tmpDir + "/worktrees",
	})

	// No mocks needed - should return early when no worktrees found
	outputMock := NewOutputCommandMock(t, []OutputCommandStub{})
	outputMock.Install()

	// Should not panic when no worktrees
	pushAllWorktrees("main")
}

func TestPushWorkspaceWorktrees_EmptyList(t *testing.T) {
	// Empty worktree list should produce no errors and no git commands
	worktrees := []WorktreeInfo{}

	// No output stubs - no commands should be called
	outputMock := NewOutputCommandMock(t, []OutputCommandStub{})
	outputMock.Install()

	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	// Should not panic or call any commands
	pushWorkspaceWorktrees(worktrees, "", "main")
}

func TestPushBranchInRepo_WorktreeConflict_UsesDetached(t *testing.T) {
	// When IsRefCheckedOutInWorktree returns true, pushBranchInRepo should use
	// the detached HEAD approach instead of the normal checkout flow.
	worktreeListOutput := "worktree /home/user/project\nHEAD abc1234\nbranch refs/heads/main\n\n" +
		"worktree /home/user/worktrees/falcon\nHEAD def5678\nbranch refs/heads/falcon\n\n"

	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},                // GitFetchRemote
		{Args: []string{"stash"}, Err: nil},                         // GitStash (no-op)
		{Args: nil, Err: nil},                                        // GitCheckoutDetached (origin/main)
		{Args: nil, Err: nil},                                        // GitCreateBranchFromHead (temp branch)
		{Args: nil, Err: nil},                                        // GitMergeRemote
		{Args: nil, Err: nil},                                        // GitPushRefspec (temp:main)
		{Args: nil, Err: nil},                                        // GitDeleteBranch (cleanup temp, deferred)
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                        // getStashCount (before)
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                        // getStashCount (after, same = not stashed)
		{Name: "git", Args: []string{"worktree", "list", "--porcelain"}, Stdout: worktreeListOutput},      // IsRefCheckedOutInWorktree -> true
		{Name: "git", Args: []string{"log", "main..origin/feature", "--oneline"}, Stdout: "abc commit\n"}, // HasCommitsBetweenRemote
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

	err := pushBranchInRepo("/repo", "feature", "main", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPushBranchInRepoDetached_Success(t *testing.T) {
	// Test the full detached push flow:
	// checkout detached -> create temp branch -> merge -> push refspec -> cleanup
	outputStubs := []OutputCommandStub{
		{Args: nil, Err: nil}, // GitCheckoutDetached (origin/main)
		{Args: nil, Err: nil}, // GitCreateBranchFromHead (temp branch)
		{Args: nil, Err: nil}, // GitMergeRemote (origin/feature)
		{Args: nil, Err: nil}, // GitPushRefspec (temp:main)
		{Args: nil, Err: nil}, // GitDeleteBranch (cleanup temp, deferred)
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"log", "main..origin/feature", "--oneline"}, Stdout: "abc commit\n"}, // HasCommitsBetweenRemote
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

	err := pushBranchInRepoDetached("/repo", "feature", "main", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPushBranchInRepoDetached_AlreadyUpToDate(t *testing.T) {
	// When HasCommitsBetweenRemote returns false, the detached flow should
	// return early before creating the temp branch, without merging or pushing.
	outputStubs := []OutputCommandStub{
		{Args: nil, Err: nil}, // GitCheckoutDetached (origin/main)
		// No create branch, merge, or push - already up to date
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"log", "main..origin/feature", "--oneline"}, Stdout: ""}, // no commits
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

	err := pushBranchInRepoDetached("/repo", "feature", "main", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPushBranchInRepoDetached_MergeConflicts_InvokesClaude(t *testing.T) {
	// When merge fails with conflicts in the detached flow, Claude should be
	// invoked with a custom pushRef (HEAD:<targetBranch>).
	outputStubs := []OutputCommandStub{
		{Args: nil, Err: nil},                      // GitCheckoutDetached (origin/main)
		{Args: nil, Err: nil},                      // GitCreateBranchFromHead (temp branch)
		{Args: nil, Err: errors.New("CONFLICT")},   // GitMergeRemote fails with conflicts
		// No push - conflicts
		{Args: nil, Err: nil},                      // GitDeleteBranch (cleanup temp, deferred)
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"log", "main..origin/feature", "--oneline"}, Stdout: "abc commit\n"},    // HasCommitsBetweenRemote
		{Name: "git", Args: []string{"diff", "--name-only", "--diff-filter=U"}, Stdout: "file1.go\nfile2.go\n"}, // GetConflictedFiles
	}

	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.Install()
	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.Install()

	claudeCalled := false
	var capturedPrompt string
	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		claudeCalled = true
		capturedPrompt = prompt
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	err := pushBranchInRepoDetached("/repo", "feature", "main", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !claudeCalled {
		t.Error("expected claude to be invoked for conflict resolution")
	}
	// The detached flow should use "HEAD:main" as the push ref
	if !strings.Contains(capturedPrompt, "HEAD:main") {
		t.Errorf("expected prompt to contain 'HEAD:main' push ref, but it did not")
	}
}

func TestPushBranchInRepoDetached_CheckoutDetachedFails(t *testing.T) {
	// When GitCheckoutDetached fails, the function should return an error
	// No cleanup needed since temp branch was never created
	outputStubs := []OutputCommandStub{
		{Args: nil, Err: errors.New("checkout failed")}, // GitCheckoutDetached fails
	}

	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.Install()

	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	err := pushBranchInRepoDetached("/repo", "feature", "main", "")
	if err == nil {
		t.Error("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "detached") {
		t.Errorf("expected error to mention 'detached', got: %v", err)
	}
}

func TestPushBranchInRepoDetached_CustomRemote(t *testing.T) {
	// Test that custom remote is passed through to all git commands
	outputStubs := []OutputCommandStub{
		{Args: nil, Err: nil}, // GitCheckoutDetached (upstream/main)
		{Args: nil, Err: nil}, // GitCreateBranchFromHead (temp branch)
		{Args: nil, Err: nil}, // GitMergeRemote (upstream/feature)
		{Args: nil, Err: nil}, // GitPushRefspec (temp:main via upstream)
		{Args: nil, Err: nil}, // GitDeleteBranch (cleanup temp, deferred)
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"log", "main..upstream/feature", "--oneline"}, Stdout: "abc commit\n"}, // HasCommitsBetweenRemote
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

	err := pushBranchInRepoDetached("/repo", "feature", "main", "upstream")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPushBranch_WorktreeConflict_UsesDetached(t *testing.T) {
	// When IsRefCheckedOutInWorktree returns true in legacy pushBranch,
	// it should use pushBranchDetached instead of the normal checkout flow.
	worktreeListOutput := "worktree /home/user/project\nHEAD abc1234\nbranch refs/heads/main\n\n"

	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil}, // GitFetch
		{Args: []string{"stash"}, Err: nil},           // GitStash (no-op)
		// Detached flow:
		{Args: nil, Err: nil}, // GitCheckoutDetached (origin/main)
		{Args: nil, Err: nil}, // GitCreateBranchFromHead (temp branch)
		{Args: nil, Err: nil}, // GitMergeOrigin
		{Args: nil, Err: nil}, // GitPushRefspec (temp:main)
		{Args: nil, Err: nil}, // GitDeleteBranch (cleanup temp, deferred)
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                                  // getStashCount (before)
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                                  // getStashCount (after)
		{Name: "git", Args: []string{"worktree", "list", "--porcelain"}, Stdout: worktreeListOutput},                // IsRefCheckedOutInWorktree -> true for "main"
		{Name: "git", Args: []string{"log", "main..origin/feature/test", "--oneline"}, Stdout: "abc123 commit\n"},   // HasCommitsBetween
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

	// pushBranch doesn't return an error - it prints to stderr
	pushBranch("feature/test", "main")
}

func TestPushBranchDetached_AlreadyUpToDate(t *testing.T) {
	// Legacy pushBranchDetached should return early when no commits to merge
	// No temp branch is created when already up to date
	outputStubs := []OutputCommandStub{
		{Args: nil, Err: nil}, // GitCheckoutDetached (origin/main)
		// No create branch, merge, or push - already up to date
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"log", "main..origin/feature", "--oneline"}, Stdout: ""}, // HasCommitsBetween - no commits
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

	err := pushBranchDetached("/tmp/test-dir", "feature", "main")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPushBranchDetached_Success(t *testing.T) {
	// Full detached push flow in legacy mode (uses origin, HasCommitsBetween, GitMergeOrigin)
	outputStubs := []OutputCommandStub{
		{Args: nil, Err: nil}, // GitCheckoutDetached (origin/main)
		{Args: nil, Err: nil}, // GitCreateBranchFromHead (temp branch)
		{Args: nil, Err: nil}, // GitMergeOrigin (origin/feature)
		{Args: nil, Err: nil}, // GitPushRefspec (temp:main via origin)
		{Args: nil, Err: nil}, // GitDeleteBranch (cleanup temp, deferred)
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"log", "main..origin/feature", "--oneline"}, Stdout: "abc commit\n"}, // HasCommitsBetween
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

	err := pushBranchDetached("/tmp/test-dir", "feature", "main")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPushBranchDetached_MergeConflicts_InvokesClaude(t *testing.T) {
	// When merge fails with conflicts in the legacy detached flow,
	// Claude should be invoked with HEAD:<targetBranch> push ref
	outputStubs := []OutputCommandStub{
		{Args: nil, Err: nil},                    // GitCheckoutDetached (origin/main)
		{Args: nil, Err: nil},                    // GitCreateBranchFromHead (temp branch)
		{Args: nil, Err: errors.New("CONFLICT")}, // GitMergeOrigin fails
		// No push - conflicts
		{Args: nil, Err: nil},                    // GitDeleteBranch (cleanup temp, deferred)
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"log", "main..origin/feature", "--oneline"}, Stdout: "abc commit\n"},    // HasCommitsBetween
		{Name: "git", Args: []string{"diff", "--name-only", "--diff-filter=U"}, Stdout: "file1.go\nfile2.go\n"}, // GetConflictedFiles
	}

	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.Install()
	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.Install()

	claudeCalled := false
	var capturedPrompt string
	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		claudeCalled = true
		capturedPrompt = prompt
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	err := pushBranchDetached("/tmp/test-dir", "feature", "main")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !claudeCalled {
		t.Error("expected claude to be invoked for conflict resolution")
	}
	if !strings.Contains(capturedPrompt, "HEAD:main") {
		t.Errorf("expected prompt to contain 'HEAD:main' push ref, but it did not")
	}
}

func TestPushCmd_WorkspaceModeArgsValidation(t *testing.T) {
	// Save and restore the global flags
	origPushAll := pushAll
	origPushWorkspace := pushWorkspace
	defer func() {
		pushAll = origPushAll
		pushWorkspace = origPushWorkspace
	}()

	// Create a temp config directory with config.yaml to enable workspace mode
	tmpDir := t.TempDir()
	configDir := tmpDir + "/.loom"
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}

	configContent := `workspaces:
  test:
    path: /tmp/test
    repos:
      - name: repo1
        path: repo1
`
	configPath := configDir + "/config.yaml"
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	SetupTestEnv(t, map[string]string{
		"LOOM_CONFIG_DIR": configDir,
	})

	// Verify workspace mode is enabled
	if !IsWorkspaceMode() {
		t.Skip("workspace mode not enabled - config not loaded properly")
	}

	tests := []struct {
		name        string
		args        []string
		allFlag     bool
		wantError   bool
		errorSubstr string
	}{
		// Workspace mode with --all flag
		{
			name:      "workspace mode with --all, no args (success)",
			args:      []string{},
			allFlag:   true,
			wantError: false,
		},
		{
			name:      "workspace mode with --all, one arg (target branch, success)",
			args:      []string{"main"},
			allFlag:   true,
			wantError: false,
		},
		{
			name:        "workspace mode with --all, two args (error)",
			args:        []string{"main", "extra"},
			allFlag:     true,
			wantError:   true,
			errorSubstr: "--all flag accepts at most 1 argument",
		},
		// Workspace mode without --all flag
		{
			name:        "workspace mode without --all, no args",
			args:        []string{},
			allFlag:     false,
			wantError:   true,
			errorSubstr: "requires 1-2 arguments",
		},
		{
			name:      "workspace mode without --all, one arg (worktree only, success)",
			args:      []string{"falcon"},
			allFlag:   false,
			wantError: false,
		},
		{
			name:      "workspace mode without --all, two args (success)",
			args:      []string{"falcon", "main"},
			allFlag:   false,
			wantError: false,
		},
		{
			name:        "workspace mode without --all, three args",
			args:        []string{"falcon", "main", "extra"},
			allFlag:     false,
			wantError:   true,
			errorSubstr: "requires 1-2 arguments",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pushAll = tc.allFlag
			pushWorkspace = ""

			err := pushCmd.Args(pushCmd, tc.args)

			if tc.wantError {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tc.errorSubstr)
					return
				}
				if tc.errorSubstr != "" && !strings.Contains(err.Error(), tc.errorSubstr) {
					t.Errorf("expected error containing %q, got %q", tc.errorSubstr, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			}
		})
	}
}
