package agentcomposition

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/prreviewer"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// prReviewerAuthorityProvider is intentionally not generic: its two public
// methods bind the PR-reviewer application workflow to the only Agents
// actions it needs.
type prReviewerAuthorityProvider struct {
	issuer *authority.Issuer
	now    func() time.Time
}

var _ prreviewer.AuthorityProvider = (*prReviewerAuthorityProvider)(nil)

func (provider *prReviewerAuthorityProvider) AuthorityForReviewerRole(
	ctx context.Context,
	workspace,
	reason string,
) (authority.SystemAuthority, error) {
	return provider.issue(ctx, workspace, agents.ActionEnsureManagedRole, reason)
}

func (provider *prReviewerAuthorityProvider) AuthorityForReviewerAgent(
	ctx context.Context,
	workspace,
	reason string,
) (authority.SystemAuthority, error) {
	return provider.issue(ctx, workspace, agents.ActionEnsureManagedAgent, reason)
}

func (provider *prReviewerAuthorityProvider) issue(
	ctx context.Context,
	workspace string,
	action authority.Action,
	reason string,
) (authority.SystemAuthority, error) {
	if provider == nil || provider.issuer == nil || provider.now == nil {
		return authority.SystemAuthority{}, prreviewer.ErrUnavailable
	}
	if ctx == nil {
		return authority.SystemAuthority{}, fmt.Errorf(
			"pr reviewer authority context is required: %w",
			authority.ErrInvalidScope,
		)
	}
	if err := ctx.Err(); err != nil {
		return authority.SystemAuthority{}, err
	}
	workspace = strings.TrimSpace(workspace)
	reason = strings.TrimSpace(reason)
	if workspace == "" || reason == "" {
		return authority.SystemAuthority{}, fmt.Errorf(
			"pr reviewer authority scope and reason are required: %w",
			authority.ErrInvalidScope,
		)
	}
	principal, err := provider.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject:   prreviewer.AuthoritySubject,
		Class:     authority.ClassSystem,
		Workspace: workspace,
		Actions:   []authority.Action{action},
		ExpiresAt: provider.now().Add(time.Minute),
	})
	if err != nil {
		return authority.SystemAuthority{}, err
	}
	return provider.issuer.IssueSystem(principal, workspace, action, reason)
}
