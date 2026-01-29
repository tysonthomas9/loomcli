package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetUncommittedChangesCount(t *testing.T) {
	tests := []struct {
		name     string
		stdout   string
		err      error
		expected int
	}{
		{
			name:     "empty_output_returns_zero",
			stdout:   "",
			err:      nil,
			expected: 0,
		},
		{
			name:     "whitespace_only_returns_zero",
			stdout:   "   \n  ",
			err:      nil,
			expected: 0,
		},
		{
			name:     "single_change",
			stdout:   "M file.go\n",
			err:      nil,
			expected: 1,
		},
		{
			name:     "multiple_changes",
			stdout:   "M a.go\n?? b.go\nA c.go\n",
			err:      nil,
			expected: 3,
		},
		{
			name:     "mixed_status_types",
			stdout:   "M modified.go\nA added.go\nD deleted.go\n?? untracked.go\nR renamed.go\n",
			err:      nil,
			expected: 5,
		},
		{
			name:     "git_error_returns_zero",
			stdout:   "",
			err:      fmt.Errorf("git error"),
			expected: 0,
		},
		{
			name:     "single_newline_returns_zero",
			stdout:   "\n",
			err:      nil,
			expected: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := NewCommandMock(t, []CommandStub{
				{
					Name:   "git",
					Args:   []string{"status", "--porcelain"},
					Stdout: tc.stdout,
					Err:    tc.err,
				},
			})
			mock.Install()

			got := getUncommittedChangesCount("/some/path")
			if got != tc.expected {
				t.Errorf("getUncommittedChangesCount() = %d, want %d", got, tc.expected)
			}
		})
	}
}

func TestRunListNoWorktrees(t *testing.T) {
	// Save and restore working directory
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmpDir := t.TempDir()
	// Resolve symlinks for comparison (macOS /var -> /private/var)
	tmpDir, err = filepath.EvalSymlinks(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Create empty worktrees directory
	worktreesDir := filepath.Join(tmpDir, "worktrees")
	if err := os.MkdirAll(worktreesDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// Run the command
	runList(nil, nil)

	// Restore stdout and read output
	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Verify output
	if !strings.Contains(output, "No agents (worktrees) found") {
		t.Errorf("expected 'No agents (worktrees) found' in output, got: %s", output)
	}
	if !strings.Contains(output, "worktrees") {
		t.Errorf("expected worktrees directory path in output, got: %s", output)
	}
}

func TestRunListWithWorktrees(t *testing.T) {
	tests := []struct {
		name           string
		worktreeNames  []string
		gitOutputs     map[string]string // worktree name -> git status output
		branches       map[string]string // worktree name -> branch name
		expectedOutput []string          // strings that should appear in output
	}{
		{
			name:          "single_worktree_ready",
			worktreeNames: []string{"falcon"},
			gitOutputs:    map[string]string{"falcon": ""},
			branches:      map[string]string{"falcon": "falcon"},
			expectedOutput: []string{
				"falcon",
				"ready",
				"Total: 1 agents",
			},
		},
		{
			name:          "single_worktree_dirty",
			worktreeNames: []string{"nova"},
			gitOutputs:    map[string]string{"nova": "M file1.go\nM file2.go\n"},
			branches:      map[string]string{"nova": "nova"},
			expectedOutput: []string{
				"nova",
				"2 changes",
				"Total: 1 agents",
			},
		},
		{
			name:          "multiple_worktrees",
			worktreeNames: []string{"falcon", "nova"},
			gitOutputs: map[string]string{
				"falcon": "",
				"nova":   "M dirty.go\n",
			},
			branches: map[string]string{
				"falcon": "falcon",
				"nova":   "feature-branch",
			},
			expectedOutput: []string{
				"falcon",
				"nova",
				"feature-branch",
				"ready",
				"1 changes",
				"Total: 2 agents",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Save and restore working directory
			origDir, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			tmpDir := t.TempDir()
			tmpDir, err = filepath.EvalSymlinks(tmpDir)
			if err != nil {
				t.Fatal(err)
			}
			os.Chdir(tmpDir)
			t.Cleanup(func() { os.Chdir(origDir) })

			// Create worktree structure
			for _, name := range tc.worktreeNames {
				wtPath := filepath.Join(tmpDir, "worktrees", name, ".git")
				if err := os.MkdirAll(wtPath, 0755); err != nil {
					t.Fatal(err)
				}
			}

			// Build command stubs for git commands
			var stubs []CommandStub
			for _, name := range tc.worktreeNames {
				// branch --show-current for DiscoverWorktrees
				stubs = append(stubs, CommandStub{
					Name:   "git",
					Args:   []string{"branch", "--show-current"},
					Stdout: tc.branches[name],
				})
			}
			for _, name := range tc.worktreeNames {
				// status --porcelain for IsCleanWorkingTree
				stubs = append(stubs, CommandStub{
					Name:   "git",
					Args:   []string{"status", "--porcelain"},
					Stdout: tc.gitOutputs[name],
				})
				// status --porcelain for getUncommittedChangesCount
				stubs = append(stubs, CommandStub{
					Name:   "git",
					Args:   []string{"status", "--porcelain"},
					Stdout: tc.gitOutputs[name],
				})
			}

			mock := NewCommandMock(t, stubs)
			mock.Install()

			// Capture stdout
			oldStdout := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w

			// Run the command
			runList(nil, nil)

			// Restore stdout and read output
			w.Close()
			os.Stdout = oldStdout
			var buf bytes.Buffer
			buf.ReadFrom(r)
			output := buf.String()

			// Verify expected strings in output
			for _, expected := range tc.expectedOutput {
				if !strings.Contains(output, expected) {
					t.Errorf("expected %q in output, got:\n%s", expected, output)
				}
			}
		})
	}
}

func TestRunListShowsDefaultBranch(t *testing.T) {
	// Save and restore working directory and env
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmpDir := t.TempDir()
	tmpDir, err = filepath.EvalSymlinks(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Set custom default branch
	SetupTestEnv(t, map[string]string{
		"LOOM_DEFAULT_BRANCH": "develop",
	})

	// Create worktree
	wtPath := filepath.Join(tmpDir, "worktrees", "test", ".git")
	if err := os.MkdirAll(wtPath, 0755); err != nil {
		t.Fatal(err)
	}

	mock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "test"},
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""},
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""},
	})
	mock.Install()

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runList(nil, nil)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "Default branch: develop") {
		t.Errorf("expected 'Default branch: develop' in output, got:\n%s", output)
	}
}

