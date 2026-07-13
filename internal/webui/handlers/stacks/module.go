package stacks

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// Module registers read-only workspace stack lineage routes.
type Module struct {
	stackSvc service.StackService
}

// NewModule returns a stack lineage route module.
func NewModule(stackSvc service.StackService) *Module {
	return &Module{stackSvc: stackSvc}
}

// Register implements the workspace module contract.
func (m *Module) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/workspaces/{ws}/stacks", HandleListStacks(m.stackSvc))
}
