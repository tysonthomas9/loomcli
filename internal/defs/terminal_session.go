package defs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type TerminalSessionModule struct {
	TerminalID      string                       `json:"terminal_id"`
	SourcePath      string                       `json:"source_path"`
	SourceHash      string                       `json:"source_hash"`
	Version         string                       `json:"version"`
	AgentID         string                       `json:"agent_id,omitempty"`
	SessionID       string                       `json:"session_id,omitempty"`
	NodeID          string                       `json:"node_id,omitempty"`
	TaskID          string                       `json:"task_id,omitempty"`
	Title           string                       `json:"title,omitempty"`
	Kind            string                       `json:"kind,omitempty"`
	Status          domain.TerminalSessionStatus `json:"status,omitempty"`
	PTYProvider     string                       `json:"pty_provider,omitempty"`
	StreamRef       string                       `json:"stream_ref,omitempty"`
	TranscriptRef   string                       `json:"transcript_ref,omitempty"`
	AttachedClients int                          `json:"attached_clients,omitempty"`
	LastSeenAt      *time.Time                   `json:"last_seen_at,omitempty"`
	EndedAt         *time.Time                   `json:"ended_at,omitempty"`
	Metadata        map[string]string            `json:"metadata,omitempty"`
}

func applyTerminalSessions(ctx context.Context, st store.Store, ws string, terminals []TerminalSessionModule) error {
	if len(terminals) == 0 {
		return nil
	}
	if st.TerminalSessions() == nil {
		return fmt.Errorf("terminal session store not configured")
	}
	for _, terminal := range terminals {
		if err := applyTerminalSession(ctx, st, ws, terminal); err != nil {
			return err
		}
	}
	return nil
}

func applyTerminalSession(ctx context.Context, st store.Store, ws string, terminal TerminalSessionModule) error {
	if terminal.TerminalID != "" {
		existing, err := st.TerminalSessions().Get(ctx, ws, terminal.TerminalID)
		if err == nil {
			return syncTerminalSessionState(ctx, st, ws, existing.TerminalID, terminal)
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("get terminal session %s: %w", terminal.TerminalID, err)
		}
	}
	created, err := st.TerminalSessions().Create(ctx, store.TerminalSessionCreate{
		WorkspaceKey:    ws,
		TerminalID:      terminal.TerminalID,
		AgentID:         terminal.AgentID,
		SessionID:       terminal.SessionID,
		NodeID:          terminal.NodeID,
		TaskID:          terminal.TaskID,
		Title:           terminal.Title,
		Kind:            terminal.Kind,
		Status:          terminalSessionStatusOrOpen(terminal.Status),
		PTYProvider:     terminal.PTYProvider,
		StreamRef:       terminal.StreamRef,
		TranscriptRef:   terminal.TranscriptRef,
		AttachedClients: terminal.AttachedClients,
		Metadata:        cloneStringMap(terminal.Metadata),
	})
	if err == nil {
		return syncTerminalSessionState(ctx, st, ws, created.TerminalID, terminal)
	}
	if !errors.Is(err, domain.ErrAlreadyExists) {
		return fmt.Errorf("create terminal session %s: %w", terminal.TerminalID, err)
	}
	existing, getErr := st.TerminalSessions().Get(ctx, ws, terminal.TerminalID)
	if getErr != nil {
		return fmt.Errorf("get existing terminal session %s after create conflict: %w", terminal.TerminalID, getErr)
	}
	return syncTerminalSessionState(ctx, st, ws, existing.TerminalID, terminal)
}

func syncTerminalSessionState(ctx context.Context, st store.Store, ws, terminalID string, terminal TerminalSessionModule) error {
	agentID := terminal.AgentID
	sessionID := terminal.SessionID
	nodeID := terminal.NodeID
	taskID := terminal.TaskID
	title := terminal.Title
	kind := terminal.Kind
	status := terminalSessionStatusOrOpen(terminal.Status)
	ptyProvider := terminal.PTYProvider
	streamRef := terminal.StreamRef
	transcriptRef := terminal.TranscriptRef
	attachedClients := terminal.AttachedClients
	endedAt := cloneWorkflowRunTime(terminal.EndedAt)
	metadata := cloneStringMap(terminal.Metadata)
	patch := store.TerminalSessionUpdate{
		AgentID:         &agentID,
		SessionID:       &sessionID,
		NodeID:          &nodeID,
		TaskID:          &taskID,
		Title:           &title,
		Kind:            &kind,
		Status:          &status,
		PTYProvider:     &ptyProvider,
		StreamRef:       &streamRef,
		TranscriptRef:   &transcriptRef,
		AttachedClients: &attachedClients,
		EndedAt:         &endedAt,
		Metadata:        &metadata,
	}
	if terminal.LastSeenAt != nil {
		lastSeenAt := *terminal.LastSeenAt
		patch.LastSeenAt = &lastSeenAt
	}
	if _, err := st.TerminalSessions().Update(ctx, ws, terminalID, patch); err != nil {
		return fmt.Errorf("update terminal session %s: %w", terminalID, err)
	}
	return nil
}

func terminalSessionStatusOrOpen(status domain.TerminalSessionStatus) domain.TerminalSessionStatus {
	if status == "" {
		return domain.TerminalSessionOpen
	}
	return status
}
