package terminal

import (
	"context"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// WithTerminalNotifications decorates Interaction's terminal owner with the
// WebUI delivery events consumed by connected browser clients. Interaction
// stays independent of SSE while every mutation path, including agent-session
// creation, shares one notification policy.
func WithTerminalNotifications(svc interaction.TerminalTabs, hub *realtime.Hub) interaction.TerminalTabs {
	if svc == nil || hub == nil {
		return svc
	}
	return &notifyingTerminalTabs{TerminalTabs: svc, hub: hub}
}

type notifyingTerminalTabs struct {
	interaction.TerminalTabs
	hub *realtime.Hub
}

func (s *notifyingTerminalTabs) PatchTab(
	ctx context.Context,
	workspace,
	session string,
	fields map[string]string,
) (*interaction.PatchTabResult, error) {
	result, err := s.TerminalTabs.PatchTab(ctx, workspace, session, fields)
	if err != nil {
		return nil, err
	}
	s.broadcastMetadata(workspace, session)
	if result != nil && result.IssueIDChanged {
		s.broadcastSessionChange(workspace, session)
	}
	return result, nil
}

func (s *notifyingTerminalTabs) PutTab(
	ctx context.Context,
	command interaction.PutTerminalTabCommand,
) (*interaction.TabMetadata, error) {
	meta, err := s.TerminalTabs.PutTab(ctx, command)
	if err != nil {
		return nil, err
	}
	if meta != nil {
		s.broadcastMetadata(command.WorkspaceKey, meta.SessionName)
	}
	return meta, nil
}

func (s *notifyingTerminalTabs) DeleteTab(ctx context.Context, workspace, session string) error {
	if err := s.TerminalTabs.DeleteTab(ctx, workspace, session); err != nil {
		return err
	}
	s.broadcastMetadata(workspace, session)
	return nil
}

func (s *notifyingTerminalTabs) StartSetup(
	ctx context.Context,
	workspace string,
	request interaction.TerminalSetupRequest,
) (*interaction.TerminalSetupResult, error) {
	result, err := s.TerminalTabs.StartSetup(ctx, workspace, request)
	if err != nil {
		return nil, err
	}
	if result != nil {
		s.broadcastMetadata(workspace, result.SessionName)
	}
	return result, nil
}

func (s *notifyingTerminalTabs) broadcastMetadata(workspace, session string) {
	s.hub.Broadcast(terminalMutation("terminal_metadata", "terminal.metadata", workspace, session))
}

func (s *notifyingTerminalTabs) broadcastSessionChange(workspace, session string) {
	s.hub.Broadcast(terminalMutation("terminal_session_change", "terminal.session_change", workspace, session))
}

func terminalMutation(eventType, action, workspace, session string) *realtime.MutationPayload {
	return &realtime.MutationPayload{
		Type:        eventType,
		EntityType:  "terminal",
		EntityID:    session,
		Action:      action,
		Timestamp:   time.Now().UTC().Format(time.RFC3339Nano),
		WorkspaceID: workspace,
	}
}
