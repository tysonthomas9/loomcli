package misc

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// Module registers the workspace file operation routes on a [*http.ServeMux]:
// three agent-scoped routes (the per-agent file panel) and two read-only
// scope-rooted routes (the dedicated workspace file browser).
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

// Register implements [Module] by registering the file operation routes.
func (m *Module) Register(mux *http.ServeMux) {
	// Agent-scoped (per-agent file panel): list/read/write within a worktree.
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/files/tree", HandleFileTree(m.fileSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/files", HandleFileRead(m.fileSvc))
	mux.HandleFunc("PUT /api/workspaces/{ws}/agents/{name}/files", HandleFileWrite(m.fileSvc))

	// Scope-rooted (dedicated file browser): read-only list/read. scope defaults
	// to the workspace folder; repo/agent scopes and writes are added later.
	mux.HandleFunc("GET /api/workspaces/{ws}/files/tree", HandleScopedFileTree(m.fileSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/files", HandleScopedFileRead(m.fileSvc))
}
