package app

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/agentcoord"
)

// mockAgentService implements agentcoord.AgentService with no-op defaults for module tests.
type logModuleMockAgentService struct {
	getTerminalInfoFunc       func(ctx context.Context, wsID, agentName string) (*agentcoord.AgentTerminalInfoResult, error)
	generateTerminalTokenFunc func(ctx context.Context, wsID, agentName, userID string) (string, error)
	getLogFunc                func(ctx context.Context, wsID, agentName string, lines int, beforeLine int64) (*agentcoord.AgentLogResult, error)
	getDiffStatFunc           func(ctx context.Context, wsID, agentName string) (*agentcoord.AgentDiffStatResult, error)
	gitPushFunc               func(ctx context.Context, wsID, agentName, target string) (*ops.GitPushResult, error)
	gitPushAllFunc            func(ctx context.Context, wsID string) (*agentcoord.GitPushAllResult, error)
	gitPullFunc               func(ctx context.Context, wsID, agentName, source string) (*ops.GitPullResult, error)
	gitSyncFunc               func(ctx context.Context, wsID, agentName string) (*agentcoord.GitSyncResult, error)
	createPRFunc              func(ctx context.Context, wsID, agentName, target string) (*ops.GitPRResult, error)
	gitResetFunc              func(ctx context.Context, wsID, agentName, branch string, force, push bool) (*ops.GitResetResult, error)
	gitStatusFunc             func(ctx context.Context, wsID, agentName string) (*ops.GitStatusResult, error)
	setTargetBranchFunc       func(ctx context.Context, wsID, agentName, branch string) error
}

func (m *logModuleMockAgentService) GetTerminalInfo(ctx context.Context, wsID, agentName string) (*agentcoord.AgentTerminalInfoResult, error) {
	if m.getTerminalInfoFunc != nil {
		return m.getTerminalInfoFunc(ctx, wsID, agentName)
	}
	return &agentcoord.AgentTerminalInfoResult{Agent: agentName, Mode: "archive"}, nil
}
func (m *logModuleMockAgentService) GenerateTerminalToken(ctx context.Context, wsID, agentName, userID string) (string, error) {
	if m.generateTerminalTokenFunc != nil {
		return m.generateTerminalTokenFunc(ctx, wsID, agentName, userID)
	}
	return "test-token", nil
}
func (m *logModuleMockAgentService) GetLog(ctx context.Context, wsID, agentName string, lines int, beforeLine int64) (*agentcoord.AgentLogResult, error) {
	if m.getLogFunc != nil {
		return m.getLogFunc(ctx, wsID, agentName, lines, beforeLine)
	}
	return &agentcoord.AgentLogResult{Lines: []string{}, LineCount: 0, StartLine: 1}, nil
}
func (m *logModuleMockAgentService) GetDiffStat(ctx context.Context, wsID, agentName string) (*agentcoord.AgentDiffStatResult, error) {
	return &agentcoord.AgentDiffStatResult{}, nil
}
func (m *logModuleMockAgentService) GitPush(ctx context.Context, wsID, agentName, target string) (*ops.GitPushResult, error) {
	return &ops.GitPushResult{Success: true, Message: "pushed"}, nil
}
func (m *logModuleMockAgentService) GitPushAll(ctx context.Context, wsID string) (*agentcoord.GitPushAllResult, error) {
	return &agentcoord.GitPushAllResult{}, nil
}
func (m *logModuleMockAgentService) GitPull(ctx context.Context, wsID, agentName, source string) (*ops.GitPullResult, error) {
	return &ops.GitPullResult{Success: true, Message: "pulled"}, nil
}
func (m *logModuleMockAgentService) GitSync(ctx context.Context, wsID, agentName string) (*agentcoord.GitSyncResult, error) {
	return &agentcoord.GitSyncResult{}, nil
}
func (m *logModuleMockAgentService) CreatePR(ctx context.Context, wsID, agentName, target string) (*ops.GitPRResult, error) {
	return &ops.GitPRResult{}, nil
}

func (m *logModuleMockAgentService) ListPullRequests(context.Context, string, string) (*ops.GitPullRequestList, error) {
	return &ops.GitPullRequestList{PullRequests: []ops.GitPullRequest{}}, nil
}

func (m *logModuleMockAgentService) GitReset(ctx context.Context, wsID, agentName, branch string, force, push bool) (*ops.GitResetResult, error) {
	return &ops.GitResetResult{}, nil
}
func (m *logModuleMockAgentService) GitStatus(ctx context.Context, wsID, agentName string) (*ops.GitStatusResult, error) {
	return &ops.GitStatusResult{}, nil
}
func (m *logModuleMockAgentService) SetTargetBranch(ctx context.Context, wsID, agentName, branch string) error {
	return nil
}
