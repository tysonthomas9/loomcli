package defs

import (
	"context"
	"errors"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type ArtifactModule struct {
	ArtifactID string            `json:"artifact_id"`
	SourcePath string            `json:"source_path"`
	SourceHash string            `json:"source_hash"`
	Version    string            `json:"version"`
	AgentID    string            `json:"agent_id,omitempty"`
	SessionID  string            `json:"session_id,omitempty"`
	TerminalID string            `json:"terminal_id,omitempty"`
	TaskID     string            `json:"task_id,omitempty"`
	Type       string            `json:"type"`
	URI        string            `json:"uri"`
	Summary    string            `json:"summary,omitempty"`
	MIMEType   string            `json:"mime_type,omitempty"`
	SizeBytes  int64             `json:"size_bytes,omitempty"`
	Checksum   string            `json:"checksum,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

func applyArtifacts(ctx context.Context, st store.Store, ws string, artifacts []ArtifactModule) error {
	if len(artifacts) == 0 {
		return nil
	}
	if st.Artifacts() == nil {
		return fmt.Errorf("artifact store not configured")
	}
	for _, artifact := range artifacts {
		if err := applyArtifact(ctx, st, ws, artifact); err != nil {
			return err
		}
	}
	return nil
}

func applyArtifact(ctx context.Context, st store.Store, ws string, artifact ArtifactModule) error {
	created, err := st.Artifacts().Create(ctx, store.ArtifactCreate{
		WorkspaceKey: ws,
		ArtifactID:   artifact.ArtifactID,
		AgentID:      artifact.AgentID,
		SessionID:    artifact.SessionID,
		TerminalID:   artifact.TerminalID,
		TaskID:       artifact.TaskID,
		Type:         artifact.Type,
		URI:          artifact.URI,
		Summary:      artifact.Summary,
		MIMEType:     artifact.MIMEType,
		SizeBytes:    artifact.SizeBytes,
		Checksum:     artifact.Checksum,
		Metadata:     cloneStringMap(artifact.Metadata),
	})
	if err == nil {
		return syncArtifactState(ctx, st, ws, created.ArtifactID, artifact)
	}
	if !errors.Is(err, domain.ErrAlreadyExists) {
		return fmt.Errorf("create artifact %s: %w", artifact.ArtifactID, err)
	}
	return syncArtifactState(ctx, st, ws, artifact.ArtifactID, artifact)
}

func syncArtifactState(ctx context.Context, st store.Store, ws, artifactID string, artifact ArtifactModule) error {
	agentID := artifact.AgentID
	sessionID := artifact.SessionID
	terminalID := artifact.TerminalID
	taskID := artifact.TaskID
	artifactType := artifact.Type
	uri := artifact.URI
	summary := artifact.Summary
	mimeType := artifact.MIMEType
	sizeBytes := artifact.SizeBytes
	checksum := artifact.Checksum
	metadata := cloneStringMap(artifact.Metadata)
	patch := store.ArtifactUpdate{
		AgentID:    &agentID,
		SessionID:  &sessionID,
		TerminalID: &terminalID,
		TaskID:     &taskID,
		Type:       &artifactType,
		URI:        &uri,
		Summary:    &summary,
		MIMEType:   &mimeType,
		SizeBytes:  &sizeBytes,
		Checksum:   &checksum,
		Metadata:   &metadata,
	}
	if _, err := st.Artifacts().Update(ctx, ws, artifactID, patch); err != nil {
		return fmt.Errorf("update artifact %s: %w", artifactID, err)
	}
	return nil
}
