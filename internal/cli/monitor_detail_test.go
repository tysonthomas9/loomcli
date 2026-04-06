//go:build ignore

package cli

import (
	"fmt"
	"strings"
	"testing"
)

// TestGetGitHubRemoteURL tests conversion of git remote URLs to GitHub HTTPS URLs.
func TestGetGitHubRemoteURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		stdout   string
		err      error
		expected string
	}{
		{
			name:     "SSH URL",
			stdout:   "git@github.com:user/repo.git\n",
			expected: "https://github.com/user/repo",
		},
		{
			name:     "SSH URL without trailing newline",
			stdout:   "git@github.com:user/repo.git",
			expected: "https://github.com/user/repo",
		},
		{
			name:     "SSH URL without .git suffix",
			stdout:   "git@github.com:user/repo\n",
			expected: "https://github.com/user/repo",
		},
		{
			name:     "HTTPS URL with .git suffix",
			stdout:   "https://github.com/user/repo.git\n",
			expected: "https://github.com/user/repo",
		},
		{
			name:     "HTTPS URL without .git suffix",
			stdout:   "https://github.com/user/repo\n",
			expected: "https://github.com/user/repo",
		},
		{
			name:     "non-GitHub remote",
			stdout:   "https://gitlab.com/user/repo.git\n",
			expected: "",
		},
		{
			name:     "non-GitHub SSH remote",
			stdout:   "git@gitlab.com:user/repo.git\n",
			expected: "",
		},
		{
			name:     "git command error",
			stdout:   "",
			err:      fmt.Errorf("not a git repository"),
			expected: "",
		},
		{
			name:     "empty output",
			stdout:   "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewCommandMock(t, []CommandStub{
				{
					Name:   "git",
					Args:   []string{"remote", "get-url", "origin"},
					Stdout: tt.stdout,
					Err:    tt.err,
				},
			})
			mock.InstallOn(deps)

			result := getGitHubRemoteURLDeps(deps, "/some/path")
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