// Note: Testing error handling when worktrees directory doesn't exist is not
// straightforward because runList calls os.Exit(1) on error. Proper testing
// would require either:
// 1. Refactoring runList to return an error instead of calling os.Exit
// 2. Using exec.Command to run the test binary as a subprocess
// The DiscoverWorktrees function properly returns an error which is tested
// in worktree_test.go, so the core error path is covered.

func TestRunListSkipsNonGitDirectories(t *testing.T) {
	// Save and restore working directory
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmpDir := t.TempDir()
	tmpDir, err = filepath.EvalSymlinks(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Create worktrees directory with one git worktree and one non-git directory
	gitWorktree := filepath.Join(tmpDir, "worktrees", "valid", ".git")
	if err := os.MkdirAll(gitWorktree, 0755); err != nil {
		t.Fatal(err)
	}
	nonGitDir := filepath.Join(tmpDir, "worktrees", "invalid")
	if err := os.MkdirAll(nonGitDir, 0755); err != nil {
		t.Fatal(err)
	}

	mock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "valid"},
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""},
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""},
	})
	mock.Install()

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runList(nil, nil)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Should only show the valid worktree
	if !strings.Contains(output, "valid") {
		t.Errorf("expected 'valid' worktree in output, got:\n%s", output)
	}
	// Should show only 1 agent (note: implementation always uses "agents" plural)
	if !strings.Contains(output, "Total: 1 agents") {
		t.Errorf("expected 'Total: 1 agents' in output, got:\n%s", output)
	}
	// Should NOT show the invalid directory (though it might not be listed anyway)
	// This is implicitly tested by the "Total: 1 agent" check
}
