// Package clitest provides shared test utilities for cli subpackages.
// This is a non-test package so it can be imported by _test.go files
// in any cli subpackage.
package clitest

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// --- Small helpers ---

func IntPtr(v int) *int    { return &v }
func BoolPtr(v bool) *bool { return &v }

func MustJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func SlicesEqual(a, b []string) bool {
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

// --- Git env helpers ---

// GitEnvVars lists GIT_* environment variables that can redirect git commands.
var GitEnvVars = []string{
	"GIT_DIR",
	"GIT_WORK_TREE",
	"GIT_INDEX_FILE",
	"GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES",
	"GIT_CEILING_DIRECTORIES",
	"GIT_COMMON_DIR",
}

// GitSafeEnv returns os.Environ() with all GIT_* redirect variables removed.
func GitSafeEnv(extra ...string) []string {
	strip := make(map[string]bool, len(GitEnvVars))
	for _, k := range GitEnvVars {
		strip[k] = true
	}
	var env []string
	for _, e := range os.Environ() {
		idx := strings.IndexByte(e, '=')
		if idx < 0 {
			env = append(env, e)
			continue
		}
		if !strip[e[:idx]] {
			env = append(env, e)
		}
	}
	return append(env, extra...)
}

// ClearGitEnvVars unsets GIT_* env vars and restores them after the test.
func ClearGitEnvVars(t *testing.T) {
	t.Helper()
	for _, k := range GitEnvVars {
		if orig, ok := os.LookupEnv(k); ok {
			t.Cleanup(func() { _ = os.Setenv(k, orig) })
		} else {
			t.Cleanup(func() { _ = os.Unsetenv(k) })
		}
		_ = os.Unsetenv(k)
	}
}

// --- Mock types ---

// MockGitRunner records calls and returns configurable results.
type MockGitRunner struct {
	mu         sync.Mutex
	RunCalls   []MockGitCall
	RunResult  cli.CommandResult
	RunFunc    func(dir string, args ...string) cli.CommandResult
	WithOutput error
}

type MockGitCall struct {
	Dir  string
	Args []string
}

func (m *MockGitRunner) Run(dir string, args ...string) cli.CommandResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.RunCalls = append(m.RunCalls, MockGitCall{Dir: dir, Args: args})
	if m.RunFunc != nil {
		return m.RunFunc(dir, args...)
	}
	return m.RunResult
}

func (m *MockGitRunner) RunContext(_ context.Context, dir string, args ...string) cli.CommandResult {
	return m.Run(dir, args...)
}

func (m *MockGitRunner) RunWithOutput(dir string, args ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.RunCalls = append(m.RunCalls, MockGitCall{Dir: dir, Args: args})
	return m.WithOutput
}

// MockExecRunner records calls and returns configurable results.
type MockExecRunner struct {
	mu      sync.Mutex
	Calls   []MockExecCall
	Result  cli.CommandResult
	RunFunc func(dir, name string, args ...string) cli.CommandResult
}

type MockExecCall struct {
	Dir  string
	Name string
	Args []string
}

func (m *MockExecRunner) Run(dir, name string, args ...string) cli.CommandResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, MockExecCall{Dir: dir, Name: name, Args: args})
	if m.RunFunc != nil {
		return m.RunFunc(dir, name, args...)
	}
	return m.Result
}

// MockExecContextRunner records calls and returns configurable results.
type MockExecContextRunner struct {
	mu      sync.Mutex
	Calls   []MockExecCall
	Result  cli.CommandResult
	RunFunc func(ctx context.Context, dir, name string, args ...string) cli.CommandResult
}

func (m *MockExecContextRunner) Run(ctx context.Context, dir, name string, args ...string) cli.CommandResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, MockExecCall{Dir: dir, Name: name, Args: args})
	if m.RunFunc != nil {
		return m.RunFunc(ctx, dir, name, args...)
	}
	return m.Result
}

// MockAgentInvoker records agent invocations for tests.
type MockAgentInvoker struct {
	mu                  sync.Mutex
	InteractiveFunc     func(workDir, prompt, agentName string) error
	NonInteractiveFunc  func(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error
	InteractiveErr      error
	NonInteractiveErr   error
	InteractiveCalls    []MockAgentCall
	NonInteractiveCalls []MockAgentCall
}

type MockAgentCall struct {
	WorkDir   string
	Prompt    string
	AgentName string
}

func (m *MockAgentInvoker) InvokeInteractive(workDir, prompt, agentName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.InteractiveCalls = append(m.InteractiveCalls, MockAgentCall{WorkDir: workDir, Prompt: prompt, AgentName: agentName})
	if m.InteractiveFunc != nil {
		return m.InteractiveFunc(workDir, prompt, agentName)
	}
	return m.InteractiveErr
}

func (m *MockAgentInvoker) InvokeNonInteractive(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.NonInteractiveCalls = append(m.NonInteractiveCalls, MockAgentCall{WorkDir: workDir, Prompt: prompt, AgentName: agentName})
	if m.NonInteractiveFunc != nil {
		return m.NonInteractiveFunc(workDir, prompt, agentName, shutdown, collector)
	}
	return m.NonInteractiveErr
}

// MockFileSystem is an in-memory filesystem for tests.
type MockFileSystem struct {
	mu    sync.Mutex
	Files map[string][]byte
	Dirs  map[string]bool
}

func NewMockFileSystem() *MockFileSystem {
	return &MockFileSystem{
		Files: make(map[string][]byte),
		Dirs:  make(map[string]bool),
	}
}

func (m *MockFileSystem) ReadFile(path string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.Files[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return data, nil
}

func (m *MockFileSystem) WriteFile(path string, data []byte, _ os.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Files[path] = data
	return nil
}

func (m *MockFileSystem) Stat(path string) (os.FileInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.Files[path]; ok {
		return nil, nil
	}
	if m.Dirs[path] {
		return nil, nil
	}
	return nil, os.ErrNotExist
}

func (m *MockFileSystem) MkdirAll(path string, _ os.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Dirs[path] = true
	return nil
}

func (m *MockFileSystem) Remove(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.Files, path)
	delete(m.Dirs, path)
	return nil
}

// --- NewTestDeps ---

// NewTestDeps returns a *cli.Deps with all fields set to mock implementations.
func NewTestDeps(t *testing.T) (*cli.Deps, *MockGitRunner, *MockExecRunner, *MockFileSystem, *MockIssueBackend) {
	t.Helper()
	git := &MockGitRunner{}
	execR := &MockExecRunner{}
	fs := NewMockFileSystem()
	tracker := NewMockIssueBackend()
	deps := &cli.Deps{
		Git:          git,
		Exec:         execR,
		FS:           fs,
		Logger:       slog.Default(),
		IssueBackend: tracker,
		Clock:        func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		LookPath:     func(file string) (string, error) { return "/usr/bin/" + file, nil },
		ExecCtx:      &MockExecContextRunner{},
		Agent:        &MockAgentInvoker{},
	}
	return deps, git, execR, fs, tracker
}

// ExecBridgeGitRunner implements cli.GitRunner by delegating Run() through an ExecRunner.
type ExecBridgeGitRunner struct {
	Exec cli.ExecRunner
}

func (g *ExecBridgeGitRunner) Run(dir string, args ...string) cli.CommandResult {
	return g.Exec.Run(dir, "git", args...)
}

func (g *ExecBridgeGitRunner) RunContext(_ context.Context, dir string, args ...string) cli.CommandResult {
	return g.Run(dir, args...)
}

func (g *ExecBridgeGitRunner) RunWithOutput(dir string, args ...string) error {
	return nil // stub for tests
}
