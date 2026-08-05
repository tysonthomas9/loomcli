package interactioncomposition

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
)

const interactionAuthorityTTL = time.Minute

// InteractionSessionAuthorityResolver converts one validated raw AgentLease
// credential into one exact, issuer-bound SessionAuthority. The raw token is
// consumed and closed on every outcome.
type InteractionSessionAuthorityResolver interface {
	ResolveSessionAuthority(
		context.Context,
		authority.Action,
		interaction.SessionAuthorityProof,
	) (authority.SessionAuthority, error)
}

type interactionSessionAuthorityResolver struct {
	validator interaction.SessionAuthorityValidator
	issuer    *authority.Issuer
	now       func() time.Time
}

var _ InteractionSessionAuthorityResolver = (*interactionSessionAuthorityResolver)(nil)

func newInteractionSessionAuthorityResolver(
	validator interaction.SessionAuthorityValidator,
	issuer *authority.Issuer,
	now func() time.Time,
) InteractionSessionAuthorityResolver {
	if validator == nil || issuer == nil || now == nil {
		return nil
	}
	return &interactionSessionAuthorityResolver{validator: validator, issuer: issuer, now: now}
}

//nolint:cyclop,funlen // Authority resolution exhaustively binds operation, owner generation, and allowed action before issuance.
func (resolver *interactionSessionAuthorityResolver) ResolveSessionAuthority(
	ctx context.Context,
	action authority.Action,
	proof interaction.SessionAuthorityProof,
) (authority.SessionAuthority, error) {
	if proof.Token != nil {
		defer proof.Token.Close()
	}
	if resolver == nil || resolver.validator == nil || resolver.issuer == nil || resolver.now == nil {
		return authority.SessionAuthority{}, interaction.ErrUnavailable
	}
	if ctx == nil {
		return authority.SessionAuthority{}, fmt.Errorf("session authority context is required: %w", authority.ErrInvalidScope)
	}
	if err := ctx.Err(); err != nil {
		return authority.SessionAuthority{}, err
	}
	if !interactionSessionAction(action) {
		return authority.SessionAuthority{}, fmt.Errorf(
			"action %q is not session-authorized: %w",
			action,
			authority.ErrActionNotAllowed,
		)
	}
	if interactionTerminalAction(action) && strings.TrimSpace(proof.TerminalID) == "" {
		return authority.SessionAuthority{}, fmt.Errorf(
			"terminal-scoped action %q requires durable terminal identity: %w",
			action,
			authority.ErrInvalidScope,
		)
	}
	verified, err := resolver.validator.ValidateSessionAuthority(ctx, proof)
	if err != nil {
		return authority.SessionAuthority{}, err
	}
	if verified == nil ||
		verified.WorkspaceKey != proof.WorkspaceKey ||
		verified.SessionID != proof.SessionID ||
		verified.AgentID != proof.AgentID ||
		verified.NodeID != proof.NodeID ||
		verified.LeaseID != proof.LeaseID ||
		verified.FencingToken != proof.FencingToken ||
		(proof.TerminalID != "" && verified.TerminalID != proof.TerminalID) ||
		verified.ExpiresAt.IsZero() {
		return authority.SessionAuthority{}, fmt.Errorf(
			"session authority validator returned mismatched durable identity: %w",
			interaction.ErrInvalidPersistedState,
		)
	}
	now := resolver.now()
	expiresAt := now.Add(interactionAuthorityTTL)
	if verified.ExpiresAt.Before(expiresAt) {
		expiresAt = verified.ExpiresAt
	}
	if !now.Before(expiresAt) {
		return authority.SessionAuthority{}, fmt.Errorf(
			"validated session lease is expired: %w",
			interaction.ErrNotOwner,
		)
	}
	principal, err := resolver.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject:   "session:" + verified.SessionID,
		Class:     authority.ClassSession,
		Workspace: verified.WorkspaceKey,
		Actions:   []authority.Action{action},
		ExpiresAt: expiresAt,
	})
	if err != nil {
		return authority.SessionAuthority{}, err
	}
	credential := proof.Token.Bytes()
	defer clear(credential)
	return resolver.issuer.IssueSessionForOwnerWithCredential(
		principal,
		verified.WorkspaceKey,
		action,
		authority.SessionOwner{
			SessionID:    verified.SessionID,
			AgentID:      verified.AgentID,
			TerminalID:   verified.TerminalID,
			NodeID:       verified.NodeID,
			LeaseID:      verified.LeaseID,
			FencingToken: verified.FencingToken,
		},
		credential,
	)
}

