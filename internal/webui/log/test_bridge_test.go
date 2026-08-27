package log

import (
	"context"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// module is a local interface matching webui.Module for compile-time assertions.
type module interface {
	Register(mux *http.ServeMux)
}

// LogModule → Module (struct alias)
type LogModule = Module

// NewLogModule → NewModule
var NewLogModule = NewModule

// ---------------------------------------------------------------------------
// Lowercase aliases for exported functions (tests use pre-refactor names)
// ---------------------------------------------------------------------------

var fileExists = FileExists
var getWorkspaceLogDir = GetWorkspaceLogDir
var getAgentLogPath = GetAgentLogPath
var getTaskLogPath = GetTaskLogPath
var getTaskLogDir = GetTaskLogDir
var listTaskPhases = ListTaskPhases

// mockAgentService implements service.AgentService with no-op defaults for module tests.
type mockAgentService struct {
	getTerminalInfoFunc       func(ctx context.Context, wsID, agentName string) (*service.AgentTerminalInfoResult, error)
	generateTerminalTokenFunc func(ctx context.Context, wsID, agentName, userID string) (string, error)
	getLogFunc                func(ctx context.Context, wsID, agentName string, lines int, beforeLine int64) (*service.AgentLogResult, error)
	getDiffStatFunc           func(ctx context.Context, wsID, agentName string) (*service.AgentDiffStatResult, error)
	gitPullFunc               func(ctx context.Context, wsID, agentName, source string) (*ops.GitPullResult, error)
	createPRFunc              func(ctx context.Context, wsID, agentName, target string) (*ops.GitPRResult, error)
	gitResetFunc              func(ctx context.Context, wsID, agentName, branch string, force, push bool) (*ops.GitResetResult, error)
	gitStatusFunc             func(ctx context.Context, wsID, agentName string) (*ops.GitStatusResult, error)
	setTargetBranchFunc       func(ctx context.Context, wsID, agentName, branch string) error
}

func (m *mockAgentService) GetTerminalInfo(ctx context.Context, wsID, agentName string) (*service.AgentTerminalInfoResult, error) {
	if m.getTerminalInfoFunc != nil {
		return m.getTerminalInfoFunc(ctx, wsID, agentName)
	}
	return &service.AgentTerminalInfoResult{Agent: agentName, Mode: "archive"}, nil
}
func (m *mockAgentService) GenerateTerminalToken(ctx context.Context, wsID, agentName, userID string) (string, error) {
	if m.generateTerminalTokenFunc != nil {
		return m.generateTerminalTokenFunc(ctx, wsID, agentName, userID)
	}
	return "test-token", nil
}
func (m *mockAgentService) GetLog(ctx context.Context, wsID, agentName string, lines int, beforeLine int64) (*service.AgentLogResult, error) {
	if m.getLogFunc != nil {
		return m.getLogFunc(ctx, wsID, agentName, lines, beforeLine)
	}
	return &service.AgentLogResult{Lines: []string{}, LineCount: 0, StartLine: 1}, nil
}
func (m *mockAgentService) GetDiffStat(ctx context.Context, wsID, agentName string) (*service.AgentDiffStatResult, error) {
	return &service.AgentDiffStatResult{}, nil
}
func (m *mockAgentService) GitPull(ctx context.Context, wsID, agentName, source string) (*ops.GitPullResult, error) {
	return &ops.GitPullResult{Success: true, Message: "pulled"}, nil
}
func (m *mockAgentService) CreatePR(ctx context.Context, wsID, agentName, target string) (*ops.GitPRResult, error) {
	return &ops.GitPRResult{}, nil
}

func (m *mockAgentService) ListPullRequests(context.Context, string, string) (*ops.GitPullRequestList, error) {
	return &ops.GitPullRequestList{PullRequests: []ops.GitPullRequest{}}, nil
}

func (m *mockAgentService) GitReset(ctx context.Context, wsID, agentName, branch string, force, push bool) (*ops.GitResetResult, error) {
	return &ops.GitResetResult{}, nil
}
func (m *mockAgentService) GitStatus(ctx context.Context, wsID, agentName string) (*ops.GitStatusResult, error) {
	return &ops.GitStatusResult{}, nil
}
func (m *mockAgentService) SetTargetBranch(ctx context.Context, wsID, agentName, branch string) error {
	return nil
}
func (m *mockAgentService) ListAgents(_ context.Context, _ string) ([]*domain.Agent, error) {
	return nil, nil
}
func (m *mockAgentService) CreateAgent(_ context.Context, _ service.AgentCreateInput) (*domain.Agent, error) {
	return nil, nil
}
func (m *mockAgentService) UpdateAgent(_ context.Context, _, _ string, _ service.AgentUpdateInput) (*domain.Agent, error) {
	return nil, nil
}
func (m *mockAgentService) RequestAgentLifecycle(_ context.Context, _, _ string, _ service.AgentLifecycleInput) (*domain.Agent, error) {
	return nil, nil
}
func (m *mockAgentService) DeleteAgent(_ context.Context, _, _ string) error { return nil }
