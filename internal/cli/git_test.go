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

func TestGitCleanDryRun(t *testing.T) {
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
			mock := NewCommandMock(t, []CommandStub{{
				Dir:    tc.dir,
				Name:   "git",
				Args:   []string{"clean", "-fdn"},
				Stdout: tc.mockStdout,
				Stderr: "",
				Err:    tc.mockErr,
			}})
			mock.Install()

			output, err := GitCleanDryRun(tc.dir)

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

func TestResolveRemote(t *testing.T) {
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
			got := resolveRemote(tc.input)
			if got != tc.expect {
				t.Errorf("resolveRemote(%q) = %q, want %q", tc.input, got, tc.expect)
			}
		})
	}
}

func TestGitFetchRemote(t *testing.T) {
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
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: []string{"fetch", tc.wantRemote},
				Err:  tc.mockErr,
			}})
			mock.Install()

			err := GitFetchRemote(tc.dir, tc.remote)

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
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: []string{"merge", tc.wantRemote + "/" + tc.branch, "-m", tc.message},
				Err:  tc.mockErr,
			}})
			mock.Install()

			err := GitMergeRemote(tc.dir, tc.remote, tc.branch, tc.message)

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
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: []string{"push", tc.wantRemote, tc.branch},
				Err:  tc.mockErr,
			}})
			mock.Install()

			err := GitPushRemote(tc.dir, tc.remote, tc.branch)

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
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: []string{"pull", tc.wantRemote, tc.branch},
				Err:  tc.mockErr,
			}})
			mock.Install()

			err := GitPullRemote(tc.dir, tc.remote, tc.branch)

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
			mock := NewCommandMock(t, []CommandStub{{
				Name:   "git",
				Args:   []string{"log", tc.target + ".." + tc.wantRemote + "/" + tc.source, "--oneline"},
				Stdout: tc.mockOutput,
				Err:    tc.mockErr,
			}})
			mock.Install()

			hasCommits, _ := HasCommitsBetweenRemote("/repo", tc.remote, tc.target, tc.source)

			if hasCommits != tc.wantHas {
				t.Errorf("hasCommits = %v, want %v", hasCommits, tc.wantHas)
			}
		})
	}
}

func TestGitStash_DirtyWorkingTree(t *testing.T) {
	// When working tree is dirty, GitStash should call "git stash" and return true
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: " M file.go\n"}, // IsCleanWorkingTree returns false
	})
	cmdMock.Install()

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"stash"}, Err: nil}, // GitStash calls RunGitCommandWithOutput
	})
	outputMock.Install()

	stashed, err := GitStash("/repo")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !stashed {
		t.Error("expected stashed to be true for dirty working tree")
	}
}

func TestGitStash_CleanWorkingTree(t *testing.T) {
	// When working tree is clean, GitStash should not call "git stash" and return false
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: ""}, // IsCleanWorkingTree returns true
	})
	cmdMock.Install()

	// No output command mock needed - stash should not be called

	stashed, err := GitStash("/repo")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if stashed {
		t.Error("expected stashed to be false for clean working tree")
	}
}

func TestGitStash_StatusCheckFails(t *testing.T) {
	// When IsCleanWorkingTree fails, GitStash should return the error
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"status", "--porcelain"}, Err: errors.New("not a git repository")},
	})
	cmdMock.Install()

	stashed, err := GitStash("/repo")

	if err == nil {
		t.Error("expected error, got nil")
	}
	if stashed {
		t.Error("expected stashed to be false when error occurs")
	}
}

func TestGitStash_StashCommandFails(t *testing.T) {
	// When git stash command fails, GitStash should return wrapped error
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"status", "--porcelain"}, Stdout: " M file.go\n"},
	})
	cmdMock.Install()

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"stash"}, Err: errors.New("stash failed")},
	})
	outputMock.Install()

	stashed, err := GitStash("/repo")

	if err == nil {
		t.Error("expected error, got nil")
	}
	if stashed {
		t.Error("expected stashed to be false when stash command fails")
	}
}

func TestGitStashPop_Success(t *testing.T) {
	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"stash", "pop"}, Err: nil},
	})
	outputMock.Install()

	err := GitStashPop("/repo")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGitStashPop_Fails(t *testing.T) {
	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Args: []string{"stash", "pop"}, Err: errors.New("conflict during stash pop")},
	})
	outputMock.Install()

	err := GitStashPop("/repo")

	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestHasUnmergedFiles_WithConflicts(t *testing.T) {
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"diff", "--name-only", "--diff-filter=U"}, Stdout: "file1.go\nfile2.go\n"},
	})
	cmdMock.Install()

	hasUnmerged, err := HasUnmergedFiles("/repo")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !hasUnmerged {
		t.Error("expected hasUnmerged to be true when conflicts exist")
	}
}

func TestHasUnmergedFiles_Clean(t *testing.T) {
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"diff", "--name-only", "--diff-filter=U"}, Stdout: ""},
	})
	cmdMock.Install()

	hasUnmerged, err := HasUnmergedFiles("/repo")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if hasUnmerged {
		t.Error("expected hasUnmerged to be false when no conflicts")
	}
}

func TestHasUnmergedFiles_GitError(t *testing.T) {
	cmdMock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"diff", "--name-only", "--diff-filter=U"}, Err: errors.New("not a git repository")},
	})
	cmdMock.Install()

	hasUnmerged, err := HasUnmergedFiles("/repo")

	if err == nil {
		t.Error("expected error, got nil")
	}
	if hasUnmerged {
		t.Error("expected hasUnmerged to be false when error occurs")
	}
}

func TestIsRefCheckedOutInWorktree(t *testing.T) {
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
			mock := NewCommandMock(t, []CommandStub{{
				Name:   "git",
				Args:   []string{"worktree", "list", "--porcelain"},
				Stdout: tc.mockOutput,
				Err:    tc.mockErr,
			}})
			mock.Install()

			checkedOut, wtPath, err := IsRefCheckedOutInWorktree("/repo", tc.branch)

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
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: []string{"checkout", "--detach", tc.ref},
				Err:  tc.mockErr,
			}})
			mock.Install()

			err := GitCheckoutDetached(tc.dir, tc.ref)

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
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: []string{"checkout", "-b", tc.branch},
				Err:  tc.mockErr,
			}})
			mock.Install()

			err := GitCreateBranchFromHead(tc.dir, tc.branch)

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
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: []string{"branch", tc.wantArg, tc.branch},
				Err:  tc.mockErr,
			}})
			mock.Install()

			err := GitDeleteBranch(tc.dir, tc.branch, tc.force)

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
			mock := NewOutputCommandMock(t, []OutputCommandStub{{
				Dir:  tc.dir,
				Args: tc.wantArgs,
				Err:  tc.mockErr,
			}})
			mock.Install()

			err := GitPushRefspec(tc.dir, tc.remote, tc.localRef, tc.remoteRef)

			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