func interactionSessionAction(action authority.Action) bool {
	switch action {
	case interaction.ActionPatchSession,
		interaction.ActionPublishTranscript,
		interaction.ActionHeartbeatSession,
		interaction.ActionFinishSession,
		interaction.ActionOpenTerminal,
		interaction.ActionUpdateTerminal,
		interaction.ActionClaimInbox,
		interaction.ActionCompleteInbox:
		return true
	default:
		return false
	}
}

func interactionTerminalAction(action authority.Action) bool {
	return action == interaction.ActionOpenTerminal || action == interaction.ActionUpdateTerminal
}

type interactionRuntimeAuthorityProvider struct {
	issuer *authority.Issuer
	now    func() time.Time
}

var _ interaction.RuntimeAuthorityProvider = (*interactionRuntimeAuthorityProvider)(nil)

func newInteractionRuntimeAuthorityProvider(
	issuer *authority.Issuer,
	now func() time.Time,
) interaction.RuntimeAuthorityProvider {
	if issuer == nil || now == nil {
		return nil
	}
	return &interactionRuntimeAuthorityProvider{issuer: issuer, now: now}
}

//nolint:cyclop,funlen // Runtime authority issuance keeps the closed component/action matrix explicit and fail-closed.
func (provider *interactionRuntimeAuthorityProvider) AuthorityForInteractionRuntime(
	ctx context.Context,
	componentID platformruntime.ComponentID,
	workspace string,
	action authority.Action,
) (authority.SystemAuthority, error) {
	if provider == nil || provider.issuer == nil || provider.now == nil {
		return authority.SystemAuthority{}, interaction.ErrUnavailable
	}
	if ctx == nil {
		return authority.SystemAuthority{}, fmt.Errorf("runtime authority context is required: %w", authority.ErrInvalidScope)
	}
	if err := ctx.Err(); err != nil {
		return authority.SystemAuthority{}, err
	}
	workspace = strings.TrimSpace(workspace)
	registered := (componentID == interaction.SessionRecoveryComponentID &&
		(action == interaction.ActionReconcileSessions ||
			action == interaction.ActionRecoverStart)) ||
		(componentID == interaction.InboxDeliveryComponentID &&
			action == interaction.ActionEnqueueInbox) ||
		(componentID == interaction.ChatDeliveryComponentID &&
			(action == interaction.ActionDeliverChatMessage ||
				action == interaction.ActionDeliverAssignment)) ||
		(componentID == interaction.TerminalLifecycleComponentID &&
			action == interaction.ActionForceInterrupt)
	if !registered || workspace == "" {
		return authority.SystemAuthority{}, fmt.Errorf(
			"unregistered Interaction runtime authority request: component=%q action=%q: %w",
			componentID,
			action,
			authority.ErrActionNotAllowed,
		)
	}
	principal, err := provider.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject:   string(componentID),
		Class:     authority.ClassSystem,
		Workspace: workspace,
		Actions:   []authority.Action{action},
		ExpiresAt: provider.now().Add(interactionAuthorityTTL),
	})
	if err != nil {
		return authority.SystemAuthority{}, err
	}
	reason := "registered Interaction session recovery pass"
	if componentID == interaction.InboxDeliveryComponentID {
		reason = "registered Interaction inbox delivery"
	} else if componentID == interaction.ChatDeliveryComponentID {
		reason = "registered Interaction chat delivery"
	} else if componentID == interaction.TerminalLifecycleComponentID {
		reason = "registered Interaction terminal lifecycle interrupt"
	}
	return provider.issuer.IssueSystem(
		principal,
		workspace,
		action,
		reason,
	)
}
