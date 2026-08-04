package git

import (
	"errors"
	"strings"
	"testing"
)

func TestPrCmd_ArgsValidation(t *testing.T) {
	// Touches global prAll flag — keep sequential
	origPrAll := prAll
	defer func() { prAll = origPrAll }()

	tests := []struct {
		name      string
		args      []string
		allFlag   bool
		wantError bool
		errorMsg  string
	}{
		{name: "without --all, no args", args: []string{}, allFlag: false, wantError: true, errorMsg: "requires 1-2 arguments"},
		{name: "without --all, one arg (success)", args: []string{"falcon"}, allFlag: false, wantError: false},
		{name: "without --all, two args (success)", args: []string{"falcon", "main"}, allFlag: false, wantError: false},
		{name: "without --all, three args", args: []string{"falcon", "main", "extra"}, allFlag: false, wantError: true, errorMsg: "requires 1-2 arguments"},
		{name: "with --all, no args (success)", args: []string{}, allFlag: true, wantError: false},
		{name: "with --all, one arg (success)", args: []string{"main"}, allFlag: true, wantError: false},
		{name: "with --all, two args", args: []string{"main", "extra"}, allFlag: true, wantError: true, errorMsg: "--all flag accepts at most 1 argument"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prAll = tc.allFlag
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
	t.Parallel()
	if prCmd.GroupID != "git" {
		t.Errorf("expected pr command to be in 'git' group, got %q", prCmd.GroupID)
	}
}

func TestPrCmd_Flags(t *testing.T) {
	t.Parallel()
	if allFlag := prCmd.Flags().Lookup("all"); allFlag == nil {
		t.Error("expected --all flag to be registered")
	}
	if wsFlag := prCmd.Flags().Lookup("workspace"); wsFlag == nil {
		t.Error("expected --workspace flag to be registered")
	}
	if allFlag := prCmd.Flags().ShorthandLookup("a"); allFlag == nil {
		t.Error("expected -a shorthand flag to be registered")
	}
	if wsFlag := prCmd.Flags().ShorthandLookup("W"); wsFlag == nil {
		t.Error("expected -W shorthand flag to be registered")
	}
}

func TestCreatePR_Success(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"push", "origin", "feature"}, Err: nil},
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"log", "origin/main..feature", "--oneline"}, Stdout: "abc123 some commit\n"},
		{Name: "git", Args: []string{"log", "origin/main..origin/feature", "--format=%s", "--reverse"}, Stdout: "Add new feature\n"},
		{Name: "gh", Args: []string{"pr", "create", "--base", "main", "--head", "feature", "--title", "Add new feature", "--body", "---\nCreated with [loom](https://github.com/tysonthomas9/loomcli)"}, Stdout: "https://github.com/user/repo/pull/42\n"},
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	url, err := createPR(deps, "/repo", "feature", "main", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if url != "https://github.com/user/repo/pull/42" {
		t.Errorf("expected PR URL, got %q", url)
	}
}

func TestCreatePR_NoCommits(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"log", "origin/main..feature", "--oneline"}, Stdout: ""},
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	url, err := createPR(deps, "/repo", "feature", "main", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if url != "" {
		t.Errorf("expected empty URL for no commits, got %q", url)
	}
}

func TestCreatePR_PRAlreadyExists(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"push", "origin", "feature"}, Err: nil},
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"log", "origin/main..feature", "--oneline"}, Stdout: "abc123 some commit\n"},
		{Name: "git", Args: []string{"log", "origin/main..origin/feature", "--format=%s", "--reverse"}, Stdout: "Add new feature\n"},
		{Name: "gh", Args: []string{"pr", "create", "--base", "main", "--head", "feature", "--title", "Add new feature", "--body", "---\nCreated with [loom](https://github.com/tysonthomas9/loomcli)"}, Stderr: "a]pull request already exists", Err: errors.New("exit status 1")},
		{Name: "gh", Args: []string{"pr", "view", "feature", "--json", "url", "-q", ".url"}, Stdout: "https://github.com/user/repo/pull/41\n"},
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	url, err := createPR(deps, "/repo", "feature", "main", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if url != "https://github.com/user/repo/pull/41" {
		t.Errorf("expected existing PR URL, got %q", url)
	}
}

