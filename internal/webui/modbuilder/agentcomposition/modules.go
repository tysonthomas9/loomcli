// Package agentcomposition constructs task-run and unified agent modules used
// by the web UI composition root.
package agentcomposition

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/agentmodules"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/taskrunapi"
)

// NewTaskRunAPIModule creates the task-runner HTTP API module
// (POST /api/workspaces/{ws}/task-run/{op}, lease-token auth) so task runner
// processes talk to serve instead of holding fleet-db credentials.
func NewTaskRunAPIModule(st store.Store, fleetBaseURL string, localSettingsDir string) interface{ Register(*http.ServeMux) } {
	_ = localSettingsDir // compatibility input only; task runners never receive Local Settings.
	return taskrunapi.NewModule(taskrunapi.Config{Store: st, FleetBaseURL: fleetBaseURL})
}

// UnifiedAgentModuleDeps contains the dependencies for unified agent modules.
type UnifiedAgentModuleDeps = agentmodules.Deps

// NewUnifiedAgentModules creates the unified agent route modules.
func NewUnifiedAgentModules(deps UnifiedAgentModuleDeps) []interface{ Register(*http.ServeMux) } {
	return agentmodules.New(deps)
}
