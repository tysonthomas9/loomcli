package misc

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// Module registers the workspace file operation routes on a [*http.ServeMux].
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
	// Deprecated agent-scoped routes: thin delegates to the scoped agent core.
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/files/tree", HandleFileTree(m.fileSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/files", HandleFileRead(m.fileSvc))
	mux.HandleFunc("PUT /api/workspaces/{ws}/agents/{name}/files", HandleFileWrite(m.fileSvc))

	// Scope-rooted file browser. scope defaults to the workspace folder.
	mux.HandleFunc("GET /api/workspaces/{ws}/files/tree", HandleScopedFileTree(m.fileSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/files/index", HandleScopedFileIndex(m.fileSvc))
	mux.HandleFunc("POST /api/workspaces/{ws}/files/search", HandleScopedFileSearch(m.fileSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/files", HandleScopedFileRead(m.fileSvc))
	mux.HandleFunc("PUT /api/workspaces/{ws}/files", HandleScopedFileWrite(m.fileSvc))
	mux.HandleFunc("DELETE /api/workspaces/{ws}/files", HandleScopedFileDelete(m.fileSvc))
	mux.HandleFunc("POST /api/workspaces/{ws}/files/mkdir", HandleScopedFileMkdir(m.fileSvc))
	mux.HandleFunc("PATCH /api/workspaces/{ws}/files/move", HandleScopedFileMove(m.fileSvc))
}