// TestGetWorktreeCommitDetails tests fetching recent commits ahead of integration branch.
func TestGetWorktreeCommitDetails(t *testing.T) {
	t.Parallel()
	t.Run("normal commits with github URL", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewCommandMock(t, []CommandStub{
			{
				Name: "git",
				Args: []string{"log", "origin/main..HEAD", "--format=%h|%s", "-n", "5"},
				Stdout: "abc1234|First commit message\n" +
					"def5678|Second commit message\n" +
					"ghi9012|Third commit message\n",
			},
		})
		mock.InstallOn(deps)

		commits := getWorktreeCommitDetailsDeps(deps, "/some/path", "main", 5, "https://github.com/user/repo", "")
		if len(commits) != 3 {
			t.Fatalf("expected 3 commits, got %d", len(commits))
		}

		// Verify first commit
		if commits[0].Hash != "abc1234" {
			t.Errorf("expected hash 'abc1234', got %q", commits[0].Hash)
		}
		if commits[0].Message != "First commit message" {
			t.Errorf("expected message 'First commit message', got %q", commits[0].Message)
		}
		if commits[0].URL != "https://github.com/user/repo/commit/abc1234" {
			t.Errorf("expected URL 'https://github.com/user/repo/commit/abc1234', got %q", commits[0].URL)
		}

		// Verify second commit
		if commits[1].Hash != "def5678" {
			t.Errorf("expected hash 'def5678', got %q", commits[1].Hash)
		}
		if commits[1].URL != "https://github.com/user/repo/commit/def5678" {
			t.Errorf("expected URL with commit hash, got %q", commits[1].URL)
		}
	})

	t.Run("commits without github URL", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewCommandMock(t, []CommandStub{
			{
				Name:   "git",
				Args:   []string{"log", "origin/main..HEAD", "--format=%h|%s", "-n", "3"},
				Stdout: "abc1234|Some commit\n",
			},
		})
		mock.InstallOn(deps)

		commits := getWorktreeCommitDetailsDeps(deps, "/some/path", "main", 3, "", "")
		if len(commits) != 1 {
			t.Fatalf("expected 1 commit, got %d", len(commits))
		}
		if commits[0].URL != "" {
			t.Errorf("expected empty URL when no github URL, got %q", commits[0].URL)
		}
		if commits[0].Hash != "abc1234" {
			t.Errorf("expected hash 'abc1234', got %q", commits[0].Hash)
		}
		if commits[0].Message != "Some commit" {
			t.Errorf("expected message 'Some commit', got %q", commits[0].Message)
		}
	})

	t.Run("no commits ahead", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewCommandMock(t, []CommandStub{
			{
				Name:   "git",
				Args:   []string{"log", "origin/main..HEAD", "--format=%h|%s", "-n", "10"},
				Stdout: "",
			},
		})
		mock.InstallOn(deps)

		commits := getWorktreeCommitDetailsDeps(deps, "/some/path", "main", 10, "https://github.com/user/repo", "")
		if commits != nil {
			t.Errorf("expected nil for no commits, got %v", commits)
		}
	})

	t.Run("git error returns nil", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewCommandMock(t, []CommandStub{
			{
				Name: "git",
				Args: []string{"log", "origin/main..HEAD", "--format=%h|%s", "-n", "10"},
				Err:  fmt.Errorf("fatal: bad revision"),
			},
		})
		mock.InstallOn(deps)

		commits := getWorktreeCommitDetailsDeps(deps, "/some/path", "main", 10, "https://github.com/user/repo", "")
		if commits != nil {
			t.Errorf("expected nil on git error, got %v", commits)
		}
	})

	t.Run("uses branch override", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewCommandMock(t, []CommandStub{
			{
				Name:   "git",
				Args:   []string{"log", "origin/develop..HEAD", "--format=%h|%s", "-n", "5"},
				Stdout: "abc1234|Commit on develop\n",
			},
		})
		mock.InstallOn(deps)

		commits := getWorktreeCommitDetailsDeps(deps, "/some/path", "main", 5, "", "develop")
		if len(commits) != 1 {
			t.Fatalf("expected 1 commit, got %d", len(commits))
		}
		if commits[0].Message != "Commit on develop" {
			t.Errorf("expected message 'Commit on develop', got %q", commits[0].Message)
		}
	})

	t.Run("commit message with pipe character", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewCommandMock(t, []CommandStub{
			{
				Name:   "git",
				Args:   []string{"log", "origin/main..HEAD", "--format=%h|%s", "-n", "5"},
				Stdout: "abc1234|Fix bug | handle edge case\n",
			},
		})
		mock.InstallOn(deps)

		commits := getWorktreeCommitDetailsDeps(deps, "/some/path", "main", 5, "", "")
		if len(commits) != 1 {
			t.Fatalf("expected 1 commit, got %d", len(commits))
		}
		// SplitN with 2 means the pipe in the message is preserved
		if commits[0].Message != "Fix bug | handle edge case" {
			t.Errorf("expected message with pipe preserved, got %q", commits[0].Message)
		}
	})

	t.Run("whitespace-only output returns nil", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewCommandMock(t, []CommandStub{
			{
				Name:   "git",
				Args:   []string{"log", "origin/main..HEAD", "--format=%h|%s", "-n", "10"},
				Stdout: "   \n  \n",
			},
		})
		mock.InstallOn(deps)

		commits := getWorktreeCommitDetailsDeps(deps, "/some/path", "main", 10, "", "")
		if commits != nil {
			t.Errorf("expected nil for whitespace-only output, got %v", commits)
		}
	})
}

