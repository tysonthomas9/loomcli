//go:build ignore

package git

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
			} else if err != nil {
				t.Errorf("expected no error, got %v", err)
			}
		})
	}
}

func TestPushBranch(t *testing.T) {
	tests := []struct {
		name         string
		sourceBranch string
		targetBranch string
		outputStubs  []OutputCommandStub // for GitFetch, GitCheckout, GitPull, GitMerge, GitPush
		commandStubs []CommandStub       // for GetCurrentBranch, HasCommitsBetween, GetConflictedFiles
		claudeCalled bool                // whether claude should be invoked
		claudeErr    error               // error from claude invocation
	}{
		{
			name:         "successful push no conflicts",
			sourceBranch: "feature/test",
			targetBranch: "main",
			outputStubs: []OutputCommandStub{
				{Args: []string{"fetch", "origin"}, Err: nil},        // GitFetch
				{Args: []string{"stash"}, Err: nil},                  // GitStash (no-op)
				{Args: []string{"checkout", "main"}, Err: nil},       // GitCheckout
				{Args: []string{"pull", "origin", "main"}, Err: nil}, // GitPull
				{Args: []string{"merge", "-m", "Merge feature/test into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", "--", "feature/test"}, Err: nil}, // GitMerge
				{Args: []string{"push", "origin", "main"}, Err: nil},   // GitPush
				{Args: []string{"checkout", "feature/test"}, Err: nil}, // branch restore defer
			},
			commandStubs: []CommandStub{
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                              // getStashCount (before)
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                              // getStashCount (after, same = not stashed)
				{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature/test\n"},                     // GetCurrentBranch
				{Name: "git", Args: []string{"log", "main..feature/test", "--oneline"}, Stdout: "abc123 some commit\n"}, // HasCommitsBetween
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
				{Args: []string{"checkout", "feature/test"}, Err: nil}, // branch restore defer
			},
			commandStubs: []CommandStub{
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                          // getStashCount (before)
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                          // getStashCount (after)
				{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature/test\n"}, // GetCurrentBranch
				{Name: "git", Args: []string{"log", "main..feature/test", "--oneline"}, Stdout: ""}, // no commits
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
				{Args: []string{"checkout", "feature/test"}, Err: nil}, // branch restore defer
			},
			commandStubs: []CommandStub{
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                          // getStashCount (before)
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                          // getStashCount (after)
				{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature/test\n"}, // GetCurrentBranch
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
				{Args: []string{"checkout", "feature/test"}, Err: nil}, // branch restore defer
			},
			commandStubs: []CommandStub{
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                          // getStashCount (before)
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                          // getStashCount (after)
				{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature/test\n"}, // GetCurrentBranch
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
				{Args: []string{"merge", "-m", "Merge feature/test into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", "--", "feature/test"}, Err: errors.New("CONFLICT")},
				// No push after conflicts
				{Args: []string{"checkout", "feature/test"}, Err: nil}, // branch restore defer
			},
			commandStubs: []CommandStub{
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                          // getStashCount (before)
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                          // getStashCount (after)
				{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature/test\n"}, // GetCurrentBranch
				{Name: "git", Args: []string{"log", "main..feature/test", "--oneline"}, Stdout: "abc123 commit\n"},
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
				{Args: []string{"merge", "-m", "Merge feature/test into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", "--", "feature/test"}, Err: errors.New("merge failed")},
				{Args: []string{"checkout", "feature/test"}, Err: nil}, // branch restore defer
			},
			commandStubs: []CommandStub{
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                          // getStashCount (before)
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                          // getStashCount (after)
				{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature/test\n"}, // GetCurrentBranch
				{Name: "git", Args: []string{"log", "main..feature/test", "--oneline"}, Stdout: "abc123 commit\n"},
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
				{Args: []string{"merge", "-m", "Merge feature/test into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", "--", "feature/test"}, Err: nil},
				{Args: []string{"push", "origin", "main"}, Err: errors.New("push rejected")},
				{Args: []string{"checkout", "feature/test"}, Err: nil}, // branch restore defer
			},
			commandStubs: []CommandStub{
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                          // getStashCount (before)
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                          // getStashCount (after)
				{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature/test\n"}, // GetCurrentBranch
				{Name: "git", Args: []string{"log", "main..feature/test", "--oneline"}, Stdout: "abc123 commit\n"},
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
				{Args: []string{"merge", "-m", "Merge feature/test into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", "--", "feature/test"}, Err: errors.New("CONFLICT")},
				{Args: []string{"checkout", "feature/test"}, Err: nil}, // branch restore defer
			},
			commandStubs: []CommandStub{
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                          // getStashCount (before)
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                          // getStashCount (after)
				{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature/test\n"}, // GetCurrentBranch
				{Name: "git", Args: []string{"log", "main..feature/test", "--oneline"}, Stdout: "abc123 commit\n"},
				{Name: "git", Args: []string{"diff", "--name-only", "--diff-filter=U"}, Stdout: "file1.go\n"},
			},
			claudeCalled: true,
			claudeErr:    errors.New("claude failed"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)

			// Install command mock (captured git commands) - must be before output mock
			if len(tc.commandStubs) > 0 {
				cmdMock := NewCommandMock(t, tc.commandStubs)
				cmdMock.InstallOn(deps)
			}

			// Install output command mock (streaming git commands)
			outputMock := NewOutputCommandMock(t, tc.outputStubs)
			outputMock.InstallOn(deps)

			// Mock claude invoker
			claudeCalled := false
			deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
				claudeCalled = true
				return tc.claudeErr
			}}

			// Call the function under test
			pushBranch(deps, tc.sourceBranch, tc.targetBranch)

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
				{Args: []string{"merge", "-m", "Merge alpha-branch into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", "--", "alpha-branch"}, Err: nil},
				{Args: []string{"push", "origin", "main"}, Err: nil},
				{Args: []string{"checkout", "alpha-branch"}, Err: nil}, // branch restore defer
				// Second worktree push: beta-branch -> main
				{Args: []string{"fetch", "origin"}, Err: nil},
				{Args: []string{"stash"}, Err: nil},
				{Args: []string{"checkout", "main"}, Err: nil},
				{Args: []string{"pull", "origin", "main"}, Err: nil},
				{Args: []string{"merge", "-m", "Merge beta-branch into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", "--", "beta-branch"}, Err: nil},
				{Args: []string{"push", "origin", "main"}, Err: nil},
				{Args: []string{"checkout", "beta-branch"}, Err: nil}, // branch restore defer
			},
			commandStubs: []CommandStub{
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                          // getStashCount before (alpha)
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                          // getStashCount after (alpha)
				{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "alpha-branch\n"}, // GetCurrentBranch (alpha)
				{Name: "git", Args: []string{"log", "main..alpha-branch", "--oneline"}, Stdout: "abc commit\n"},
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                         // getStashCount before (beta)
				{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                         // getStashCount after (beta)
				{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "beta-branch\n"}, // GetCurrentBranch (beta)
				{Name: "git", Args: []string{"log", "main..beta-branch", "--oneline"}, Stdout: "def commit\n"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Uses SetupTestEnv + DiscoverWorktrees (global) - no t.Parallel()

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

			// Install command mock globally - DiscoverWorktrees uses global state
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

			// Install output command mock globally
			outputMock := NewOutputCommandMock(t, tc.outputStubs)
			outputMock.Install()

			// Mock claude invoker
			installClaudeInvokerMock(t, func(workDir, prompt, agentName string) error {
				t.Error("unexpected claude invocation")
				return nil
			})

			pushAllWorktrees(defaultDeps, tc.targetBranch)
		})
	}
}

func TestTargetBranchDisplay(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

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
		// repo-a: fetch, stash, checkout, pull, merge, push, restore
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"stash"}, Err: nil},
		{Args: []string{"checkout", "main"}, Err: nil},
		{Args: []string{"pull", "origin", "main"}, Err: nil},
		{Args: []string{"merge", "-m", "Merge feat-a into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", "--", "feat-a"}, Err: nil},
		{Args: []string{"push", "origin", "main"}, Err: nil},
		{Args: []string{"checkout", "feat-a"}, Err: nil}, // branch restore defer
		// repo-b: fetch, stash, checkout, pull, merge, push, restore
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"stash"}, Err: nil},
		{Args: []string{"checkout", "main"}, Err: nil},
		{Args: []string{"pull", "origin", "main"}, Err: nil},
		{Args: []string{"merge", "-m", "Merge feat-b into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", "--", "feat-b"}, Err: nil},
		{Args: []string{"push", "origin", "main"}, Err: nil},
		{Args: []string{"checkout", "feat-b"}, Err: nil}, // branch restore defer
	}

	commandStubs := []CommandStub{
		// getStashCount + GetCurrentBranch + HasCommitsBetweenRemote for repo-a
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feat-a\n"},
		{Name: "git", Args: []string{"log", "origin/main..feat-a", "--oneline"}, Stdout: "abc commit\n"},
		// getStashCount + GetCurrentBranch + HasCommitsBetweenRemote for repo-b
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feat-b\n"},
		{Name: "git", Args: []string{"log", "origin/main..feat-b", "--oneline"}, Stdout: "def commit\n"},
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	// sourceBranch="" means use wt.Branch for each; targetBranch="main" is explicit
	pushWorkspaceWorktrees(deps, worktrees, "", "main")
}

func TestPushWorkspaceWorktrees_UsesPerRepoDefaultBranch(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

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
		{Args: []string{"merge", "-m", "Merge feat-a into develop\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", "--", "feat-a"}, Err: nil},
		{Args: []string{"push", "origin", "develop"}, Err: nil},
		{Args: []string{"checkout", "feat-a"}, Err: nil}, // branch restore defer
		// repo-b pushes into "staging"
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"stash"}, Err: nil},
		{Args: []string{"checkout", "staging"}, Err: nil},
		{Args: []string{"pull", "origin", "staging"}, Err: nil},
		{Args: []string{"merge", "-m", "Merge feat-b into staging\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", "--", "feat-b"}, Err: nil},
		{Args: []string{"push", "origin", "staging"}, Err: nil},
		{Args: []string{"checkout", "feat-b"}, Err: nil}, // branch restore defer
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feat-a\n"},
		{Name: "git", Args: []string{"log", "origin/develop..feat-a", "--oneline"}, Stdout: "abc commit\n"},
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feat-b\n"},
		{Name: "git", Args: []string{"log", "origin/staging..feat-b", "--oneline"}, Stdout: "def commit\n"},
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	// targetBranch="" means use per-repo DefaultBranch
	pushWorkspaceWorktrees(deps, worktrees, "", "")
}

func TestPushWorkspaceWorktrees_CLIArgOverridesConfig(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

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
		{Args: []string{"merge", "-m", "Merge feat-a into release\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", "--", "feat-a"}, Err: nil},
		{Args: []string{"push", "origin", "release"}, Err: nil},
		{Args: []string{"checkout", "feat-a"}, Err: nil}, // branch restore defer
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feat-a\n"},
		{Name: "git", Args: []string{"log", "origin/release..feat-a", "--oneline"}, Stdout: "abc commit\n"},
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	pushWorkspaceWorktrees(deps, worktrees, "", "release")
}

func TestPushWorkspaceWorktrees_CustomRemote(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

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
		{Args: []string{"merge", "-m", "Merge feat-a into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", "--", "feat-a"}, Err: nil},
		{Args: []string{"push", "upstream", "main"}, Err: nil},
		{Args: []string{"checkout", "feat-a"}, Err: nil}, // branch restore defer
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feat-a\n"},
		{Name: "git", Args: []string{"log", "upstream/main..feat-a", "--oneline"}, Stdout: "abc commit\n"},
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	pushWorkspaceWorktrees(deps, worktrees, "", "main")
}

func TestRunPush_LegacyMode(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	// When no workspace config exists, runPush falls through to legacy pushBranch.
	// We test the legacy path by calling pushBranch directly.
	// This test verifies the same git command sequence with hardcoded "origin".

	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"stash"}, Err: nil},
		{Args: []string{"checkout", "main"}, Err: nil},
		{Args: []string{"pull", "origin", "main"}, Err: nil},
		{Args: []string{"merge", "-m", "Merge feature/test into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", "--", "feature/test"}, Err: nil},
		{Args: []string{"push", "origin", "main"}, Err: nil},
		{Args: []string{"checkout", "feature/test"}, Err: nil}, // branch restore defer
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature/test\n"},
		{Name: "git", Args: []string{"log", "main..feature/test", "--oneline"}, Stdout: "abc123 commit\n"},
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	pushBranch(deps, "feature/test", "main")
}

