package automationcomposition

import (
	"context"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// automationCatalogAuthorityProvider exposes only the authority operation
// Automation needs for its narrow Workflow Catalog resolver dependency. It
// neither exposes the Catalog issuer nor lets Automation choose an action.
type automationCatalogAuthorityProvider struct {
	issue EffectiveVersionAuthority
}

var _ automation.EffectiveVersionAuthorityProvider = (*automationCatalogAuthorityProvider)(nil)

func newAutomationCatalogAuthorityProvider(
	issue EffectiveVersionAuthority,
) automation.EffectiveVersionAuthorityProvider {
	if issue == nil {
		return nil
	}
	return &automationCatalogAuthorityProvider{issue: issue}
}

func (provider *automationCatalogAuthorityProvider) AuthorityForEffectiveVersion(
	ctx context.Context,
	workspace,
	reason string,
) (authority.SystemAuthority, error) {
	if provider == nil || provider.issue == nil {
		return authority.SystemAuthority{}, automation.ErrUnavailable
	}
	workspace = strings.TrimSpace(workspace)
	reason = strings.TrimSpace(reason)
	if workspace == "" || reason == "" {
		return authority.SystemAuthority{}, fmt.Errorf("automation catalog authority scope and reason are required: %w", authority.ErrInvalidScope)
	}
	return provider.issue(ctx, workspace, reason)
}
