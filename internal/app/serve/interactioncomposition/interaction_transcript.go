package interactioncomposition

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// interactionTranscriptArtifactStore is the composition-owned bridge from
// Interaction's session-scoped transcript port to the durable Artifacts
// content lifecycle. Interaction never receives the composite Store or a
// generic FleetDB client.
type interactionTranscriptArtifactStore struct {
	artifacts store.ArtifactStore
}

var _ interaction.TranscriptArtifactStore = (*interactionTranscriptArtifactStore)(nil)

func newInteractionTranscriptArtifactStore(
	artifacts store.ArtifactStore,
) *interactionTranscriptArtifactStore {
	if artifacts == nil {
		return nil
	}
	return &interactionTranscriptArtifactStore{artifacts: artifacts}
}

func (adapter *interactionTranscriptArtifactStore) CreateContent(
	ctx context.Context,
	command interaction.TranscriptArtifactCreate,
) (string, error) {
	if adapter == nil || adapter.artifacts == nil {
		return "", interaction.ErrUnavailable
	}
	digestBytes := sha256.Sum256(command.Content)
	contentHash := "sha256:" + hex.EncodeToString(digestBytes[:])
	existing, err := adapter.artifacts.Get(ctx, command.WorkspaceKey, command.ArtifactID)
	if err == nil {
		if existing == nil || existing.WorkspaceKey != command.WorkspaceKey ||
			existing.OwnerType != "session" || existing.OwnerID != command.SessionID ||
			existing.SessionID != command.SessionID || existing.Type != "transcript" ||
			existing.DurableStatus != "finalized" || existing.ContentHash != contentHash {
			return "", fmt.Errorf("session transcript retry differs from the finalized artifact: %w", interaction.ErrConflict)
		}
		return existing.ArtifactID, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return "", fmt.Errorf("inspect existing session transcript: %w", err)
	}
	artifact, err := store.UploadContentArtifact(ctx, adapter.artifacts, store.ArtifactCreate{
		WorkspaceKey:  command.WorkspaceKey,
		ArtifactID:    command.ArtifactID,
		AgentID:       command.AgentID,
		SessionID:     command.SessionID,
		TaskID:        command.TaskID,
		OwnerType:     "session",
		OwnerID:       command.SessionID,
		Type:          "transcript",
		Summary:       "interactive session transcript",
		MIMEType:      "application/x-ndjson",
		ContentHash:   contentHash,
		DurableStatus: "declared",
		Metadata:      cloneInteractionMap(command.Metadata),
	}, command.Content)
	if err != nil {
		return "", fmt.Errorf("create session transcript content: %w", err)
	}
	if artifact == nil {
		return "", fmt.Errorf("create session transcript content returned no artifact: %w", interaction.ErrUnavailable)
	}
	if artifact.ContentHash != contentHash {
		return "", fmt.Errorf("session transcript content differs from the finalized artifact: %w", interaction.ErrConflict)
	}
	return artifact.ArtifactID, nil
}
