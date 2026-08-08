package misc

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/filecoord"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// Module registers the workspace file operation routes on a [*http.ServeMux].
//
// The module is only constructed when fileSvc is non-nil.
// All routes are unconditional within this module.
type Module struct {
	fileSvc filecoord.FileService
	access  middleware.Middleware
}

// NewModule returns a Module that will register routes using the
// given file filecoord.
func NewModule(fileSvc filecoord.FileService, accessCfg ...middleware.FileAccessConfig) *Module {
	cfg := middleware.FileAccessConfig{}
	if len(accessCfg) > 0 {
		cfg = accessCfg[0]
	}
	return &Module{fileSvc: fileSvc, access: middleware.FileAccess(cfg)}
}

// Register implements [Module] by registering the file operation routes.
func (m *Module) Register(mux *http.ServeMux) {
	handle := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, m.access(h))
	}
	// Scope-rooted file browser. scope defaults to the workspace folder.
	handle("GET /api/workspaces/{ws}/files/capabilities", HandleFileCapabilities())
	handle("GET /api/workspaces/{ws}/files/tree", HandleScopedFileTree(m.fileSvc))
	handle("GET /api/workspaces/{ws}/files/index", HandleScopedFileIndex(m.fileSvc))
	handle("POST /api/workspaces/{ws}/files/search", HandleScopedFileSearch(m.fileSvc))
	handle("GET /api/workspaces/{ws}/files/git-status", HandleScopedGitStatus(m.fileSvc))
	handle("GET /api/workspaces/{ws}/files/checkouts", HandleFileCheckouts(m.fileSvc))
	handle("POST /api/workspaces/{ws}/files/checkouts/repair", HandleFileCheckoutRepair(m.fileSvc))
	handle("GET /api/workspaces/{ws}/files/diff", HandleScopedFileDiff(m.fileSvc))
	handle("GET /api/workspaces/{ws}/files/history", HandleScopedFileHistory(m.fileSvc))
	handle("GET /api/workspaces/{ws}/files/blame", HandleScopedFileBlame(m.fileSvc))
	handle("GET /api/workspaces/{ws}/files", HandleScopedFileRead(m.fileSvc))
	handle("GET /api/workspaces/{ws}/files/stat", HandleScopedFileStat(m.fileSvc))
	handle("PUT /api/workspaces/{ws}/files", HandleScopedFileWrite(m.fileSvc))
	handle("DELETE /api/workspaces/{ws}/files", HandleScopedFileDelete(m.fileSvc))
	handle("POST /api/workspaces/{ws}/files/mkdir", HandleScopedFileMkdir(m.fileSvc))
	handle("PATCH /api/workspaces/{ws}/files/move", HandleScopedFileMove(m.fileSvc))
}
