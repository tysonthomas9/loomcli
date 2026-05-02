package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/testutil"
)

// --- Git env helpers ---

func clearGitEnvVars(t *testing.T)        { clitest.ClearGitEnvVars(t) }
func gitSafeEnv(extra ...string) []string { return clitest.GitSafeEnv(extra...) }

// --- Type aliases from cli ---

type LockInfo = cli.LockInfo
type WorktreeInfo = cli.WorktreeInfo
type CommandResult = cli.CommandResult

const LockFileName = cli.LockFileName

var AcquireLock = cli.AcquireLock
var ReleaseLock = cli.ReleaseLock
var UpdateLockTask = cli.UpdateLockTask
var GetDefaultBranch = cli.GetDefaultBranch
var ResetWorkspaceRuntimeDirCache = cli.ResetWorkspaceRuntimeDirCache
var NewResolver = cli.NewResolver

// --- Type aliases from config ---

type LoomConfig = config.LoomConfig
type RepoConfig = config.RepoConfig
type WorkspaceConfig = config.WorkspaceConfig

var IsWorkspaceMode = config.IsWorkspaceMode

// --- Type aliases from clitest ---

type MockExecRunner = clitest.MockExecRunner
type MockAgentInvoker = clitest.MockAgentInvoker
type MockIssueBackend = clitest.MockIssueBackend
type MockFileSystem = clitest.MockFileSystem
type MockGitRunner = clitest.MockGitRunner
type ExecBridgeGitRunner = clitest.ExecBridgeGitRunner

func NewTestDeps(t *testing.T) (*cli.Deps, *clitest.MockGitRunner, *clitest.MockExecRunner, *clitest.MockFileSystem, *clitest.MockIssueBackend) {
	return clitest.NewTestDeps(t)
}

func NewMockIssueBackend() *clitest.MockIssueBackend { return clitest.NewMockIssueBackend() }

// --- testutil helpers ---

func MockStdin(t *testing.T, input string)              { testutil.MockStdin(t, input) }
func SetupTestEnv(t *testing.T, vars map[string]string) { testutil.SetupTestEnv(t, vars) }

// --- Stubs that were in cli test files ---

// CommandStub represents an expected command call and its response
type CommandStub struct {
	Dir    string
	Name   string
	Args   []string
	Stdout string
	Stderr string
	Err    error
}

// CommandMock provides a mock command executor for tests
type CommandMock struct {
	t     *testing.T
	stubs []CommandStub
	calls []CommandStub
	idx   int
}

func NewCommandMock(t *testing.T, stubs []CommandStub) *CommandMock {
	return &CommandMock{t: t, stubs: stubs}
}

func slicesEqual(a, b []string) bool { return clitest.SlicesEqual(a, b) }

func (m *CommandMock) Exec(dir, name string, args ...string) cli.CommandResult {
	call := CommandStub{Dir: dir, Name: name, Args: args}
	m.calls = append(m.calls, call)
	if m.idx >= len(m.stubs) {
		m.t.Fatalf("unexpected command call #%d: %s %v in %s", m.idx+1, name, args, dir)
	}
	stub := m.stubs[m.idx]
	m.idx++
	if stub.Name != "" && stub.Name != name {
		m.t.Errorf("call #%d: expected command %q, got %q", m.idx, stub.Name, name)
	}
	if stub.Dir != "" && stub.Dir != dir {
		m.t.Errorf("call #%d: expected dir %q, got %q", m.idx, stub.Dir, dir)
	}
	if stub.Args != nil && !slicesEqual(stub.Args, args) {
		m.t.Errorf("call #%d: expected args %v, got %v", m.idx, stub.Args, args)
	}
	return cli.CommandResult{Stdout: stub.Stdout, Stderr: stub.Stderr, Err: stub.Err}
}

func (m *CommandMock) Run(dir, name string, args ...string) cli.CommandResult {
	return m.Exec(dir, name, args...)
}

func (m *CommandMock) Verify() {
	if m.idx != len(m.stubs) {
		m.t.Errorf("expected %d command calls, got %d", len(m.stubs), m.idx)
	}
}