func TestPushWorkspaceWorktrees_SkipsNilRepo(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

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
		{Args: []string{"merge", "-m", "Merge feat-b into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", "--", "feat-b"}, Err: nil},
		{Args: []string{"push", "origin", "main"}, Err: nil},
		{Args: []string{"checkout", "feat-b"}, Err: nil}, // branch restore defer
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feat-b\n"},
		{Name: "git", Args: []string{"log", "origin/main..feat-b", "--oneline"}, Stdout: "def commit\n"},
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	pushWorkspaceWorktrees(deps, worktrees, "", "main")
}

func TestPushBranch_DirtyWorkingTree_StashesAndPops(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	// Test that when working tree is dirty, pushBranch stashes before checkout
	// and pops stash after merge completes successfully
	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"stash"}, Err: nil}, // GitStash
		{Args: []string{"checkout", "main"}, Err: nil},
		{Args: []string{"pull", "origin", "main"}, Err: nil},
		{Args: []string{"merge", "-m", "Merge feature/test into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", "--", "feature/test"}, Err: nil},
		{Args: []string{"push", "origin", "main"}, Err: nil},
		{Args: []string{"checkout", "feature/test"}, Err: nil}, // branch restore defer (runs first, LIFO)
		{Args: []string{"stash", "pop"}, Err: nil},             // GitStashPop (runs second, LIFO)
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                  // getStashCount (before, 0)
		{Name: "git", Args: []string{"stash", "list"}, Stdout: "stash@{0}: WIP on main: abc1234\n"}, // getStashCount (after, 1 = stashed)
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature/test\n"},         // GetCurrentBranch
		{Name: "git", Args: []string{"log", "main..feature/test", "--oneline"}, Stdout: "abc123 commit\n"},
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	pushBranch(deps, "feature/test", "main")
}

