package cli

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestPullCmd_ArgsValidation(t *testing.T) {
	// Save and restore the global flag
	origPullAll := pullAll
	defer func() { pullAll = origPullAll }()

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
			pullAll = tc.allFlag

			// Call the Args validation function directly
			err := pullCmd.Args(pullCmd, tc.args)

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

func TestPullWorktree(t *testing.T) {
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
			name:         "successful pull no conflicts",
			worktreeName: "falcon",
			sourceBranch: "main",
			outputStubs: []OutputCommandStub{
				{Args: []string{"fetch", "origin"}, Err: nil},
				{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
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
				{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: errors.New("CONFLICT")},
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
				{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: errors.New("merge failed")},
			},
			commandStubs: []CommandStub{
				{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "falcon-branch\n"},
				{Name: "git", Args: []string{"diff", "--name-only", "--diff-filter=U"}, Stdout: ""},
			},
		},
		{
			name:         "push fails after successful pull",
			worktreeName: "falcon",
			sourceBranch: "main",
			outputStubs: []OutputCommandStub{
				{Args: []string{"fetch", "origin"}, Err: nil},
				{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
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
				{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: errors.New("CONFLICT")},
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

			pullWorktree(tc.worktreeName, tc.sourceBranch)

			if tc.claudeCalled && !claudeCalled {
				t.Error("expected claude to be invoked, but it was not")
			}
			if !tc.claudeCalled && claudeCalled {
				t.Error("expected claude NOT to be invoked, but it was")
			}
		})
	}
}

func TestPullWorktree_NotFound(t *testing.T) {
	// Set LOOM_WORKTREES_DIR to a temp dir without the worktree
	tmpDir := t.TempDir()
	SetupTestEnv(t, map[string]string{
		"LOOM_WORKTREES_DIR": tmpDir,
	})

	// No mocks needed - should return before any git operations
	outputMock := NewOutputCommandMock(t, []OutputCommandStub{})
	outputMock.Install()

	// Should handle error gracefully
	pullWorktree("nonexistent", "main")
}

func TestPullAllWorktrees(t *testing.T) {
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
				// First worktree pull: main -> alpha
				{Args: []string{"fetch", "origin"}, Err: nil},
				{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
				{Args: []string{"push", "origin", "alpha-branch"}, Err: nil},
				// Second worktree pull: main -> beta
				{Args: []string{"fetch", "origin"}, Err: nil},
				{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
				{Args: []string{"push", "origin", "beta-branch"}, Err: nil},
			},
			commandStubs: []CommandStub{
				// DiscoverWorktrees calls GetCurrentBranch for each
				{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "alpha-branch\n"},
				{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "beta-branch\n"},
				// pullWorktree calls GetCurrentBranch for alpha
				{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "alpha-branch\n"},
				// pullWorktree calls GetCurrentBranch for beta
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

			pullAllWorktrees(tc.sourceBranch)
		})
	}
}

func TestPullAllWorktrees_NoWorktrees(t *testing.T) {
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

	pullAllWorktrees("main")
}

func TestSourceBranchDisplay(t *testing.T) {
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
			got := sourceBranchDisplay(tc.input)
			if got != tc.expect {
				t.Errorf("sourceBranchDisplay(%q) = %q, want %q", tc.input, got, tc.expect)
			}
		})
	}
}

func TestPullWorkspaceWorktrees_IteratesAllRepos(t *testing.T) {
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
		// repo-a: fetch, merge, push
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "feat-a"}, Err: nil},
		// repo-b: fetch, merge, push
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "feat-b"}, Err: nil},
	}

	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.Install()

	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	pullWorkspaceWorktrees(worktrees, "main")
}

func TestPullWorkspaceWorktrees_UsesPerRepoDefaultBranch(t *testing.T) {
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
		// repo-a pulls from "develop"
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"merge", "origin/develop", "-m", "Pull from develop\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "feat-a"}, Err: nil},
		// repo-b pulls from "staging"
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"merge", "origin/staging", "-m", "Pull from staging\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "feat-b"}, Err: nil},
	}

	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.Install()

	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	// sourceBranch="" means use per-repo DefaultBranch
	pullWorkspaceWorktrees(worktrees, "")
}