func TestCreatePR_PushFails(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"push", "origin", "feature"}, Err: errors.New("push rejected")},
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"log", "origin/main..feature", "--oneline"}, Stdout: "abc123 some commit\n"},
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	_, err := createPR(deps, "/repo", "feature", "main", "")
	if err == nil {
		t.Error("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "pushing branch") {
		t.Errorf("expected error about pushing branch, got: %v", err)
	}
}

func TestPrWorkspaceWorktrees_IteratesAllRepos(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	worktrees := []WorktreeInfo{
		{Name: "repo-a", Path: "/ws/repo-a", Branch: "feat-a", Repo: &RepoConfig{Name: "repo-a", DefaultBranch: "main", Remote: ""}},
		{Name: "repo-b", Path: "/ws/repo-b", Branch: "feat-b", Repo: &RepoConfig{Name: "repo-b", DefaultBranch: "main", Remote: ""}},
	}

	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"push", "origin", "feat-a"}, Err: nil},
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"push", "origin", "feat-b"}, Err: nil},
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"log", "origin/main..feat-a", "--oneline"}, Stdout: "abc commit\n"},
		{Name: "git", Args: []string{"log", "origin/main..origin/feat-a", "--format=%s", "--reverse"}, Stdout: "Add feature A\n"},
		{Name: "gh", Args: []string{"pr", "create", "--base", "main", "--head", "feat-a", "--title", "Add feature A", "--body", "---\nCreated with [loom](https://github.com/tysonthomas9/loomcli)"}, Stdout: "https://github.com/user/repo/pull/1\n"},
		{Name: "git", Args: []string{"log", "origin/main..feat-b", "--oneline"}, Stdout: "def commit\n"},
		{Name: "git", Args: []string{"log", "origin/main..origin/feat-b", "--format=%s", "--reverse"}, Stdout: "Add feature B\n"},
		{Name: "gh", Args: []string{"pr", "create", "--base", "main", "--head", "feat-b", "--title", "Add feature B", "--body", "---\nCreated with [loom](https://github.com/tysonthomas9/loomcli)"}, Stdout: "https://github.com/user/repo/pull/2\n"},
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	prWorkspaceWorktrees(deps, worktrees, "", "main")
}

func TestPrWorkspaceWorktrees_SkipsNilRepo(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	worktrees := []WorktreeInfo{
		{Name: "repo-a", Path: "/ws/repo-a", Branch: "feat-a", Repo: nil},
		{Name: "repo-b", Path: "/ws/repo-b", Branch: "feat-b", Repo: &RepoConfig{Name: "repo-b", DefaultBranch: "main", Remote: ""}},
	}

	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"push", "origin", "feat-b"}, Err: nil},
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"log", "origin/main..feat-b", "--oneline"}, Stdout: "def commit\n"},
		{Name: "git", Args: []string{"log", "origin/main..origin/feat-b", "--format=%s", "--reverse"}, Stdout: "Add feature B\n"},
		{Name: "gh", Args: []string{"pr", "create", "--base", "main", "--head", "feat-b", "--title", "Add feature B", "--body", "---\nCreated with [loom](https://github.com/tysonthomas9/loomcli)"}, Stdout: "https://github.com/user/repo/pull/2\n"},
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	prWorkspaceWorktrees(deps, worktrees, "", "main")
}