func TestPushBranch_StashPopConflicts_WarnsButSucceeds(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	// Test that when stash pop fails due to conflicts, a warning is printed
	// but the merge itself succeeds (no error returned to caller)
	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"stash"}, Err: nil},
		{Args: []string{"checkout", "main"}, Err: nil},
		{Args: []string{"pull", "origin", "main"}, Err: nil},
		{Args: []string{"merge", "-m", "Merge feature/test into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", "--", "feature/test"}, Err: nil},
		{Args: []string{"push", "origin", "main"}, Err: nil},
		{Args: []string{"checkout", "feature/test"}, Err: nil},                         // branch restore defer (runs first, LIFO)
		{Args: []string{"stash", "pop"}, Err: errors.New("conflict during stash pop")}, // stash pop fails (runs second, LIFO)
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                  // getStashCount (before)
		{Name: "git", Args: []string{"stash", "list"}, Stdout: "stash@{0}: WIP on main: abc1234\n"}, // getStashCount (after, stashed)
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature/test\n"},         // GetCurrentBranch
		{Name: "git", Args: []string{"log", "main..feature/test", "--oneline"}, Stdout: "abc123 commit\n"},
		{Name: "git", Args: []string{"diff", "--name-only", "--diff-filter=U"}, Stdout: "dirty.go\n"}, // HasUnmergedFiles returns true
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	// pushBranch doesn't return an error, it just prints to stderr
	// The test verifies the correct sequence of commands is called
	pushBranch(deps, "feature/test", "main")
}