// Install installs the mock on the global defaultDeps.
// WARNING: modifies global state — tests using this MUST NOT use t.Parallel().
func (m *CommandMock) Install() {
	orig := defaultDeps.Exec
	defaultDeps.Exec = m
	m.t.Cleanup(func() {
		defaultDeps.Exec = orig
		m.Verify()
	})
}

func (m *CommandMock) InstallOn(deps *cli.Deps) {
	deps.Exec = m
	deps.Git = &clitest.ExecBridgeGitRunner{Exec: m}
	m.t.Cleanup(func() { m.Verify() })
}

func (m *CommandMock) Calls() []CommandStub { return m.calls }

// OutputCommandStub represents an expected RunWithOutput call.
type OutputCommandStub struct {
	Dir  string
	Name string
	Args []string
	Err  error
}

type OutputCommandMock struct {
	t     *testing.T
	stubs []OutputCommandStub
	calls []OutputCommandStub
	idx   int
}

func NewOutputCommandMock(t *testing.T, stubs []OutputCommandStub) *OutputCommandMock {
	return &OutputCommandMock{t: t, stubs: stubs}
}

func (m *OutputCommandMock) Exec(dir string, args ...string) error {
	call := OutputCommandStub{Dir: dir, Args: args}
	m.calls = append(m.calls, call)
	if m.idx >= len(m.stubs) {
		m.t.Fatalf("unexpected output command call #%d in %s", m.idx+1, dir)
	}
	stub := m.stubs[m.idx]
	m.idx++
	return stub.Err
}

func (m *OutputCommandMock) Verify() {
	if m.idx != len(m.stubs) {
		m.t.Errorf("expected %d output command calls, got %d", len(m.stubs), m.idx)
	}
}

// compositeGitRunner delegates Run to deps.Exec and RunWithOutput to the mock.
type compositeGitRunner struct {
	exec cli.ExecRunner
	out  func(dir string, args ...string) error
}

func (c *compositeGitRunner) Run(dir string, args ...string) cli.CommandResult {
	return c.exec.Run(dir, "git", args...)
}

func (c *compositeGitRunner) RunWithOutput(dir string, args ...string) error {
	return c.out(dir, args...)
}

// gitOutputMockRunner wraps an OutputCommandMock to implement GitRunner.
// Run() delegates to defaultDeps.Exec, RunWithOutput() delegates to the mock.
type gitOutputMockRunner struct {
	outputFn func(dir string, args ...string) error
}

func (g *gitOutputMockRunner) Run(dir string, args ...string) cli.CommandResult {
	return defaultDeps.Exec.Run(dir, "git", args...)
}

func (g *gitOutputMockRunner) RunWithOutput(dir string, args ...string) error {
	return g.outputFn(dir, args...)
}

// Install installs the OutputCommandMock on the global defaultDeps.
// WARNING: modifies global state — tests using this MUST NOT use t.Parallel().
func (m *OutputCommandMock) Install() {
	m.t.Helper()
	origGit := defaultDeps.Git
	defaultDeps.Git = &gitOutputMockRunner{outputFn: m.Exec}
	m.t.Cleanup(func() {
		defaultDeps.Git = origGit
		m.Verify()
	})
}

// InstallOn installs the OutputCommandMock on the given per-test Deps.
// Call CommandMock.InstallOn(deps) BEFORE this method so that deps.Exec is
// the CommandMock when compositeGitRunner captures it.
func (m *OutputCommandMock) InstallOn(deps *cli.Deps) {
	deps.Git = &compositeGitRunner{exec: deps.Exec, out: m.Exec}
	m.t.Cleanup(func() { m.Verify() })
}

// --- Unexported helpers that were in cli package ---

// isCleanWorkingTreeDeps is a lowercase alias for the now-exported function.
func isCleanWorkingTreeDeps(deps *cli.Deps, dir string) (bool, error) {
	return IsCleanWorkingTreeDeps(deps, dir)
}

// confirmAction is a lowercase alias for the now-exported function.
func confirmAction(prompt string) bool { return ConfirmAction(prompt) }

