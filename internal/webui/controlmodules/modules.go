// Package controlmodules constructs the FleetDB-backed agent and workflow
// control-plane route modules for the web UI composition root.
package controlmodules

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/connector"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/agents"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/connectors"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/driverapi"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/onboarding"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/roles"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/triggerbindings"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/webhooks"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/workflows"
	"github.com/tysonthomas9/loomcli/internal/webui/modbuilder"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// Config contains the dependencies shared by FleetDB-backed control-plane
// handlers. Keeping this wiring here prevents the app composition package from
// depending on every individual handler package.
type Config struct {
	Store             store.Store
	AgentService      service.AgentService
	IssueService      service.IssueService
	Hub               *realtime.Hub
	LocalSettingsDir  string
	FleetDBBaseURL    string
	DriverAPIBaseURL  string
	DriverAPIToken    string //nolint:gosec // G117: intentionally carries the configured driver API token.
	DriverRunTokenKey []byte
	Dispatcher        *connector.Dispatcher
}

// New returns the FleetDB-backed agent, workflow, approval, task-run, and
// driver API modules in their established registration order.
func New(cfg Config) []interface{ Register(*http.ServeMux) } {
	return []interface{ Register(*http.ServeMux) }{
		agents.NewModule(cfg.AgentService, cfg.Store, cfg.Hub),
		onboarding.NewModule(cfg.IssueService, cfg.AgentService),
		workflows.NewModule(cfg.Store),
		webhooks.NewModule(cfg.Store),
		roles.NewModule(cfg.Store),
		triggerbindings.NewModule(cfg.Store),
		connectors.NewModule(cfg.Store, cfg.LocalSettingsDir),
		modbuilder.NewApprovalsModule(cfg.Store),
		modbuilder.NewTaskRunAPIModule(cfg.Store, cfg.FleetDBBaseURL, cfg.LocalSettingsDir),
		driverapi.NewModule(driverapi.Config{
			Store:            cfg.Store,
			FleetBaseURL:     cfg.FleetDBBaseURL,
			APIBaseURL:       cfg.DriverAPIBaseURL,
			APIToken:         cfg.DriverAPIToken,
			RunTokenKey:      cfg.DriverRunTokenKey,
			LocalSettingsDir: cfg.LocalSettingsDir,
			Dispatcher:       cfg.Dispatcher,
		}),
	}
}
