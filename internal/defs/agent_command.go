package defs

import (
	"context"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type AgentCommandModule struct {
	CommandID     string                    `json:"command_id"`
	SourcePath    string                    `json:"source_path"`
	SourceHash    string                    `json:"source_hash"`
	Version       string                    `json:"version"`
	Cursor        int64                     `json:"cursor,omitempty"`
	TargetAgentID string                    `json:"target_agent_id,omitempty"`
	TargetNodeID  string                    `json:"target_node_id,omitempty"`
	SessionID     string                    `json:"session_id,omitempty"`
	Type          string                    `json:"type"`
	Payload       map[string]string         `json:"payload,omitempty"`
	Status        domain.AgentCommandStatus `json:"status,omitempty"`
	Result        string                    `json:"result,omitempty"`
	ErrorClass    string                    `json:"error_class,omitempty"`
}

func applyAgentCommands(ctx context.Context, st store.Store, ws string, commands []AgentCommandModule) error {
	if len(commands) == 0 {
		return nil
	}
	if st.AgentCommands() == nil {
		return fmt.Errorf("agent command store not configured")
	}
	for _, command := range commands {
		if err := applyAgentCommand(ctx, st, ws, command); err != nil {
			return err
		}
	}
	return nil
}

func applyAgentCommand(ctx context.Context, st store.Store, ws string, command AgentCommandModule) error {
	created, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  ws,
		CommandID:     command.CommandID,
		TargetAgentID: command.TargetAgentID,
		TargetNodeID:  command.TargetNodeID,
		SessionID:     command.SessionID,
		Type:          command.Type,
		Payload:       cloneStringMap(command.Payload),
	})
	if err != nil {
		return fmt.Errorf("create agent command %s: %w", command.CommandID, err)
	}
	return syncAgentCommandState(ctx, st, ws, created.CommandID, command)
}

func syncAgentCommandState(ctx context.Context, st store.Store, ws, commandID string, command AgentCommandModule) error {
	status := agentCommandStatusOrQueued(command.Status)
	if status == domain.AgentCommandQueued && command.Result == "" && command.ErrorClass == "" {
		return nil
	}
	if _, err := st.AgentCommands().Complete(ctx, ws, commandID, store.AgentCommandComplete{
		Status:     status,
		Result:     command.Result,
		ErrorClass: command.ErrorClass,
	}); err != nil {
		return fmt.Errorf("update agent command %s: %w", commandID, err)
	}
	return nil
}

func agentCommandStatusOrQueued(status domain.AgentCommandStatus) domain.AgentCommandStatus {
	if status == "" {
		return domain.AgentCommandQueued
	}
	return status
}
