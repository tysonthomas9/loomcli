package workspacemgr

import (
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/app/agentscompat"
	"github.com/tysonthomas9/loomcli/internal/infra/agentscompatstore"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	storepkg "github.com/tysonthomas9/loomcli/internal/store"
)

// newManagedAgentsCommands is the legacy CLI composition edge for startup
// migration and workspace bootstrap. Application workflows receive only the
// public Agents API and issuer; this edge accepts only the three persistence
// facets required by the owner-private compatibility adapter.
func newManagedAgentsCommands(
	roles storepkg.RoleStore,
	agentServices storepkg.AgentServiceStore,
	assignments storepkg.AgentStore,
) (agentscompat.ManagedCommands, error) {
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(agents.OperationRules()...)
	if err != nil {
		return nil, fmt.Errorf("compose managed Agents admission: %w", err)
	}
	persistence, err := agentscompatstore.New(roles, agentServices, assignments)
	if err != nil {
		return nil, err
	}
	api, err := agentscompat.NewAPI(persistence, admission)
	if err != nil {
		return nil, err
	}
	return agentscompat.NewManagedCommands(api, issuer)
}
