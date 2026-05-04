package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

func setupCompletionConfig(t *testing.T, workspacePath string, repos ...RepoConfig) {
	t.Helper()
	setupWorkspaceConfig(t, &LoomConfig{
		DefaultWorkspace: "TEST",
		Workspaces: map[string]WorkspaceConfig{
			"TEST": {
				Path:  workspacePath,
				Repos: repos,
			},
		},
	})
}

func TestGetGitBranches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mockOutput string
		mockErr    error
		want       []string
		wantErr    bool
	}{
		{
			name:       "empty_output",
			mockOutput: "",
			want:       nil,
		},
		{
			name:       "single_local_branch",
			mockOutput: "main\n",
			want:       []string{"main"},
		},
		{
			name:       "multiple_branches",
			mockOutput: "main\nfeature\nfix\n",
			want:       []string{"main", "feature", "fix"},
		},
		{
			name:       "remote_branches_deduplicated",
			mockOutput: "main\norigin/main\n",
			want:       []string{"main"},
		},
		{
			name:       "skip_head_refs",
			mockOutput: "main\nHEAD\norigin/HEAD\n",
			want:       []string{"main"},
		},
		{
			name:       "mixed_local_remote",
			mockOutput: "main\nfeature\norigin/main\norigin/develop\n",
			want:       []string{"main", "feature", "develop"},
		},
		{
			name:    "git_error",
			mockErr: errors.New("not a git repository"),
			wantErr: true,
		},
		{
			name:       "whitespace_handling",
			mockOutput: "  main  \n  \n",
			want:       []string{"main"},
		},
		{
			// Note: Current implementation only strips "origin/" prefix
			// Non-origin remotes like "upstream/" are kept as-is
			name:       "non_origin_remote_not_stripped",
			mockOutput: "main\nupstream/develop\n",
			want:       []string{"main", "upstream/develop"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewCommandMock(t, []CommandStub{{
				Name:   "git",
				Args:   []string{"branch", "-a", "--format=%(refname:short)"},
				Stdout: tc.mockOutput,
				Err:    tc.mockErr,
			}})
			mock.InstallOn(deps)

			got, err := getGitBranchesDeps(deps)

			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if !tc.wantErr {
				if len(got) != len(tc.want) {
					t.Errorf("got %d branches, want %d", len(got), len(tc.want))
				}
				for i := range got {
					if i < len(tc.want) && got[i] != tc.want[i] {
						t.Errorf("branch[%d] = %q, want %q", i, got[i], tc.want[i])
					}
				}
			}
		})
	}
}

func TestBranchCompletion(t *testing.T) {
	tests := []struct {
		name            string
		mockOutput      string
		mockErr         error
		wantCompletions []string
		wantDirective   cobra.ShellCompDirective
	}{
		{
			name:            "success",
			mockOutput:      "main\nfeature\n",
			wantCompletions: []string{"main", "feature"},
			wantDirective:   cobra.ShellCompDirectiveNoFileComp,
		},
		{
			name:            "empty_branches",
			mockOutput:      "",
			wantCompletions: nil,
			wantDirective:   cobra.ShellCompDirectiveNoFileComp,
		},
		{
			name:            "git_error",
			mockErr:         errors.New("not a git repository"),
			wantCompletions: nil,
			wantDirective:   cobra.ShellCompDirectiveError,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := NewCommandMock(t, []CommandStub{{
				Name:   "git",
				Args:   []string{"branch", "-a", "--format=%(refname:short)"},
				Stdout: tc.mockOutput,
				Err:    tc.mockErr,
			}})
			mock.Install()

			completions, directive := branchCompletion(nil, nil, "")

			if directive != tc.wantDirective {
				t.Errorf("directive = %v, want %v", directive, tc.wantDirective)
			}
			if len(completions) != len(tc.wantCompletions) {
				t.Errorf("got %d completions, want %d", len(completions), len(tc.wantCompletions))
			}
			for i := range completions {
				if i < len(tc.wantCompletions) && completions[i] != tc.wantCompletions[i] {
					t.Errorf("completion[%d] = %q, want %q", i, completions[i], tc.wantCompletions[i])
				}
			}
		})
	}
}

