package serve

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
	catalog *WorkflowCatalogCapability
}

var _ automation.EffectiveVersionAuthorityProvider = (*automationCatalogAuthorityProvider)(nil)

func newAutomationCatalogAuthorityProvider(catalog *WorkflowCatalogCapability) automation.EffectiveVersionAuthorityProvider {
	if catalog == nil {
		return nil
	}
	return &automationCatalogAuthorityProvider{catalog: catalog}
}

func (provider *automationCatalogAuthorityProvider) AuthorityForEffectiveVersion(_ context.Context, workspace, reason string) (authority.SystemAuthority, error) {
	if provider == nil || provider.catalog == nil {
		return authority.SystemAuthority{}, automation.ErrUnavailable
	}
	workspace = strings.TrimSpace(workspace)
	reason = strings.TrimSpace(reason)
	if workspace == "" || reason == "" {
		return authority.SystemAuthority{}, fmt.Errorf("automation catalog authority scope and reason are required: %w", authority.ErrInvalidScope)
	}
	return provider.catalog.issueAutomationEffectiveVersionAuthority(workspace, reason)
}
