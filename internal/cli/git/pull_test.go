package git

import (
	"errors"
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
		{
			name:      "without --all, no args",
			args:      []string{},
			allFlag:   false,
			wantError: true,
			errorMsg:  "requires 1-2 arguments",
		},
		{
			name:      "without --all, one arg (success)",
			args:      []string{"falcon"},
			allFlag:   false,
			wantError: false,
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

func TestSourceBranchDisplay(t *testing.T) {
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
			got := sourceBranchDisplay(tc.input)
			if got != tc.expect {
				t.Errorf("sourceBranchDisplay(%q) = %q, want %q", tc.input, got, tc.expect)
			}
		})
	}
}

func TestPullWorkspaceWorktrees_IteratesAllRepos(t *testing.T) {
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
		// repo-a: fetch, merge, push
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "feat-a"}, Err: nil},
		// repo-b: fetch, merge, push
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "feat-b"}, Err: nil},
	}

	var stubs []CommandStub
	stubs = append(stubs, verifyStubs("origin", "main", "aaaaaaaaaaaa", "bbbbbbbbbbbb", 0)...)
	stubs = append(stubs, verifyStubs("origin", "main", "cccccccccccc", "cccccccccccc", 0)...)

	cmdMock := NewCommandMock(t, stubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	outcomes := pullWorkspaceWorktrees(deps, worktrees, "main")
	if len(outcomes) != 2 {
		t.Fatalf("expected 2 outcomes, got %d", len(outcomes))
	}
	if !outcomes[0].InSync() || outcomes[0].State != syncStateAdvanced {
		t.Errorf("repo-a: expected advanced, got state %v (%s)", outcomes[0].State, outcomes[0].Detail)
	}
	if outcomes[1].State != syncStateAlreadyCurrent {
		t.Errorf("repo-b: expected already-current, got state %v (%s)", outcomes[1].State, outcomes[1].Detail)
	}
}

func TestPullWorkspaceWorktrees_UsesPerRepoDefaultBranch(t *testing.T) {
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
		// repo-a pulls from "develop"
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"merge", "origin/develop", "-m", "Pull from develop\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "feat-a"}, Err: nil},
		// repo-b pulls from "staging"
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"merge", "origin/staging", "-m", "Pull from staging\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "feat-b"}, Err: nil},
	}

	var stubs []CommandStub
	stubs = append(stubs, verifyStubs("origin", "develop", "aaaaaaaaaaaa", "bbbbbbbbbbbb", 0)...)
	stubs = append(stubs, verifyStubs("origin", "staging", "aaaaaaaaaaaa", "bbbbbbbbbbbb", 0)...)

	cmdMock := NewCommandMock(t, stubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	// sourceBranch="" means use per-repo DefaultBranch
	pullWorkspaceWorktrees(deps, worktrees, "")
}

func TestPullRepoWorktree_CustomRemote(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "upstream"}, Err: nil},
		{Args: []string{"merge", "upstream/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "upstream", "feat-a"}, Err: nil},
	}

	cmdMock := NewCommandMock(t, verifyStubs("upstream", "main", "aaaaaaaaaaaa", "bbbbbbbbbbbb", 0))
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	outcome, err := pullRepoWorktree(deps, "/ws/repo-a", "feat-a", "main", "upstream")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !outcome.InSync() {
		t.Errorf("expected in-sync outcome, got state %v (%s)", outcome.State, outcome.Detail)
	}
}

func TestPullRepoWorktree_EmptyRemoteDefaultsToOrigin(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "feat-a"}, Err: nil},
	}

	cmdMock := NewCommandMock(t, verifyStubs("origin", "main", "aaaaaaaaaaaa", "bbbbbbbbbbbb", 0))
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	outcome, err := pullRepoWorktree(deps, "/ws/repo-a", "feat-a", "main", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !outcome.InSync() {
		t.Errorf("expected in-sync outcome, got state %v (%s)", outcome.State, outcome.Detail)
	}
}

func TestPullWorkspaceWorktrees_SkipsNilRepo(t *testing.T) {
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
		{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "feat-b"}, Err: nil},
	}

	cmdMock := NewCommandMock(t, verifyStubs("origin", "main", "aaaaaaaaaaaa", "bbbbbbbbbbbb", 0))
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	outcomes := pullWorkspaceWorktrees(deps, worktrees, "main")

	// The repo without metadata is not pulled, but it must still be reported:
	// silently dropping it is how a repo disappeared from the summary.
	if len(outcomes) != 2 {
		t.Fatalf("expected 2 outcomes (including the skipped one), got %d", len(outcomes))
	}
	if outcomes[0].State != syncStateSkipped {
		t.Errorf("repo-a: expected skipped, got state %v", outcomes[0].State)
	}
	if outcomes[0].InSync() {
		t.Error("repo-a: a skipped repo must never count as in sync")
	}
	if !outcomes[1].InSync() {
		t.Errorf("repo-b: expected in sync, got state %v (%s)", outcomes[1].State, outcomes[1].Detail)
	}
}

