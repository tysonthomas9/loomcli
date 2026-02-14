package cli

import (
	"errors"
	"strings"
	"testing"
)

func TestPrCmd_ArgsValidation(t *testing.T) {
	// Save and restore the global flag
	origPrAll := prAll
	defer func() { prAll = origPrAll }()

	tests := []struct {
		name      string
		args      []string
		allFlag   bool
		wantError bool
		errorMsg  string
	}{
		// Without --all flag: requires 1-2 args
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

		// With --all flag: accepts 0-1 args
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
			prAll = tc.allFlag

			// Call the Args validation function directly
			err := prCmd.Args(prCmd, tc.args)

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

func TestPrCmd_GroupID(t *testing.T) {
	// Verify command is in the "git" group
	if prCmd.GroupID != "git" {
		t.Errorf("expected pr command to be in 'git' group, got %q", prCmd.GroupID)
	}
}

func TestPrCmd_Flags(t *testing.T) {
	// Verify flags are registered
	if allFlag := prCmd.Flags().Lookup("all"); allFlag == nil {
		t.Error("expected --all flag to be registered")
	}
	if wsFlag := prCmd.Flags().Lookup("workspace"); wsFlag == nil {
		t.Error("expected --workspace flag to be registered")
	}

	// Verify shorthand flags
	if allFlag := prCmd.Flags().ShorthandLookup("a"); allFlag == nil {
		t.Error("expected -a shorthand flag to be registered")
	}
	if wsFlag := prCmd.Flags().ShorthandLookup("W"); wsFlag == nil {
		t.Error("expected -W shorthand flag to be registered")
	}
}

func TestCreatePR_Success(t *testing.T) {
	// createPR: fetch (output), HasCommitsBetweenRemote (command), push (output),
	// generatePRInfo (command), gh pr create (command)
	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},           // GitFetchRemote
		{Args: []string{"push", "origin", "feature"}, Err: nil}, // GitPushRemote
	}

	commandStubs := []CommandStub{
		// HasCommitsBetweenRemote: git log origin/main..feature --oneline
		{Name: "git", Args: []string{"log", "origin/main..feature", "--oneline"}, Stdout: "abc123 some commit\n"},
		// generatePRInfo: git log origin/main..origin/feature --format=%s --reverse
		{Name: "git", Args: []string{"log", "origin/main..origin/feature", "--format=%s", "--reverse"}, Stdout: "Add new feature\n"},
		// gh pr create
		{Name: "gh", Args: []string{"pr", "create", "--base", "main", "--head", "feature", "--title", "Add new feature", "--body", "---\nCreated with [loom](https://github.com/tysonthomas9/loomcli)"}, Stdout: "https://github.com/user/repo/pull/42\n"},
	}

	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.Install()
	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.Install()

	// Mock claude invoker (should NOT be called)
	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	url, err := createPR("/repo", "feature", "main", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if url != "https://github.com/user/repo/pull/42" {
		t.Errorf("expected PR URL, got %q", url)
	}
}

func TestCreatePR_NoCommits(t *testing.T) {
	// fetch succeeds, HasCommitsBetweenRemote returns no commits
	// No push or gh commands should be called
	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil}, // GitFetchRemote
	}

	commandStubs := []CommandStub{
		// HasCommitsBetweenRemote: no commits
		{Name: "git", Args: []string{"log", "origin/main..feature", "--oneline"}, Stdout: ""},
	}

	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.Install()
	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.Install()

	// Mock claude invoker (should NOT be called)
	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	url, err := createPR("/repo", "feature", "main", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if url != "" {
		t.Errorf("expected empty URL for no commits, got %q", url)
	}
}

