package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/testutil"
	"github.com/tysonthomas9/loomcli/internal/usage"
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
	testutil.MockStdin(t, input)
}

// SetupTestEnv sets environment variables and registers cleanup with t.Cleanup().
func SetupTestEnv(t *testing.T, vars map[string]string) {
	t.Helper()
	testutil.SetupTestEnv(t, vars)
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
func LoadFixture(t *testing.T, path string) string {
	t.Helper()
	return testutil.LoadFixture(t, path)
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

	installClaudeInvokerMock(t, func(workDir, prompt, agentName string) error {
		recorder.mu.Lock()
		recorder.Invocations = append(recorder.Invocations, struct {
			WorkDir   string
			Prompt    string
			AgentName string
		}{workDir, prompt, agentName})
		recorder.mu.Unlock()
		return recorder.ReturnErr
	})

	return recorder
}

// MockClaudeInvokerRecorder is a backwards-compatible alias.
type MockClaudeInvokerRecorder = MockAgentInvokerRecorder

// SetupMockClaudeInvoker is a backwards-compatible alias for SetupMockAgentInvoker.
func SetupMockClaudeInvoker(t *testing.T, returnErr error) *MockAgentInvokerRecorder {
	return SetupMockAgentInvoker(t, returnErr)
}

// installExecMock installs a MockExecRunner as the global execCommand and
// registers cleanup. Bridge pattern: sets the global for production code
// that calls execCommand directly.
func installExecMock(t *testing.T, m *MockExecRunner) {
	t.Helper()
	orig := execCommand
	execCommand = m.Run
	t.Cleanup(func() { execCommand = orig })
}

// installGitOutputMock installs an OutputCommandMock as the global
// runGitWithOutputFunc and registers cleanup with verification.
func installGitOutputMock(t *testing.T, m *OutputCommandMock) {
	t.Helper()
	orig := runGitWithOutputFunc
	runGitWithOutputFunc = m.Exec
	t.Cleanup(func() {
		runGitWithOutputFunc = orig
		m.Verify()
	})
}

// installLookPathMock installs a mock lookPath function and registers cleanup.
func installLookPathMock(t *testing.T, fn func(string) (string, error)) {
	t.Helper()
	orig := lookPath
	lookPath = fn
	t.Cleanup(func() { lookPath = orig })
}

// installExecContextMock installs a mock execCommandContext function and registers cleanup.
func installExecContextMock(t *testing.T, fn func(context.Context, string, string, ...string) CommandResult) {
	t.Helper()
	orig := execCommandContext
	execCommandContext = fn
	t.Cleanup(func() { execCommandContext = orig })
}

// installClaudeInvokerMock installs a mock claudeInvoker and registers cleanup.
func installClaudeInvokerMock(t *testing.T, fn func(workDir, prompt, agentName string) error) {
	t.Helper()
	orig := claudeInvoker
	claudeInvoker = fn
	t.Cleanup(func() { claudeInvoker = orig })
}

// installClaudeNonInteractiveMock installs a mock claudeNonInteractiveInvoker and registers cleanup.
func installClaudeNonInteractiveMock(t *testing.T, fn func(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error) {
	t.Helper()
	orig := claudeNonInteractiveInvoker
	claudeNonInteractiveInvoker = fn
	t.Cleanup(func() { claudeNonInteractiveInvoker = orig })
}

// installCodexInvokerMock installs a mock codexInvoker and registers cleanup.
func installCodexInvokerMock(t *testing.T, fn func(workDir, prompt, agentName string) error) {
	t.Helper()
	orig := codexInvoker
	codexInvoker = fn
	t.Cleanup(func() { codexInvoker = orig })
}

// installCodexNonInteractiveMock installs a mock codexNonInteractiveInvoker and registers cleanup.
func installCodexNonInteractiveMock(t *testing.T, fn func(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error) {
	t.Helper()
	orig := codexNonInteractiveInvoker
	codexNonInteractiveInvoker = fn
	t.Cleanup(func() { codexNonInteractiveInvoker = orig })
}

// installOpenCodeInvokerMock installs a mock openCodeInvoker and registers cleanup.
func installOpenCodeInvokerMock(t *testing.T, fn func(workDir, prompt, agentName string) error) {
	t.Helper()
	orig := openCodeInvoker
	openCodeInvoker = fn
	t.Cleanup(func() { openCodeInvoker = orig })
}

// installOpenCodeNonInteractiveMock installs a mock openCodeNonInteractiveInvoker and registers cleanup.
func installOpenCodeNonInteractiveMock(t *testing.T, fn func(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error) {
	t.Helper()
	orig := openCodeNonInteractiveInvoker
	openCodeNonInteractiveInvoker = fn
	t.Cleanup(func() { openCodeNonInteractiveInvoker = orig })
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
	installGitOutputMock(m.t, m)
}

// Calls returns the actual calls made to the mock
func (m *OutputCommandMock) Calls() []OutputCommandStub {
	return m.calls
}

// containsSubstring checks if any element in the slice contains the substring.
func containsSubstring(slice []string, substr string) bool {
	return testutil.ContainsSubstring(slice, substr)
}