func TestPullWorkspaceWorktrees_CLIArgOverridesConfig(t *testing.T) {
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

	// CLI source "release" overrides per-repo "develop"
	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"merge", "origin/release", "-m", "Pull from release\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "feat-a"}, Err: nil},
	}

	cmdMock := NewCommandMock(t, verifyStubs("origin", "release", "aaaaaaaaaaaa", "bbbbbbbbbbbb", 0))
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	pullWorkspaceWorktrees(deps, worktrees, "release")
}

func TestPullCmd_GroupID(t *testing.T) {
	t.Parallel()
	// Verify command is in the "git" group
	if pullCmd.GroupID != "git" {
		t.Errorf("expected pull command to be in 'git' group, got %q", pullCmd.GroupID)
	}
}

func TestPullCmd_Flags(t *testing.T) {
	t.Parallel()
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
	pullWorkspaceWorktrees(deps, worktrees, "main")
}

// A merge that reported success while the worktree is still behind must reach
// the caller as an error — this is the reported incident at the function
// boundary, where it used to return nil.
func TestPullRepoWorktree_StillBehindIsAnError(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "origin", "feat-a"}, Err: nil},
	})

	cmdMock := NewCommandMock(t, verifyStubs("origin", "main", "aaaaaaaaaaaa", "bbbbbbbbbbbb", 8))
	cmdMock.InstallOn(deps)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	outcome, err := pullRepoWorktree(deps, "/ws/repo-a", "feat-a", "main", "")
	if err == nil {
		t.Fatal("expected an error when the worktree is still behind after the merge")
	}
	if outcome.State != syncStateBehind {
		t.Errorf("state = %v, want syncStateBehind", outcome.State)
	}
	if outcome.InSync() {
		t.Error("a worktree still behind must never report InSync")
	}
	if outcome.Behind != 8 {
		t.Errorf("Behind = %d, want 8", outcome.Behind)
	}
}

// The conflict path used to return nil the moment the agent was launched. If
// the agent leaves the merge open, that is not a success.
func TestPullRepoWorktree_ConflictAgentLeavesMergeOpen(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: errors.New("conflict")},
	})

	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"rev-parse", "--verify", "HEAD"}, Stdout: "aaaaaaaaaaaa\n"},
		// conflict detection after the failed merge
		{Name: "git", Args: []string{"diff", "--name-only", "--diff-filter=U"}, Stdout: "a.go\n"},
		// verification: the agent ran but the conflict is still there
		{Name: "git", Args: []string{"diff", "--name-only", "--diff-filter=U"}, Stdout: "a.go\n"},
	})
	cmdMock.InstallOn(deps)
	outputMock.InstallOn(deps)

	invoked := false
	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		invoked = true
		return nil
	}}

	outcome, err := pullRepoWorktree(deps, "/ws/repo-a", "feat-a", "main", "")
	if !invoked {
		t.Error("expected the conflict agent to be invoked")
	}
	if err == nil {
		t.Fatal("expected an error when the agent left the merge unresolved")
	}
	if outcome.State != syncStateUnresolved {
		t.Errorf("state = %v, want syncStateUnresolved", outcome.State)
	}
	if outcome.InSync() {
		t.Error("an unresolved merge must never report InSync")
	}
}

func TestPullRepoWorktree_ConflictAgentResolvesCleanly(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: errors.New("conflict")},
	})

	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"rev-parse", "--verify", "HEAD"}, Stdout: "aaaaaaaaaaaa\n"},
		{Name: "git", Args: []string{"diff", "--name-only", "--diff-filter=U"}, Stdout: "a.go\n"},
		// verification: the agent resolved and committed
		{Name: "git", Args: []string{"diff", "--name-only", "--diff-filter=U"}, Stdout: ""},
		{Name: "git", Args: []string{"rev-parse", "--verify", "MERGE_HEAD"}, Err: errNoMergeHead},
		{Name: "git", Args: []string{"rev-parse", "--verify", "refs/remotes/origin/main"}, Stdout: "ccc\n"},
		{Name: "git", Args: []string{"rev-list", "--count", "HEAD..origin/main"}, Stdout: "0\n"},
		{Name: "git", Args: []string{"rev-parse", "--verify", "HEAD"}, Stdout: "bbbbbbbbbbbb\n"},
	})
	cmdMock.InstallOn(deps)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		return nil
	}}

	outcome, err := pullRepoWorktree(deps, "/ws/repo-a", "feat-a", "main", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !outcome.InSync() || outcome.State != syncStateAdvanced {
		t.Errorf("state = %v, want syncStateAdvanced", outcome.State)
	}
}
