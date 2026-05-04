package monitor

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
)

type CommandResult = cli.CommandResult
type LockInfo = cli.LockInfo
type MockExecRunner = clitest.MockExecRunner
type MockIssueBackend = clitest.MockIssueBackend
type MockGitRunner = clitest.MockGitRunner
type MockFileSystem = clitest.MockFileSystem
type ExecBridgeGitRunner = clitest.ExecBridgeGitRunner

var ResetWorkspaceRuntimeDirCache = cli.ResetWorkspaceRuntimeDirCache
var resetDefaultIssueBackend = cli.ResetDefaultIssueBackend
var setDefaultIssueBackend = cli.SetDefaultIssueBackend

func NewTestDeps(t *testing.T) (*cli.Deps, *clitest.MockGitRunner, *clitest.MockExecRunner, *clitest.MockFileSystem, *clitest.MockIssueBackend) {
	return clitest.NewTestDeps(t)
}

func NewMockIssueBackend() *clitest.MockIssueBackend { return clitest.NewMockIssueBackend() }

func installExecMock(t *testing.T, m *clitest.MockExecRunner) {
	t.Helper()
	dd := cli.TestingGetDefaultDeps()
	orig := dd.Exec
	dd.Exec = m
	t.Cleanup(func() { dd.Exec = orig })
}

// defaultResolver is the package-level Resolver for backward-compat tests.
var defaultResolver *cli.Resolver

// collectMonitorData delegates to CollectMonitorData.
func collectMonitorData(readyLimit int, branch string) *MonitorData {
	return CollectMonitorData(readyLimit, branch)
}

// execBridgeGitRunner wraps clitest.ExecBridgeGitRunner for lowercase compat.
type execBridgeGitRunner = clitest.ExecBridgeGitRunner

// getWorktreeGitSyncStatus is a lowercase alias for GetWorktreeGitSyncStatus.
var getWorktreeGitSyncStatus = GetWorktreeGitSyncStatus

var collectReadyTasksByPriority = CollectReadyTasksByPriority
var displayWidth = DisplayWidth

// --- Command stubs for monitor tests ---

type CommandStub struct {
	Dir    string
	Name   string
	Args   []string
	Stdout string
	Stderr string
	Err    error
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

func (m *CommandMock) Verify() {
	if m.idx != len(m.stubs) {
		m.t.Errorf("expected %d command calls, got %d", len(m.stubs), m.idx)
	}
}

func (m *CommandMock) InstallOn(deps *cli.Deps) {
	deps.Exec = m
	deps.Git = &clitest.ExecBridgeGitRunner{Exec: m}
	m.t.Cleanup(func() { m.Verify() })
}

var truncateToWidth = TruncateToWidth
var padRight = PadRight
var centerText = CenterText
var renderBoxTop = RenderBoxTop
var renderBoxBottom = RenderBoxBottom
var renderBoxSeparator = RenderBoxSeparator
var renderBoxLine = RenderBoxLine