func TestPushBranch_StashFails_ReturnsEarly(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

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

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	// pushBranch prints error and returns early - no panic
	pushBranch(deps, "feature/test", "main")
}

func TestPushBranchInRepo_DirtyWorkingTree_StashesAndPops(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	// Test that pushBranchInRepo stashes dirty changes and pops after merge
	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"stash"}, Err: nil},
		{Args: []string{"checkout", "main"}, Err: nil},
		{Args: []string{"pull", "origin", "main"}, Err: nil},
		{Args: []string{"merge", "-m", "Merge feature into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", "--", "feature"}, Err: nil},
		{Args: []string{"push", "origin", "main"}, Err: nil},
		{Args: []string{"checkout", "feature"}, Err: nil}, // branch restore defer (runs first, LIFO)
		{Args: []string{"stash", "pop"}, Err: nil},        // stash pop (runs second, LIFO)
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                  // getStashCount (before)
		{Name: "git", Args: []string{"stash", "list"}, Stdout: "stash@{0}: WIP on main: abc1234\n"}, // getStashCount (after, stashed)
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature\n"},              // GetCurrentBranch
		{Name: "git", Args: []string{"log", "origin/main..feature", "--oneline"}, Stdout: "abc commit\n"},
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	err := pushBranchInRepo(deps, "/repo", "feature", "main", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPushBranchInRepo_StashFails_ReturnsError(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	// Test that when GitStash fails, pushBranchInRepo returns the error
	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"stash"}, Err: errors.New("stash failed")},
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""}, // getStashCount (before)
		// No second stash list - git stash command fails
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	err := pushBranchInRepo(deps, "/repo", "feature", "main", "")
	if err == nil {
		t.Error("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "stashing") {
		t.Errorf("expected error to mention stashing, got: %v", err)
	}
}