func TestPrWorkspaceWorktrees_UsesPerRepoDefaultBranch(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	worktrees := []WorktreeInfo{
		{Name: "repo-a", Path: "/ws/repo-a", Branch: "feat-a", Repo: &RepoConfig{Name: "repo-a", DefaultBranch: "develop", Remote: ""}},
		{Name: "repo-b", Path: "/ws/repo-b", Branch: "feat-b", Repo: &RepoConfig{Name: "repo-b", DefaultBranch: "staging", Remote: ""}},
	}

	outputStubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"push", "origin", "feat-a"}, Err: nil},
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"push", "origin", "feat-b"}, Err: nil},
	}

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"log", "origin/develop..feat-a", "--oneline"}, Stdout: "abc commit\n"},
		{Name: "git", Args: []string{"log", "origin/develop..origin/feat-a", "--format=%s", "--reverse"}, Stdout: "Feature A for develop\n"},
		{Name: "gh", Args: []string{"pr", "create", "--base", "develop", "--head", "feat-a", "--title", "Feature A for develop", "--body", "---\nCreated with [loom](https://github.com/tysonthomas9/loomcli)"}, Stdout: "https://github.com/user/repo/pull/10\n"},
		{Name: "git", Args: []string{"log", "origin/staging..feat-b", "--oneline"}, Stdout: "def commit\n"},
		{Name: "git", Args: []string{"log", "origin/staging..origin/feat-b", "--format=%s", "--reverse"}, Stdout: "Feature B for staging\n"},
		{Name: "gh", Args: []string{"pr", "create", "--base", "staging", "--head", "feat-b", "--title", "Feature B for staging", "--body", "---\nCreated with [loom](https://github.com/tysonthomas9/loomcli)"}, Stdout: "https://github.com/user/repo/pull/11\n"},
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)
	outputMock := NewOutputCommandMock(t, outputStubs)
	outputMock.InstallOn(deps)

	prWorkspaceWorktrees(deps, worktrees, "", "")
}

func TestGeneratePRInfo_SingleCommit(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"log", "origin/main..origin/feature", "--format=%s", "--reverse"}, Stdout: "Fix authentication bug\n"},
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)

	title, body := generatePRInfo(deps, "/repo", "origin", "main", "feature")
	if title != "Fix authentication bug" {
		t.Errorf("expected title %q, got %q", "Fix authentication bug", title)
	}
	expectedBody := "---\nCreated with [loom](https://github.com/tysonthomas9/loomcli)"
	if body != expectedBody {
		t.Errorf("expected body %q, got %q", expectedBody, body)
	}
}

func TestGeneratePRInfo_MultipleCommits(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"log", "origin/main..origin/feature", "--format=%s", "--reverse"}, Stdout: "Add login form\nAdd validation\nAdd tests\n"},
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)

	title, body := generatePRInfo(deps, "/repo", "origin", "main", "feature")
	if title != "Add login form" {
		t.Errorf("expected title %q, got %q", "Add login form", title)
	}
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
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	commandStubs := []CommandStub{
		{Name: "git", Args: []string{"log", "origin/main..origin/feature", "--format=%s", "--reverse"}, Stdout: "", Err: errors.New("unknown revision")},
		{Name: "git", Args: []string{"log", "origin/main..feature", "--format=%s", "--reverse"}, Stdout: "", Err: errors.New("unknown revision")},
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)

	title, body := generatePRInfo(deps, "/repo", "origin", "main", "feature")
	if title != "feature" {
		t.Errorf("expected title to be branch name %q, got %q", "feature", title)
	}
	if body != "" {
		t.Errorf("expected empty body, got %q", body)
	}
}

func TestCheckGhInstalled_Success(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	commandStubs := []CommandStub{
		{Name: "gh", Args: []string{"--version"}, Stdout: "gh version 2.40.0\n"},
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)

	err := checkGhInstalled(deps)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCheckGhInstalled_NotFound(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	commandStubs := []CommandStub{
		{Name: "gh", Args: []string{"--version"}, Err: errors.New("exec: \"gh\": executable file not found")},
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)

	err := checkGhInstalled(deps)
	if err == nil {
		t.Error("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "'gh' CLI not found") {
		t.Errorf("expected error about gh CLI not found, got: %v", err)
	}
}

func TestGetExistingPRURL(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	commandStubs := []CommandStub{
		{Name: "gh", Args: []string{"pr", "view", "feature", "--json", "url", "-q", ".url"}, Stdout: "https://github.com/user/repo/pull/42\n"},
	}

	cmdMock := NewCommandMock(t, commandStubs)
	cmdMock.InstallOn(deps)

	url, err := getExistingPRURL(deps, "/repo", "feature")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if url != "https://github.com/user/repo/pull/42" {
		t.Errorf("expected URL %q, got %q", "https://github.com/user/repo/pull/42", url)
	}
}
