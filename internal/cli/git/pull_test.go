package git

import (
	"bytes"
	"errors"
	"io"
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

	cmdMock := NewCommandMock(t, []CommandStub{})
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	pullWorkspaceWorktrees(deps, worktrees, "main", true)
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

	cmdMock := NewCommandMock(t, []CommandStub{})
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	// sourceBranch="" means use per-repo DefaultBranch
	pullWorkspaceWorktrees(deps, worktrees, "", true)
}

func TestPullRepoWorktree_CustomRemote(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "upstream"}, Err: nil},
		{Args: []string{"merge", "upstream/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
		{Args: []string{"push", "upstream", "feat-a"}, Err: nil},
	}

	cmdMock := NewCommandMock(t, []CommandStub{})
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	err := pullRepoWorktree(deps, "/ws/repo-a", "feat-a", "main", "upstream", true)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
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

	cmdMock := NewCommandMock(t, []CommandStub{})
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	err := pullRepoWorktree(deps, "/ws/repo-a", "feat-a", "main", "", true)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
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

	cmdMock := NewCommandMock(t, []CommandStub{})
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	pullWorkspaceWorktrees(deps, worktrees, "main", true)
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

	cmdMock := NewCommandMock(t, []CommandStub{})
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	pullWorkspaceWorktrees(deps, worktrees, "release", true)
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
	if noPushFlag := pullCmd.Flags().Lookup("no-push"); noPushFlag == nil {
		t.Error("expected --no-push flag to be registered")
	} else if noPushFlag.DefValue != "false" {
		// The default must stay "push", so `loom pull` keeps publishing as before.
		t.Errorf("expected --no-push to default to false, got %q", noPushFlag.DefValue)
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
	pullWorkspaceWorktrees(deps, worktrees, "main", true)
}

// captureStdout runs fn with os.Stdout redirected and returns what it printed.
// Callers must not use t.Parallel() — os.Stdout is process-global.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()

	os.Stdout = orig
	w.Close()
	out := <-done
	r.Close()
	return out
}

// TestPullRepoWorktree_NoPushWhenDisabled is the core regression guard for
// PUPPET-42: with pushAfterPull=false the pull must issue fetch and merge only.
// The output mock t.Fatal's on any call beyond its stubs, so the absence of a
// push stub proves the push command was never run; the stdout assertion
// separately proves the success line does not lie about having pushed.
func TestPullRepoWorktree_NoPushWhenDisabled(t *testing.T) {
	// not parallel: captureStdout redirects the process-global os.Stdout
	deps, _, _, _, _ := NewTestDeps(t)

	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: nil},
	}

	cmdMock := NewCommandMock(t, []CommandStub{})
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	var err error
	out := captureStdout(t, func() {
		err = pullRepoWorktree(deps, "/ws/repo-a", "feat-a", "main", "", false)
	})

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if strings.Contains(out, "Pushed to") {
		t.Errorf("suppressed push must not print a push confirmation, got:\n%s", out)
	}
	if !strings.Contains(out, "Pull completed successfully") {
		t.Errorf("expected the pull success line, got:\n%s", out)
	}
}

// TestPullWorkspaceWorktrees_NoPushWhenDisabled verifies the suppressed push is
// not reported as a per-repo failure: both repos must still be summarized ✓.
func TestPullWorkspaceWorktrees_NoPushWhenDisabled(t *testing.T) {
	// not parallel: captureStdout redirects the process-global os.Stdout
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

	mergeMsg := "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
	outputStubs := []OutputCommandStub{
		// repo-a: fetch, merge — no push
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"merge", "origin/main", "-m", mergeMsg}, Err: nil},
		// repo-b: fetch, merge — no push
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"merge", "origin/main", "-m", mergeMsg}, Err: nil},
	}

	cmdMock := NewCommandMock(t, []CommandStub{})
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}}

	out := captureStdout(t, func() {
		pullWorkspaceWorktrees(deps, worktrees, "main", false)
	})

	for _, repo := range []string{"repo-a", "repo-b"} {
		if !strings.Contains(out, "✓ "+repo) {
			t.Errorf("expected %s to be summarized as succeeded, got:\n%s", repo, out)
		}
	}
	if strings.Contains(out, "Pushed to") {
		t.Errorf("suppressed push must not print a push confirmation, got:\n%s", out)
	}
}

// TestPullRepoWorktree_ConflictPathNeverPushes pins edge case 1: the conflict
// hand-off returns before the push block, so even pushAfterPull=true must not
// publish a half-resolved merge.
func TestPullRepoWorktree_ConflictPathNeverPushes(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"merge", "origin/main", "-m", "Pull from main\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}, Err: errors.New("CONFLICT")},
	}
	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"diff", "--name-only", "--diff-filter=U"}, Stdout: "src/data.go\n"},
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	agentCalled := false
	deps.Agent = &MockAgentInvoker{InteractiveFunc: func(workDir, prompt, agentName string) error {
		agentCalled = true
		return nil
	}}

	// pushAfterPull=true: the guard must not have moved the push before the
	// conflict return.
	if err := pullRepoWorktree(deps, "/ws/repo-a", "feat-a", "main", "", true); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !agentCalled {
		t.Error("expected the conflict resolution agent to be invoked")
	}
}
