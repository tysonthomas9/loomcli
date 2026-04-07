package automode

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// --- Type aliases ---

type RoleConfig = config.RoleConfig
type AgentEntry = config.AgentEntry
type WorkspaceConfig = config.WorkspaceConfig
type CommandResult = cli.CommandResult
type LockInfo = cli.LockInfo
type ClaudeBackend = backends.ClaudeBackend
type CodexBackend = backends.CodexBackend
type MockExecRunner = clitest.MockExecRunner

const (
	LockFileName = cli.LockFileName
	StateActive  = cli.StateActive
	StateIdle    = cli.StateIdle
)

var (
	AcquireLock         = cli.AcquireLock
	ReleaseLock         = cli.ReleaseLock
	UpdateLockTask      = cli.UpdateLockTask
	ClearLockTaskID     = cli.ClearLockTaskID
	ReadLockFile        = cli.ReadLockFile
	GetSignalFilePath   = cli.GetSignalFilePath
	HasUnclosedBlockers = cli.HasUnclosedBlockers
	RegisterBackend     = cli.RegisterBackend
	SetBackend          = cli.SetBackend
)

func generateTestPlanPrompt(agentName string) string {
	return "test-plan-prompt-for-" + agentName
}

func generateTestTaskPrompt(agentName string) string {
	return "test-task-prompt-for-" + agentName
}

func NewMockIssueBackend() *clitest.MockIssueBackend { return clitest.NewMockIssueBackend() }

var resetDefaultIssueBackend = cli.ResetDefaultIssueBackend
var setDefaultIssueBackend = cli.SetDefaultIssueBackend

func resetBackendState(t *testing.T) {
	t.Helper()
	cli.TestingResetBackendState(t)
}

func installExecMock(t *testing.T, m *clitest.MockExecRunner) {
	t.Helper()
	dd := cli.TestingGetDefaultDeps()
	orig := dd.Exec
	dd.Exec = m
	t.Cleanup(func() { dd.Exec = orig })
}

func installClaudeNonInteractiveMock(t *testing.T, fn func(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error) {
	t.Helper()
	dd := cli.TestingGetDefaultDeps()
	orig := dd.Agent
	dd.Agent = &clitest.MockAgentInvoker{NonInteractiveFunc: fn}
	t.Cleanup(func() { dd.Agent = orig })
}

func installCodexNonInteractiveMock(t *testing.T, fn func(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error) {
	t.Helper()
	dd := cli.TestingGetDefaultDeps()
	orig := dd.Agent
	dd.Agent = &clitest.MockAgentInvoker{NonInteractiveFunc: fn}
	t.Cleanup(func() { dd.Agent = orig })
}
