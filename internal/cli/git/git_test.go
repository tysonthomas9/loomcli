package git

import (
	"errors"
	"strings"
	"testing"
)

func TestRunGitCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		dir        string
		args       []string
		mockStdout string
		mockStderr string
		mockErr    error
		wantOutput string
		wantErr    bool
	}{
		{
			name:       "successful command with no output",
			dir:        "/repo",
			args:       []string{"status", "--porcelain"},
			mockStdout: "",
			wantOutput: "",
			wantErr:    false,
		},
		{
			name:       "successful command with output",
			dir:        "/repo",
			args:       []string{"branch", "--show-current"},
			mockStdout: "feature/test\n",
			wantOutput: "feature/test\n",
			wantErr:    false,
		},
		{
			name:       "command fails",
			dir:        "/repo",
			args:       []string{"checkout", "nonexistent"},
			mockStderr: "error: pathspec 'nonexistent' did not match\n",
			mockErr:    errors.New("exit status 1"),
			wantErr:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewCommandMock(t, []CommandStub{{
				Dir:    tc.dir,
				Name:   "git",
				Args:   tc.args,
				Stdout: tc.mockStdout,
				Stderr: tc.mockStderr,
				Err:    tc.mockErr,
			}})
			mock.InstallOn(deps)

			output, err := runGit(deps, tc.dir, tc.args...)

			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if output != tc.wantOutput {
				t.Errorf("output = %q, want %q", output, tc.wantOutput)
			}
		})
	}
}

func TestIsCleanWorkingTree(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mockOutput string
		mockErr    error
		wantClean  bool
		wantErr    bool
	}{
		{
			name:       "clean working tree",
			mockOutput: "",
			wantClean:  true,
		},
		{
			name:       "clean with whitespace only",
			mockOutput: "  \n",
			wantClean:  true,
		},
		{
			name:       "dirty working tree - modified file",
			mockOutput: " M file.go\n",
			wantClean:  false,
		},
		{
			name:       "dirty working tree - untracked files",
			mockOutput: "?? new.go\n",
			wantClean:  false,
		},
		{
			name:    "git error",
			mockErr: errors.New("not a git repository"),
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewCommandMock(t, []CommandStub{{
				Name:   "git",
				Args:   []string{"status", "--porcelain"},
				Stdout: tc.mockOutput,
				Err:    tc.mockErr,
			}})
			mock.InstallOn(deps)

			clean, err := isCleanWorkingTreeDeps(deps, "/repo")

			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if !tc.wantErr && clean != tc.wantClean {
				t.Errorf("clean = %v, want %v", clean, tc.wantClean)
			}
		})
	}
}

func TestGetConflictedFiles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mockOutput string
		mockErr    error
		wantFiles  []string
		wantErr    bool
	}{
		{
			name:       "no conflicts",
			mockOutput: "",
			wantFiles:  nil,
		},
		{
			name:       "single conflict",
			mockOutput: "src/main.go\n",
			wantFiles:  []string{"src/main.go"},
		},
		{
			name:       "multiple conflicts",
			mockOutput: "src/main.go\npkg/util.go\nREADME.md\n",
			wantFiles:  []string{"src/main.go", "pkg/util.go", "README.md"},
		},
		{
			name:    "git error",
			mockErr: errors.New("not a git repository"),
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewCommandMock(t, []CommandStub{{
				Name:   "git",
				Args:   []string{"diff", "--name-only", "--diff-filter=U"},
				Stdout: tc.mockOutput,
				Err:    tc.mockErr,
			}})
			mock.InstallOn(deps)

			files, err := getConflictedFilesDeps(deps, "/repo")

			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if len(files) != len(tc.wantFiles) {
				t.Errorf("got %d files, want %d", len(files), len(tc.wantFiles))
			}
			for i, f := range files {
				if f != tc.wantFiles[i] {
					t.Errorf("file[%d] = %q, want %q", i, f, tc.wantFiles[i])
				}
			}
		})
	}
}

func TestHasCommitsBetween(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		target     string
		source     string
		mockOutput string
		mockErr    error
		wantHas    bool
	}{
		{
			name:       "has commits",
			target:     "main",
			source:     "feature",
			mockOutput: "abc123 commit message\ndef456 another commit\n",
			wantHas:    true,
		},
		{
			name:       "no commits",
			target:     "main",
			source:     "feature",
			mockOutput: "",
			wantHas:    false,
		},
		{
			name:       "whitespace only",
			target:     "main",
			source:     "feature",
			mockOutput: "  \n",
			wantHas:    false,
		},
		{
			name:    "git error - assume has commits",
			target:  "main",
			source:  "feature",
			mockErr: errors.New("ambiguous ref"),
			wantHas: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewCommandMock(t, []CommandStub{{
				Name:   "git",
				Args:   []string{"log", tc.target + ".." + tc.source, "--oneline"},
				Stdout: tc.mockOutput,
				Err:    tc.mockErr,
			}})
			mock.InstallOn(deps)

			hasCommits, _ := hasCommitsBetweenDeps(deps, "/repo", tc.target, tc.source)

			if hasCommits != tc.wantHas {
				t.Errorf("hasCommits = %v, want %v", hasCommits, tc.wantHas)
			}
		})
	}
}

func TestRunGitCommandWithOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dir     string
		args    []string
		mockErr error
		wantErr bool
	}{
		{
			name:    "success",
			dir:     "/repo",
			args:    []string{"status"},
			mockErr: nil,
			wantErr: false,
		},
		{
			name:    "error",
			dir:     "/repo",
			args:    []string{"checkout", "nonexistent"},
			mockErr: errors.New("pathspec did not match"),
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: tc.args,
				Err:  tc.mockErr,
			}})
			mock.InstallOn(deps)

			err := runGitOutput(deps, tc.dir, tc.args...)

			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestGitFetch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dir     string
		mockErr error
		wantErr bool
	}{
		{"success", "/repo", nil, false},
		{"network_error", "/repo", errors.New("Could not resolve host"), true},
		{"auth_error", "/repo", errors.New("Authentication failed"), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: []string{"fetch", "origin"},
				Err:  tc.mockErr,
			}})
			mock.InstallOn(deps)

			err := gitFetch(deps, tc.dir)

			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestGitCheckout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dir     string
		branch  string
		mockErr error
		wantErr bool
	}{
		{"success", "/repo", "main", nil, false},
		{"branch_not_found", "/repo", "nonexistent", errors.New("pathspec did not match"), true},
		{"uncommitted_changes", "/repo", "other", errors.New("uncommitted changes"), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: []string{"checkout", tc.branch},
				Err:  tc.mockErr,
			}})
			mock.InstallOn(deps)

			err := gitCheckout(deps, tc.dir, tc.branch)

			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestGitPull(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dir     string
		branch  string
		mockErr error
		wantErr bool
	}{
		{"success", "/repo", "main", nil, false},
		{"merge_conflict", "/repo", "main", errors.New("CONFLICT"), true},
		{"not_fast_forward", "/repo", "main", errors.New("non-fast-forward"), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: []string{"pull", "origin", tc.branch},
				Err:  tc.mockErr,
			}})
			mock.InstallOn(deps)

			err := gitPull(deps, tc.dir, tc.branch)

			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestGitMerge(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dir     string
		branch  string
		message string
		mockErr error
		wantErr bool
	}{
		{"success", "/repo", "feature", "Merge feature", nil, false},
		{"merge_conflict", "/repo", "feature", "Merge feature", errors.New("CONFLICT"), true},
		{"already_merged", "/repo", "feature", "Merge feature", nil, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: []string{"merge", "-m", tc.message, "--", tc.branch},
				Err:  tc.mockErr,
			}})
			mock.InstallOn(deps)

			err := gitMerge(deps, tc.dir, tc.branch, tc.message)

			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestGitMergeOrigin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dir     string
		branch  string
		message string
		mockErr error
		wantErr bool
	}{
		{"success", "/repo", "main", "Merge origin/main", nil, false},
		{"remote_not_found", "/repo", "nonexistent", "Merge", errors.New("not something we can merge"), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: []string{"merge", "origin/" + tc.branch, "-m", tc.message},
				Err:  tc.mockErr,
			}})
			mock.InstallOn(deps)

			err := gitMergeOrigin(deps, tc.dir, tc.branch, tc.message)

			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestGitPush(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dir     string
		branch  string
		mockErr error
		wantErr bool
	}{
		{"success", "/repo", "main", nil, false},
		{"rejected_non_fast_forward", "/repo", "main", errors.New("rejected"), true},
		{"auth_error", "/repo", "main", errors.New("Authentication failed"), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: []string{"push", "origin", tc.branch},
				Err:  tc.mockErr,
			}})
			mock.InstallOn(deps)

			err := gitPush(deps, tc.dir, tc.branch)

			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestGitPushForce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dir     string
		branch  string
		mockErr error
		wantErr bool
	}{
		{"success", "/repo", "feature", nil, false},
		{"protected_branch", "/repo", "main", errors.New("protected branch"), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: []string{"push", "origin", tc.branch, "--force"},
				Err:  tc.mockErr,
			}})
			mock.InstallOn(deps)

			err := gitPushForce(deps, tc.dir, tc.branch)

			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestGitReset(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dir     string
		ref     string
		mockErr error
		wantErr bool
	}{
		{"success_to_head", "/repo", "HEAD", nil, false},
		{"success_to_commit", "/repo", "abc1234", nil, false},
		{"success_to_origin", "/repo", "origin/main", nil, false},
		{"invalid_ref", "/repo", "nonexistent", errors.New("ambiguous argument"), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: []string{"reset", "--hard", tc.ref},
				Err:  tc.mockErr,
			}})
			mock.InstallOn(deps)

			err := gitReset(deps, tc.dir, tc.ref)

			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestGitCleanDryRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		dir        string
		mockStdout string
		mockErr    error
		wantOutput string
		wantErr    bool
	}{
		{
			name:       "files to clean",
			dir:        "/repo",
			mockStdout: "Would remove test.txt\nWould remove screenshots/\n",
			wantOutput: "Would remove test.txt\nWould remove screenshots/\n",
		},
		{
			name:       "nothing to clean",
			dir:        "/repo",
			mockStdout: "",
			wantOutput: "",
		},
		{
			name:    "git error",
			dir:     "/repo",
			mockErr: errors.New("not a git repository"),
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewCommandMock(t, []CommandStub{{
				Dir:    tc.dir,
				Name:   "git",
				Args:   []string{"clean", "-fdn"},
				Stdout: tc.mockStdout,
				Stderr: "",
				Err:    tc.mockErr,
			}})
			mock.InstallOn(deps)

			output, err := gitCleanDryRun(deps, tc.dir)

			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if output != tc.wantOutput {
				t.Errorf("output = %q, want %q", output, tc.wantOutput)
			}
		})
	}
}

