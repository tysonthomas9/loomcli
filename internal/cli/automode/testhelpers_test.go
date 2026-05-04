package automode

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
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

type execReadyIssueBackend struct {
	*clitest.MockIssueBackend
	run func(dir, name string, args ...string) cli.CommandResult
}

func newExecReadyIssueBackend(m *clitest.MockExecRunner) *execReadyIssueBackend {
	return &execReadyIssueBackend{
		MockIssueBackend: clitest.NewMockIssueBackend(),
		run:              m.Run,
	}
}

func (b *execReadyIssueBackend) Ready(ctx context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error) {
	b.MockIssueBackend.Ready(ctx, opts)
	result := b.run(cli.GetWorkspaceRuntimeDir(), "issue-store", readyArgs(opts)...)
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