func TestPushBranchInRepo_StashPopConflicts_WarnsButSucceeds(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	// Test that stash pop conflicts produce warning but don't fail the merge
	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"stash"}, Err: nil},
		{Args: []string{"checkout", "main"}, Err: nil},
		{Args: []string{"pull", "origin", "main"}, Err: nil},
		{Args: []string{"merge", "-m", "Merge feature into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", "--", "feature"}, Err: nil},
		{Args: []string{"push", "origin", "main"}, Err: nil},
		{Args: []string{"checkout", "feature"}, Err: nil},             // branch restore defer (runs first, LIFO)
		{Args: []string{"stash", "pop"}, Err: errors.New("conflict")}, // stash pop (runs second, LIFO)
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                  // getStashCount (before)
		{Name: "git", Args: []string{"stash", "list"}, Stdout: "stash@{0}: WIP on main: abc1234\n"}, // getStashCount (after, stashed)
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature\n"},              // GetCurrentBranch
		{Name: "git", Args: []string{"log", "origin/main..feature", "--oneline"}, Stdout: "abc commit\n"},
		{Name: "git", Args: []string{"diff", "--name-only", "--diff-filter=U"}, Stdout: "file.go\n"}, // HasUnmergedFiles
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	// Should succeed despite stash pop conflict
	err := pushBranchInRepo(deps, "/repo", "feature", "main", "")
	if err != nil {
		t.Errorf("expected success despite stash pop conflict, got: %v", err)
	}
}

func TestPushBranchInRepo_CleanWorkingTree_NoStash(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	// Test that clean working tree skips stash pop (stash count unchanged)
	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"stash"}, Err: nil}, // git stash runs but is no-op
		{Args: []string{"checkout", "main"}, Err: nil},
		{Args: []string{"pull", "origin", "main"}, Err: nil},
		{Args: []string{"merge", "-m", "Merge feature into main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", "--", "feature"}, Err: nil},
		{Args: []string{"push", "origin", "main"}, Err: nil},
		{Args: []string{"checkout", "feature"}, Err: nil}, // branch restore defer
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""}, // getStashCount (before)
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""}, // getStashCount (after, same = not stashed)
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature\n"},
		{Name: "git", Args: []string{"log", "origin/main..feature", "--oneline"}, Stdout: "abc commit\n"},
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	err := pushBranchInRepo(deps, "/repo", "feature", "main", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPushCmd_MergeAlias(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	// Verify command is in the "git" group
	if pushCmd.GroupID != "git" {
		t.Errorf("expected push command to be in 'git' group, got %q", pushCmd.GroupID)
	}
}

func TestPushCmd_Flags(t *testing.T) {
	t.Parallel()
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
	// Uses SetupTestEnv (env mutation) - no t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

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
	cmdMock := NewCommandMock(t, []CommandStub{})
	cmdMock.InstallOn(deps)
	outputMock.InstallOn(deps)

	// Should not panic when no worktrees
	pushAllWorktrees(deps, "main")
}

func TestPushWorkspaceWorktrees_EmptyList(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	// Empty worktree list should produce no errors and no git commands
	worktrees := []WorktreeInfo{}

	// No output stubs - no commands should be called
	cmdMock := NewCommandMock(t, []CommandStub{})
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, []OutputCommandStub{})
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	// Should not panic or call any commands
	pushWorkspaceWorktrees(deps, worktrees, "", "main")
}

