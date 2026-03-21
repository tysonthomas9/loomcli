package cli

import (
	"errors"
	"strings"
	"testing"
)

func TestEpicBranchName(t *testing.T) {
	tests := []struct {
		name   string
		epicID string
		want   string
	}{
		{"basic epic ID", "bd-spq5", "epic/bd-spq5"},
		{"numeric epic ID", "12345", "epic/12345"},
		{"already has prefix", "epic-1", "epic/epic-1"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := epicBranchName(tc.epicID)
			if got != tc.want {
				t.Errorf("epicBranchName(%q) = %q, want %q", tc.epicID, got, tc.want)
			}
		})
	}
}

func TestEnsureWorktreeBranch_AlreadyOnCorrectBranch(t *testing.T) {
	// CommandMock calls: GetCurrentBranch only (returns target branch, so early return)
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "epic/bd-spq5\n"},
	})
	cmdMock.Install()

	// No OutputCommandMock calls expected (no fetch, checkout, etc.)
	outMock := NewOutputCommandMock(t, []OutputCommandStub{})
	outMock.Install()

	err := EnsureWorktreeBranch("/repo", "epic/bd-spq5", "origin", "origin/main")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestEnsureWorktreeBranch_SwitchToLocalBranch(t *testing.T) {
	// Call sequence:
	// 1. GetCurrentBranch → "falcon" (CommandMock)
	// 2. IsCleanWorkingTree → "" clean (CommandMock)
	// 3. GitFetch → ok (OutputCommandMock)
	// 4. BranchExistsLocally → ok, branch exists (CommandMock)
	// 5. GitCheckout → ok (OutputCommandMock)
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "falcon\n"},
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""},
		{Name: "git", Args: []string{"rev-parse", "--verify", "refs/heads/epic/bd-spq5"}, Stdout: "abc123\n"},
	})
	cmdMock.Install()

	outMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"checkout", "epic/bd-spq5"}, Err: nil},
	})
	outMock.Install()

	err := EnsureWorktreeBranch("/repo", "epic/bd-spq5", "origin", "origin/main")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestEnsureWorktreeBranch_CreateFromRemote(t *testing.T) {
	// Call sequence:
	// 1. GetCurrentBranch → "falcon" (CommandMock)
	// 2. IsCleanWorkingTree → "" clean (CommandMock)
	// 3. GitFetch → ok (OutputCommandMock)
	// 4. BranchExistsLocally → fails, not found (CommandMock)
	// 5. RemoteBranchExists → ok, found (CommandMock)
	// 6. GitCheckoutNewFromRef → ok (OutputCommandMock)
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "falcon\n"},
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""},
		{Name: "git", Args: []string{"rev-parse", "--verify", "refs/heads/epic/bd-spq5"}, Err: errors.New("not found")},
		{Name: "git", Args: []string{"rev-parse", "--verify", "refs/remotes/origin/epic/bd-spq5"}, Stdout: "def456\n"},
	})
	cmdMock.Install()

	outMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"checkout", "-b", "epic/bd-spq5", "origin/epic/bd-spq5"}, Err: nil},
	})
	outMock.Install()

	err := EnsureWorktreeBranch("/repo", "epic/bd-spq5", "origin", "origin/main")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestEnsureWorktreeBranch_CreateFromFallback(t *testing.T) {
	// Call sequence:
	// 1. GetCurrentBranch → "falcon" (CommandMock)
	// 2. IsCleanWorkingTree → "" clean (CommandMock)
	// 3. GitFetch → ok (OutputCommandMock)
	// 4. BranchExistsLocally → fails, not found (CommandMock)
	// 5. RemoteBranchExists → fails, not found (CommandMock)
	// 6. GitCheckoutNewFromRef with fallback → ok (OutputCommandMock)
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "falcon\n"},
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""},
		{Name: "git", Args: []string{"rev-parse", "--verify", "refs/heads/epic/bd-spq5"}, Err: errors.New("not found")},
		{Name: "git", Args: []string{"rev-parse", "--verify", "refs/remotes/origin/epic/bd-spq5"}, Err: errors.New("not found")},
	})
	cmdMock.Install()

	outMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"checkout", "-b", "epic/bd-spq5", "origin/main"}, Err: nil},
	})
	outMock.Install()

	err := EnsureWorktreeBranch("/repo", "epic/bd-spq5", "origin", "origin/main")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestEnsureWorktreeBranch_DirtyTree_CommitsWIP(t *testing.T) {
	// Call sequence:
	// 1. GetCurrentBranch → "falcon" (CommandMock)
	// 2. IsCleanWorkingTree → dirty (CommandMock)
	// 3. commitWIP: git add -A (CommandMock)
	// 4. commitWIP: git commit -m (CommandMock)
	// 5. GitFetch → ok (OutputCommandMock)
	// 6. BranchExistsLocally → ok (CommandMock)
	// 7. GitCheckout → ok (OutputCommandMock)
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "falcon\n"},
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: " M file.go\n"},
		{Name: "git", Args: []string{"add", "-A"}, Stdout: ""},
		{Name: "git", Args: []string{"commit", "-m", "WIP: daemon branch switch from falcon to epic/bd-spq5"}, Stdout: ""},
		{Name: "git", Args: []string{"rev-parse", "--verify", "refs/heads/epic/bd-spq5"}, Stdout: "abc123\n"},
	})
	cmdMock.Install()

	outMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"checkout", "epic/bd-spq5"}, Err: nil},
	})
	outMock.Install()

	err := EnsureWorktreeBranch("/repo", "epic/bd-spq5", "origin", "origin/main")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestEnsureWorktreeBranch_FetchFailsNonFatal(t *testing.T) {
	// Call sequence:
	// 1. GetCurrentBranch → "falcon" (CommandMock)
	// 2. IsCleanWorkingTree → clean (CommandMock)
	// 3. GitFetch → error (OutputCommandMock) — non-fatal, continues
	// 4. BranchExistsLocally → ok (CommandMock)
	// 5. GitCheckout → ok (OutputCommandMock)
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "falcon\n"},
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""},
		{Name: "git", Args: []string{"rev-parse", "--verify", "refs/heads/epic/bd-spq5"}, Stdout: "abc123\n"},
	})
	cmdMock.Install()

	outMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: errors.New("Could not resolve host")},
		{Args: []string{"checkout", "epic/bd-spq5"}, Err: nil},
	})
	outMock.Install()

	err := EnsureWorktreeBranch("/repo", "epic/bd-spq5", "origin", "origin/main")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCommitWIP(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		cmdMock := NewCommandMock(t, []CommandStub{
			{Name: "git", Args: []string{"add", "-A"}, Stdout: ""},
			{Name: "git", Args: []string{"commit", "-m", "WIP: test message"}, Stdout: ""},
		})
		cmdMock.Install()

		err := commitWIP("/repo", "WIP: test message")

		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("add fails", func(t *testing.T) {
		cmdMock := NewCommandMock(t, []CommandStub{
			{Name: "git", Args: []string{"add", "-A"}, Err: errors.New("add failed")},
		})
		cmdMock.Install()

		err := commitWIP("/repo", "WIP: test message")

		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("commit fails", func(t *testing.T) {
		cmdMock := NewCommandMock(t, []CommandStub{
			{Name: "git", Args: []string{"add", "-A"}, Stdout: ""},
			{Name: "git", Args: []string{"commit", "-m", "WIP: test message"}, Err: errors.New("nothing to commit")},
		})
		cmdMock.Install()

		err := commitWIP("/repo", "WIP: test message")

		if err == nil {
			t.Error("expected error, got nil")
		}
	})
}

func TestEnsureWorktreeBranch_DirtyTree_CommitFails_StashSucceeds(t *testing.T) {
	// Call sequence:
	// 1. GetCurrentBranch → "falcon" (CommandMock)
	// 2. IsCleanWorkingTree → dirty (CommandMock)
	// 3. commitWIP: git add -A → success (CommandMock)
	// 4. commitWIP: git commit -m → FAILS (CommandMock)
	// 5. GitStash - getStashCount before (CommandMock)
	// 6. GitStash - stash (OutputCommandMock)
	// 7. GitStash - getStashCount after (CommandMock)
	// 8. GitFetch (OutputCommandMock)
	// 9. BranchExistsLocally → ok (CommandMock)
	// 10. GitCheckout (OutputCommandMock)
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "falcon\n"},
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: " M file.go\n"},
		{Name: "git", Args: []string{"add", "-A"}, Stdout: ""},
		{Name: "git", Args: []string{"commit", "-m", "WIP: daemon branch switch from falcon to epic/bd-spq5"}, Err: errors.New("nothing to commit")},
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
		{Name: "git", Args: []string{"stash", "list"}, Stdout: "stash@{0}: WIP on falcon\n"},
		{Name: "git", Args: []string{"rev-parse", "--verify", "refs/heads/epic/bd-spq5"}, Stdout: "abc123\n"},
	})
	cmdMock.Install()

	outMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"stash"}, Err: nil},
		{Args: []string{"fetch", "origin"}, Err: nil},
		{Args: []string{"checkout", "epic/bd-spq5"}, Err: nil},
	})
	outMock.Install()

	err := EnsureWorktreeBranch("/repo", "epic/bd-spq5", "origin", "origin/main")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestEnsureWorktreeBranch_DirtyTree_BothFail(t *testing.T) {
	// Call sequence:
	// 1. GetCurrentBranch → "falcon" (CommandMock)
	// 2. IsCleanWorkingTree → dirty (CommandMock)
	// 3. commitWIP: git add -A → success (CommandMock)
	// 4. commitWIP: git commit -m → FAILS (CommandMock)
	// 5. GitStash - getStashCount before (CommandMock)
	// 6. GitStash - stash → FAILS (OutputCommandMock)
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "falcon\n"},
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: " M file.go\n"},
		{Name: "git", Args: []string{"add", "-A"}, Stdout: ""},
		{Name: "git", Args: []string{"commit", "-m", "WIP: daemon branch switch from falcon to epic/bd-spq5"}, Err: errors.New("nothing to commit")},
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},
	})
	cmdMock.Install()

	outMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"stash"}, Err: errors.New("stash failed")},
	})
	outMock.Install()

	err := EnsureWorktreeBranch("/repo", "epic/bd-spq5", "origin", "origin/main")

	if err == nil {
		t.Error("expected error, got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "dirty worktree and both WIP commit and stash failed") {
		t.Errorf("error message should contain expected text, got: %v", err)
	}
}

