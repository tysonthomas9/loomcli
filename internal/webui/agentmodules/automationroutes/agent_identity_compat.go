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

// newStoreAgentIdentityCompatibility is the composition-owned read seam that
// keeps unattached binding identifiers disjoint from the two supervised-agent
// namespaces. The HTTP adapter sees only the semantic availability check, not
// the underlying persistence interfaces.
func newStoreAgentIdentityCompatibility(
	agents store.AgentStore,
	services store.AgentServiceStore,
) triggerbindings.UnattachedBindingIdentityChecker {
	if agents == nil || services == nil {
		return nil
	}
	return &storeAgentIdentityCompatibility{agents: agents, services: services}
}

type storeAgentIdentityCompatibility struct {
	agents   store.AgentStore
	services store.AgentServiceStore
}

func (compatibility *storeAgentIdentityCompatibility) CheckUnattachedBindingID(
	ctx context.Context,
	workspaceKey, bindingID string,
) error {
	if compatibility == nil || compatibility.agents == nil || compatibility.services == nil {
		return automation.ErrUnavailable
	}
	workspaceKey = strings.TrimSpace(workspaceKey)
	bindingID = strings.TrimSpace(bindingID)
	if workspaceKey == "" || bindingID == "" {
		return automation.ErrInvalid
	}

	agent, err := compatibility.agents.Get(ctx, workspaceKey, bindingID)
	switch {
	case err == nil && agent == nil:
		return automation.ErrInvalidPersistedState
	case err == nil:
		return fmt.Errorf(
			"trigger binding identifier %q is already used by a supervised agent: %w",
			bindingID, automation.ErrConflict,
		)
	case !errors.Is(err, domain.ErrNotFound):
		return fmt.Errorf("check supervised agent identifier %q: %w", bindingID, err)
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
