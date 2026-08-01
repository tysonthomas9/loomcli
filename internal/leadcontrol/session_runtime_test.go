package leadcontrol

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// storeBackedSessionRuntime is a legacy-store test double only. Production
// child code receives the owner-fenced HTTP client.
type storeBackedSessionRuntime struct {
	store store.Store
}

func testSessionRuntime(st store.Store) SessionRuntime {
	return &storeBackedSessionRuntime{store: st}
}

func (runtime *storeBackedSessionRuntime) Enqueue(
	ctx context.Context,
	command interaction.EnqueueInboxCommand,
) (*interaction.InboxMessage, error) {
	message, err := runtime.store.AgentInboxMessages().Create(ctx, store.AgentInboxMessageCreate{
		WorkspaceKey: command.WorkspaceKey, TargetAgentID: command.TargetAgentID,
		SessionID: command.SessionID, Body: command.Body,
		SourceKind: command.SourceKind, SourceRef: command.SourceRef,
		DriverRunID: command.DriverRunID, TaskRunID: command.TaskRunID,
		TriggerEventID: command.TriggerEventID, TriggerDeliveryID: command.TriggerDeliveryID,
		DedupeKey: command.DedupeKey,
	})
	if err != nil {
		return nil, err
	}
	return interactionInboxMessageForTest(message), nil
}

func (runtime *storeBackedSessionRuntime) HeartbeatSession(
	ctx context.Context,
	command interaction.HeartbeatSessionCommand,
) error {
	_, err := runtime.store.AgentSessions().Heartbeat(ctx, command.WorkspaceKey, command.SessionID)
	return err
}

func (runtime *storeBackedSessionRuntime) PatchSessionRuntimeContext(
	ctx context.Context,
	command interaction.PatchSessionCommand,
) error {
	session, err := runtime.store.AgentSessions().Get(ctx, command.WorkspaceKey, command.SessionID)
	if err != nil {
		return err
	}
	metadata := cloneMetadata(session.Metadata)
	for key, value := range command.MetadataUpserts {
		metadata[key] = value
	}
	for _, key := range command.MetadataRemovals {
		delete(metadata, key)
	}
	if command.TranscriptArtifactID != nil {
		metadata["transcript_ref"] = "artifact://" + *command.TranscriptArtifactID
	}
	_, err = runtime.store.AgentSessions().Update(
		ctx,
		command.WorkspaceKey,
		command.SessionID,
		store.AgentSessionUpdate{Phase: command.Phase, Metadata: &metadata},
	)
	return err
}

func (runtime *storeBackedSessionRuntime) PublishTranscript(
	ctx context.Context,
	command interaction.PublishTranscriptCommand,
) error {
	session, err := runtime.store.AgentSessions().Get(ctx, command.WorkspaceKey, command.SessionID)
	if err != nil {
		return err
	}
	artifactID := "transcript-" + command.SessionID
	if _, err := store.UploadContentArtifact(ctx, runtime.store.Artifacts(), store.ArtifactCreate{
		WorkspaceKey: command.WorkspaceKey, ArtifactID: artifactID,
		AgentID: session.AgentID, SessionID: session.SessionID, TaskID: session.TaskID,
		OwnerType: "session", OwnerID: session.SessionID, Type: "transcript",
		MIMEType: "application/x-ndjson", DurableStatus: "declared",
		Metadata: command.Metadata,
	}, command.Content); err != nil {
		return err
	}
	return runtime.PatchSessionRuntimeContext(ctx, interaction.PatchSessionCommand{
		WorkspaceKey: command.WorkspaceKey, SessionID: command.SessionID,
		TranscriptArtifactID: &artifactID,
	})
}

func (runtime *storeBackedSessionRuntime) FinishSession(
	ctx context.Context,
	command interaction.FinishSessionCommand,
) error {
	status := domain.AgentSessionStatus(command.Status)
	_, err := runtime.store.AgentSessions().Update(ctx, command.WorkspaceKey, command.SessionID, store.AgentSessionUpdate{
		Status:     &status,
		Summary:    &command.Summary,
		ErrorClass: &command.ErrorClass,
		ExitCode:   &command.ExitCode,
	})
	return err
}

func (runtime *storeBackedSessionRuntime) ClaimNextInbox(
	ctx context.Context,
	command interaction.ClaimInboxCommand,
) (*interaction.InboxMessage, error) {
	message, err := runtime.store.AgentInboxMessages().ClaimNext(ctx, store.AgentInboxMessageClaim{
		WorkspaceKey:  command.WorkspaceKey,
		TargetAgentID: command.AgentID,
		SessionID:     command.SessionID,
		ClaimedBy:     "test:" + command.SessionID,
		LeaseTTL:      command.LeaseTTL,
	})
	if err != nil {
		return nil, err
	}
	return interactionInboxMessageForTest(message), nil
}

func interactionInboxMessageForTest(message *domain.AgentInboxMessage) *interaction.InboxMessage {
	if message == nil {
		return nil
	}
	return &interaction.InboxMessage{
		WorkspaceKey:      message.WorkspaceKey,
		MessageID:         message.InboxMessageID,
		Cursor:            message.Cursor,
		TargetAgentID:     message.TargetAgentID,
		SessionID:         message.SessionID,
		Body:              message.Body,
		Status:            interaction.InboxStatus(message.Status),
		SourceKind:        message.SourceKind,
		SourceRef:         message.SourceRef,
		DriverRunID:       message.DriverRunID,
		TaskRunID:         message.TaskRunID,
		TriggerEventID:    message.TriggerEventID,
		TriggerDeliveryID: message.TriggerDeliveryID,
		DedupeKey:         message.DedupeKey,
		Attempt:           message.Attempt,
		ClaimedBy:         message.ClaimedBy,
		ClaimExpiresAt:    message.ClaimExpiresAt,
		ErrorClass:        message.ErrorClass,
		DeliveredThreadID: message.DeliveredThreadID,
		DeliveredAt:       message.DeliveredAt,
		CreatedAt:         message.CreatedAt,
		UpdatedAt:         message.UpdatedAt,
	}
}

func (runtime *storeBackedSessionRuntime) CompleteInbox(
	ctx context.Context,
	command interaction.CompleteInboxCommand,
) error {
	outcome := "retry"
	if command.Status == interaction.InboxDelivered {
		outcome = "delivered"
	} else if command.Status == interaction.InboxFailed {
		outcome = "failed"
	}
	_, err := runtime.store.AgentInboxMessages().Complete(
		ctx,
		command.WorkspaceKey,
		command.MessageID,
		store.AgentInboxMessageComplete{
			Outcome:           outcome,
			DeliveredThreadID: command.DeliveredThreadID,
			ErrorClass:        command.ErrorClass,
		},
	)
	return err
}

func (*storeBackedSessionRuntime) Close() error { return nil }

func TestRegisteredSessionMutationFailsClosedWithoutRuntime(t *testing.T) {
	err := UpdateCodexRuntimeMetadata(
		t.Context(),
		nil,
		"WS",
		"session-1",
		CodexRuntimeMetadata{Status: RuntimeStatusActive},
	)
	if !errors.Is(err, ErrSessionRuntimeUnavailable) {
		t.Fatalf("UpdateCodexRuntimeMetadata error = %v, want unavailable", err)
	}
}