func TestPushBranchInRepo_WorktreeConflict_UsesDetached(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	// When checkout fails because the target branch is checked out in another
	// worktree, pushBranchInRepo should fall back to the detached HEAD approach.
	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil}, // GitFetchRemote
		{Args: []string{"stash"}, Err: nil},           // GitStash (no-op)
		{Args: []string{"checkout", "main"}, Err: errors.New("fatal: 'main' is already used by worktree at '/home/user/project'")}, // GitCheckout fails → fallback
		{Args: nil, Err: nil}, // GitCheckoutDetached (origin/main)
		{Args: nil, Err: nil}, // GitCreateBranchFromHead (temp branch)
		{Args: nil, Err: nil}, // GitMerge
		{Args: nil, Err: nil}, // GitPushRefspec (temp:main)
		{Args: nil, Err: nil}, // GitDeleteBranch (cleanup temp, detached defer LIFO)
		{Args: nil, Err: nil}, // GitCheckout (source, detached defer LIFO)
		{Args: nil, Err: nil}, // GitCheckout (restore original, caller defer)
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                        // getStashCount (before)
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                        // getStashCount (after, same = not stashed)
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature\n"},                    // GetCurrentBranch
		{Name: "git", Args: []string{"log", "origin/main..feature", "--oneline"}, Stdout: "abc commit\n"}, // HasCommitsBetweenRemote
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	err := pushBranchInRepo(deps, "/repo", "feature", "main", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPushBranchInRepoDetached_Success(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	// Test the full detached push flow:
	// checkout detached -> create temp branch -> merge -> push refspec -> cleanup
	outputStubs := []OutputCommandStub{
		{Args: nil, Err: nil}, // GitCheckoutDetached (origin/main)
		{Args: nil, Err: nil}, // GitCreateBranchFromHead (temp branch)
		{Args: nil, Err: nil}, // GitMerge (feature)
		{Args: nil, Err: nil}, // GitPushRefspec (temp:main)
		{Args: nil, Err: nil}, // GitDeleteBranch (cleanup temp, defer LIFO - runs first)
		{Args: nil, Err: nil}, // GitCheckout (source, defer LIFO - runs second)
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"log", "origin/main..feature", "--oneline"}, Stdout: "abc commit\n"}, // HasCommitsBetweenRemote
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	err := pushBranchInRepoDetached(deps, "/repo", "feature", "main", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPushBranchInRepoDetached_AlreadyUpToDate(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	// When HasCommitsBetweenRemote returns false, the detached flow should
	// return early before creating the temp branch, without merging or pushing.
	outputStubs := []OutputCommandStub{
		{Args: nil, Err: nil}, // GitCheckoutDetached (origin/main)
		// No create branch, merge, or push - already up to date
		{Args: nil, Err: nil}, // GitCheckout (source, defer - always runs)
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"log", "origin/main..feature", "--oneline"}, Stdout: ""}, // no commits
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	err := pushBranchInRepoDetached(deps, "/repo", "feature", "main", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPushBranchInRepoDetached_MergeConflicts_InvokesClaude(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	// When merge fails with conflicts in the detached flow, Claude should be
	// invoked with a custom pushRef (HEAD:<targetBranch>).
	outputStubs := []OutputCommandStub{
		{Args: nil, Err: nil},                    // GitCheckoutDetached (origin/main)
		{Args: nil, Err: nil},                    // GitCreateBranchFromHead (temp branch)
		{Args: nil, Err: errors.New("CONFLICT")}, // GitMerge fails with conflicts
		// No push - conflicts
		{Args: nil, Err: nil}, // GitDeleteBranch (cleanup temp, defer LIFO - runs first)
		{Args: nil, Err: nil}, // GitCheckout (source, defer LIFO - runs second)
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"log", "origin/main..feature", "--oneline"}, Stdout: "abc commit\n"},       // HasCommitsBetweenRemote
		{Name: "git", Args: []string{"diff", "--name-only", "--diff-filter=U"}, Stdout: "file1.go\nfile2.go\n"}, // GetConflictedFiles
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	claudeCalled := false
	var capturedPrompt string
	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		claudeCalled = true
		capturedPrompt = prompt
		return nil
	}}

	err := pushBranchInRepoDetached(deps, "/repo", "feature", "main", "")
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
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	// When GitCheckoutDetached fails, the function should return an error
	// No cleanup needed since temp branch was never created
	outputStubs := []OutputCommandStub{
		{Args: nil, Err: errors.New("checkout failed")}, // GitCheckoutDetached fails
	}

	cmdMock := NewCommandMock(t, []CommandStub{})
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	err := pushBranchInRepoDetached(deps, "/repo", "feature", "main", "")
	if err == nil {
		t.Error("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "detached") {
		t.Errorf("expected error to mention 'detached', got: %v", err)
	}
}

func TestPushBranchInRepoDetached_CustomRemote(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	// Test that custom remote is passed through to all git commands
	outputStubs := []OutputCommandStub{
		{Args: nil, Err: nil}, // GitCheckoutDetached (upstream/main)
		{Args: nil, Err: nil}, // GitCreateBranchFromHead (temp branch)
		{Args: nil, Err: nil}, // GitMerge (feature)
		{Args: nil, Err: nil}, // GitPushRefspec (temp:main via upstream)
		{Args: nil, Err: nil}, // GitDeleteBranch (cleanup temp, defer LIFO - runs first)
		{Args: nil, Err: nil}, // GitCheckout (source, defer LIFO - runs second)
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"log", "upstream/main..feature", "--oneline"}, Stdout: "abc commit\n"}, // HasCommitsBetweenRemote
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	err := pushBranchInRepoDetached(deps, "/repo", "feature", "main", "upstream")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPushBranch_WorktreeConflict_UsesDetached(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	// When checkout fails because the target branch is checked out in another
	// worktree, pushBranch should fall back to pushBranchDetached.
	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil}, // GitFetch
		{Args: []string{"stash"}, Err: nil},           // GitStash (no-op)
		{Args: []string{"checkout", "main"}, Err: errors.New("fatal: 'main' is already used by worktree at '/home/user/project'")}, // GitCheckout fails → fallback
		// Detached flow:
		{Args: nil, Err: nil}, // GitCheckoutDetached (origin/main)
		{Args: nil, Err: nil}, // GitCreateBranchFromHead (temp branch)
		{Args: nil, Err: nil}, // GitMerge
		{Args: nil, Err: nil}, // GitPushRefspec (temp:main)
		{Args: nil, Err: nil}, // GitDeleteBranch (cleanup temp, detached defer LIFO)
		{Args: nil, Err: nil}, // GitCheckout (source, detached defer LIFO)
		{Args: nil, Err: nil}, // GitCheckout (restore original, caller defer)
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                         // getStashCount (before)
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                         // getStashCount (after)
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature/test\n"},                // GetCurrentBranch
		{Name: "git", Args: []string{"log", "main..feature/test", "--oneline"}, Stdout: "abc123 commit\n"}, // HasCommitsBetween
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	// pushBranch doesn't return an error - it prints to stderr
	pushBranch(deps, "feature/test", "main")
}

