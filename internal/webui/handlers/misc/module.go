package misc

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// Module registers the 3 workspace-scoped file operation routes on a
// [*http.ServeMux].
//
// The module is only constructed when fileSvc is non-nil.
// All routes are unconditional within this module.
type Module struct {
	fileSvc service.FileService
}

// NewModule returns a Module that will register routes using the
// given file service.
func NewModule(fileSvc service.FileService) *Module {
	return &Module{fileSvc: fileSvc}
}

// Register implements [Module] by registering 3 file operation routes.
func (m *Module) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/files/tree", HandleFileTree(m.fileSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/files", HandleFileRead(m.fileSvc))
	mux.HandleFunc("PUT /api/workspaces/{ws}/agents/{name}/files", HandleFileWrite(m.fileSvc))
}
