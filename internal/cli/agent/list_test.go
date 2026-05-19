package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetUncommittedChangesCount(t *testing.T) {
	t.Parallel()

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
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			mock := NewCommandMock(t, []CommandStub{
				{
					Name:   "git",
					Args:   []string{"status", "--porcelain"},
					Stdout: tc.stdout,
					Err:    tc.err,
				},
			})
			mock.InstallOn(deps)

			got := GetUncommittedChangesCountDeps(deps, "/some/path")
			if got != tc.expected {
				t.Errorf("GetUncommittedChangesCountDeps() = %d, want %d", got, tc.expected)
			}
		})
	}
}

func TestGetUncommittedChangesCountPublicWrapper(t *testing.T) {
	deps := defaultDeps
	oldGit := deps.Git
	t.Cleanup(func() { deps.Git = oldGit })

	deps.Git = &MockGitRunner{RunResult: CommandResult{Stdout: " M api.go\n?? new.go\n"}}
	if got := GetUncommittedChangesCount("/repo"); got != 2 {
		t.Fatalf("GetUncommittedChangesCount = %d, want 2", got)
	}

	deps.Git = &MockGitRunner{RunResult: CommandResult{Err: errors.New("git failed"), Stderr: "fatal"}}
	if got := GetUncommittedChangesCount("/repo"); got != 0 {
		t.Fatalf("GetUncommittedChangesCount error = %d, want 0", got)
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

// Note: Testing error handling when worktrees directory doesn't exist is not
// straightforward because runList calls os.Exit(1) on error. Proper testing
// would require either:
// 1. Refactoring runList to return an error instead of calling os.Exit
// 2. Using exec.Command to run the test binary as a subprocess //nolint:norawexec
// The DiscoverWorktrees function properly returns an error which is tested
// in worktree_test.go, so the core error path is covered.

func TestGetWorktreeListStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		lockRunning    bool   // whether to create a running lock file
		lockCommand    string // lock command type (e.g., "task")
		gitClean       bool   // whether git status --porcelain returns empty
		gitChanges     string // porcelain output for dirty case
		expectedStatus string
	}{
		{
			name:           "clean_worktree_shows_ready",
			lockRunning:    false,
			gitClean:       true,
			gitChanges:     "",
			expectedStatus: "✓ ready",
		},
		{
			name:           "dirty_worktree_with_changes_shows_count",
			lockRunning:    false,
			gitClean:       false,
			gitChanges:     "M file1.go\nM file2.go\n?? file3.go\n",
			expectedStatus: "● 3 changes",
		},
		// Note: the "dirty" code path (line 128 in list.go) is reached when
		// IsCleanWorkingTree returns false but getUncommittedChangesCount returns 0.
		// This is tested via separate mock stubs below.
		{
			name:           "running_lock_shows_lock_status",
			lockRunning:    true,
			lockCommand:    "task",
			gitClean:       true,
			gitChanges:     "",
			expectedStatus: "●", // lock status will start with ●
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)

			tmpDir := t.TempDir()
			wtPath := filepath.Join(tmpDir, "test-wt")
			if err := os.MkdirAll(wtPath, 0755); err != nil {
				t.Fatal(err)
			}

			if tc.lockRunning {
				// Create lock file with current PID so it appears running
				lockInfo := LockInfo{
					PID:       os.Getpid(),
					Command:   tc.lockCommand,
					AgentName: "test",
				}
				lockData, _ := json.Marshal(lockInfo)
				os.WriteFile(filepath.Join(wtPath, ".agent.lock"), lockData, 0644)
			}

			// Set up git command mocks
			var stubs []CommandStub
			if !tc.lockRunning {
				// IsCleanWorkingTree call
				porcelainOut := tc.gitChanges
				if tc.gitClean {
					porcelainOut = ""
				}
				stubs = append(stubs, CommandStub{
					Name:   "git",
					Args:   []string{"status", "--porcelain"},
					Stdout: porcelainOut,
				})
				// getUncommittedChangesCount call (second porcelain)
				stubs = append(stubs, CommandStub{
					Name:   "git",
					Args:   []string{"status", "--porcelain"},
					Stdout: tc.gitChanges,
				})
			}

			mock := NewCommandMock(t, stubs)
			mock.InstallOn(deps)

			wt := WorktreeInfo{
				Name:   "test",
				Path:   wtPath,
				Branch: "test-branch",
			}

			status := getWorktreeListStatusDeps(deps, wt)

			if tc.lockRunning {
				// Just verify it starts with the lock icon
				if !strings.HasPrefix(status, "●") {
					t.Errorf("expected status to start with '●', got %q", status)
				}
			} else if status != tc.expectedStatus {
				t.Errorf("getWorktreeListStatusDeps() = %q, want %q", status, tc.expectedStatus)
			}
		})
	}

	// Test the "dirty" path separately: IsCleanWorkingTree returns false (non-empty porcelain)
	// but getUncommittedChangesCount returns 0 (empty after trim).
	t.Run("dirty_worktree_no_counted_changes", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)

		tmpDir := t.TempDir()
		wtPath := filepath.Join(tmpDir, "test-wt")
		if err := os.MkdirAll(wtPath, 0755); err != nil {
			t.Fatal(err)
		}

		stubs := []CommandStub{
			// IsCleanWorkingTree: returns non-empty -> clean=false
			{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: "M something\n"},
			// getUncommittedChangesCount: returns empty -> count=0
			{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""},
		}
		mock := NewCommandMock(t, stubs)
		mock.InstallOn(deps)

		wt := WorktreeInfo{Name: "test", Path: wtPath, Branch: "test-branch"}
		status := getWorktreeListStatusDeps(deps, wt)

		// With clean=false and changes=0, status should be "● dirty"
		if status != "● dirty" {
			t.Errorf("getWorktreeListStatusDeps() = %q, want %q", status, "● dirty")
		}
	})
}

