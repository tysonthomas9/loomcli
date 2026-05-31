package defs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type AgentSessionModule struct {
	SessionID       string                    `json:"session_id"`
	AgentID         string                    `json:"agent_id"`
	SourcePath      string                    `json:"source_path"`
	SourceHash      string                    `json:"source_hash"`
	Version         string                    `json:"version"`
	NodeID          string                    `json:"node_id,omitempty"`
	Kind            domain.AgentSessionKind   `json:"kind,omitempty"`
	TaskID          string                    `json:"task_id,omitempty"`
	TerminalID      string                    `json:"terminal_id,omitempty"`
	ParentSessionID string                    `json:"parent_session_id,omitempty"`
	Status          domain.AgentSessionStatus `json:"status,omitempty"`
	Phase           string                    `json:"phase,omitempty"`
	Attempt         int                       `json:"attempt,omitempty"`
	LastHeartbeat   *time.Time                `json:"last_heartbeat,omitempty"`
	FinishedAt      *time.Time                `json:"finished_at,omitempty"`
	Summary         string                    `json:"summary,omitempty"`
	ErrorClass      string                    `json:"error_class,omitempty"`
	ExitCode        *int                      `json:"exit_code,omitempty"`
	Metadata        map[string]string         `json:"metadata,omitempty"`
}

func applyAgentSessions(ctx context.Context, st store.Store, ws string, sessions []AgentSessionModule) error {
	if len(sessions) == 0 {
		return nil
	}
	if st.AgentSessions() == nil {
		return fmt.Errorf("agent session store not configured")
	}
	for _, session := range sessions {
		if err := applyAgentSession(ctx, st, ws, session); err != nil {
			return err
		}
	}
	return nil
}

func applyAgentSession(ctx context.Context, st store.Store, ws string, session AgentSessionModule) error {
	if session.SessionID != "" {
		existing, err := st.AgentSessions().Get(ctx, ws, session.SessionID)
		if err == nil {
			return syncAgentSessionState(ctx, st, ws, existing.SessionID, session)
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("get agent session %s: %w", session.SessionID, err)
		}
	}
	created, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey:    ws,
		SessionID:       session.SessionID,
		AgentID:         session.AgentID,
		NodeID:          session.NodeID,
		Kind:            agentSessionKindOrTask(session.Kind),
		TaskID:          session.TaskID,
		TerminalID:      session.TerminalID,
		ParentSessionID: session.ParentSessionID,
		Status:          agentSessionStatusOrRunning(session.Status),
		Phase:           session.Phase,
		Attempt:         session.Attempt,
		Metadata:        cloneStringMap(session.Metadata),
	})
	if err == nil {
		return syncAgentSessionState(ctx, st, ws, created.SessionID, session)
	}
	if !errors.Is(err, domain.ErrAlreadyExists) {
		return fmt.Errorf("create agent session %s: %w", session.SessionID, err)
	}
	existing, getErr := st.AgentSessions().Get(ctx, ws, session.SessionID)
	if getErr != nil {
		return fmt.Errorf("get existing agent session %s after create conflict: %w", session.SessionID, getErr)
	}
	return syncAgentSessionState(ctx, st, ws, existing.SessionID, session)
}

func syncAgentSessionState(ctx context.Context, st store.Store, ws, sessionID string, session AgentSessionModule) error {
	nodeID := session.NodeID
	taskID := session.TaskID
	status := agentSessionStatusOrRunning(session.Status)
	phase := session.Phase
	finishedAt := cloneWorkflowRunTime(session.FinishedAt)
	summary := session.Summary
	errorClass := session.ErrorClass
	exitCode := cloneInt(session.ExitCode)
	metadata := cloneStringMap(session.Metadata)
	patch := store.AgentSessionUpdate{
		NodeID:     &nodeID,
		TaskID:     &taskID,
		Status:     &status,
		Phase:      &phase,
		FinishedAt: &finishedAt,
		Summary:    &summary,
		ErrorClass: &errorClass,
		ExitCode:   &exitCode,
		Metadata:   &metadata,
	}
	if session.LastHeartbeat != nil {
		lastHeartbeat := *session.LastHeartbeat
		patch.LastHeartbeat = &lastHeartbeat
	}
	if _, err := st.AgentSessions().Update(ctx, ws, sessionID, patch); err != nil {
		return fmt.Errorf("update agent session %s: %w", sessionID, err)
	}
	return nil
}

func agentSessionKindOrTask(kind domain.AgentSessionKind) domain.AgentSessionKind {
	if kind == "" {
		return domain.AgentSessionKindTask
	}
	return kind
}

func agentSessionStatusOrRunning(status domain.AgentSessionStatus) domain.AgentSessionStatus {
	if status == "" {
		return domain.AgentSessionRunning
	}
	return status
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
