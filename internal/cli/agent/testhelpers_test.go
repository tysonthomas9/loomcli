package agent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/automode"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/testutil"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

type Deps = cli.Deps
type CommandResult = cli.CommandResult
type LockInfo = cli.LockInfo
type WorktreeInfo = cli.WorktreeInfo
type LoomConfig = config.LoomConfig
type RepoConfig = config.RepoConfig
type WorkspaceConfig = config.WorkspaceConfig
type MockExecRunner = clitest.MockExecRunner
type MockIssueBackend = clitest.MockIssueBackend
type MockGitRunner = clitest.MockGitRunner
type MockFileSystem = clitest.MockFileSystem
type MockAgentInvoker = clitest.MockAgentInvoker

const LockFileName = cli.LockFileName

var (
	ResetWorkspaceRuntimeDirCache   = cli.ResetWorkspaceRuntimeDirCache
	ResolveActiveWorkspace          = config.ResolveActiveWorkspace
	GetSignalFilePath               = cli.GetSignalFilePath
	WithDeps                        = cli.WithDeps
	HasAvailablePlanningTasks       = automode.HasAvailablePlanningTasks
	GetAvailablePlanningTasks       = automode.GetAvailablePlanningTasks
	HasAvailableImplementationTasks = automode.HasAvailableImplementationTasks
	GetAvailableImplementationTasks = automode.GetAvailableImplementationTasks
)

func NewTestDeps(t *testing.T) (*cli.Deps, *clitest.MockGitRunner, *clitest.MockExecRunner, *clitest.MockFileSystem, *clitest.MockIssueBackend) {
	return clitest.NewTestDeps(t)
}

func NewMockIssueBackend() *clitest.MockIssueBackend { return clitest.NewMockIssueBackend() }

func mustJSON(v interface{}) string { return clitest.MustJSON(v) }

func installExecMock(t *testing.T, m *clitest.MockExecRunner) {
	t.Helper()
	dd := cli.TestingGetDefaultDeps()
	orig := dd.Exec
	origIssueBackend := dd.IssueBackend
	dd.Exec = m
	dd.IssueBackend = newExecReadyIssueBackend(m)
	cli.ResetDefaultIssueBackend()
	t.Cleanup(func() {
		dd.Exec = orig
		dd.IssueBackend = origIssueBackend
		cli.ResetDefaultIssueBackend()
	})
}

var (
	resetDefaultIssueBackend = cli.ResetDefaultIssueBackend
	setDefaultIssueBackend   = cli.SetDefaultIssueBackend
	RegisterBackend          = cli.RegisterBackend
	SetBackend               = cli.SetBackend
)

func resetBackendState(t *testing.T) {
	t.Helper()
	cli.TestingResetBackendState(t)
}

// mockBackend implements cli.Backend for testing in the agent package.
type mockBackend struct {
	name             string
	interactiveCalls []struct {
		workDir, prompt, agentName string
	}
	interactiveErr error
}

