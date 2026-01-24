package main

import (
	"errors"
	"testing"
)

func TestRunGitCommand(t *testing.T) {
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
			mock := NewCommandMock(t, []CommandStub{{
				Dir:    tc.dir,
				Name:   "git",
				Args:   tc.args,
				Stdout: tc.mockStdout,
				Stderr: tc.mockStderr,
				Err:    tc.mockErr,
			}})
			mock.Install()

			output, err := RunGitCommand(tc.dir, tc.args...)

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
			mock := NewCommandMock(t, []CommandStub{{
				Name:   "git",
				Args:   []string{"status", "--porcelain"},
				Stdout: tc.mockOutput,
				Err:    tc.mockErr,
			}})
			mock.Install()

			clean, err := IsCleanWorkingTree("/repo")

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
			mock := NewCommandMock(t, []CommandStub{{
				Name:   "git",
				Args:   []string{"diff", "--name-only", "--diff-filter=U"},
				Stdout: tc.mockOutput,
				Err:    tc.mockErr,
			}})
			mock.Install()

			files, err := GetConflictedFiles("/repo")

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
			mock := NewCommandMock(t, []CommandStub{{
				Name:   "git",
				Args:   []string{"log", tc.target + "..origin/" + tc.source, "--oneline"},
				Stdout: tc.mockOutput,
				Err:    tc.mockErr,
			}})
			mock.Install()

			hasCommits, _ := HasCommitsBetween("/repo", tc.target, tc.source)

			if hasCommits != tc.wantHas {
				t.Errorf("hasCommits = %v, want %v", hasCommits, tc.wantHas)
			}
		})
	}
}
