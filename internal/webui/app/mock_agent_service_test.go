package app

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/agentcoord"
)

// mockAgentService implements AgentService for handler-level testing.
type mockAgentService struct {
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

func (m *mockAgentService) GetTerminalInfo(ctx context.Context, wsID, agentName string) (*agentcoord.AgentTerminalInfoResult, error) {
	if m.getTerminalInfoFunc != nil {
		return m.getTerminalInfoFunc(ctx, wsID, agentName)
	}
	return &agentcoord.AgentTerminalInfoResult{Agent: agentName, Mode: "archive"}, nil
}

func (m *mockAgentService) GenerateTerminalToken(ctx context.Context, wsID, agentName, userID string) (string, error) {
	if m.generateTerminalTokenFunc != nil {
		return m.generateTerminalTokenFunc(ctx, wsID, agentName, userID)
	}
	return "test-token", nil
}

func (m *mockAgentService) GetLog(ctx context.Context, wsID, agentName string, lines int, beforeLine int64) (*agentcoord.AgentLogResult, error) {
	if m.getLogFunc != nil {
		return m.getLogFunc(ctx, wsID, agentName, lines, beforeLine)
	}
	return &agentcoord.AgentLogResult{Lines: []string{}, LineCount: 0, StartLine: 1}, nil
}

func (m *mockAgentService) GetDiffStat(ctx context.Context, wsID, agentName string) (*agentcoord.AgentDiffStatResult, error) {
	if m.getDiffStatFunc != nil {
		return m.getDiffStatFunc(ctx, wsID, agentName)
	}
	return &agentcoord.AgentDiffStatResult{}, nil
}

func (m *mockAgentService) GitPush(ctx context.Context, wsID, agentName, target string) (*ops.GitPushResult, error) {
	if m.gitPushFunc != nil {
		return m.gitPushFunc(ctx, wsID, agentName, target)
	}
	return &ops.GitPushResult{Success: true, Message: "pushed"}, nil
}

func (m *mockAgentService) GitPushAll(ctx context.Context, wsID string) (*agentcoord.GitPushAllResult, error) {
	if m.gitPushAllFunc != nil {
		return m.gitPushAllFunc(ctx, wsID)
	}
	return &agentcoord.GitPushAllResult{}, nil
}

func (m *mockAgentService) GitPull(ctx context.Context, wsID, agentName, source string) (*ops.GitPullResult, error) {
	if m.gitPullFunc != nil {
		return m.gitPullFunc(ctx, wsID, agentName, source)
	}
	return &ops.GitPullResult{Success: true, Message: "pulled"}, nil
}

func (m *mockAgentService) GitSync(ctx context.Context, wsID, agentName string) (*agentcoord.GitSyncResult, error) {
	if m.gitSyncFunc != nil {
		return m.gitSyncFunc(ctx, wsID, agentName)
	}
	return &agentcoord.GitSyncResult{
		PushResult: &ops.GitPushResult{Success: true},
		PullResult: &ops.GitPullResult{Success: true},
	}, nil
}

func (m *mockAgentService) CreatePR(ctx context.Context, wsID, agentName, target string) (*ops.GitPRResult, error) {
	if m.createPRFunc != nil {
		return m.createPRFunc(ctx, wsID, agentName, target)
	}
	return &ops.GitPRResult{URL: "https://github.com/test/pr/1", Created: true}, nil
}

func (m *mockAgentService) ListPullRequests(context.Context, string, string) (*ops.GitPullRequestList, error) {
	return &ops.GitPullRequestList{PullRequests: []ops.GitPullRequest{}}, nil
}

func (m *mockAgentService) GitReset(ctx context.Context, wsID, agentName, branch string, force, push bool) (*ops.GitResetResult, error) {
	if m.gitResetFunc != nil {
		return m.gitResetFunc(ctx, wsID, agentName, branch, force, push)
	}
	return &ops.GitResetResult{Success: true, Message: "reset done"}, nil
}

func (m *mockAgentService) GitStatus(ctx context.Context, wsID, agentName string) (*ops.GitStatusResult, error) {
	if m.gitStatusFunc != nil {
		return m.gitStatusFunc(ctx, wsID, agentName)
	}
	return &ops.GitStatusResult{Branch: "feature", TargetBranch: "main", IsClean: true}, nil
}

func (m *mockAgentService) SetTargetBranch(ctx context.Context, wsID, agentName, branch string) error {
	if m.setTargetBranchFunc != nil {
		return m.setTargetBranchFunc(ctx, wsID, agentName, branch)
	}
	return nil
}
