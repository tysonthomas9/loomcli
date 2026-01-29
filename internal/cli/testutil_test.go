package cli

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// CommandStub represents an expected command call and its response
type CommandStub struct {
	Dir    string   // expected directory (empty = any)
	Name   string   // expected command (e.g., "git", "bd")
	Args   []string // expected arguments (nil = any)
	Stdout string   // response stdout
	Stderr string   // response stderr
	Err    error    // response error
}

// CommandMock provides a mock command executor for tests
type CommandMock struct {
	t     *testing.T
	stubs []CommandStub
	calls []CommandStub // actual calls received
	idx   int
}

// NewCommandMock creates a new command mock with expected stubs
func NewCommandMock(t *testing.T, stubs []CommandStub) *CommandMock {
	return &CommandMock{t: t, stubs: stubs}
}

// Exec implements the commandExecutor interface
func (m *CommandMock) Exec(dir, name string, args ...string) CommandResult {
	call := CommandStub{Dir: dir, Name: name, Args: args}
	m.calls = append(m.calls, call)

	if m.idx >= len(m.stubs) {
		m.t.Fatalf("unexpected command call #%d: %s %v in %s", m.idx+1, name, args, dir)
	}

	stub := m.stubs[m.idx]
	m.idx++

	// Validate command matches expectations (empty = any)
	if stub.Name != "" && stub.Name != name {
		m.t.Errorf("call #%d: expected command %q, got %q", m.idx, stub.Name, name)
	}
	if stub.Dir != "" && stub.Dir != dir {
		m.t.Errorf("call #%d: expected dir %q, got %q", m.idx, stub.Dir, dir)
	}
	if stub.Args != nil && !slicesEqual(stub.Args, args) {
		m.t.Errorf("call #%d: expected args %v, got %v", m.idx, stub.Args, args)
	}

	return CommandResult{
		Stdout: stub.Stdout,
		Stderr: stub.Stderr,
		Err:    stub.Err,
	}
}

// slicesEqual compares two string slices for equality
func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Verify ensures all expected calls were made
func (m *CommandMock) Verify() {
	if m.idx != len(m.stubs) {
		m.t.Errorf("expected %d command calls, got %d", len(m.stubs), m.idx)
	}
}

// Install installs the mock and registers cleanup with t.Cleanup()
func (m *CommandMock) Install() {
	orig := execCommand
	execCommand = m.Exec
	m.t.Cleanup(func() {
		execCommand = orig
		m.Verify()
	})
}

// Calls returns the actual calls made to the mock
func (m *CommandMock) Calls() []CommandStub {
	return m.calls
}

// SetupTestWorktree creates a mock worktree directory structure
func SetupTestWorktree(t *testing.T, name string) string {
	t.Helper()
	tmpDir := t.TempDir()

	// Create worktrees/<name> directory
	wtPath := filepath.Join(tmpDir, "worktrees", name)
	if err := os.MkdirAll(wtPath, 0755); err != nil {
		t.Fatalf("failed to create worktree dir: %v", err)
	}

	// Create .git directory (marks as git worktree)
	gitDir := filepath.Join(wtPath, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatalf("failed to create .git dir: %v", err)
	}

	return tmpDir
}

// MockStdin replaces os.Stdin with a pipe containing the given input.
// Restores original stdin via t.Cleanup().
func MockStdin(t *testing.T, input string) {
	t.Helper()
	origStdin := os.Stdin

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer w.Close()

	_, err = io.WriteString(w, input)
	if err != nil {
		t.Fatalf("failed to write to pipe: %v", err)
	}

	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = origStdin
		r.Close()
	})
}

// SetupTestEnv sets environment variables and registers cleanup with t.Cleanup()
func SetupTestEnv(t *testing.T, vars map[string]string) {
	t.Helper()
	origVals := make(map[string]string)
	origSet := make(map[string]bool)

	for k, v := range vars {
		origVals[k], origSet[k] = os.LookupEnv(k)
		os.Setenv(k, v)
	}

	t.Cleanup(func() {
		for k := range vars {
			if origSet[k] {
				os.Setenv(k, origVals[k])
			} else {
				os.Unsetenv(k)
			}
		}
	})
}
