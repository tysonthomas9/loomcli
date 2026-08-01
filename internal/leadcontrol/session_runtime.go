package leadcontrol

import (
	"context"
	"errors"
	"strings"

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
