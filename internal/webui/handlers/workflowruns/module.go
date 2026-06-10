// Package workflowruns registers the workspace-scoped HTTP API for
// dynamic workflow runs. All reads come from fleet-db's platform API
// (via platform.Store) — the serve process never queries the Flue
// execution plane.
package workflowruns

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/workflows/platform"
)

// Module registers the workflow-run routes.
type Module struct {
	store platform.Store
}

// NewModule returns a module backed by the platform store. A nil store
// disables registration (serve without fleet-db).
func NewModule(store platform.Store) *Module {
	return &Module{store: store}
}

// Register implements the webui wsModule contract.
func (m *Module) Register(mux *http.ServeMux) {
	if m.store == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces/{ws}/workflows/runs", m.handleListRuns)
	mux.HandleFunc("GET /api/workspaces/{ws}/workflows/runs/{id}", m.handleGetRun)
	mux.HandleFunc("GET /api/workspaces/{ws}/workflows/runs/{id}/events", m.handleRunEvents)
	mux.HandleFunc("GET /api/workspaces/{ws}/workflows/runs/{id}/tail", m.handleTailRun)
	mux.HandleFunc("POST /api/workspaces/{ws}/workflows/epics/{epic}/run", m.handleRunEpic)
}
