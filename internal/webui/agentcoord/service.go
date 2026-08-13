package agentcoord

import (
	"context"

	webuilog "github.com/tysonthomas9/loomcli/internal/logstore"
	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

var _ AgentService = (*agentServiceImpl)(nil)

// agentServiceImpl is the remaining delivery service for agent terminal and
// local log observation. Source Control owns every checkout and Git operation.
type agentServiceImpl struct {
	termMgr  *terminal.AgentTmuxManager
	termAuth *realtime.TerminalAuth
}

func NewAgentService(termMgr *terminal.AgentTmuxManager, termAuth *realtime.TerminalAuth) AgentService {
	return &agentServiceImpl{termMgr: termMgr, termAuth: termAuth}
}

func agentLogTokenScope(agentName string) string { return "agent:" + agentName + ":logs" }

func (service *agentServiceImpl) GetTerminalInfo(_ context.Context, workspace, agent string) (*AgentTerminalInfoResult, error) {
	if err := ValidateAgentName(agent); err != nil {
		return nil, err
	}
	if service.termMgr == nil {
		return nil, apperrors.ErrUnavailable("terminal manager not initialized")
	}
	mode := AgentTerminalModeArchive
	if _, found, err := service.termMgr.FindLatestAgentSession(workspace, agent); err != nil {
		logger.Error("failed to resolve agent tmux session", "agent", agent, "err", err)
		return nil, apperrors.ErrInternal("failed to inspect terminal sessions", err)
	} else if found {
		mode = AgentTerminalModeTmux
	}
	return &AgentTerminalInfoResult{Agent: agent, Mode: mode}, nil
}

func (service *agentServiceImpl) GenerateTerminalToken(_ context.Context, workspace, agent, user string) (string, error) {
	if err := ValidateAgentName(agent); err != nil {
		return "", err
	}
	if service.termAuth == nil {
		return "", apperrors.ErrUnavailable("terminal authentication not initialized")
	}
	token, err := service.termAuth.GenerateToken(agentLogTokenScope(agent), workspace, user)
	if err != nil {
		logger.Error("failed to generate agent terminal token", "agent", agent, "workspace", workspace, "err", err)
		return "", apperrors.ErrInternal("failed to generate token", err)
	}
	return token, nil
}

func (service *agentServiceImpl) GetLog(_ context.Context, workspace, agent string, lines int, beforeLine int64) (*AgentLogResult, error) {
	if err := ValidateAgentName(agent); err != nil {
		return nil, err
	}
	if lines <= 0 {
		lines = webuilog.LogReadDefaultLines
	}
	if lines > webuilog.LogReadMaxLines {
		lines = webuilog.LogReadMaxLines
	}
	logPath, err := webuilog.GetAgentLogPath(workspace, agent)
	if err != nil {
		logger.Error("agent log path error", "agent", agent, "err", err)
		return nil, apperrors.ErrInternal("failed to resolve log path", err)
	}
	if !webuilog.FileExists(logPath) {
		return nil, apperrors.ErrNotFound("log file not found - agent may not be active")
	}
	content, startLine, err := webuilog.ReadFileLastLines(logPath, lines, beforeLine)
	if err != nil {
		logger.Error("failed to read agent log", "agent", agent, "err", err)
		return nil, apperrors.ErrInternal("failed to read log file", err)
	}
	return &AgentLogResult{
		Lines: content, LineCount: startLine + int64(len(content)) - 1, StartLine: startLine,
	}, nil
}
