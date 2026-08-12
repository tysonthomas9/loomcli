package automode

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/testdata/clitest"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// --- Type aliases ---

type RoleConfig = config.RoleConfig
type AgentEntry = config.AgentEntry
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
	AcquireLock       = cli.AcquireLock
	ReleaseLock       = cli.ReleaseLock
	UpdateLockTask    = cli.UpdateLockTask
	ClearLockTaskID   = cli.ClearLockTaskID
	ReadLockFile      = cli.ReadLockFile
	GetSignalFilePath = cli.GetSignalFilePath
	RegisterBackend   = cli.RegisterBackend
	SetBackend        = cli.SetBackend
)

func generateTestPlanPrompt(agentName string) string {
	return "test-plan-prompt-for-" + agentName
}

func generateTestTaskPrompt(agentName string) string {
	return "test-task-prompt-for-" + agentName
}

func NewMockWorkItems() *clitest.MockWorkItems { return clitest.NewMockWorkItems() }

var resetDefaultWorkItems = cli.ResetDefaultWorkItems
var setDefaultWorkItems = cli.SetDefaultWorkItems

func resetBackendState(t *testing.T) {
	t.Helper()
	cli.TestingResetBackendState(t)
}

func installExecMock(t *testing.T, m *clitest.MockExecRunner) {
	t.Helper()
	dd := cli.TestingGetDefaultDeps()
	orig := dd.Exec
	origWorkItems := dd.WorkItems
	dd.Exec = m
	dd.WorkItems = newExecReadyWorkItems(m)
	cli.ResetDefaultWorkItems()
	t.Cleanup(func() {
		dd.Exec = orig
		dd.WorkItems = origWorkItems
		cli.ResetDefaultWorkItems()
	})
}

type execReadyWorkItems struct {
	*clitest.MockWorkItems
	run func(dir, name string, args ...string) cli.CommandResult
}

func newExecReadyWorkItems(m *clitest.MockExecRunner) *execReadyWorkItems {
	return &execReadyWorkItems{
		MockWorkItems: clitest.NewMockWorkItems(),
		run:           m.Run,
	}
}

func (b *execReadyWorkItems) Ready(ctx context.Context, opts workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
	b.MockWorkItems.Ready(ctx, opts)
	result := b.run(cli.GetWorkspaceRuntimeDir(), "issue-store", readyArgs(opts)...)
	if result.Err != nil {
		return nil, result.Err
	}
	return parseReadyIssues(result.Stdout)
}

func readyArgs(opts workitems.AvailabilityQuery) []string {
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

func parseReadyIssues(stdout string) ([]workitems.IssueSummary, error) {
	type issueWire struct {
		workitems.IssueSummary
		Type string `json:"type,omitempty"`
	}
	var wire []issueWire
	if err := json.Unmarshal([]byte(stdout), &wire); err != nil {
		return nil, err
	}
	issues := make([]workitems.IssueSummary, len(wire))
	for i, item := range wire {
		issues[i] = item.IssueSummary
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
