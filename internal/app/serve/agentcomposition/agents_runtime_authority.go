package agentcomposition

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
)

type agentsRuntimeAuthorityProvider struct {
	issuer *authority.Issuer
	now    func() time.Time
}

var _ agents.RuntimeAuthorityProvider = (*agentsRuntimeAuthorityProvider)(nil)

func newAgentsRuntimeAuthorityProvider(issuer *authority.Issuer, now func() time.Time) agents.RuntimeAuthorityProvider {
	if issuer == nil || now == nil {
		return nil
	}
	return &agentsRuntimeAuthorityProvider{issuer: issuer, now: now}
}

func (provider *agentsRuntimeAuthorityProvider) AuthorityForAgentsRuntime(
	ctx context.Context,
	componentID platformruntime.ComponentID,
	workspace string,
	action authority.Action,
) (authority.SystemAuthority, error) {
	if provider == nil || provider.issuer == nil || provider.now == nil {
		return authority.SystemAuthority{}, agents.ErrUnavailable
	}
	if ctx == nil {
		return authority.SystemAuthority{}, fmt.Errorf("agents runtime authority context is required: %w", authority.ErrInvalidScope)
	}
	if err := ctx.Err(); err != nil {
		return authority.SystemAuthority{}, err
	}
	workspace = strings.TrimSpace(workspace)
	if workspace == "" || componentID != agents.DesiredStateReconciliationComponentID || action != agents.ActionReconcileDesiredState {
		return authority.SystemAuthority{}, fmt.Errorf(
			"unregistered Agents runtime authority request: component=%q action=%q: %w",
			componentID, action, authority.ErrActionNotAllowed,
		)
	}
	principal, err := provider.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: string(componentID), Class: authority.ClassSystem, Workspace: workspace,
		Actions: []authority.Action{action}, ExpiresAt: provider.now().Add(time.Minute),
	})
	if err != nil {
		return authority.SystemAuthority{}, err
	}
	return provider.issuer.IssueSystem(principal, workspace, action, "registered Agents desired-state reconciliation pass")
}
