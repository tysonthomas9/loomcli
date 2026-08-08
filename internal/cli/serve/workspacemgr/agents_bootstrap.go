package workspacemgr

import (
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/app/agentsbootstrap"
	"github.com/tysonthomas9/loomcli/internal/infra/agentsbootstrapstore"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// newManagedAgentsCommands composes the canonical Agents bootstrap workflow
// from only the Role and Agent identity persistence facets Workspace owns.
func newManagedAgentsCommands(
	roles store.RoleStore,
	agentServices store.AgentServiceStore,
) (agentsbootstrap.ManagedCommands, error) {
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(agents.OperationRules()...)
	if err != nil {
		return nil, fmt.Errorf("compose managed Agents admission: %w", err)
	}
	persistence, err := agentsbootstrapstore.New(roles, agentServices)
	if err != nil {
		return nil, err
	}
	api, err := agentsbootstrap.NewAPI(persistence, admission)
	if err != nil {
		return nil, err
	}
	return agentsbootstrap.NewManagedCommands(api, issuer)
}