func TestPushBranchDetached_AlreadyUpToDate(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	// Legacy pushBranchDetached should return early when no commits to merge
	// No temp branch is created when already up to date
	outputStubs := []OutputCommandStub{
		{Args: nil, Err: nil}, // GitCheckoutDetached (origin/main)
		// No create branch, merge, or push - already up to date
		{Args: nil, Err: nil}, // GitCheckout (source, defer - always runs)
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"log", "main..feature", "--oneline"}, Stdout: ""}, // HasCommitsBetween - no commits
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	err := pushBranchDetached(deps, "/tmp/test-dir", "feature", "main")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPushBranchDetached_Success(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	// Full detached push flow in legacy mode (uses origin, HasCommitsBetween, GitMerge)
	outputStubs := []OutputCommandStub{
		{Args: nil, Err: nil}, // GitCheckoutDetached (origin/main)
		{Args: nil, Err: nil}, // GitCreateBranchFromHead (temp branch)
		{Args: nil, Err: nil}, // GitMerge (feature)
		{Args: nil, Err: nil}, // GitPushRefspec (temp:main via origin)
		{Args: nil, Err: nil}, // GitDeleteBranch (cleanup temp, defer LIFO - runs first)
		{Args: nil, Err: nil}, // GitCheckout (source, defer LIFO - runs second)
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"log", "main..feature", "--oneline"}, Stdout: "abc commit\n"}, // HasCommitsBetween
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	err := pushBranchDetached(deps, "/tmp/test-dir", "feature", "main")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPushBranchDetached_MergeConflicts_InvokesClaude(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	// When merge fails with conflicts in the legacy detached flow,
	// Claude should be invoked with HEAD:<targetBranch> push ref
	outputStubs := []OutputCommandStub{
		{Args: nil, Err: nil},                    // GitCheckoutDetached (origin/main)
		{Args: nil, Err: nil},                    // GitCreateBranchFromHead (temp branch)
		{Args: nil, Err: errors.New("CONFLICT")}, // GitMerge fails
		// No push - conflicts
		{Args: nil, Err: nil}, // GitDeleteBranch (cleanup temp, defer LIFO - runs first)
		{Args: nil, Err: nil}, // GitCheckout (source, defer LIFO - runs second)
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"log", "main..feature", "--oneline"}, Stdout: "abc commit\n"},              // HasCommitsBetween
		{Name: "git", Args: []string{"diff", "--name-only", "--diff-filter=U"}, Stdout: "file1.go\nfile2.go\n"}, // GetConflictedFiles
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	claudeCalled := false
	var capturedPrompt string
	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		claudeCalled = true
		capturedPrompt = prompt
		return nil
	}}

	err := pushBranchDetached(deps, "/tmp/test-dir", "feature", "main")
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