func TestWorktreeCompletion(t *testing.T) {
	t.Run("already_has_arg", func(t *testing.T) {
		// No mocks needed - should return early
		completions, directive := worktreeCompletion(nil, []string{"existing"}, "")

		if completions != nil {
			t.Errorf("expected nil completions, got %v", completions)
		}
		if directive != cobra.ShellCompDirectiveNoFileComp {
			t.Errorf("directive = %v, want NoFileComp", directive)
		}
	})

	t.Run("single_worktree", func(t *testing.T) {
		tmpDir := t.TempDir()
		wtPath := filepath.Join(tmpDir, "worktrees", "falcon")
		if err := os.MkdirAll(filepath.Join(wtPath, ".git"), 0755); err != nil {
			t.Fatalf("failed to create worktree dir: %v", err)
		}
		setupCompletionConfig(t, tmpDir, RepoConfig{Name: "falcon", Path: wtPath})

		// Change to temp dir and restore after
		origDir, _ := os.Getwd()
		if err := os.Chdir(tmpDir); err != nil {
			t.Fatalf("failed to chdir: %v", err)
		}
		t.Cleanup(func() { os.Chdir(origDir) })

		// Mock git branch --show-current for GetCurrentBranch (Dir empty = any)
		mock := NewCommandMock(t, []CommandStub{{
			Name:   "git",
			Args:   []string{"branch", "--show-current"},
			Stdout: "falcon-branch\n",
		}})
		mock.Install()

		completions, directive := worktreeCompletion(nil, []string{}, "")

		if directive != cobra.ShellCompDirectiveNoFileComp {
			t.Errorf("directive = %v, want NoFileComp", directive)
		}
		if len(completions) != 2 {
			t.Fatalf("got %d completions, want 2", len(completions))
		}
		if completions[0] != "TEST\tworkspace" {
			t.Errorf("completion = %q, want %q", completions[0], "TEST\tworkspace")
		}
		if completions[1] != "falcon\tfalcon-branch" {
			t.Errorf("completion = %q, want %q", completions[1], "falcon\tfalcon-branch")
		}
	})

	t.Run("multiple_worktrees", func(t *testing.T) {
		tmpDir := t.TempDir()
		for _, name := range []string{"alpha", "beta"} {
			wtDir := filepath.Join(tmpDir, "worktrees", name)
			if err := os.MkdirAll(filepath.Join(wtDir, ".git"), 0755); err != nil {
				t.Fatalf("failed to create worktree dir: %v", err)
			}
		}
		setupCompletionConfig(t, tmpDir,
			RepoConfig{Name: "alpha", Path: filepath.Join(tmpDir, "worktrees", "alpha")},
			RepoConfig{Name: "beta", Path: filepath.Join(tmpDir, "worktrees", "beta")},
		)

		origDir, _ := os.Getwd()
		if err := os.Chdir(tmpDir); err != nil {
			t.Fatalf("failed to chdir: %v", err)
		}
		t.Cleanup(func() { os.Chdir(origDir) })

		// Mock git commands for both worktrees (order depends on ReadDir)
		mock := NewCommandMock(t, []CommandStub{
			{
				Name:   "git",
				Args:   []string{"branch", "--show-current"},
				Stdout: "alpha-branch\n",
			},
			{
				Name:   "git",
				Args:   []string{"branch", "--show-current"},
				Stdout: "beta-branch\n",
			},
		})
		mock.Install()

		completions, directive := worktreeCompletion(nil, []string{}, "")

		if directive != cobra.ShellCompDirectiveNoFileComp {
			t.Errorf("directive = %v, want NoFileComp", directive)
		}
		if len(completions) != 3 {
			t.Errorf("got %d completions, want 3", len(completions))
		}
	})

	t.Run("worktrees_dir_missing", func(t *testing.T) {
		tmpDir := t.TempDir()
		// Don't create worktrees dir

		origDir, _ := os.Getwd()
		if err := os.Chdir(tmpDir); err != nil {
			t.Fatalf("failed to chdir: %v", err)
		}
		t.Cleanup(func() { os.Chdir(origDir) })

		completions, directive := worktreeCompletion(nil, []string{}, "")

		if completions != nil {
			t.Errorf("expected nil completions, got %v", completions)
		}
		if directive != cobra.ShellCompDirectiveNoFileComp {
			t.Errorf("directive = %v, want NoFileComp", directive)
		}
	})

	t.Run("no_worktrees", func(t *testing.T) {
		tmpDir := t.TempDir()
		// Create empty worktrees dir
		if err := os.MkdirAll(filepath.Join(tmpDir, "worktrees"), 0755); err != nil {
			t.Fatalf("failed to create worktrees dir: %v", err)
		}
		setupCompletionConfig(t, tmpDir)

		origDir, _ := os.Getwd()
		if err := os.Chdir(tmpDir); err != nil {
			t.Fatalf("failed to chdir: %v", err)
		}
		t.Cleanup(func() { os.Chdir(origDir) })

		completions, directive := worktreeCompletion(nil, []string{}, "")

		if directive != cobra.ShellCompDirectiveNoFileComp {
			t.Errorf("directive = %v, want NoFileComp", directive)
		}
		if len(completions) != 1 {
			t.Errorf("got %d completions, want 1", len(completions))
		}
		if completions[0] != "TEST\tworkspace" {
			t.Errorf("completion = %q, want %q", completions[0], "TEST\tworkspace")
		}
	})

	t.Run("skip_non_git_directories", func(t *testing.T) {
		tmpDir := t.TempDir()
		// Create one valid worktree and one non-git directory
		validWt := filepath.Join(tmpDir, "worktrees", "valid")
		if err := os.MkdirAll(filepath.Join(validWt, ".git"), 0755); err != nil {
			t.Fatalf("failed to create valid worktree: %v", err)
		}
		invalidWt := filepath.Join(tmpDir, "worktrees", "invalid")
		if err := os.MkdirAll(invalidWt, 0755); err != nil {
			t.Fatalf("failed to create invalid dir: %v", err)
		}
		setupCompletionConfig(t, tmpDir,
			RepoConfig{Name: "valid", Path: validWt},
			RepoConfig{Name: "invalid", Path: invalidWt},
		)

		origDir, _ := os.Getwd()
		if err := os.Chdir(tmpDir); err != nil {
			t.Fatalf("failed to chdir: %v", err)
		}
		t.Cleanup(func() { os.Chdir(origDir) })

		mock := NewCommandMock(t, []CommandStub{{
			Name:   "git",
			Args:   []string{"branch", "--show-current"},
			Stdout: "valid-branch\n",
		}})
		mock.Install()

		completions, directive := worktreeCompletion(nil, []string{}, "")

		if directive != cobra.ShellCompDirectiveNoFileComp {
			t.Errorf("directive = %v, want NoFileComp", directive)
		}
		if len(completions) != 2 {
			t.Fatalf("got %d completions, want 2", len(completions))
		}
		if completions[1] != "valid\tvalid-branch" {
			t.Errorf("completion = %q, want %q", completions[1], "valid\tvalid-branch")
		}
	})

	t.Run("git_branch_error_shows_unknown", func(t *testing.T) {
		tmpDir := t.TempDir()
		wtPath := filepath.Join(tmpDir, "worktrees", "falcon")
		if err := os.MkdirAll(filepath.Join(wtPath, ".git"), 0755); err != nil {
			t.Fatalf("failed to create worktree dir: %v", err)
		}
		setupCompletionConfig(t, tmpDir, RepoConfig{Name: "falcon", Path: wtPath})

		origDir, _ := os.Getwd()
		if err := os.Chdir(tmpDir); err != nil {
			t.Fatalf("failed to chdir: %v", err)
		}
		t.Cleanup(func() { os.Chdir(origDir) })

		// Mock git branch command to fail (e.g., detached HEAD)
		mock := NewCommandMock(t, []CommandStub{{
			Name: "git",
			Args: []string{"branch", "--show-current"},
			Err:  errors.New("detached HEAD"),
		}})
		mock.Install()

		completions, directive := worktreeCompletion(nil, []string{}, "")

		if directive != cobra.ShellCompDirectiveNoFileComp {
			t.Errorf("directive = %v, want NoFileComp", directive)
		}
		if len(completions) != 2 {
			t.Fatalf("got %d completions, want 2", len(completions))
		}
		if completions[1] != "falcon\tunknown" {
			t.Errorf("completion = %q, want %q", completions[1], "falcon\tunknown")
		}
	})
}