func TestGitClean(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dir     string
		mockErr error
		wantErr bool
	}{
		{"success", "/repo", nil, false},
		{"nothing_to_clean", "/repo", nil, false},
		{"permission_error", "/repo", errors.New("Permission denied"), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: []string{"clean", "-fd"},
				Err:  tc.mockErr,
			}})
			mock.InstallOn(deps)

			err := gitClean(deps, tc.dir)

			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestResolveRemote(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{"empty defaults to origin", "", "origin"},
		{"non-empty returns as-is", "upstream", "upstream"},
		{"origin stays origin", "origin", "origin"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := resolveRemote(tc.input)
			if got != tc.expect {
				t.Errorf("resolveRemote(%q) = %q, want %q", tc.input, got, tc.expect)
			}
		})
	}
}

func TestGitFetchRemote(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		dir        string
		remote     string
		wantRemote string
		mockErr    error
		wantErr    bool
	}{
		{"empty remote defaults to origin", "/repo", "", "origin", nil, false},
		{"custom remote", "/repo", "upstream", "upstream", nil, false},
		{"network error", "/repo", "upstream", "upstream", errors.New("Could not resolve host"), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: []string{"fetch", tc.wantRemote},
				Err:  tc.mockErr,
			}})
			mock.InstallOn(deps)

			err := gitFetchRemote(deps, tc.dir, tc.remote)

			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestGitMergeRemote(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		dir        string
		remote     string
		branch     string
		message    string
		wantRemote string
		mockErr    error
		wantErr    bool
	}{
		{"empty remote defaults to origin", "/repo", "", "main", "Merge msg", "origin", nil, false},
		{"custom remote", "/repo", "upstream", "main", "Merge msg", "upstream", nil, false},
		{"merge conflict", "/repo", "upstream", "feat", "Merge", "upstream", errors.New("CONFLICT"), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: []string{"merge", tc.wantRemote + "/" + tc.branch, "-m", tc.message},
				Err:  tc.mockErr,
			}})
			mock.InstallOn(deps)

			err := gitMergeRemote(deps, tc.dir, tc.remote, tc.branch, tc.message)

			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestGitPushRemote(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		dir        string
		remote     string
		branch     string
		wantRemote string
		mockErr    error
		wantErr    bool
	}{
		{"empty remote defaults to origin", "/repo", "", "main", "origin", nil, false},
		{"custom remote", "/repo", "upstream", "main", "upstream", nil, false},
		{"push rejected", "/repo", "upstream", "main", "upstream", errors.New("rejected"), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: []string{"push", tc.wantRemote, tc.branch},
				Err:  tc.mockErr,
			}})
			mock.InstallOn(deps)

			err := gitPushRemote(deps, tc.dir, tc.remote, tc.branch)

			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestGitPullRemote(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		dir        string
		remote     string
		branch     string
		wantRemote string
		mockErr    error
		wantErr    bool
	}{
		{"empty remote defaults to origin", "/repo", "", "main", "origin", nil, false},
		{"custom remote", "/repo", "upstream", "main", "upstream", nil, false},
		{"pull fails", "/repo", "upstream", "main", "upstream", errors.New("non-fast-forward"), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: []string{"pull", tc.wantRemote, tc.branch},
				Err:  tc.mockErr,
			}})
			mock.InstallOn(deps)

			err := gitPullRemote(deps, tc.dir, tc.remote, tc.branch)

			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestHasCommitsBetweenRemote(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		remote     string
		target     string
		source     string
		wantRemote string
		mockOutput string
		mockErr    error
		wantHas    bool
	}{
		{
			name:       "empty remote defaults to origin",
			remote:     "",
			target:     "main",
			source:     "feature",
			wantRemote: "origin",
			mockOutput: "abc123 commit message\n",
			wantHas:    true,
		},
		{
			name:       "custom remote with commits",
			remote:     "upstream",
			target:     "main",
			source:     "feature",
			wantRemote: "upstream",
			mockOutput: "abc123 commit message\n",
			wantHas:    true,
		},
		{
			name:       "no commits",
			remote:     "upstream",
			target:     "main",
			source:     "feature",
			wantRemote: "upstream",
			mockOutput: "",
			wantHas:    false,
		},
		{
			name:       "git error assumes has commits",
			remote:     "upstream",
			target:     "main",
			source:     "feature",
			wantRemote: "upstream",
			mockErr:    errors.New("ambiguous ref"),
			wantHas:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewCommandMock(t, []CommandStub{{
				Name:   "git",
				Args:   []string{"log", tc.wantRemote + "/" + tc.target + ".." + tc.source, "--oneline"},
				Stdout: tc.mockOutput,
				Err:    tc.mockErr,
			}})
			mock.InstallOn(deps)

			hasCommits, _ := hasCommitsBetweenRemoteDeps(deps, "/repo", tc.remote, tc.target, tc.source)

			if hasCommits != tc.wantHas {
				t.Errorf("hasCommits = %v, want %v", hasCommits, tc.wantHas)
			}
		})
	}
}