func TestCreatePR_PRAlreadyExists(t *testing.T) {
	// fetch, push, gh pr create fails with "already exists",
	// then gh pr view returns existing URL
	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},           // GitFetchRemote
		{Args: []string{"push", "origin", "feature"}, Err: nil}, // GitPushRemote
	}

	commandStubs := []CommandStub{
		// HasCommitsBetweenRemote
		{Name: "git", Args: []string{"log", "origin/main..feature", "--oneline"}, Stdout: "abc123 some commit\n"},
		// generatePRInfo
		{Name: "git", Args: []string{"log", "origin/main..origin/feature", "--format=%s", "--reverse"}, Stdout: "Add new feature\n"},
		// gh pr create fails with "already exists"
		{Name: "gh", Args: []string{"pr", "create", "--base", "main", "--head", "feature", "--title", "Add new feature", "--body", "---\nCreated with [loom](https://github.com/tysonthomas9/loomcli)"}, Stderr: "a]pull request already exists", Err: errors.New("exit status 1")},
		// gh pr view returns existing URL
		{Name: "gh", Args: []string{"pr", "view", "feature", "--json", "url", "-q", ".url"}, Stdout: "https://github.com/user/repo/pull/41\n"},
	}

	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.Install()
	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.Install()

	// Mock claude invoker (should NOT be called)
	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	url, err := createPR("/repo", "feature", "main", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if url != "https://github.com/user/repo/pull/41" {
		t.Errorf("expected existing PR URL, got %q", url)
	}
}

func TestCreatePR_PushFails(t *testing.T) {
	// fetch succeeds, push fails; gh pr create should not be called
	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},                                   // GitFetchRemote
		{Args: []string{"push", "origin", "feature"}, Err: errors.New("push rejected")}, // GitPushRemote fails
	}

	commandStubs := []CommandStub{
		// HasCommitsBetweenRemote
		{Name: "git", Args: []string{"log", "origin/main..feature", "--oneline"}, Stdout: "abc123 some commit\n"},
	}

	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.Install()
	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.Install()

	// Mock claude invoker (should NOT be called)
	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	_, err := createPR("/repo", "feature", "main", "")
	if err == nil {
		t.Error("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "pushing branch") {
		t.Errorf("expected error about pushing branch, got: %v", err)
	}
}

func TestCreatePRLegacy_Success(t *testing.T) {
	// createPRLegacy: GitFetch (output), HasCommitsBetween (command), GitPush (output),
	// generatePRInfo (command), gh pr create (command)
	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},           // GitFetch
		{Args: []string{"push", "origin", "feature"}, Err: nil}, // GitPush
	}

	commandStubs := []CommandStub{
		// HasCommitsBetween: git log main..feature --oneline
		{Name: "git", Args: []string{"log", "main..feature", "--oneline"}, Stdout: "abc123 some commit\n"},
		// generatePRInfo: git log origin/main..origin/feature --format=%s --reverse
		{Name: "git", Args: []string{"log", "origin/main..origin/feature", "--format=%s", "--reverse"}, Stdout: "Add legacy feature\n"},
		// gh pr create
		{Name: "gh", Args: []string{"pr", "create", "--base", "main", "--head", "feature", "--title", "Add legacy feature", "--body", "---\nCreated with [loom](https://github.com/tysonthomas9/loomcli)"}, Stdout: "https://github.com/user/repo/pull/99\n"},
	}

	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.Install()
	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.Install()

	// Mock claude invoker (should NOT be called)
	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	url, err := createPRLegacy("/repo", "feature", "main")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if url != "https://github.com/user/repo/pull/99" {
		t.Errorf("expected PR URL, got %q", url)
	}
}

func TestCreatePRLegacy_NoCommits(t *testing.T) {
	// Legacy mode, no commits ahead
	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil}, // GitFetch
	}

	commandStubs := []CommandStub{
		// HasCommitsBetween: no commits
		{Name: "git", Args: []string{"log", "main..feature", "--oneline"}, Stdout: ""},
	}

	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.Install()
	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.Install()

	// Mock claude invoker (should NOT be called)
	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	url, err := createPRLegacy("/repo", "feature", "main")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if url != "" {
		t.Errorf("expected empty URL for no commits, got %q", url)
	}
}

