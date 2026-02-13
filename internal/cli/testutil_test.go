package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// Install installs the mock and registers cleanup with t.Cleanup().
// WARNING: This modifies global state (execCommand). Tests using this mock
// MUST NOT use t.Parallel() as it would cause race conditions.
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

// FlexibleStub represents a command pattern that can match multiple calls
type FlexibleStub struct {
	Name      string        // Command name to match (required)
	ArgPrefix []string      // Args prefix to match (optional, nil = match any)
	Result    CommandResult // Result to return
	MinCalls  int           // Minimum expected calls (default 0)
	MaxCalls  int           // Maximum expected calls (0 = unlimited)
	callCount int           // Internal: actual call count
}

// FlexibleCommandMock provides a pattern-based command mock for tests where
// exact call order or count is not predictable (e.g., variable worktree counts)
type FlexibleCommandMock struct {
	t     *testing.T
	mu    sync.Mutex
	stubs []*FlexibleStub
	calls []CommandStub // actual calls received
}

// NewFlexibleCommandMock creates a new flexible command mock
func NewFlexibleCommandMock(t *testing.T) *FlexibleCommandMock {
	return &FlexibleCommandMock{t: t}
}

// AddStub adds a stub that matches by command name and optional arg prefix
func (m *FlexibleCommandMock) AddStub(name string, argPrefix []string, result CommandResult) *FlexibleStub {
	m.mu.Lock()
	defer m.mu.Unlock()
	stub := &FlexibleStub{
		Name:      name,
		ArgPrefix: argPrefix,
		Result:    result,
	}
	m.stubs = append(m.stubs, stub)
	return stub
}

// WithMinCalls sets the minimum expected call count for a stub
func (s *FlexibleStub) WithMinCalls(n int) *FlexibleStub {
	s.MinCalls = n
	return s
}

// WithMaxCalls sets the maximum expected call count for a stub
func (s *FlexibleStub) WithMaxCalls(n int) *FlexibleStub {
	s.MaxCalls = n
	return s
}

// Exec implements the commandExecutor interface with pattern matching
func (m *FlexibleCommandMock) Exec(dir, name string, args ...string) CommandResult {
	m.mu.Lock()
	defer m.mu.Unlock()

	call := CommandStub{Dir: dir, Name: name, Args: args}
	m.calls = append(m.calls, call)

	// Find matching stub (first match wins)
	for _, stub := range m.stubs {
		if stub.Name != name {
			continue
		}
		if stub.ArgPrefix != nil && !hasArgPrefix(args, stub.ArgPrefix) {
			continue
		}
		// Check max calls
		if stub.MaxCalls > 0 && stub.callCount >= stub.MaxCalls {
			continue
		}
		stub.callCount++
		return stub.Result
	}

	// No match found - fail with helpful message
	m.t.Errorf("no matching stub for command: %s %v", name, args)
	return CommandResult{Err: os.ErrNotExist}
}

// hasArgPrefix checks if args starts with the given prefix
func hasArgPrefix(args, prefix []string) bool {
	if len(args) < len(prefix) {
		return false
	}
	for i, p := range prefix {
		if args[i] != p {
			return false
		}
	}
	return true
}

// Verify ensures all min/max call constraints were met
func (m *FlexibleCommandMock) Verify() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, stub := range m.stubs {
		if stub.MinCalls > 0 && stub.callCount < stub.MinCalls {
			m.t.Errorf("stub %s %v: expected at least %d calls, got %d",
				stub.Name, stub.ArgPrefix, stub.MinCalls, stub.callCount)
		}
	}
}

// Install installs the mock and registers cleanup with t.Cleanup().
// WARNING: This modifies global state (execCommand). Tests using this mock
// MUST NOT use t.Parallel() as it would cause race conditions.
func (m *FlexibleCommandMock) Install() {
	orig := execCommand
	execCommand = m.Exec
	m.t.Cleanup(func() {
		execCommand = orig
		m.Verify()
	})
}

// Calls returns the actual calls made to the mock
func (m *FlexibleCommandMock) Calls() []CommandStub {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]CommandStub, len(m.calls))
	copy(result, m.calls)
	return result
}