func TestGitStash_DirtyWorkingTree(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	// When tracked changes exist, git stash increases stash count → stashed=true
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                  // before: 0 entries
		{Name: "git", Args: []string{"stash", "list"}, Stdout: "stash@{0}: WIP on main: abc1234\n"}, // after: 1 entry
	})
	cmdMock.InstallOn(deps)

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"stash"}, Err: nil},
	})
	outputMock.InstallOn(deps)

	stashed, err := gitStash(deps, "/repo")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !stashed {
		t.Error("expected stashed to be true for dirty working tree")
	}
}

func TestGitStash_UntrackedFilesOnly(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	// When only untracked files exist, git stash is a no-op → stash count unchanged → stashed=false
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""}, // before: 0 entries
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""}, // after: still 0 entries
	})
	cmdMock.InstallOn(deps)

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"stash"}, Err: nil}, // git stash runs but is a no-op
	})
	outputMock.InstallOn(deps)

	stashed, err := gitStash(deps, "/repo")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if stashed {
		t.Error("expected stashed to be false when only untracked files exist")
	}
}

func TestGitStash_NothingToStash(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	// Clean working tree: git stash is a no-op, stash count stays 0
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""}, // before: 0
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""}, // after: 0
	})
	cmdMock.InstallOn(deps)

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"stash"}, Err: nil},
	})
	outputMock.InstallOn(deps)

	stashed, err := gitStash(deps, "/repo")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if stashed {
		t.Error("expected stashed to be false for clean working tree")
	}
}

func TestGitStash_StashListFails(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	// When initial stash list fails, GitStash should return the error
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"stash", "list"}, Err: errors.New("not a git repository")},
	})
	cmdMock.InstallOn(deps)

	stashed, err := gitStash(deps, "/repo")

	if err == nil {
		t.Error("expected error, got nil")
	}
	if stashed {
		t.Error("expected stashed to be false when error occurs")
	}
}

func TestGitStash_StashCommandFails(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	// When git stash command fails, GitStash should return wrapped error
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""}, // before: succeeds
	})
	cmdMock.InstallOn(deps)

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"stash"}, Err: errors.New("stash failed")},
	})
	outputMock.InstallOn(deps)

	stashed, err := gitStash(deps, "/repo")

	if err == nil {
		t.Error("expected error, got nil")
	}
	if stashed {
		t.Error("expected stashed to be false when stash command fails")
	}
}

func TestGitStash_SecondStashListFails(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	// When the second stash list call fails after a successful stash, return error
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"stash", "list"}, Stdout: ""},                                // before: 0
		{Name: "git", Args: []string{"stash", "list"}, Err: errors.New("unexpected stash error")}, // after: fails
	})
	cmdMock.InstallOn(deps)

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"stash"}, Err: nil}, // stash succeeds
	})
	outputMock.InstallOn(deps)

	stashed, err := gitStash(deps, "/repo")

	if err == nil {
		t.Error("expected error, got nil")
	}
	if stashed {
		t.Error("expected stashed to be false when second stash list fails")
	}
}

func TestGitStashPop_Success(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"stash", "pop"}, Err: nil},
	})
	outputMock.InstallOn(deps)

	err := gitStashPop(deps, "/repo")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGitStashPop_Fails(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"stash", "pop"}, Err: errors.New("conflict during stash pop")},
	})
	outputMock.InstallOn(deps)

	err := gitStashPop(deps, "/repo")

	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestHasUnmergedFiles_WithConflicts(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"diff", "--name-only", "--diff-filter=U"}, Stdout: "file1.go\nfile2.go\n"},
	})
	cmdMock.InstallOn(deps)

	hasUnmerged, err := hasUnmergedFilesDeps(deps, "/repo")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !hasUnmerged {
		t.Error("expected hasUnmerged to be true when conflicts exist")
	}
}

func TestHasUnmergedFiles_Clean(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"diff", "--name-only", "--diff-filter=U"}, Stdout: ""},
	})
	cmdMock.InstallOn(deps)

	hasUnmerged, err := hasUnmergedFilesDeps(deps, "/repo")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if hasUnmerged {
		t.Error("expected hasUnmerged to be false when no conflicts")
	}
}

func TestHasUnmergedFiles_GitError(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)

	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"diff", "--name-only", "--diff-filter=U"}, Err: errors.New("not a git repository")},
	})
	cmdMock.InstallOn(deps)

	hasUnmerged, err := hasUnmergedFilesDeps(deps, "/repo")

	if err == nil {
		t.Error("expected error, got nil")
	}
	if hasUnmerged {
		t.Error("expected hasUnmerged to be false when error occurs")
	}
}

