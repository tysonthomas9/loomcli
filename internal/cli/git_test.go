package cli

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

// OutputCommandStub represents an expected output command call and its response
type OutputCommandStub struct {
	Dir  string   // expected directory (empty = any)
	Args []string // expected arguments (nil = any)
	Err  error    // response error
}

// OutputCommandMock provides a mock for output-streaming git commands
type OutputCommandMock struct {
	t     *testing.T
	stubs []OutputCommandStub
	calls []OutputCommandStub
	idx   int
}

// NewOutputCommandMock creates a new output command mock with expected stubs
func NewOutputCommandMock(t *testing.T, stubs []OutputCommandStub) *OutputCommandMock {
	return &OutputCommandMock{t: t, stubs: stubs}
}

// Exec implements the outputCommandExecutor interface
func (m *OutputCommandMock) Exec(dir string, args ...string) error {
	call := OutputCommandStub{Dir: dir, Args: args}
	m.calls = append(m.calls, call)

	if m.idx >= len(m.stubs) {
		m.t.Fatalf("unexpected output command call #%d: git %v in %s", m.idx+1, args, dir)
	}

	stub := m.stubs[m.idx]
	m.idx++

	// Validate command matches expectations (empty = any)
	if stub.Dir != "" && stub.Dir != dir {
		m.t.Errorf("call #%d: expected dir %q, got %q", m.idx, stub.Dir, dir)
	}
	if stub.Args != nil && !slicesEqual(stub.Args, args) {
		m.t.Errorf("call #%d: expected args %v, got %v", m.idx, stub.Args, args)
	}

	return stub.Err
}

// Verify ensures all expected calls were made
func (m *OutputCommandMock) Verify() {
	if m.idx != len(m.stubs) {
		m.t.Errorf("expected %d output command calls, got %d", len(m.stubs), m.idx)
	}
}

// Install installs the mock and registers cleanup with t.Cleanup()
func (m *OutputCommandMock) Install() {
	orig := runGitWithOutputFunc
	runGitWithOutputFunc = m.Exec
	m.t.Cleanup(func() {
		runGitWithOutputFunc = orig
		m.Verify()
	})
}

// Calls returns the actual calls made to the mock
func (m *OutputCommandMock) Calls() []OutputCommandStub {
	return m.calls
}

func TestRunGitCommandWithOutput(t *testing.T) {
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
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: tc.args,
				Err:  tc.mockErr,
			}})
			mock.Install()

			err := RunGitCommandWithOutput(tc.dir, tc.args...)

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
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: []string{"fetch", "origin"},
				Err:  tc.mockErr,
			}})
			mock.Install()

			err := GitFetch(tc.dir)

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
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: []string{"checkout", tc.branch},
				Err:  tc.mockErr,
			}})
			mock.Install()

			err := GitCheckout(tc.dir, tc.branch)

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
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: []string{"pull", "origin", tc.branch},
				Err:  tc.mockErr,
			}})
			mock.Install()

			err := GitPull(tc.dir, tc.branch)

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
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: []string{"merge", tc.branch, "-m", tc.message},
				Err:  tc.mockErr,
			}})
			mock.Install()

			err := GitMerge(tc.dir, tc.branch, tc.message)

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
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: []string{"merge", "origin/" + tc.branch, "-m", tc.message},
				Err:  tc.mockErr,
			}})
			mock.Install()

			err := GitMergeOrigin(tc.dir, tc.branch, tc.message)

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
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: []string{"push", "origin", tc.branch},
				Err:  tc.mockErr,
			}})
			mock.Install()

			err := GitPush(tc.dir, tc.branch)

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
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: []string{"push", "origin", tc.branch, "--force"},
				Err:  tc.mockErr,
			}})
			mock.Install()

			err := GitPushForce(tc.dir, tc.branch)

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
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: []string{"reset", "--hard", tc.ref},
				Err:  tc.mockErr,
			}})
			mock.Install()

			err := GitReset(tc.dir, tc.ref)

			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestGitClean(t *testing.T) {
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
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: []string{"clean", "-fd"},
				Err:  tc.mockErr,
			}})
			mock.Install()

			err := GitClean(tc.dir)

			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