func TestRunListWorkspaceMode(t *testing.T) {
	resetIntegrationBranchCache()
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

	// Test renderListWorkspace directly by capturing stdout, since setting up
	// full workspace config for DiscoverWorktrees is complex.
	worktrees := []WorktreeInfo{
		{Name: "falcon", Path: "/tmp/ws/falcon", Branch: "falcon", Workspace: "my-workspace"},
		{Name: "nova", Path: "/tmp/ws/nova", Branch: "nova", Workspace: "my-workspace"},
		{Name: "spark", Path: "/tmp/ws/spark", Branch: "spark", Workspace: "other-ws"},
		{Name: "unassigned-agent", Path: "/tmp/ws/unassigned-agent", Branch: "unassigned-agent", Workspace: ""},
	}

	// Mock git commands for getWorktreeListStatus calls (4 worktrees x 2 porcelain calls each)
	var stubs []CommandStub
	for i := 0; i < len(worktrees); i++ {
		// IsCleanWorkingTree
		stubs = append(stubs, CommandStub{
			Name:   "git",
			Args:   []string{"status", "--porcelain"},
			Stdout: "",
		})
		// getUncommittedChangesCount
		stubs = append(stubs, CommandStub{
			Name:   "git",
			Args:   []string{"status", "--porcelain"},
			Stdout: "",
		})
	}

	// GetDefaultBranchForWorktrees with 4 worktrees triggers auto-detection
	stubs = append(stubs, CommandStub{
		Name:   "git",
		Args:   []string{"branch", "-r", "--format=%(refname:short)"},
		Stdout: "",
		Err:    fmt.Errorf("not a real repo"),
	})

	mock := NewCommandMock(t, stubs)
	mock.Install()

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	renderListWorkspace(worktrees)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Verify workspace header
	if !strings.Contains(output, "Agents by Workspace:") {
		t.Errorf("expected 'Agents by Workspace:' header, got:\n%s", output)
	}

	// Verify workspace group headers
	if !strings.Contains(output, "[my-workspace]") {
		t.Errorf("expected '[my-workspace]' group header, got:\n%s", output)
	}
	if !strings.Contains(output, "[other-ws]") {
		t.Errorf("expected '[other-ws]' group header, got:\n%s", output)
	}
	if !strings.Contains(output, "[unassigned]") {
		t.Errorf("expected '[unassigned]' group header for empty workspace, got:\n%s", output)
	}

	// Verify agent names appear
	if !strings.Contains(output, "falcon") {
		t.Errorf("expected 'falcon' agent in output, got:\n%s", output)
	}
	if !strings.Contains(output, "nova") {
		t.Errorf("expected 'nova' agent in output, got:\n%s", output)
	}
	if !strings.Contains(output, "spark") {
		t.Errorf("expected 'spark' agent in output, got:\n%s", output)
	}
	if !strings.Contains(output, "unassigned-agent") {
		t.Errorf("expected 'unassigned-agent' in output, got:\n%s", output)
	}

	// Verify workspace count in summary
	if !strings.Contains(output, "across 3 workspaces") {
		t.Errorf("expected 'across 3 workspaces' in output, got:\n%s", output)
	}

	// Verify total agent count
	if !strings.Contains(output, "Total: 4 agents") {
		t.Errorf("expected 'Total: 4 agents' in output, got:\n%s", output)
	}
}

func TestRenderListWorkspaceUnassigned(t *testing.T) {
	resetIntegrationBranchCache()
	t.Setenv("LOOM_DEFAULT_BRANCH", "main")
	worktrees := []WorktreeInfo{
		{Name: "alpha", Path: "/tmp/alpha", Branch: "alpha"},
		{Name: "beta", Path: "/tmp/beta", Branch: "beta"},
	}

	stubs := []CommandStub{
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""},
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""},
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""},
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""},
	}
	mock := NewCommandMock(t, stubs)
	mock.Install()

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	renderListWorkspace(worktrees)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "Agents by Workspace:") {
		t.Errorf("expected workspace header, got:\n%s", output)
	}
	if !strings.Contains(output, "[unassigned]") {
		t.Errorf("expected unassigned group, got:\n%s", output)
	}
}