func TestIsRefCheckedOutInWorktree(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		branch           string
		mockOutput       string
		mockErr          error
		wantCheckedOut   bool
		wantWorktreePath string
		wantErr          bool
	}{
		{
			name:   "branch checked out in another worktree",
			branch: "main",
			mockOutput: "worktree /home/user/project\nHEAD abc1234\nbranch refs/heads/develop\n\n" +
				"worktree /home/user/worktrees/falcon\nHEAD def5678\nbranch refs/heads/main\n\n",
			wantCheckedOut:   true,
			wantWorktreePath: "/home/user/worktrees/falcon",
		},
		{
			name:   "branch not checked out anywhere",
			branch: "feature/new",
			mockOutput: "worktree /home/user/project\nHEAD abc1234\nbranch refs/heads/main\n\n" +
				"worktree /home/user/worktrees/falcon\nHEAD def5678\nbranch refs/heads/develop\n\n",
			wantCheckedOut: false,
		},
		{
			name:           "no worktrees (empty output)",
			branch:         "main",
			mockOutput:     "",
			wantCheckedOut: false,
		},
		{
			name:    "git command fails",
			branch:  "main",
			mockErr: errors.New("not a git repository"),
			wantErr: true,
		},
		{
			name:   "branch checked out in first worktree",
			branch: "main",
			mockOutput: "worktree /home/user/project\nHEAD abc1234\nbranch refs/heads/main\n\n" +
				"worktree /home/user/worktrees/falcon\nHEAD def5678\nbranch refs/heads/falcon\n\n",
			wantCheckedOut:   true,
			wantWorktreePath: "/home/user/project",
		},
		{
			name:             "single worktree with matching branch",
			branch:           "develop",
			mockOutput:       "worktree /repo\nHEAD abc1234\nbranch refs/heads/develop\n\n",
			wantCheckedOut:   true,
			wantWorktreePath: "/repo",
		},
		{
			name:           "detached HEAD worktree (no branch line)",
			branch:         "main",
			mockOutput:     "worktree /repo\nHEAD abc1234\ndetached\n\n",
			wantCheckedOut: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewCommandMock(t, []CommandStub{{
				Name:   "git",
				Args:   []string{"worktree", "list", "--porcelain"},
				Stdout: tc.mockOutput,
				Err:    tc.mockErr,
			}})
			mock.InstallOn(deps)

			checkedOut, wtPath, err := isRefCheckedOutInWorktreeDeps(deps, "/repo", tc.branch)

			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if checkedOut != tc.wantCheckedOut {
				t.Errorf("checkedOut = %v, want %v", checkedOut, tc.wantCheckedOut)
			}
			if tc.wantCheckedOut && wtPath != tc.wantWorktreePath {
				t.Errorf("worktreePath = %q, want %q", wtPath, tc.wantWorktreePath)
			}
		})
	}
}

func TestGitCheckoutDetached(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dir     string
		ref     string
		mockErr error
		wantErr bool
	}{
		{"success", "/repo", "origin/main", nil, false},
		{"invalid ref", "/repo", "nonexistent", errors.New("pathspec did not match"), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: []string{"checkout", "--detach", tc.ref},
				Err:  tc.mockErr,
			}})
			mock.InstallOn(deps)

			err := gitCheckoutDetached(deps, tc.dir, tc.ref)

			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestGitCreateBranchFromHead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dir     string
		branch  string
		mockErr error
		wantErr bool
	}{
		{"success", "/repo", "loom-push-temp-123", nil, false},
		{"branch already exists", "/repo", "existing-branch", errors.New("already exists"), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: []string{"checkout", "-b", tc.branch},
				Err:  tc.mockErr,
			}})
			mock.InstallOn(deps)

			err := gitCreateBranchFromHead(deps, tc.dir, tc.branch)

			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestGitDeleteBranch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dir     string
		branch  string
		force   bool
		wantArg string
		mockErr error
		wantErr bool
	}{
		{"soft delete success", "/repo", "temp-branch", false, "-d", nil, false},
		{"force delete success", "/repo", "temp-branch", true, "-D", nil, false},
		{"soft delete fails (unmerged)", "/repo", "unmerged-branch", false, "-d", errors.New("not fully merged"), true},
		{"force delete fails", "/repo", "protected", true, "-D", errors.New("cannot delete"), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: []string{"branch", tc.wantArg, "--", tc.branch},
				Err:  tc.mockErr,
			}})
			mock.InstallOn(deps)

			err := gitDeleteBranch(deps, tc.dir, tc.branch, tc.force)

			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestGitPushRefspec(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		dir        string
		remote     string
		localRef   string
		remoteRef  string
		wantRemote string
		wantArgs   []string
		mockErr    error
		wantErr    bool
	}{
		{
			name:       "empty remote defaults to origin",
			dir:        "/repo",
			remote:     "",
			localRef:   "loom-push-temp-123",
			remoteRef:  "main",
			wantRemote: "origin",
			wantArgs:   []string{"push", "origin", "loom-push-temp-123:main"},
			mockErr:    nil,
			wantErr:    false,
		},
		{
			name:       "custom remote",
			dir:        "/repo",
			remote:     "upstream",
			localRef:   "temp-branch",
			remoteRef:  "develop",
			wantRemote: "upstream",
			wantArgs:   []string{"push", "upstream", "temp-branch:develop"},
			mockErr:    nil,
			wantErr:    false,
		},
		{
			name:       "push fails",
			dir:        "/repo",
			remote:     "",
			localRef:   "temp",
			remoteRef:  "main",
			wantRemote: "origin",
			wantArgs:   []string{"push", "origin", "temp:main"},
			mockErr:    errors.New("rejected"),
			wantErr:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: tc.wantArgs,
				Err:  tc.mockErr,
			}})
			mock.InstallOn(deps)

			err := gitPushRefspec(deps, tc.dir, tc.remote, tc.localRef, tc.remoteRef)

			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestBranchExistsLocally(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		branch     string
		mockOutput string
		mockErr    error
		wantExists bool
		wantErr    bool
	}{
		{
			name:       "branch exists",
			branch:     "main",
			mockOutput: "abc123\n",
			wantExists: true,
		},
		{
			name:       "branch does not exist",
			branch:     "nonexistent",
			mockErr:    errors.New("fatal: Needed a single revision"),
			wantExists: false,
		},
		{
			name:       "feature branch exists",
			branch:     "feature/new-thing",
			mockOutput: "def456\n",
			wantExists: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewCommandMock(t, []CommandStub{{
				Name:   "git",
				Args:   []string{"rev-parse", "--verify", "refs/heads/" + tc.branch},
				Stdout: tc.mockOutput,
				Err:    tc.mockErr,
			}})
			mock.InstallOn(deps)

			exists, err := branchExistsLocallyDeps(deps, "/repo", tc.branch)

			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if exists != tc.wantExists {
				t.Errorf("exists = %v, want %v", exists, tc.wantExists)
			}
		})
	}
}

