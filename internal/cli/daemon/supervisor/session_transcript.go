package supervisor

import (
	"context"
	"log/slog"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// localTranscriptFormat reports how the session's on-disk transcript is
// encoded, so a reader on another host knows how to parse the artifact. The Go
// leaf mirrors the provider's raw stream and the TS leaf writes canonical
// events; the local metadata is the only place that distinction is recorded.
// Returns "" when it cannot be read: guessing canonical over a raw rollout
// yields events that parse without error and carry nothing.
func (s *Supervisor) localTranscriptFormat(sessionID string) string {
	local, err := sessions.NewStore(cli.GetWorkspaceRuntimeDir())
	if err != nil {
		return ""
	}
	meta, loadErr := local.LoadMetadata(sessionID)
	if loadErr != nil || meta == nil {
		return ""
	}
	return meta.TranscriptFormat
}

// uploadTranscriptArtifact stores the leaf transcript as a control-plane artifact.
func (s *Supervisor) uploadTranscriptArtifact(ctx context.Context, sessionID, taskID, backend, transcriptFormat string, data []byte) string {
	if s.ControlStore == nil {
		return ""
	}
	metadata := map[string]string{"runtime": "daemon-leaf", "backend": backend}
	if transcriptFormat != "" {
		metadata["transcript_format"] = transcriptFormat
		metadata["transcript_backend"] = backend
	}
	finalized, err := store.UploadContentArtifact(ctx, s.ControlStore.Artifacts(), store.ArtifactCreate{
		WorkspaceKey: s.WorkspaceID, ArtifactID: "transcript-" + sessionID, SessionID: sessionID,
		TaskID: taskID, OwnerType: "session", OwnerID: sessionID, Type: "transcript",
		Summary: "agent session transcript", MIMEType: "application/x-ndjson", DurableStatus: "declared", Metadata: metadata,
	}, data)
	if err != nil {
		slog.Warn("daemon transcript artifact upload failed", "session_id", sessionID, "err", err)
		return ""
	}
	return "artifact://" + finalized.ArtifactID
}