func TestPrWorkspaceWorktrees_IteratesAllRepos(t *testing.T) {
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
		// repo-a: fetch, push
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"push", "origin", "feat-a"}, Err: nil},
		// repo-b: fetch, push
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"push", "origin", "feat-b"}, Err: nil},
	}

	commandStubs := []CommandStub{
		// repo-a: HasCommitsBetweenRemote
		{Name: "git", Args: []string{"log", "origin/main..feat-a", "--oneline"}, Stdout: "abc commit\n"},
		// repo-a: generatePRInfo
		{Name: "git", Args: []string{"log", "origin/main..origin/feat-a", "--format=%s", "--reverse"}, Stdout: "Add feature A\n"},
		// repo-a: gh pr create
		{Name: "gh", Args: []string{"pr", "create", "--base", "main", "--head", "feat-a", "--title", "Add feature A", "--body", "---\nCreated with [loom](https://github.com/tysonthomas9/loomcli)"}, Stdout: "https://github.com/user/repo/pull/1\n"},
		// repo-b: HasCommitsBetweenRemote
		{Name: "git", Args: []string{"log", "origin/main..feat-b", "--oneline"}, Stdout: "def commit\n"},
		// repo-b: generatePRInfo
		{Name: "git", Args: []string{"log", "origin/main..origin/feat-b", "--format=%s", "--reverse"}, Stdout: "Add feature B\n"},
		// repo-b: gh pr create
		{Name: "gh", Args: []string{"pr", "create", "--base", "main", "--head", "feat-b", "--title", "Add feature B", "--body", "---\nCreated with [loom](https://github.com/tysonthomas9/loomcli)"}, Stdout: "https://github.com/user/repo/pull/2\n"},
	}

	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.Install()
	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.Install()

	// Mock claude invoker (should NOT be called)
	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	// sourceBranch="" means use wt.Branch for each; targetBranch="main" is explicit
	prWorkspaceWorktrees(worktrees, "", "main")
}

func TestPrWorkspaceWorktrees_SkipsNilRepo(t *testing.T) {
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
		{Args: []string{"push", "origin", "feat-b"}, Err: nil},
	}

	commandStubs := []CommandStub{
		// repo-b: HasCommitsBetweenRemote
		{Name: "git", Args: []string{"log", "origin/main..feat-b", "--oneline"}, Stdout: "def commit\n"},
		// repo-b: generatePRInfo
		{Name: "git", Args: []string{"log", "origin/main..origin/feat-b", "--format=%s", "--reverse"}, Stdout: "Add feature B\n"},
		// repo-b: gh pr create
		{Name: "gh", Args: []string{"pr", "create", "--base", "main", "--head", "feat-b", "--title", "Add feature B", "--body", "---\nCreated with [loom](https://github.com/tysonthomas9/loomcli)"}, Stdout: "https://github.com/user/repo/pull/2\n"},
	}

	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.Install()
	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.Install()

	// Mock claude invoker (should NOT be called)
	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	prWorkspaceWorktrees(worktrees, "", "main")
}

func TestPrWorkspaceWorktrees_UsesPerRepoDefaultBranch(t *testing.T) {
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
		// repo-a: fetch, push (target=develop)
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"push", "origin", "feat-a"}, Err: nil},
		// repo-b: fetch, push (target=staging)
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"push", "origin", "feat-b"}, Err: nil},
	}

	commandStubs := []CommandStub{
		// repo-a: HasCommitsBetweenRemote with develop
		{Name: "git", Args: []string{"log", "origin/develop..feat-a", "--oneline"}, Stdout: "abc commit\n"},
		// repo-a: generatePRInfo with develop
		{Name: "git", Args: []string{"log", "origin/develop..origin/feat-a", "--format=%s", "--reverse"}, Stdout: "Feature A for develop\n"},
		// repo-a: gh pr create with --base develop
		{Name: "gh", Args: []string{"pr", "create", "--base", "develop", "--head", "feat-a", "--title", "Feature A for develop", "--body", "---\nCreated with [loom](https://github.com/tysonthomas9/loomcli)"}, Stdout: "https://github.com/user/repo/pull/10\n"},
		// repo-b: HasCommitsBetweenRemote with staging
		{Name: "git", Args: []string{"log", "origin/staging..feat-b", "--oneline"}, Stdout: "def commit\n"},
		// repo-b: generatePRInfo with staging
		{Name: "git", Args: []string{"log", "origin/staging..origin/feat-b", "--format=%s", "--reverse"}, Stdout: "Feature B for staging\n"},
		// repo-b: gh pr create with --base staging
		{Name: "gh", Args: []string{"pr", "create", "--base", "staging", "--head", "feat-b", "--title", "Feature B for staging", "--body", "---\nCreated with [loom](https://github.com/tysonthomas9/loomcli)"}, Stdout: "https://github.com/user/repo/pull/11\n"},
	}

	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.Install()
	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.Install()

	// Mock claude invoker (should NOT be called)
	origClaude := claudeInvoker
	claudeInvoker = func(workDir, prompt, agentName string) error {
		t.Error("unexpected claude invocation")
		return nil
	}
	t.Cleanup(func() { claudeInvoker = origClaude })

	// targetBranch="" means use per-repo DefaultBranch
	prWorkspaceWorktrees(worktrees, "", "")
}