func TestPullRepoWorktree_CustomRemote(t *testing.T) {
	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "upstream"}, Err: nil},
		{Args: []string{"merge", "upstream/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "upstream", "feat-a"}, Err: nil},
	}

	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.Install()

	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	err := pullRepoWorktree("/ws/repo-a", "feat-a", "main", "upstream")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPullRepoWorktree_EmptyRemoteDefaultsToOrigin(t *testing.T) {
	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "feat-a"}, Err: nil},
	}

	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.Install()

	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	err := pullRepoWorktree("/ws/repo-a", "feat-a", "main", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunPull_LegacyMode(t *testing.T) {
	// Test legacy pullWorktree path - same as existing TestPullWorktree "successful pull" case
	tmpDir := t.TempDir()
	wtPath := tmpDir + "/falcon"
	if err := os.MkdirAll(wtPath+"/.git", 0755); err != nil {
		t.Fatalf("failed to create worktree: %v", err)
	}

	SetupTestEnv(t, map[string]string{
		"LOOM_WORKTREES_DIR": tmpDir,
	})

	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "falcon-branch"}, Err: nil},
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "falcon-branch\n"},
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

	pullWorktree("falcon", "main")
}

func TestPullWorkspaceWorktrees_SkipsNilRepo(t *testing.T) {
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
		{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "feat-b"}, Err: nil},
	}

	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.Install()

	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	pullWorkspaceWorktrees(worktrees, "main")
}

func TestPullWorkspaceWorktrees_CLIArgOverridesConfig(t *testing.T) {
	worktrees := []WorktreeInfo{
		{
			Name:   "repo-a",
			Path:   "/ws/repo-a",
			Branch: "feat-a",
			Repo:   &RepoConfig{Name: "repo-a", DefaultBranch: "develop", Remote: ""},
		},
	}

	// CLI source "release" overrides per-repo "develop"
	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"merge", "origin/release", "-m", "Pull from release\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "feat-a"}, Err: nil},
	}

	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.Install()

	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	pullWorkspaceWorktrees(worktrees, "release")
}

func TestPullCmd_GroupID(t *testing.T) {
	// Verify command is in the "git" group
	if pullCmd.GroupID != "git" {
		t.Errorf("expected pull command to be in 'git' group, got %q", pullCmd.GroupID)
	}
}

func TestPullCmd_Flags(t *testing.T) {
	// Verify flags are registered
	if allFlag := pullCmd.Flags().Lookup("all"); allFlag == nil {
		t.Error("expected --all flag to be registered")
	}
	if wsFlag := pullCmd.Flags().Lookup("workspace"); wsFlag == nil {
		t.Error("expected --workspace flag to be registered")
	}

	// Verify shorthand flags
	if allFlag := pullCmd.Flags().ShorthandLookup("a"); allFlag == nil {
		t.Error("expected -a shorthand flag to be registered")
	}
	if wsFlag := pullCmd.Flags().ShorthandLookup("W"); wsFlag == nil {
		t.Error("expected -W shorthand flag to be registered")
	}
}

func TestPullWorkspaceWorktrees_EmptyList(t *testing.T) {
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
	pullWorkspaceWorktrees(worktrees, "main")
}

func TestPullCmd_WorkspaceModeArgsValidation(t *testing.T) {
	// Save and restore the global flags
	origPullAll := pullAll
	origPullWorkspace := pullWorkspace
	defer func() {
		pullAll = origPullAll
		pullWorkspace = origPullWorkspace
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
			name:      "workspace mode with --all, one arg (source branch, success)",
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
			pullAll = tc.allFlag
			pullWorkspace = ""

			err := pullCmd.Args(pullCmd, tc.args)

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