func TestRemoteBranchExists(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		remote     string
		branch     string
		wantRemote string
		mockOutput string
		mockErr    error
		wantExists bool
		wantErr    bool
	}{
		{
			name:       "remote branch exists with empty remote",
			remote:     "",
			branch:     "main",
			wantRemote: "origin",
			mockOutput: "abc123\n",
			wantExists: true,
		},
		{
			name:       "remote branch does not exist",
			remote:     "",
			branch:     "nonexistent",
			wantRemote: "origin",
			mockErr:    errors.New("fatal: Needed a single revision"),
			wantExists: false,
		},
		{
			name:       "custom remote branch exists",
			remote:     "upstream",
			branch:     "develop",
			wantRemote: "upstream",
			mockOutput: "def456\n",
			wantExists: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewCommandMock(t, []CommandStub{{
				Name:   "git",
				Args:   []string{"rev-parse", "--verify", "refs/remotes/" + tc.wantRemote + "/" + tc.branch},
				Stdout: tc.mockOutput,
				Err:    tc.mockErr,
			}})
			mock.InstallOn(deps)

			exists, err := remoteBranchExistsDeps(deps, "/repo", tc.remote, tc.branch)

			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if exists != tc.wantExists {
				t.Errorf("exists = %v, want %v", exists, tc.wantExists)
			}
		})
	}
}

func TestGitCheckoutNewFromRef(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		dir        string
		branch     string
		startPoint string
		mockErr    error
		wantErr    bool
	}{
		{"success", "/repo", "feature/new", "origin/main", nil, false},
		{"branch already exists", "/repo", "existing", "origin/main", errors.New("already exists"), true},
		{"invalid start point", "/repo", "feature/new", "bad-ref", errors.New("not a valid ref"), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: []string{"checkout", "-b", tc.branch, tc.startPoint},
				Err:  tc.mockErr,
			}})
			mock.InstallOn(deps)

			err := gitCheckoutNewFromRef(deps, tc.dir, tc.branch, tc.startPoint)

			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateGitRef(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid_main", "main", false},
		{"valid_feature_branch", "feature/branch", false},
		{"valid_version_tag", "v1.0", false},
		{"valid_commit_hash", "abc123", false},
		{"valid_empty", "", false},
		{"valid_underscore", "feature_branch", false},
		{"valid_dots_single", "v1.0.0", false},
		{"valid_slash", "feature/sub/branch", false},
		{"valid_dashes_mid", "feature-branch-v2", false},
		{"invalid_flag", "-flag", true},
		{"invalid_option", "--option", true},
		{"invalid_dash_only", "-", true},
		{"invalid_shell_injection", "HEAD~1; rm -rf /", true},
		{"invalid_upload_pack", "--upload-pack=evil", true},
		{"invalid_dot_dot_traversal", "refs/heads/../etc/passwd", true},
		{"invalid_backtick", "ref`whoami`", true},
		{"invalid_pipe", "main|evil", true},
		{"invalid_space", "main branch", true},
		{"invalid_null_byte", "main\x00evil", true},
		{"invalid_at_brace", "ref@{0}", true},
		{"invalid_colon", "HEAD:file", true},
		{"invalid_dot_dot", "main..feature", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateGitRef(tc.input)

			if tc.wantErr && err == nil {
				t.Errorf("validateGitRef(%q): expected error, got nil", tc.input)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("validateGitRef(%q): unexpected error: %v", tc.input, err)
			}
			if tc.wantErr && err != nil && !strings.Contains(err.Error(), "invalid git ref") {
				t.Errorf("validateGitRef(%q): error %q does not mention invalid git ref", tc.input, err.Error())
			}
		})
	}
}