func TestGeneratePRInfo_SingleCommit(t *testing.T) {
	commandStubs := []CommandStub{
		// git log origin/main..origin/feature --format=%s --reverse
		{Name: "git", Args: []string{"log", "origin/main..origin/feature", "--format=%s", "--reverse"}, Stdout: "Fix authentication bug\n"},
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.Install()

	title, body := generatePRInfo("/repo", "origin", "main", "feature")
	if title != "Fix authentication bug" {
		t.Errorf("expected title %q, got %q", "Fix authentication bug", title)
	}
	expectedBody := "---\nCreated with [loom](https://github.com/tysonthomas9/loomcli)"
	if body != expectedBody {
		t.Errorf("expected body %q, got %q", expectedBody, body)
	}
}

func TestGeneratePRInfo_MultipleCommits(t *testing.T) {
	commandStubs := []CommandStub{
		// git log origin/main..origin/feature --format=%s --reverse
		{Name: "git", Args: []string{"log", "origin/main..origin/feature", "--format=%s", "--reverse"}, Stdout: "Add login form\nAdd validation\nAdd tests\n"},
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.Install()

	title, body := generatePRInfo("/repo", "origin", "main", "feature")
	if title != "Add login form" {
		t.Errorf("expected title %q, got %q", "Add login form", title)
	}
	// Body should have bulleted list of all commits
	if !strings.Contains(body, "- Add login form") {
		t.Errorf("expected body to contain '- Add login form', got %q", body)
	}
	if !strings.Contains(body, "- Add validation") {
		t.Errorf("expected body to contain '- Add validation', got %q", body)
	}
	if !strings.Contains(body, "- Add tests") {
		t.Errorf("expected body to contain '- Add tests', got %q", body)
	}
	if !strings.Contains(body, "Created with [loom]") {
		t.Errorf("expected body to contain loom footer, got %q", body)
	}
}

func TestGeneratePRInfo_NoCommits(t *testing.T) {
	commandStubs := []CommandStub{
		// First attempt: git log origin/main..origin/feature --format=%s --reverse (fails)
		{Name: "git", Args: []string{"log", "origin/main..origin/feature", "--format=%s", "--reverse"}, Stdout: "", Err: errors.New("unknown revision")},
		// Fallback: git log origin/main..feature --format=%s --reverse (also fails)
		{Name: "git", Args: []string{"log", "origin/main..feature", "--format=%s", "--reverse"}, Stdout: "", Err: errors.New("unknown revision")},
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.Install()

	title, body := generatePRInfo("/repo", "origin", "main", "feature")
	// When git log fails completely, branch name should be used as title
	if title != "feature" {
		t.Errorf("expected title to be branch name %q, got %q", "feature", title)
	}
	if body != "" {
		t.Errorf("expected empty body, got %q", body)
	}
}

func TestCheckGhInstalled_Success(t *testing.T) {
	commandStubs := []CommandStub{
		{Name: "gh", Args: []string{"--version"}, Stdout: "gh version 2.40.0\n"},
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.Install()

	err := checkGhInstalled()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCheckGhInstalled_NotFound(t *testing.T) {
	commandStubs := []CommandStub{
		{Name: "gh", Args: []string{"--version"}, Err: errors.New("exec: \"gh\": executable file not found")},
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.Install()

	err := checkGhInstalled()
	if err == nil {
		t.Error("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "'gh' CLI not found") {
		t.Errorf("expected error about gh CLI not found, got: %v", err)
	}
}

func TestGetExistingPRURL(t *testing.T) {
	commandStubs := []CommandStub{
		{Name: "gh", Args: []string{"pr", "view", "feature", "--json", "url", "-q", ".url"}, Stdout: "https://github.com/user/repo/pull/42\n"},
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.Install()

	url, err := getExistingPRURL("/repo", "feature")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if url != "https://github.com/user/repo/pull/42" {
		t.Errorf("expected URL %q, got %q", "https://github.com/user/repo/pull/42", url)
	}
}