func TestWorktreeThenBranchCompletion(t *testing.T) {
	t.Run("no_args_returns_worktrees", func(t *testing.T) {
		tmpDir := t.TempDir()
		wtDir := filepath.Join(tmpDir, "worktrees", "falcon")
		if err := os.MkdirAll(filepath.Join(wtDir, ".git"), 0755); err != nil {
			t.Fatalf("failed to create worktree dir: %v", err)
		}
		setupCompletionConfig(t, tmpDir, RepoConfig{Name: "falcon", Path: wtDir})

		origDir, _ := os.Getwd()
		if err := os.Chdir(tmpDir); err != nil {
			t.Fatalf("failed to chdir: %v", err)
		}
		t.Cleanup(func() { os.Chdir(origDir) })

		mock := NewCommandMock(t, []CommandStub{{
			Name:   "git",
			Args:   []string{"branch", "--show-current"},
			Stdout: "falcon\n",
		}})
		mock.Install()

		completions, directive := worktreeThenBranchCompletion(nil, []string{}, "")

		if directive != cobra.ShellCompDirectiveNoFileComp {
			t.Errorf("directive = %v, want NoFileComp", directive)
		}
		if len(completions) != 2 {
			t.Errorf("got %d completions, want 2", len(completions))
		}
	})

	t.Run("one_arg_returns_branches", func(t *testing.T) {
		mock := NewCommandMock(t, []CommandStub{{
			Name:   "git",
			Args:   []string{"branch", "-a", "--format=%(refname:short)"},
			Stdout: "main\nfeature\n",
		}})
		mock.Install()

		completions, directive := worktreeThenBranchCompletion(nil, []string{"falcon"}, "")

		if directive != cobra.ShellCompDirectiveNoFileComp {
			t.Errorf("directive = %v, want NoFileComp", directive)
		}
		if len(completions) != 2 {
			t.Errorf("got %d completions, want 2", len(completions))
		}
	})

	t.Run("two_args_stops_completion", func(t *testing.T) {
		completions, directive := worktreeThenBranchCompletion(nil, []string{"falcon", "main"}, "")

		if completions != nil {
			t.Errorf("expected nil completions, got %v", completions)
		}
		if directive != cobra.ShellCompDirectiveNoFileComp {
			t.Errorf("directive = %v, want NoFileComp", directive)
		}
	})
}