func TestGitRefInjectionRejected(t *testing.T) {
	t.Parallel()
	dir := "/repo"

	t.Run("GitCheckout", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewOutputCommandMock(t, []OutputCommandStub{})
		mock.InstallOn(deps)

		err := gitCheckout(deps, dir, "-flag")
		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("GitPull", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewOutputCommandMock(t, []OutputCommandStub{})
		mock.InstallOn(deps)

		err := gitPull(deps, dir, "--upload-pack=evil")
		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("GitMerge", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewOutputCommandMock(t, []OutputCommandStub{})
		mock.InstallOn(deps)

		err := gitMerge(deps, dir, "--strategy=evil", "msg")
		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("GitMergeOrigin", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewOutputCommandMock(t, []OutputCommandStub{})
		mock.InstallOn(deps)

		err := gitMergeOrigin(deps, dir, "--strategy=evil", "msg")
		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("GitPush", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewOutputCommandMock(t, []OutputCommandStub{})
		mock.InstallOn(deps)

		err := gitPush(deps, dir, "--receive-pack=evil")
		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("GitPushForce", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewOutputCommandMock(t, []OutputCommandStub{})
		mock.InstallOn(deps)

		err := gitPushForce(deps, dir, "-flag")
		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("GitReset", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewOutputCommandMock(t, []OutputCommandStub{})
		mock.InstallOn(deps)

		err := gitReset(deps, dir, "--flag")
		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("GitCheckoutDetached", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewOutputCommandMock(t, []OutputCommandStub{})
		mock.InstallOn(deps)

		err := gitCheckoutDetached(deps, dir, "--flag")
		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("GitCreateBranchFromHead", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewOutputCommandMock(t, []OutputCommandStub{})
		mock.InstallOn(deps)

		err := gitCreateBranchFromHead(deps, dir, "--orphan")
		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("GitDeleteBranch", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewOutputCommandMock(t, []OutputCommandStub{})
		mock.InstallOn(deps)

		err := gitDeleteBranch(deps, dir, "--flag", false)
		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("GitFetchRemote", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewOutputCommandMock(t, []OutputCommandStub{})
		mock.InstallOn(deps)

		err := gitFetchRemote(deps, dir, "-evil")
		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("GitMergeRemote_bad_remote", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewOutputCommandMock(t, []OutputCommandStub{})
		mock.InstallOn(deps)

		err := gitMergeRemote(deps, dir, "-evil", "main", "msg")
		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("GitMergeRemote_bad_branch", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewOutputCommandMock(t, []OutputCommandStub{})
		mock.InstallOn(deps)

		err := gitMergeRemote(deps, dir, "", "-evil", "msg")
		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("GitPushRemote_bad_remote", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewOutputCommandMock(t, []OutputCommandStub{})
		mock.InstallOn(deps)

		err := gitPushRemote(deps, dir, "-evil", "main")
		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("GitPushRemote_bad_branch", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewOutputCommandMock(t, []OutputCommandStub{})
		mock.InstallOn(deps)

		err := gitPushRemote(deps, dir, "", "--evil")
		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("GitPullRemote_bad_remote", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewOutputCommandMock(t, []OutputCommandStub{})
		mock.InstallOn(deps)

		err := gitPullRemote(deps, dir, "-evil", "main")
		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("GitPullRemote_bad_branch", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewOutputCommandMock(t, []OutputCommandStub{})
		mock.InstallOn(deps)

		err := gitPullRemote(deps, dir, "", "--evil")
		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("GitPushRefspec_bad_remote", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewOutputCommandMock(t, []OutputCommandStub{})
		mock.InstallOn(deps)

		err := gitPushRefspec(deps, dir, "-evil", "local", "remote")
		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("GitPushRefspec_bad_localRef", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewOutputCommandMock(t, []OutputCommandStub{})
		mock.InstallOn(deps)

		err := gitPushRefspec(deps, dir, "", "-local", "remote")
		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("GitPushRefspec_bad_remoteRef", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewOutputCommandMock(t, []OutputCommandStub{})
		mock.InstallOn(deps)

		err := gitPushRefspec(deps, dir, "", "local", "-remote")
		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("HasCommitsBetween_bad_target", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewCommandMock(t, []CommandStub{})
		mock.InstallOn(deps)

		hasCommits, err := hasCommitsBetweenDeps(deps, dir, "-target", "source")
		if err == nil {
			t.Error("expected error, got nil")
		}
		if hasCommits {
			t.Error("expected hasCommits to be false")
		}
	})

	t.Run("HasCommitsBetween_bad_source", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewCommandMock(t, []CommandStub{})
		mock.InstallOn(deps)

		hasCommits, err := hasCommitsBetweenDeps(deps, dir, "target", "-source")
		if err == nil {
			t.Error("expected error, got nil")
		}
		if hasCommits {
			t.Error("expected hasCommits to be false")
		}
	})

	t.Run("HasCommitsBetweenRemote_bad_remote", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewCommandMock(t, []CommandStub{})
		mock.InstallOn(deps)

		hasCommits, err := hasCommitsBetweenRemoteDeps(deps, dir, "-evil", "target", "source")
		if err == nil {
			t.Error("expected error, got nil")
		}
		if hasCommits {
			t.Error("expected hasCommits to be false")
		}
	})

	t.Run("HasCommitsBetweenRemote_bad_target", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewCommandMock(t, []CommandStub{})
		mock.InstallOn(deps)

		hasCommits, err := hasCommitsBetweenRemoteDeps(deps, dir, "", "-target", "source")
		if err == nil {
			t.Error("expected error, got nil")
		}
		if hasCommits {
			t.Error("expected hasCommits to be false")
		}
	})

	t.Run("HasCommitsBetweenRemote_bad_source", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewCommandMock(t, []CommandStub{})
		mock.InstallOn(deps)

		hasCommits, err := hasCommitsBetweenRemoteDeps(deps, dir, "", "target", "-source")
		if err == nil {
			t.Error("expected error, got nil")
		}
		if hasCommits {
			t.Error("expected hasCommits to be false")
		}
	})

	t.Run("BranchExistsLocally_bad_branch", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewCommandMock(t, []CommandStub{})
		mock.InstallOn(deps)

		exists, err := branchExistsLocallyDeps(deps, dir, "--flag")
		if err == nil {
			t.Error("expected error, got nil")
		}
		if exists {
			t.Error("expected exists to be false")
		}
	})

	t.Run("RemoteBranchExists_bad_remote", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewCommandMock(t, []CommandStub{})
		mock.InstallOn(deps)

		exists, err := remoteBranchExistsDeps(deps, dir, "-evil", "main")
		if err == nil {
			t.Error("expected error, got nil")
		}
		if exists {
			t.Error("expected exists to be false")
		}
	})

	t.Run("RemoteBranchExists_bad_branch", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewCommandMock(t, []CommandStub{})
		mock.InstallOn(deps)

		exists, err := remoteBranchExistsDeps(deps, dir, "", "--evil")
		if err == nil {
			t.Error("expected error, got nil")
		}
		if exists {
			t.Error("expected exists to be false")
		}
	})

	t.Run("GitCheckoutNewFromRef_bad_branch", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewOutputCommandMock(t, []OutputCommandStub{})
		mock.InstallOn(deps)

		err := gitCheckoutNewFromRef(deps, dir, "--orphan", "origin/main")
		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("GitCheckoutNewFromRef_bad_startPoint", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		mock := NewOutputCommandMock(t, []OutputCommandStub{})
		mock.InstallOn(deps)

		err := gitCheckoutNewFromRef(deps, dir, "feature", "--evil")
		if err == nil {
			t.Error("expected error, got nil")
		}
	})
}

// ---------- GitCleanExclude / GitCleanDryRunExclude ----------

func TestGitCleanExclude(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		dir      string
		excludes []string
		wantArgs []string
		mockErr  error
		wantErr  bool
	}{
		{
			name:     "with_excludes",
			dir:      "/repo",
			excludes: []string{".beads", ".loom", "sessions"},
			wantArgs: []string{"clean", "-fd", "--exclude=.beads", "--exclude=.loom", "--exclude=sessions"},
			mockErr:  nil,
			wantErr:  false,
		},
		{
			name:     "empty_excludes",
			dir:      "/repo",
			excludes: []string{},
			wantArgs: []string{"clean", "-fd"},
			mockErr:  nil,
			wantErr:  false,
		},
		{
			name:     "nil_excludes",
			dir:      "/repo",
			excludes: nil,
			wantArgs: []string{"clean", "-fd"},
			mockErr:  nil,
			wantErr:  false,
		},
		{
			name:     "single_exclude",
			dir:      "/repo",
			excludes: []string{"loom.yaml"},
			wantArgs: []string{"clean", "-fd", "--exclude=loom.yaml"},
			mockErr:  nil,
			wantErr:  false,
		},
		{
			name:     "error_propagated",
			dir:      "/repo",
			excludes: []string{".loom"},
			wantArgs: []string{"clean", "-fd", "--exclude=.loom"},
			mockErr:  errors.New("git clean failed"),
			wantErr:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: tc.wantArgs,
				Err:  tc.mockErr,
			}})
			mock.InstallOn(deps)

			err := gitCleanExclude(deps, tc.dir, tc.excludes)

			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestGitCleanDryRunExclude(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		dir        string
		excludes   []string
		wantArgs   []string
		mockStdout string
		mockErr    error
		wantOutput string
		wantErr    bool
	}{
		{
			name:       "with_excludes",
			dir:        "/repo",
			excludes:   []string{".beads", ".loom"},
			wantArgs:   []string{"clean", "-fdn", "--exclude=.beads", "--exclude=.loom"},
			mockStdout: "Would remove leftover.txt\n",
			wantOutput: "Would remove leftover.txt\n",
			wantErr:    false,
		},
		{
			name:       "empty_excludes",
			dir:        "/repo",
			excludes:   []string{},
			wantArgs:   []string{"clean", "-fdn"},
			mockStdout: "Would remove foo.txt\nWould remove bar.txt\n",
			wantOutput: "Would remove foo.txt\nWould remove bar.txt\n",
			wantErr:    false,
		},
		{
			name:       "no_files_to_clean",
			dir:        "/repo",
			excludes:   []string{"sessions"},
			wantArgs:   []string{"clean", "-fdn", "--exclude=sessions"},
			mockStdout: "",
			wantOutput: "",
			wantErr:    false,
		},
		{
			name:     "error_propagated",
			dir:      "/repo",
			excludes: []string{".loom"},
			wantArgs: []string{"clean", "-fdn", "--exclude=.loom"},
			mockErr:  errors.New("not a git repository"),
			wantErr:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewCommandMock(t, []CommandStub{{
				Dir:    tc.dir,
				Name:   "git",
				Args:   tc.wantArgs,
				Stdout: tc.mockStdout,
				Err:    tc.mockErr,
			}})
			mock.InstallOn(deps)

			output, err := gitCleanDryRunExclude(deps, tc.dir, tc.excludes)

			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if !tc.wantErr && output != tc.wantOutput {
				t.Errorf("output = %q, want %q", output, tc.wantOutput)
			}
		})
	}
}