func TestEnsureWorktreeBranch_CustomRemote(t *testing.T) {
	// Verify that a custom remote ("upstream") is used for fetch and remote branch lookup.
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "falcon\n"},
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""},
		{Name: "git", Args: []string{"rev-parse", "--verify", "refs/heads/epic/bd-spq5"}, Err: errors.New("not found")},
		{Name: "git", Args: []string{"rev-parse", "--verify", "refs/remotes/upstream/epic/bd-spq5"}, Stdout: "def456\n"},
	})
	cmdMock.Install()

	outMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"fetch", "upstream"}, Err: nil},
		{Args: []string{"checkout", "-b", "epic/bd-spq5", "upstream/epic/bd-spq5"}, Err: nil},
	})
	outMock.Install()

	err := EnsureWorktreeBranch("/repo", "epic/bd-spq5", "upstream", "upstream/develop")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestEnsureWorktreeBranch_CustomRemote_FallbackToRef(t *testing.T) {
	// Custom remote, branch doesn't exist locally or remotely — falls back to ref.
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "falcon\n"},
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""},
		{Name: "git", Args: []string{"rev-parse", "--verify", "refs/heads/epic/bd-spq5"}, Err: errors.New("not found")},
		{Name: "git", Args: []string{"rev-parse", "--verify", "refs/remotes/upstream/epic/bd-spq5"}, Err: errors.New("not found")},
	})
	cmdMock.Install()

	outMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"fetch", "upstream"}, Err: nil},
		{Args: []string{"checkout", "-b", "epic/bd-spq5", "upstream/develop"}, Err: nil},
	})
	outMock.Install()

	err := EnsureWorktreeBranch("/repo", "epic/bd-spq5", "upstream", "upstream/develop")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAgentProcess_resolveRemote(t *testing.T) {
	tests := []struct {
		name       string
		repoConfig *RepoConfig
		want       string
	}{
		{"nil config", nil, "origin"},
		{"empty remote", &RepoConfig{}, "origin"},
		{"custom remote", &RepoConfig{Remote: "upstream"}, "upstream"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ap := &AgentProcess{repoConfig: tc.repoConfig}
			got := ap.resolveRemote()
			if got != tc.want {
				t.Errorf("resolveRemote() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAgentProcess_resolveRemoteBranch(t *testing.T) {
	tests := []struct {
		name       string
		repoConfig *RepoConfig
		want       string
	}{
		{"nil config", nil, "origin/main"},
		{"empty config", &RepoConfig{}, "origin/main"},
		{"custom remote and branch", &RepoConfig{Remote: "upstream", DefaultBranch: "develop"}, "upstream/develop"},
		{"custom branch only", &RepoConfig{DefaultBranch: "develop"}, "origin/develop"},
		{"custom remote only", &RepoConfig{Remote: "upstream"}, "upstream/main"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ap := &AgentProcess{repoConfig: tc.repoConfig}
			got := ap.resolveRemoteBranch()
			if got != tc.want {
				t.Errorf("resolveRemoteBranch() = %q, want %q", got, tc.want)
			}
		})
	}
}