func (m *mockBackend) Name() string { return m.name }
func (m *mockBackend) InvokeInteractive(workDir, prompt, agentName string) error {
	m.interactiveCalls = append(m.interactiveCalls, struct {
		workDir, prompt, agentName string
	}{workDir, prompt, agentName})
	return m.interactiveErr
}
func (m *mockBackend) InvokeNonInteractive(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error {
	return nil
}

// defaultResolver is a package-level Resolver for tests.
var defaultResolver *cli.Resolver

var defaultDeps = cli.TestingGetDefaultDeps()

// --- Command stubs ---

type CommandStub struct {
	Dir    string
	Name   string
	Args   []string
	Stdout string
	Stderr string
	Err    error
}

type OutputCommandStub struct {
	Dir  string
	Name string
	Args []string
	Err  error
}

type CommandMock struct {
	t     *testing.T
	stubs []CommandStub
	calls []CommandStub
	idx   int
}

func NewCommandMock(t *testing.T, stubs []CommandStub) *CommandMock {
	return &CommandMock{t: t, stubs: stubs}
}

func (m *CommandMock) Run(dir, name string, args ...string) cli.CommandResult {
	call := CommandStub{Dir: dir, Name: name, Args: args}
	m.calls = append(m.calls, call)
	if m.idx >= len(m.stubs) {
		m.t.Fatalf("unexpected command call #%d: %s %v in %s", m.idx+1, name, args, dir)
	}
	stub := m.stubs[m.idx]
	m.idx++
	return cli.CommandResult{Stdout: stub.Stdout, Stderr: stub.Stderr, Err: stub.Err}
}

func (m *CommandMock) Calls() []CommandStub {
	return m.calls
}

func (m *CommandMock) Verify() {
	if m.idx != len(m.stubs) {
		m.t.Errorf("expected %d command calls, got %d", len(m.stubs), m.idx)
	}
}

func (m *CommandMock) Install() {
	m.InstallOn(cli.TestingGetDefaultDeps())
}

func (m *CommandMock) InstallOn(deps *cli.Deps) {
	origExec := deps.Exec
	origGit := deps.Git
	origIssueBackend := deps.IssueBackend
	deps.Exec = m
	deps.Git = &clitest.ExecBridgeGitRunner{Exec: m}
	if deps == cli.TestingGetDefaultDeps() {
		if m.hasReadyStubs() {
			deps.IssueBackend = newCommandReadyIssueBackend(m)
		} else if _, ok := origIssueBackend.(*clitest.MockIssueBackend); !ok {
			deps.IssueBackend = clitest.NewMockIssueBackend()
		}
	}
	cli.ResetDefaultIssueBackend()
	m.t.Cleanup(func() {
		m.Verify()
		deps.Exec = origExec
		deps.Git = origGit
		deps.IssueBackend = origIssueBackend
		cli.ResetDefaultIssueBackend()
	})
}

func (m *CommandMock) hasReadyStubs() bool {
	for _, stub := range m.stubs {
		if stub.Name == "bd" && len(stub.Args) > 0 && stub.Args[0] == "ready" {
			return true
		}
	}
	return false
}

type commandReadyIssueBackend struct {
	*clitest.MockIssueBackend
	run func(dir, name string, args ...string) cli.CommandResult
}

func newCommandReadyIssueBackend(m *CommandMock) *commandReadyIssueBackend {
	return &commandReadyIssueBackend{
		MockIssueBackend: clitest.NewMockIssueBackend(),
		run:              m.Run,
	}
}

func newExecReadyIssueBackend(m *clitest.MockExecRunner) *commandReadyIssueBackend {
	return &commandReadyIssueBackend{
		MockIssueBackend: clitest.NewMockIssueBackend(),
		run:              m.Run,
	}
}

func (b *commandReadyIssueBackend) Ready(ctx context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error) {
	b.MockIssueBackend.Ready(ctx, opts)
	result := b.run(cli.GetWorkspaceRuntimeDir(), "bd", readyArgs(opts)...)
	if result.Err != nil {
		return nil, result.Err
	}
	return parseReadyIssues(result.Stdout)
}

func readyArgs(opts backend.ReadyOpts) []string {
	limit := opts.Limit
	if limit <= 0 {
		limit = 10000
	}
	args := []string{"ready", "--json", "--limit", strconv.Itoa(limit)}
	if opts.ParentID != "" {
		args = append(args, "--parent", opts.ParentID)
	}
	return args
}

func parseReadyIssues(stdout string) ([]backend.IssueData, error) {
	type issueWire struct {
		backend.IssueData
		Type string `json:"type,omitempty"`
	}
	var wire []issueWire
	if err := json.Unmarshal([]byte(stdout), &wire); err != nil {
		return nil, err
	}
	issues := make([]backend.IssueData, len(wire))
	for i, item := range wire {
		issues[i] = item.IssueData
		if issues[i].IssueType == "" {
			issues[i].IssueType = item.Type
		}
	}
	return issues, nil
}

type OutputCommandMock struct {
	t     *testing.T
	stubs []OutputCommandStub
	idx   int
}

func NewOutputCommandMock(t *testing.T, stubs []OutputCommandStub) *OutputCommandMock {
	return &OutputCommandMock{t: t, stubs: stubs}
}

func (m *OutputCommandMock) RunWithOutput(_ string, _ ...string) error {
	if m.idx >= len(m.stubs) {
		m.t.Fatalf("unexpected RunWithOutput call #%d", m.idx+1)
	}
	stub := m.stubs[m.idx]
	m.idx++
	return stub.Err
}

func (m *OutputCommandMock) Verify() {
	if m.idx != len(m.stubs) {
		m.t.Errorf("OutputCommandMock: expected %d calls, got %d", len(m.stubs), m.idx)
	}
}

// compositeGitRunner delegates Run to an existing GitRunner and RunWithOutput
// to an OutputCommandMock.
type compositeGitRunner struct {
	run        cli.GitRunner
	outputMock *OutputCommandMock
}

func (c *compositeGitRunner) Run(dir string, args ...string) cli.CommandResult {
	return c.run.Run(dir, args...)
}

func (c *compositeGitRunner) RunWithOutput(dir string, args ...string) error {
	return c.outputMock.RunWithOutput(dir, args...)
}

func (m *OutputCommandMock) Install() {
	m.InstallOn(cli.TestingGetDefaultDeps())
}

func (m *OutputCommandMock) InstallOn(deps *cli.Deps) {
	deps.Git = &compositeGitRunner{run: deps.Git, outputMock: m}
	m.t.Cleanup(func() { m.Verify() })
}

// --- Workspace/git helpers ---

func SetupTestEnv(t *testing.T, vars map[string]string) { testutil.SetupTestEnv(t, vars) }
func SetupMockAgentInvokerOn(t *testing.T, deps *cli.Deps, returnErr error) *clitest.MockAgentInvoker {
	t.Helper()
	recorder := &clitest.MockAgentInvoker{InteractiveErr: returnErr}
	deps.Agent = recorder
	return recorder
}

func SetupMockClaudeInvoker(t *testing.T, returnErr error) *clitest.MockAgentInvoker {
	t.Helper()
	recorder := &clitest.MockAgentInvoker{InteractiveErr: returnErr}
	dd := cli.TestingGetDefaultDeps()
	orig := dd.Agent
	dd.Agent = recorder
	t.Cleanup(func() { dd.Agent = orig })
	return recorder
}

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
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
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
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), data, 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOOM_CONFIG_DIR", configDir)
}

func setupLockWorkspaceConfig(t *testing.T, cfg *config.LoomConfig) {
	setupWorkspaceConfig(t, cfg)
}

func workspaceHash(name string) string {
	return cli.WorkspaceHash(name)
}

func resetIntegrationBranchCache() { cli.TestingResetIntegrationBranchCache() }
