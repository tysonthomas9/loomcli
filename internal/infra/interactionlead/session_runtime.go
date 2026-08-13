package leadcontrol

import (
	"context"
	"errors"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

var ErrSessionRuntimeUnavailable = errors.New("session-owned Interaction runtime is unavailable")

// SessionRuntime is the complete mutation authority available to one
// interactive child. Implementations are expected to use the SessionEnvelope
// credential and must not expose a generic Store or FleetDB transport.
type SessionRuntime interface {
	HeartbeatSession(context.Context, interaction.HeartbeatSessionCommand) error
	PatchSessionRuntimeContext(context.Context, interaction.PatchSessionCommand) error
	PublishTranscript(context.Context, interaction.PublishTranscriptCommand) error
	FinishSession(context.Context, interaction.FinishSessionCommand) error
	ClaimNextInbox(context.Context, interaction.ClaimInboxCommand) (*interaction.InboxMessage, error)
	CompleteInbox(context.Context, interaction.CompleteInboxCommand) error
	Close() error
}

func requireSessionRuntime(runtime SessionRuntime, workspace, sessionID string) error {
	if runtime != nil {
		return nil
	}
	if strings.TrimSpace(workspace) == "" && strings.TrimSpace(sessionID) == "" {
		return nil
	}
	return ErrSessionRuntimeUnavailable
}

func persistTranscriptCaptureFailure(
	ctx context.Context,
	runtime SessionRuntime,
	workspace, sessionID string,
	cause error,
) error {
	if cause == nil {
		return nil
	}
	workspace = strings.TrimSpace(workspace)
	sessionID = strings.TrimSpace(sessionID)
	if workspace == "" || sessionID == "" {
		return cause
	}
	if runtime == nil {
		return errors.Join(cause, ErrSessionRuntimeUnavailable)
	}
	recordErr := runtime.PublishTranscript(ctx, interaction.PublishTranscriptCommand{
		WorkspaceKey: workspace,
		SessionID:    sessionID,
		FailureClass: transcriptCaptureFailureClass(cause),
	})
	return errors.Join(cause, recordErr)
}

func transcriptCaptureFailureClass(cause error) string {
	switch {
	case errors.Is(cause, context.Canceled), errors.Is(cause, context.DeadlineExceeded):
		return artifacts.EvidenceFailureInterrupted
	case errors.Is(cause, artifacts.ErrEvidenceCorrupt):
		return artifacts.EvidenceFailureCorrupt
	case errors.Is(cause, artifacts.ErrInvalid):
		return artifacts.EvidenceFailureRejected
	case errors.Is(cause, artifacts.ErrUnavailable), errors.Is(cause, artifacts.ErrContentUnavailable):
		return artifacts.EvidenceFailureUnavailable
	default:
		return artifacts.EvidenceFailureUnavailable
	}
}
