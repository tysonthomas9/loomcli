package interaction

import (
	"context"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
)

func (s *TerminalTabService) AgentTerminalInfo(
	ctx context.Context,
	workspaceKey, agentID string,
) (*AgentTerminalInfo, error) {
	workspaceKey = strings.TrimSpace(workspaceKey)
	agentID = strings.TrimSpace(agentID)
	if workspaceKey == "" || !agents.ValidAgentIdentifier(agentID) {
		return nil, terminalError(ErrInvalid, "valid workspace and agent are required", nil)
	}
	if s == nil || s.agentTerminal.LiveView == nil {
		return nil, terminalError(ErrUnavailable, "agent terminal runtime is unavailable", nil)
	}
	sessionName, found, err := s.agentTerminal.LiveView.FindLatestAgentSession(
		workspaceKey, agentID,
	)
	if err != nil {
		return nil, terminalError(ErrUnavailable, "failed to inspect agent terminal sessions", err)
	}
	return &AgentTerminalInfo{AgentID: agentID, Live: found, SessionName: sessionName}, nil
}

func (s *TerminalTabService) AttachAgentTerminal(
	ctx context.Context,
	command AttachAgentTerminalCommand,
) (*AgentTerminalAttachResult, error) {
	info, err := s.AgentTerminalInfo(ctx, command.WorkspaceKey, command.AgentID)
	if err != nil {
		return nil, err
	}
	if !info.Live || strings.TrimSpace(info.SessionName) == "" {
		return nil, terminalError(ErrNotFound, "no active terminal session for agent", nil)
	}
	runtime := s.agentTerminal.LiveView
	if runtime.SessionCount() >= runtime.MaxSessions() {
		return nil, terminalError(ErrTerminalCapacity, "maximum terminal sessions reached", nil)
	}
	connection, err := runtime.Attach(info.SessionName, command.Columns, command.Rows)
	if err != nil {
		return nil, err
	}
	return &AgentTerminalAttachResult{
		Connection: connection,
		Monitor:    agentTerminalRuntimeMonitor{runtime: runtime},
	}, nil
}

func (s *TerminalTabService) DetachAgentTerminal(
	_ context.Context,
	attachmentID string,
) error {
	if s == nil || s.agentTerminal.LiveView == nil {
		return terminalError(ErrUnavailable, "agent terminal runtime is unavailable", nil)
	}
	if err := s.agentTerminal.LiveView.Detach(strings.TrimSpace(attachmentID)); err != nil {
		return terminalError(ErrUnavailable, "failed to detach agent terminal", err)
	}
	return nil
}

type agentTerminalRuntimeMonitor struct{ runtime AgentTerminalRuntime }

func (monitor agentTerminalRuntimeMonitor) HasSession(name string) bool {
	return monitor.runtime.HasSession(name)
}
func (monitor agentTerminalRuntimeMonitor) PaneDead(name string) bool {
	return monitor.runtime.PaneDead(name)
}
func (monitor agentTerminalRuntimeMonitor) CapturePane(name string, lines int) string {
	return monitor.runtime.CapturePane(name, lines)
}
