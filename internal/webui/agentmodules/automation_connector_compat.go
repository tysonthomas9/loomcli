package agentmodules

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/triggerbindings"
)

// newStoreConnectorCompatibility is the temporary composition-owned seam for
// legacy per-binding webhook secrets and binding-scoped grants. It receives
// only the two narrow stores it needs; neither HTTP handler nor Automation
// receives those persistence interfaces. Connectors replaces it in Phase 5.
func newStoreConnectorCompatibility(bindings store.TriggerBindingStore, grants store.ConnectorGrantStore) triggerbindings.ConnectorCompatibility {
	if bindings == nil && grants == nil {
		return nil
	}
	return &storeConnectorCompatibility{bindings: bindings, grants: grants}
}

type storeConnectorCompatibility struct {
	bindings store.TriggerBindingStore
	grants   store.ConnectorGrantStore
}

func (compatibility *storeConnectorCompatibility) ConfigureBindingSecret(
	ctx context.Context,
	workspaceKey, bindingID, _ string,
	secret string,
) error {
	if compatibility == nil || compatibility.bindings == nil {
		return automation.ErrUnavailable
	}
	workspaceKey = strings.TrimSpace(workspaceKey)
	bindingID = strings.TrimSpace(bindingID)
	if workspaceKey == "" || bindingID == "" || secret == "" {
		return automation.ErrInvalid
	}
	if _, err := compatibility.bindings.Update(ctx, workspaceKey, bindingID, store.TriggerBindingUpdate{WebhookSecret: &secret}); err != nil {
		return fmt.Errorf("configure binding webhook secret: %w", err)
	}
	return nil
}

func (compatibility *storeConnectorCompatibility) RevokeBindingGrants(
	ctx context.Context,
	workspaceKey, bindingID string,
) (int, error) {
	if compatibility == nil || compatibility.grants == nil {
		return 0, automation.ErrUnavailable
	}
	workspaceKey = strings.TrimSpace(workspaceKey)
	bindingID = strings.TrimSpace(bindingID)
	if workspaceKey == "" || bindingID == "" {
		return 0, automation.ErrInvalid
	}
	grants, err := compatibility.grants.ListByBinding(ctx, workspaceKey, bindingID)
	if err != nil {
		return 0, fmt.Errorf("list binding connector grants: %w", err)
	}
	revoked := 0
	for _, grant := range grants {
		if grant == nil {
			continue
		}
		if err := compatibility.grants.Revoke(ctx, workspaceKey, grant.GrantID); err != nil {
			if errors.Is(err, domain.ErrGrantRevoked) {
				continue
			}
			return revoked, fmt.Errorf("revoke binding connector grant %q: %w", grant.GrantID, err)
		}
		revoked++
	}
	return revoked, nil
}
