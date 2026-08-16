package lead

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"

	leadcontrol "github.com/tysonthomas9/loomcli/internal/infra/interactionlead"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

type testLeadSessionRuntime struct {
	store *memstore.Store
}

var _ leadcontrol.SessionRuntime = (*testLeadSessionRuntime)(nil)

func (runtime *testLeadSessionRuntime) HeartbeatSession(
	ctx context.Context,
	command interaction.HeartbeatSessionCommand,
) error {
	_, err := runtime.store.AgentSessions().Heartbeat(ctx, command.WorkspaceKey, command.SessionID)
	return err
}

func (runtime *testLeadSessionRuntime) PatchSessionRuntimeContext(
	ctx context.Context,
	command interaction.PatchSessionCommand,
) error {
	session, err := runtime.store.AgentSessions().Get(ctx, command.WorkspaceKey, command.SessionID)
	if err != nil {
		return err
	}
	metadata := make(map[string]string, len(session.Metadata)+len(command.MetadataUpserts))
	for key, value := range session.Metadata {
		metadata[key] = value
	}
	for key, value := range command.MetadataUpserts {
		metadata[key] = value
	}
	for _, key := range command.MetadataRemovals {
		delete(metadata, key)
	}
	_, err = runtime.store.AgentSessions().Update(
		ctx,
		command.WorkspaceKey,
		command.SessionID,
		interaction.AgentSessionUpdate{Phase: command.Phase, Metadata: &metadata},
	)
	return err
}

func (runtime *testLeadSessionRuntime) PublishTranscript(
	ctx context.Context,
	command interaction.PublishTranscriptCommand,
) error {
	artifactID := "transcript-" + command.SessionID
	return runtime.PatchSessionRuntimeContext(ctx, interaction.PatchSessionCommand{
		WorkspaceKey: command.WorkspaceKey, SessionID: command.SessionID,
		TranscriptArtifactID: &artifactID,
	})
}

func (runtime *testLeadSessionRuntime) FinishSession(
	ctx context.Context,
	command interaction.FinishSessionCommand,
) error {
	status := interaction.SessionRecordStatus(command.Status)
	_, err := runtime.store.AgentSessions().Update(ctx, command.WorkspaceKey, command.SessionID, interaction.AgentSessionUpdate{
		Status: &status,
	})
	return err
}

func (*testLeadSessionRuntime) ClaimNextInbox(
	context.Context,
	interaction.ClaimInboxCommand,
) (*interaction.InboxMessage, error) {
	return nil, persistence.ErrNotFound
}

func (*testLeadSessionRuntime) CompleteInbox(
	context.Context,
	interaction.CompleteInboxCommand,
) error {
	return nil
}

func (*testLeadSessionRuntime) Close() error { return nil }
