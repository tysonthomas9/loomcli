package capabilitycomposition

import (
	"context"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// InteractionPTYTabStore is the server-owned tab identity view needed to
// converge a process-owned PTY termination into the canonical Interaction
// lifecycle.
type InteractionPTYTabStore interface {
	Get(context.Context, string, string) (*tabmeta.TabMetadata, error)
}

// InteractionForceInterrupter returns the exact interrupt port published by an
// Interaction capability, or nil when that capability is unavailable.
func InteractionForceInterrupter(capability interface {
	ForceInterrupter() interaction.ForceInterrupter
}) interaction.ForceInterrupter {
	if capability == nil {
		return nil
	}
	return capability.ForceInterrupter()
}

// NewInteractionPTYBeforeKill binds every process-owned PTY termination to the
// canonical Interaction lifecycle recorded in server-owned tab metadata.
// Ordinary shell tabs have no canonical IDs and pass through unchanged.
func NewInteractionPTYBeforeKill(
	tabs InteractionPTYTabStore,
	interrupter interaction.ForceInterrupter,
) terminal.BeforeKillFunc {
	if tabs == nil {
		return nil
	}
	return func(
		ctx context.Context,
		key terminal.SessionKey,
		reason string,
	) error {
		return interruptInteractionPTYBeforeKill(ctx, key, reason, tabs, interrupter)
	}
}

func interruptInteractionPTYBeforeKill(
	ctx context.Context,
	key terminal.SessionKey,
	reason string,
	tabs InteractionPTYTabStore,
	interrupter interaction.ForceInterrupter,
) error {
	meta, err := tabs.Get(ctx, key.Workspace, key.Name)
	if err != nil {
		return fmt.Errorf("load terminal lifecycle identity: %w", err)
	}
	if meta == nil {
		return nil
	}
	sessionID := strings.TrimSpace(meta.InteractionSessionID)
	terminalID := strings.TrimSpace(meta.InteractionTerminalID)
	expectedLeaseID := strings.TrimSpace(meta.InteractionLeaseID)
	expectedLeaseFence := meta.InteractionLeaseFencingToken
	if sessionID == "" && terminalID == "" &&
		expectedLeaseID == "" && expectedLeaseFence == 0 {
		return nil
	}
	agentID := strings.TrimSpace(meta.AgentID)
	if meta.Kind != "agent" || agentID == "" ||
		sessionID == "" || terminalID == "" ||
		expectedLeaseID == "" || expectedLeaseFence <= 0 {
		return fmt.Errorf("terminal has incomplete canonical Interaction lifecycle identity")
	}
	if interrupter == nil {
		return interaction.ErrUnavailable
	}
	_, err = interrupter.ForceInterrupt(ctx, interaction.ForceInterruptCommand{
		WorkspaceKey:              key.Workspace,
		SessionID:                 sessionID,
		AgentID:                   agentID,
		TerminalID:                terminalID,
		ExpectedLeaseID:           expectedLeaseID,
		ExpectedLeaseFencingToken: expectedLeaseFence,
		StreamRef:                 "terminal:" + key.String(),
		TerminalTab:               key.Name,
		Reason:                    "server PTY " + strings.TrimSpace(reason),
	})
	if err != nil {
		return fmt.Errorf("force interrupt canonical terminal lifecycle: %w", err)
	}
	return nil
}
