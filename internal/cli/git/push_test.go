package git

import (
	"errors"
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
		{
			name:      "without --all, no args",
			args:      []string{},
			allFlag:   false,
			wantError: true,
			errorMsg:  "requires 1-2 arguments",
		},
		{
			name:      "without --all, one arg (success)",
			args:      []string{"feature/branch"},
			allFlag:   false,
			wantError: false,
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
			errorMsg:  "requires 1-2 arguments",
		},
		{
			name:      "with --all, no args (success)",
			args:      []string{},
			allFlag:   true,
			wantError: false,
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
			errorMsg:  "--all flag accepts at most 1 argument",
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
	if err := pushWorkspaceWorktrees(deps, worktrees, "", "main"); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestPushWorkspaceWorktrees_ReturnsErrorWhenAnyRepoFails(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	worktrees := []WorktreeInfo{
		{
			Name:   "repo-a",
			Path:   "/ws/repo-a",
			Branch: "feat-a",
			Repo:   &RepoConfig{Name: "repo-a", DefaultBranch: "main", Remote: ""},
		},
	}

	cmdMock := NewCommandMock(t, []CommandStub{})
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: errors.New("fetch failed")},
	})
	outputMock.InstallOn(deps)

	err := pushWorkspaceWorktrees(deps, worktrees, "", "main")
	if err == nil {
		t.Fatal("expected pushWorkspaceWorktrees to return an error")
	}
	if !strings.Contains(err.Error(), "1 repo(s) failed") {
		t.Fatalf("unexpected error: %v", err)
	}
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