func TestPushBranchInRepo_CheckoutAlreadyCheckedOut_UsesDetached(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	// When checkout fails with the alternate "already checked out" message
	// (instead of "already used by worktree"), pushBranchInRepo should still
	// fall back to the detached HEAD approach.
	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil}, // GitFetchRemote
		{Args: []string{"stash"}, Err: nil},           // GitStash (no-op)
		{Args: []string{"checkout", "main"}, Err: errors.New("fatal: 'main' is already checked out at '/home/user/other'")}, // GitCheckout fails → fallback
		{Args: nil, Err: nil}, // GitCheckoutDetached (origin/main)
		{Args: nil, Err: nil}, // GitCreateBranchFromHead (temp branch)
		{Args: nil, Err: nil}, // GitMerge
		{Args: nil, Err: nil}, // GitPushRefspec (temp:main)
		{Args: nil, Err: nil}, // GitDeleteBranch (cleanup temp, detached defer LIFO)
		{Args: nil, Err: nil}, // GitCheckout (source, detached defer LIFO)
		{Args: nil, Err: nil}, // GitCheckout (restore original, caller defer)
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                        // getStashCount (before)
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                        // getStashCount (after, same = not stashed)
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature\n"},                    // GetCurrentBranch
		{Name: "git", Args: []string{"log", "origin/main..feature", "--oneline"}, Stdout: "abc commit\n"}, // HasCommitsBetweenRemote
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	err := pushBranchInRepo(deps, "/repo", "feature", "main", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPushBranch_CheckoutAlreadyCheckedOut_UsesDetached(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	// When checkout fails with the alternate "already checked out" message,
	// pushBranch should fall back to pushBranchDetached.
	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil}, // GitFetch
		{Args: []string{"stash"}, Err: nil},           // GitStash (no-op)
		{Args: []string{"checkout", "main"}, Err: errors.New("fatal: 'main' is already checked out at '/home/user/other'")}, // GitCheckout fails → fallback
		// Detached flow:
		{Args: nil, Err: nil}, // GitCheckoutDetached (origin/main)
		{Args: nil, Err: nil}, // GitCreateBranchFromHead (temp branch)
		{Args: nil, Err: nil}, // GitMerge
		{Args: nil, Err: nil}, // GitPushRefspec (temp:main)
		{Args: nil, Err: nil}, // GitDeleteBranch (cleanup temp, detached defer LIFO)
		{Args: nil, Err: nil}, // GitCheckout (source, detached defer LIFO)
		{Args: nil, Err: nil}, // GitCheckout (restore original, caller defer)
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                         // getStashCount (before)
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                         // getStashCount (after)
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "feature/test\n"},                // GetCurrentBranch
		{Name: "git", Args: []string{"log", "main..feature/test", "--oneline"}, Stdout: "abc123 commit\n"}, // HasCommitsBetween
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	// pushBranch doesn't return an error - it prints to stderr
	pushBranch(deps, "feature/test", "main")
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
			} else if err != nil {
				t.Errorf("expected no error, got %v", err)
			}
		})
	}
}