// TestGetWorktreeFileChanges tests parsing of git status --porcelain output.
func TestGetWorktreeFileChanges(t *testing.T) {
	t.Parallel()
	t.Run("mixed statuses", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		porcelainOutput := " M internal/cli/monitor.go\n" +
			"A  internal/cli/new_file.go\n" +
			" D internal/cli/deleted.go\n" +
			"?? untracked_file.txt\n" +
			" R renamed_file.go\n"

		mock := NewCommandMock(t, []CommandStub{
			{
				Name:   "git",
				Args:   []string{"status", "--porcelain"},
				Stdout: porcelainOutput,
			},
		})
		mock.InstallOn(deps)

		changes := getWorktreeFileChangesDeps(deps, "/some/path")
		if len(changes) != 5 {
			t.Fatalf("expected 5 changes, got %d", len(changes))
		}

		// Verify statuses are parsed correctly
		expectedStatuses := []struct {
			status string
			path   string
		}{
			{"M", "internal/cli/monitor.go"},
			{"A", "internal/cli/new_file.go"},
			{"D", "internal/cli/deleted.go"},
			{"??", "untracked_file.txt"},
			{"R", "renamed_file.go"},
		}

		for i, expected := range expectedStatuses {
			if changes[i].Status != expected.status {
				t.Errorf("change %d: expected status %q, got %q", i, expected.status, changes[i].Status)
			}
			if changes[i].Path != expected.path {
				t.Errorf("change %d: expected path %q, got %q", i, expected.path, changes[i].Path)
			}
		}
	})

	t.Run("empty working tree", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewCommandMock(t, []CommandStub{
			{
				Name:   "git",
				Args:   []string{"status", "--porcelain"},
				Stdout: "",
			},
		})
		mock.InstallOn(deps)

		changes := getWorktreeFileChangesDeps(deps, "/some/path")
		if changes != nil {
			t.Errorf("expected nil for empty working tree, got %v", changes)
		}
	})

	t.Run("git error returns nil", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewCommandMock(t, []CommandStub{
			{
				Name: "git",
				Args: []string{"status", "--porcelain"},
				Err:  fmt.Errorf("not a git repository"),
			},
		})
		mock.InstallOn(deps)

		changes := getWorktreeFileChangesDeps(deps, "/some/path")
		if changes != nil {
			t.Errorf("expected nil on git error, got %v", changes)
		}
	})

	t.Run("more than 20 files limited", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		// Generate 25 file changes
		var lines []string
		for i := 0; i < 25; i++ {
			lines = append(lines, fmt.Sprintf(" M file_%02d.go", i))
		}
		porcelainOutput := strings.Join(lines, "\n") + "\n"

		mock := NewCommandMock(t, []CommandStub{
			{
				Name:   "git",
				Args:   []string{"status", "--porcelain"},
				Stdout: porcelainOutput,
			},
		})
		mock.InstallOn(deps)

		changes := getWorktreeFileChangesDeps(deps, "/some/path")
		if len(changes) != 20 {
			t.Errorf("expected 20 changes (limited), got %d", len(changes))
		}

		// Verify first and last included files
		if changes[0].Path != "file_00.go" {
			t.Errorf("expected first file 'file_00.go', got %q", changes[0].Path)
		}
		if changes[19].Path != "file_19.go" {
			t.Errorf("expected last file 'file_19.go', got %q", changes[19].Path)
		}
	})

	t.Run("exactly 20 files", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		var lines []string
		for i := 0; i < 20; i++ {
			lines = append(lines, fmt.Sprintf(" M file_%02d.go", i))
		}
		porcelainOutput := strings.Join(lines, "\n") + "\n"

		mock := NewCommandMock(t, []CommandStub{
			{
				Name:   "git",
				Args:   []string{"status", "--porcelain"},
				Stdout: porcelainOutput,
			},
		})
		mock.InstallOn(deps)

		changes := getWorktreeFileChangesDeps(deps, "/some/path")
		if len(changes) != 20 {
			t.Errorf("expected 20 changes, got %d", len(changes))
		}
	})

	t.Run("whitespace-only output returns nil", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewCommandMock(t, []CommandStub{
			{
				Name:   "git",
				Args:   []string{"status", "--porcelain"},
				Stdout: "   \n  \n",
			},
		})
		mock.InstallOn(deps)

		changes := getWorktreeFileChangesDeps(deps, "/some/path")
		if changes != nil {
			t.Errorf("expected nil for whitespace-only output, got %v", changes)
		}
	})

	t.Run("short lines are skipped", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		// Lines shorter than 4 chars should be skipped
		porcelainOutput := " M valid_file.go\n" +
			"ab\n" + // too short (len < 4)
			" M another_file.go\n"

		mock := NewCommandMock(t, []CommandStub{
			{
				Name:   "git",
				Args:   []string{"status", "--porcelain"},
				Stdout: porcelainOutput,
			},
		})
		mock.InstallOn(deps)

		changes := getWorktreeFileChangesDeps(deps, "/some/path")
		if len(changes) != 2 {
			t.Fatalf("expected 2 changes (short line skipped), got %d", len(changes))
		}
		if changes[0].Path != "valid_file.go" {
			t.Errorf("expected path 'valid_file.go', got %q", changes[0].Path)
		}
		if changes[1].Path != "another_file.go" {
			t.Errorf("expected path 'another_file.go', got %q", changes[1].Path)
		}
	})

	t.Run("staged modification status", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		// Staged modifications have a letter in the first column
		porcelainOutput := "M  staged_file.go\n" +
			"MM both_modified.go\n"

		mock := NewCommandMock(t, []CommandStub{
			{
				Name:   "git",
				Args:   []string{"status", "--porcelain"},
				Stdout: porcelainOutput,
			},
		})
		mock.InstallOn(deps)

		changes := getWorktreeFileChangesDeps(deps, "/some/path")
		if len(changes) != 2 {
			t.Fatalf("expected 2 changes, got %d", len(changes))
		}
		if changes[0].Status != "M" {
			t.Errorf("expected status 'M' for staged file, got %q", changes[0].Status)
		}
		if changes[0].Path != "staged_file.go" {
			t.Errorf("expected path 'staged_file.go', got %q", changes[0].Path)
		}
		if changes[1].Status != "MM" {
			t.Errorf("expected status 'MM' for both-modified file, got %q", changes[1].Status)
		}
	})
}