func containsSubstring(slice []string, substr string) bool {
	for _, s := range slice {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}

// defaultResolver is a package-level Resolver for tests.
var defaultResolver *cli.Resolver

func createGitRepo(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "test"},
		{"git", "commit", "--allow-empty", "-m", "initial"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...) //nolint:gosec //nolint:norawexec
		cmd.Dir = path
		cmd.Env = clitest.GitSafeEnv(
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}
}

func setupWorkspaceConfig(t *testing.T, cfg *config.LoomConfig) {
	t.Helper()
	configDir := t.TempDir()
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), data, 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOOM_CONFIG_DIR", configDir)
}

// installClaudeInvokerMock is a no-op stub for tests that used the cli package global.
// Since the invoker is now in the backends package, tests that need this should
// use the deps-based mock injection instead.
func installClaudeInvokerMock(t *testing.T, fn func(workDir, prompt, agentName string) error) {
	t.Helper()
	// The invoker global is now in backends package. Tests should use
	// deps.Agent = &MockAgentInvoker{InteractiveFunc: fn} instead.
	// This is a compatibility shim that does nothing.
}

// FlexibleStub represents a pattern-based command stub with call count constraints.
type FlexibleStub struct {
	Name      string
	ArgPrefix []string
	Result    cli.CommandResult
	MinCalls  int
	MaxCalls  int
	callCount int
}

// WithMinCalls sets the minimum expected call count for a stub.
func (s *FlexibleStub) WithMinCalls(n int) *FlexibleStub {
	s.MinCalls = n
	return s
}

// WithMaxCalls sets the maximum expected call count for a stub.
func (s *FlexibleStub) WithMaxCalls(n int) *FlexibleStub {
	s.MaxCalls = n
	return s
}

// FlexibleCommandMock provides a pattern-based command mock for tests where
// exact call order or count is not predictable.
type FlexibleCommandMock struct {
	t     *testing.T
	mu    sync.Mutex
	stubs []*FlexibleStub
	calls []CommandStub
}

// NewFlexibleCommandMock creates a new flexible command mock.
func NewFlexibleCommandMock(t *testing.T) *FlexibleCommandMock {
	return &FlexibleCommandMock{t: t}
}

// AddStub adds a stub that matches by command name and optional arg prefix.
func (m *FlexibleCommandMock) AddStub(name string, argPrefix []string, result cli.CommandResult) *FlexibleStub {
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

// Exec implements the command executor interface with pattern matching.
func (m *FlexibleCommandMock) Exec(dir, name string, args ...string) cli.CommandResult {
	m.mu.Lock()
	defer m.mu.Unlock()

	call := CommandStub{Dir: dir, Name: name, Args: args}
	m.calls = append(m.calls, call)

	for _, stub := range m.stubs {
		if stub.Name != name {
			continue
		}
		if stub.ArgPrefix != nil && !hasArgPrefix(args, stub.ArgPrefix) {
			continue
		}
		if stub.MaxCalls > 0 && stub.callCount >= stub.MaxCalls {
			continue
		}
		stub.callCount++
		return stub.Result
	}

	m.t.Errorf("no matching stub for command: %s %v", name, args)
	return cli.CommandResult{Err: os.ErrNotExist}
}

// hasArgPrefix checks if args starts with the given prefix.
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

// Verify ensures all min/max call constraints were met.
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

// Run implements ExecRunner, delegating to Exec.
func (m *FlexibleCommandMock) Run(dir, name string, args ...string) cli.CommandResult {
	return m.Exec(dir, name, args...)
}

// Install installs the mock on the global defaultDeps.
// WARNING: modifies global state — tests using this MUST NOT use t.Parallel().
func (m *FlexibleCommandMock) Install() {
	orig := defaultDeps.Exec
	defaultDeps.Exec = m
	m.t.Cleanup(func() {
		defaultDeps.Exec = orig
		m.Verify()
	})
}

// InstallOn installs the mock on the given per-test Deps.
func (m *FlexibleCommandMock) InstallOn(deps *cli.Deps) {
	deps.Exec = m
	deps.Git = &clitest.ExecBridgeGitRunner{Exec: m}
	m.t.Cleanup(func() { m.Verify() })
}

// Calls returns the actual calls made to the mock.
func (m *FlexibleCommandMock) Calls() []CommandStub {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]CommandStub, len(m.calls))
	copy(result, m.calls)
	return result
}
