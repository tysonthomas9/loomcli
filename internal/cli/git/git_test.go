package git

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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

func TestGetChangedFilesPreservesPorcelainPaths(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	runGitCommand(t, dir, "init", "-b", "main")
	runGitCommand(t, dir, "config", "user.email", "test@example.com")
	runGitCommand(t, dir, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("initial\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src/main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatalf("write src/main.go: %v", err)
	}
	runGitCommand(t, dir, "add", ".")
	runGitCommand(t, dir, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("changed\n"), 0644); err != nil {
		t.Fatalf("modify README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "NEW.md"), []byte("new\n"), 0644); err != nil {
		t.Fatalf("write NEW.md: %v", err)
	}

	files, err := getChangedFiles(dir)
	if err != nil {
		t.Fatalf("getChangedFiles: %v", err)
	}
	want := []string{"README.md", "NEW.md"}
	if len(files) != len(want) {
		t.Fatalf("files = %#v, want %#v", files, want)
	}
	for i := range want {
		if files[i] != want[i] {
			t.Fatalf("files = %#v, want %#v", files, want)
		}
	}
}

func TestParsePorcelainStatusPreservesXYCodes(t *testing.T) {
	t.Parallel()

	input := strings.Join([]string{
		"UU conflicted.go",
		"?? new-file.go",
		" M modified.go",
		"A  added.go",
		" D deleted.go",
		"R  old-name.go -> new-name.go",
	}, "\n") + "\n"

	got := ParsePorcelainStatus(input)
	want := PorcelainStatus{
		"conflicted.go": "UU",
		"new-file.go":   "??",
		"modified.go":   " M",
		"added.go":      "A ",
		"deleted.go":    " D",
		"new-name.go":   "R ",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParsePorcelainStatus() = %#v, want %#v", got, want)
	}
}

func TestGetPorcelainStatusListsFilesInUntrackedDirectories(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	runGitCommand(t, dir, "init", "-b", "main")
	if err := os.MkdirAll(filepath.Join(dir, "newdir", "nested"), 0755); err != nil {
		t.Fatalf("mkdir newdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "newdir", "file.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatalf("write file.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "newdir", "nested", "file.txt"), []byte("hello\n"), 0644); err != nil {
		t.Fatalf("write nested file: %v", err)
	}

	got, err := GetPorcelainStatus(dir)
	if err != nil {
		t.Fatalf("GetPorcelainStatus() error: %v", err)
	}
	want := PorcelainStatus{
		"newdir/file.go":         "??",
		"newdir/nested/file.txt": "??",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GetPorcelainStatus() = %#v, want %#v", got, want)
	}
	if _, ok := got["newdir/"]; ok {
		t.Fatalf("GetPorcelainStatus() included collapsed directory entry: %#v", got)
	}
}

func runGitCommand(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec // Test helper uses fixed git commands in a temp repo.
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
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
}

// ---------- GitCleanExclude / GitCleanDryRunExclude ----------