// LoadFixture reads a test fixture file from the testdata directory.
// It searches for testdata in the current directory and common relative paths.
func LoadFixture(t *testing.T, path string) string {
	t.Helper()

	// Try multiple possible locations for testdata
	searchPaths := []string{
		filepath.Join("testdata", path),
		filepath.Join("internal", "cli", "testdata", path),
		filepath.Join("..", "..", "internal", "cli", "testdata", path),
	}

	// Also try from GOPATH or module root
	if cwd, err := os.Getwd(); err == nil {
		// Walk up to find the repo root (has go.mod)
		dir := cwd
		for i := 0; i < 10; i++ {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				searchPaths = append(searchPaths, filepath.Join(dir, "internal", "cli", "testdata", path))
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	for _, searchPath := range searchPaths {
		data, err := os.ReadFile(searchPath)
		if err == nil {
			return string(data)
		}
	}

	t.Fatalf("failed to load fixture %s: not found in any search path", path)
	return ""
}

// SetupMultiWorktreeEnv creates multiple worktree directories with optional lock files.
// Returns the base directory path (containing the "worktrees" subdirectory).
// Each worktree is created as tmpDir/worktrees/<name>/.git/
// If withLock is provided and matches a name, a lock file is created for that worktree.
func SetupMultiWorktreeEnv(t *testing.T, names []string, withLock map[string]*LockInfo) string {
	t.Helper()
	tmpDir := t.TempDir()
	worktreesDir := filepath.Join(tmpDir, "worktrees")

	for _, name := range names {
		wtPath := filepath.Join(worktreesDir, name)
		if err := os.MkdirAll(filepath.Join(wtPath, ".git"), 0755); err != nil {
			t.Fatalf("failed to create worktree %s: %v", name, err)
		}

		// Create lock file if specified
		if lockInfo, ok := withLock[name]; ok && lockInfo != nil {
			lockPath := filepath.Join(wtPath, LockFileName)
			data, err := json.Marshal(lockInfo)
			if err != nil {
				t.Fatalf("failed to marshal lock info: %v", err)
			}
			if err := os.WriteFile(lockPath, data, 0644); err != nil {
				t.Fatalf("failed to write lock file: %v", err)
			}
		}
	}

	return tmpDir
}

// MockAgentInvokerRecorder records invocations of the mocked agent invoker.
// Thread-safe for concurrent invocations within a single test.
type MockAgentInvokerRecorder struct {
	mu          sync.Mutex
	Invocations []struct {
		WorkDir   string
		Prompt    string
		AgentName string
	}
	ReturnErr error
}

// SetupMockAgentInvoker installs a mock agent invoker and registers cleanup.
// WARNING: This modifies global state (claudeInvoker). Tests using this mock
// MUST NOT use t.Parallel() as it would cause race conditions.
func SetupMockAgentInvoker(t *testing.T, returnErr error) *MockAgentInvokerRecorder {
	t.Helper()
	recorder := &MockAgentInvokerRecorder{ReturnErr: returnErr}
	orig := claudeInvoker

	claudeInvoker = func(workDir, prompt, agentName string) error {
		recorder.mu.Lock()
		recorder.Invocations = append(recorder.Invocations, struct {
			WorkDir   string
			Prompt    string
			AgentName string
		}{workDir, prompt, agentName})
		recorder.mu.Unlock()
		return recorder.ReturnErr
	}

	t.Cleanup(func() {
		claudeInvoker = orig
	})

	return recorder
}

// MockClaudeInvokerRecorder is a backwards-compatible alias.
type MockClaudeInvokerRecorder = MockAgentInvokerRecorder

// SetupMockClaudeInvoker is a backwards-compatible alias for SetupMockAgentInvoker.
func SetupMockClaudeInvoker(t *testing.T, returnErr error) *MockAgentInvokerRecorder {
	return SetupMockAgentInvoker(t, returnErr)
}

// containsSubstring checks if any element in the slice contains the substring
func containsSubstring(slice []string, substr string) bool {
	for _, s := range slice {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}
