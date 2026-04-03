package webui

import "context"

// mockAgentService implements AgentService for handler-level testing.
type mockAgentService struct {
	getTerminalInfoFunc       func(ctx context.Context, wsID, agentName string) (*AgentTerminalInfoResult, error)
	generateTerminalTokenFunc func(ctx context.Context, agentName, userID string) (string, error)
	getLogFunc                func(ctx context.Context, wsID, agentName string, lines int, beforeLine int64) (*AgentLogResult, error)
	getDiffStatFunc           func(ctx context.Context, wsID, agentName string) (*AgentDiffStatResult, error)
	gitPushFunc               func(ctx context.Context, wsID, agentName, target string) (*GitPushResult, error)
	gitPushAllFunc            func(ctx context.Context, wsID string) (*GitPushAllResult, error)
	gitPullFunc               func(ctx context.Context, wsID, agentName, source string) (*GitPullResult, error)
	gitSyncFunc               func(ctx context.Context, wsID, agentName string) (*GitSyncResult, error)
	createPRFunc              func(ctx context.Context, wsID, agentName, target string) (*GitPRResult, error)
	gitResetFunc              func(ctx context.Context, wsID, agentName, branch string, force, push bool) (*GitResetResult, error)
	gitStatusFunc             func(ctx context.Context, wsID, agentName string) (*GitStatusResult, error)
	setTargetBranchFunc       func(ctx context.Context, wsID, agentName, branch string) error
}

func (m *mockAgentService) GetTerminalInfo(ctx context.Context, wsID, agentName string) (*AgentTerminalInfoResult, error) {
	if m.getTerminalInfoFunc != nil {
		return m.getTerminalInfoFunc(ctx, wsID, agentName)
	}
	return &AgentTerminalInfoResult{Agent: agentName, Mode: "archive"}, nil
}

func (m *mockAgentService) GenerateTerminalToken(ctx context.Context, agentName, userID string) (string, error) {
	if m.generateTerminalTokenFunc != nil {
		return m.generateTerminalTokenFunc(ctx, agentName, userID)
	}
	return "test-token", nil
}

func (m *mockAgentService) GetLog(ctx context.Context, wsID, agentName string, lines int, beforeLine int64) (*AgentLogResult, error) {
	if m.getLogFunc != nil {
		return m.getLogFunc(ctx, wsID, agentName, lines, beforeLine)
	}
	return &AgentLogResult{Lines: []string{}, LineCount: 0, StartLine: 1}, nil
}

func (m *mockAgentService) GetDiffStat(ctx context.Context, wsID, agentName string) (*AgentDiffStatResult, error) {
	if m.getDiffStatFunc != nil {
		return m.getDiffStatFunc(ctx, wsID, agentName)
	}
	return &AgentDiffStatResult{}, nil
}

func (m *mockAgentService) GitPush(ctx context.Context, wsID, agentName, target string) (*GitPushResult, error) {
	if m.gitPushFunc != nil {
		return m.gitPushFunc(ctx, wsID, agentName, target)
	}
	return &GitPushResult{Success: true, Message: "pushed"}, nil
}

func (m *mockAgentService) GitPushAll(ctx context.Context, wsID string) (*GitPushAllResult, error) {
	if m.gitPushAllFunc != nil {
		return m.gitPushAllFunc(ctx, wsID)
	}
	return &GitPushAllResult{}, nil
}

func (m *mockAgentService) GitPull(ctx context.Context, wsID, agentName, source string) (*GitPullResult, error) {
	if m.gitPullFunc != nil {
		return m.gitPullFunc(ctx, wsID, agentName, source)
	}
	return &GitPullResult{Success: true, Message: "pulled"}, nil
}

func (m *mockAgentService) GitSync(ctx context.Context, wsID, agentName string) (*GitSyncResult, error) {
	if m.gitSyncFunc != nil {
		return m.gitSyncFunc(ctx, wsID, agentName)
	}
	return &GitSyncResult{
		PushResult: &GitPushResult{Success: true},
		PullResult: &GitPullResult{Success: true},
	}, nil
}

func (m *mockAgentService) CreatePR(ctx context.Context, wsID, agentName, target string) (*GitPRResult, error) {
	if m.createPRFunc != nil {
		return m.createPRFunc(ctx, wsID, agentName, target)
	}
	return &GitPRResult{URL: "https://github.com/test/pr/1", Created: true}, nil
}

func (m *mockAgentService) GitReset(ctx context.Context, wsID, agentName, branch string, force, push bool) (*GitResetResult, error) {
	if m.gitResetFunc != nil {
		return m.gitResetFunc(ctx, wsID, agentName, branch, force, push)
	}
	return &GitResetResult{Success: true, Message: "reset done"}, nil
}

func (m *mockAgentService) GitStatus(ctx context.Context, wsID, agentName string) (*GitStatusResult, error) {
	if m.gitStatusFunc != nil {
		return m.gitStatusFunc(ctx, wsID, agentName)
	}
	return &GitStatusResult{Branch: "feature", TargetBranch: "main", IsClean: true}, nil
}

func (m *mockAgentService) SetTargetBranch(ctx context.Context, wsID, agentName, branch string) error {
	if m.setTargetBranchFunc != nil {
		return m.setTargetBranchFunc(ctx, wsID, agentName, branch)
	}
	return nil
}
