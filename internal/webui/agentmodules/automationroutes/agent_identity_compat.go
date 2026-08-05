package automationroutes

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

// newStoreAgentIdentityCompatibility keeps unattached binding identifiers
// disjoint from canonical Agent identities. The retired role-agent namespace
// is deliberately absent.
func newStoreAgentIdentityCompatibility(
	services store.AgentServiceStore,
) triggerbindings.UnattachedBindingIdentityChecker {
	if services == nil {
		return nil
	}
	return &storeAgentIdentityCompatibility{services: services}
}

type storeAgentIdentityCompatibility struct {
	services store.AgentServiceStore
}

func (compatibility *storeAgentIdentityCompatibility) CheckUnattachedBindingID(
	ctx context.Context,
	workspaceKey, bindingID string,
) error {
	if compatibility == nil || compatibility.services == nil {
		return automation.ErrUnavailable
	}
	workspaceKey = strings.TrimSpace(workspaceKey)
	bindingID = strings.TrimSpace(bindingID)
	if workspaceKey == "" || bindingID == "" {
		return automation.ErrInvalid
	}

	service, err := compatibility.services.Get(ctx, workspaceKey, bindingID)
	switch {
	case err == nil && service == nil:
		return automation.ErrInvalidPersistedState
	case err == nil:
		return fmt.Errorf(
			"trigger binding identifier %q is already used by a durable agent record: %w",
			bindingID, automation.ErrConflict,
		)
	case !errors.Is(err, domain.ErrNotFound):
		return fmt.Errorf("check durable agent record identifier %q: %w", bindingID, err)
	default:
		return nil
	}
}
