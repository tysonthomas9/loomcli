package agentmodules

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/connector"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/agents"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/approvals"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/connectors"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/driverapi"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/onboarding"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/roles"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/taskrunapi"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/triggerbindings"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/webhooks"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/workflows"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

type Deps struct {
	Store             store.Store
	AgentSvc          service.AgentService
	IssueSvc          service.IssueService
	Hub               *realtime.Hub
	FleetBaseURL      string
	DriverAPIBaseURL  string
	DriverAPIToken    string
	DriverRunTokenKey []byte
	LocalSettingsDir  string
	Dispatcher        *connector.Dispatcher
}

func New(deps Deps) []interface{ Register(*http.ServeMux) } {
	return []interface{ Register(*http.ServeMux) }{
		agents.NewModule(deps.AgentSvc, deps.Store, deps.Hub),
		onboarding.NewModule(deps.IssueSvc, deps.AgentSvc), workflows.NewModule(deps.Store),
		webhooks.NewModule(deps.Store), roles.NewModule(deps.Store), triggerbindings.NewModule(deps.Store),
		connectors.NewModule(deps.Store, deps.LocalSettingsDir), approvals.NewModule(deps.Store),
		taskrunapi.NewModule(taskrunapi.Config{Store: deps.Store, FleetBaseURL: deps.FleetBaseURL, LocalSettingsDir: deps.LocalSettingsDir}),
		driverapi.NewModule(driverapi.Config{
			Store: deps.Store, FleetBaseURL: deps.FleetBaseURL, APIBaseURL: deps.DriverAPIBaseURL,
			APIToken: deps.DriverAPIToken, RunTokenKey: deps.DriverRunTokenKey,
			LocalSettingsDir: deps.LocalSettingsDir, Dispatcher: deps.Dispatcher,
		}),
	}
}
