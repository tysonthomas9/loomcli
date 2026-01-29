package cli

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestSyncCmd_ArgsValidation(t *testing.T) {
	// Save and restore the global flag
	origSyncAll := syncAll
	defer func() { syncAll = origSyncAll }()

	tests := []struct {
		name      string
		args      []string
		allFlag   bool
		wantError bool
		errorMsg  string
	}{
		// Without --all flag: requires exactly 2 args (worktree, branch)
		{
			name:      "without --all, no args",
			args:      []string{},
			allFlag:   false,
			wantError: true,
			errorMsg:  "requires exactly 2 arguments",
		},
		{
			name:      "without --all, one arg",
			args:      []string{"falcon"},
			allFlag:   false,
			wantError: true,
			errorMsg:  "requires exactly 2 arguments",
		},
		{
			name:      "without --all, two args (success)",
			args:      []string{"falcon", "main"},
			allFlag:   false,
			wantError: false,
		},
		{
			name:      "without --all, three args",
			args:      []string{"falcon", "main", "extra"},
			allFlag:   false,
			wantError: true,
			errorMsg:  "requires exactly 2 arguments",
		},

		// With --all flag: requires exactly 1 arg (branch)
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
			syncAll = tc.allFlag

			// Call the Args validation function directly
			err := syncCmd.Args(syncCmd, tc.args)

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

func TestSyncWorktree(t *testing.T) {
	tests := []struct {
		name         string
		worktreeName string
		sourceBranch string
		outputStubs  []OutputCommandStub
		commandStubs []CommandStub
		claudeCalled bool
		claudeErr    error
	}{
		{
			name:         "successful sync no conflicts",
			worktreeName: "falcon",
			sourceBranch: "main",
			outputStubs: []OutputCommandStub{
				{Args: []string{"fetch", "origin"}, Err: nil},
				{Args: []string{"merge", "origin/main", "-m", "Sync with main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
				{Args: []string{"push", "origin", "falcon-branch"}, Err: nil},
			},
			commandStubs: []CommandStub{
				{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "falcon-branch\n"},
			},
		},
		{
			name:         "fetch fails",
			worktreeName: "falcon",
			sourceBranch: "main",
			outputStubs: []OutputCommandStub{
				{Args: []string{"fetch", "origin"}, Err: errors.New("network error")},
			},
			commandStubs: []CommandStub{
				{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "falcon-branch\n"},
			},
		},
		{
			name:         "merge with conflicts invokes claude",
			worktreeName: "falcon",
			sourceBranch: "main",
			outputStubs: []OutputCommandStub{
				{Args: []string{"fetch", "origin"}, Err: nil},
				{Args: []string{"merge", "origin/main", "-m", "Sync with main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: errors.New("CONFLICT")},
			},
			commandStubs: []CommandStub{
				{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "falcon-branch\n"},
				{Name: "git", Args: []string{"diff", "--name-only", "--diff-filter=U"}, Stdout: "file1.go\n"},
			},
			claudeCalled: true,
		},
		{
			name:         "merge fails no conflicts",
			worktreeName: "falcon",
			sourceBranch: "main",
			outputStubs: []OutputCommandStub{
				{Args: []string{"fetch", "origin"}, Err: nil},
				{Args: []string{"merge", "origin/main", "-m", "Sync with main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: errors.New("merge failed")},
			},
			commandStubs: []CommandStub{
				{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "falcon-branch\n"},
				{Name: "git", Args: []string{"diff", "--name-only", "--diff-filter=U"}, Stdout: ""},
			},
		},
		{
			name:         "push fails after successful sync",
			worktreeName: "falcon",
			sourceBranch: "main",
			outputStubs: []OutputCommandStub{
				{Args: []string{"fetch", "origin"}, Err: nil},
				{Args: []string{"merge", "origin/main", "-m", "Sync with main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
				{Args: []string{"push", "origin", "falcon-branch"}, Err: errors.New("push rejected")},
			},
			commandStubs: []CommandStub{
				{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "falcon-branch\n"},
			},
		},
		{
			name:         "conflicts with claude error",
			worktreeName: "falcon",
			sourceBranch: "main",
			outputStubs: []OutputCommandStub{
				{Args: []string{"fetch", "origin"}, Err: nil},
				{Args: []string{"merge", "origin/main", "-m", "Sync with main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: errors.New("CONFLICT")},
			},
			commandStubs: []CommandStub{
				{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "falcon-branch\n"},
				{Name: "git", Args: []string{"diff", "--name-only", "--diff-filter=U"}, Stdout: "file1.go\n"},
			},
			claudeCalled: true,
			claudeErr:    errors.New("claude failed"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Create a temp dir with the worktree
			tmpDir := t.TempDir()
			wtPath := tmpDir + "/" + tc.worktreeName
			if err := os.MkdirAll(wtPath+"/.git", 0755); err != nil {
				t.Fatalf("failed to create worktree: %v", err)
			}

			// Point LOOM_WORKTREES_DIR to our temp dir
			SetupTestEnv(t, map[string]string{
				"LOOM_WORKTREES_DIR": tmpDir,
			})

			// Install output command mock
			outputMock := NewOutputCommandMock(t, tc.outputStubs)
			outputMock.Install()

			// Install command mock
			cmdMock := NewCommandMock(t, tc.commandStubs)
			cmdMock.Install()

			// Mock claude invoker
			claudeCalled := false
			origClaude := claudeInvoker
			claudeInvoker = func(workDir, prompt, agentName string) error {
				claudeCalled = true
				return tc.claudeErr
			}
			t.Cleanup(func() { claudeInvoker = origClaude })

			syncWorktree(tc.worktreeName, tc.sourceBranch)

			if tc.claudeCalled && !claudeCalled {
				t.Error("expected claude to be invoked, but it was not")
			}
			if !tc.claudeCalled && claudeCalled {
				t.Error("expected claude NOT to be invoked, but it was")
			}
		})
	}
}

func TestSyncWorktree_NotFound(t *testing.T) {
	// Set LOOM_WORKTREES_DIR to a temp dir without the worktree
	tmpDir := t.TempDir()
	SetupTestEnv(t, map[string]string{
		"LOOM_WORKTREES_DIR": tmpDir,
	})

	// No mocks needed - should return before any git operations
	outputMock := NewOutputCommandMock(t, []OutputCommandStub{})
	outputMock.Install()

	// Should handle error gracefully
	syncWorktree("nonexistent", "main")
}

func TestSyncAllWorktrees(t *testing.T) {
	tests := []struct {
		name         string
		sourceBranch string
		worktrees    []WorktreeInfo
		outputStubs  []OutputCommandStub
		commandStubs []CommandStub
	}{
		{
			name:         "multiple worktrees",
			sourceBranch: "main",
			worktrees: []WorktreeInfo{
				{Name: "alpha", Branch: "alpha-branch"},
				{Name: "beta", Branch: "beta-branch"},
			},
			outputStubs: []OutputCommandStub{
				// First worktree sync: main -> alpha
				{Args: []string{"fetch", "origin"}, Err: nil},
				{Args: []string{"merge", "origin/main", "-m", "Sync with main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
				{Args: []string{"push", "origin", "alpha-branch"}, Err: nil},
				// Second worktree sync: main -> beta
				{Args: []string{"fetch", "origin"}, Err: nil},
				{Args: []string{"merge", "origin/main", "-m", "Sync with main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
				{Args: []string{"push", "origin", "beta-branch"}, Err: nil},
			},
			commandStubs: []CommandStub{
				// DiscoverWorktrees calls GetCurrentBranch for each
				{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "alpha-branch\n"},
				{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "beta-branch\n"},
				// syncWorktree calls GetCurrentBranch for alpha
				{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "alpha-branch\n"},
				// syncWorktree calls GetCurrentBranch for beta
				{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "beta-branch\n"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()

			// Create worktree directories
			for _, wt := range tc.worktrees {
				if err := os.MkdirAll(tmpDir+"/"+wt.Name+"/.git", 0755); err != nil {
					t.Fatalf("failed to create worktree dir: %v", err)
				}
			}

			SetupTestEnv(t, map[string]string{
				"LOOM_WORKTREES_DIR": tmpDir,
			})

			outputMock := NewOutputCommandMock(t, tc.outputStubs)
			outputMock.Install()

			cmdMock := NewCommandMock(t, tc.commandStubs)
			cmdMock.Install()

			// Mock claude
			origClaude := claudeInvoker
			claudeInvoker = func(workDir, prompt, agentName string) error {
				t.Error("unexpected claude invocation")
				return nil
			}
			t.Cleanup(func() { claudeInvoker = origClaude })

			syncAllWorktrees(tc.sourceBranch)
		})
	}
}

func TestSyncAllWorktrees_NoWorktrees(t *testing.T) {
	tmpDir := t.TempDir()

	// Create the worktrees dir but leave it empty
	if err := os.MkdirAll(tmpDir+"/worktrees", 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	SetupTestEnv(t, map[string]string{
		"LOOM_WORKTREES_DIR": tmpDir + "/worktrees",
	})

	// No mocks needed - should return early
	outputMock := NewOutputCommandMock(t, []OutputCommandStub{})
	outputMock.Install()

	syncAllWorktrees("main")
}
