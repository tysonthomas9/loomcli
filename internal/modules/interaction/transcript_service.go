package interaction

import (
	"context"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// PublishTranscript persists canonical transcript bytes through Interaction's
// narrow Artifacts port, then links the deterministic artifact to the exact
// still-live session generation. Authority is resolved before the upload and
// the final PatchOwned revalidates the lease and fence, so a stale child cannot
// attach evidence to a successor session.
func (service *Service) PublishTranscript(
	ctx context.Context,
	auth authority.SessionAuthority,
	command PublishTranscriptCommand,
) (*AgentSession, error) {
	command = normalizeTranscriptPublish(command)
	if err := service.requireSession(
		ctx,
		ActionPublishTranscript,
		command.WorkspaceKey,
		command.SessionID,
		"",
		auth,
	); err != nil {
		return nil, err
	}
	if err := validateTranscriptPublish(command); err != nil {
		return nil, err
	}
	session, err := service.sessions.Get(ctx, command.WorkspaceKey, command.SessionID)
	if err != nil {
		return nil, fmt.Errorf("load transcript AgentSession: %w", err)
	}
	if err := validateSession(session, command.WorkspaceKey, command.SessionID, auth.AgentID()); err != nil {
		return nil, err
	}
	return service.persistOwnedTranscript(ctx, auth, command, session)
}

func normalizeTranscriptPublish(command PublishTranscriptCommand) PublishTranscriptCommand {
	command.WorkspaceKey = strings.TrimSpace(command.WorkspaceKey)
	command.SessionID = strings.TrimSpace(command.SessionID)
	command.Content = append([]byte(nil), command.Content...)
	command.Metadata = cloneMetadata(command.Metadata)
	command.FailureClass = strings.TrimSpace(command.FailureClass)
	return command
}

func (service *Service) persistOwnedTranscript(
	ctx context.Context,
	auth authority.SessionAuthority,
	command PublishTranscriptCommand,
	session *AgentSession,
) (*AgentSession, error) {
	artifactID := "transcript-" + command.SessionID
	if command.FailureClass != "" {
		return service.persistTranscriptFailure(ctx, auth, command, session, artifactID)
	}
	persistedID, err := service.transcripts.CreateContent(ctx, auth, TranscriptArtifactCreate{
		WorkspaceKey: command.WorkspaceKey,
		ArtifactID:   artifactID,
		AgentID:      session.AgentID,
		SessionID:    session.SessionID,
		TaskID:       session.TaskID,
		Content:      command.Content,
		Metadata:     command.Metadata,
	})
	if err != nil {
		return nil, fmt.Errorf("publish session transcript artifact: %w", err)
	}
	if strings.TrimSpace(persistedID) != artifactID {
		return nil, fmt.Errorf("transcript artifact store returned mismatched identity: %w", ErrInvalidPersistedState)
	}
	return service.linkOwnedTranscript(ctx, auth, command, artifactID)
}

func (service *Service) persistTranscriptFailure(
	ctx context.Context,
	auth authority.SessionAuthority,
	command PublishTranscriptCommand,
	session *AgentSession,
	artifactID string,
) (*AgentSession, error) {
	if err := service.transcripts.RecordFailure(ctx, auth, TranscriptArtifactFailure{
		WorkspaceKey: command.WorkspaceKey,
		ArtifactID:   artifactID,
		AgentID:      session.AgentID,
		SessionID:    session.SessionID,
		TaskID:       session.TaskID,
		FailureClass: command.FailureClass,
	}); err != nil {
		return nil, fmt.Errorf("record session transcript capture failure: %w", err)
	}
	return cloneSession(session), nil
}

func (service *Service) linkOwnedTranscript(
	ctx context.Context,
	auth authority.SessionAuthority,
	command PublishTranscriptCommand,
	artifactID string,
) (*AgentSession, error) {
	now := service.now()
	updated, lease, err := service.sessions.PatchOwned(
		ctx,
		command.WorkspaceKey,
		sessionOwner(auth),
		SessionPatch{TranscriptArtifactID: &artifactID, At: now},
	)
	if err != nil {
		return nil, fmt.Errorf("link owned AgentSession transcript: %w", err)
	}
	if err := validateSession(updated, command.WorkspaceKey, command.SessionID, auth.AgentID()); err != nil {
		return nil, err
	}
	if err := validateOwnedLease(lease, command.WorkspaceKey, sessionOwner(auth), now, true); err != nil {
		return nil, err
	}
	if updated.TranscriptArtifactID != artifactID {
		return nil, fmt.Errorf("session store did not link transcript artifact: %w", ErrInvalidPersistedState)
	}
	return cloneSession(updated), nil
}

func validateTranscriptPublish(command PublishTranscriptCommand) error {
	if command.WorkspaceKey == "" || command.SessionID == "" {
		return fmt.Errorf("canonical transcript workspace and session are required: %w", ErrInvalid)
	}
	hasContent := len(command.Content) > 0
	hasFailure := command.FailureClass != ""
	if hasContent == hasFailure {
		return fmt.Errorf("exactly one transcript content or failure class is required: %w", ErrInvalid)
	}
	if len(command.Content) > maxSessionTranscriptBytes {
		return fmt.Errorf("canonical transcript must contain 1..%d bytes: %w", maxSessionTranscriptBytes, ErrInvalid)
	}
	if len(command.FailureClass) > 128 || (hasFailure && len(command.Metadata) != 0) {
		return fmt.Errorf("transcript failure class must be bounded and cannot carry metadata: %w", ErrInvalid)
	}
	if len(command.Metadata) > maxSessionPatchMetadataItems {
		return fmt.Errorf("transcript metadata exceeds %d entries: %w", maxSessionPatchMetadataItems, ErrInvalid)
	}
	for key, value := range command.Metadata {
		if !validSessionPatchMetadata(key, value) {
			return fmt.Errorf("invalid transcript metadata %q: %w", key, ErrInvalid)
		}
	}
	return nil
}
