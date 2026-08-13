package misc

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// Module registers the workspace file operation routes on a [*http.ServeMux].
//
// The module is only constructed when fileSvc is non-nil.
// All routes are unconditional within this module.
type Module struct {
	browse   sourcecontrol.Browse
	mutate   sourcecontrol.Mutate
	checkout sourcecontrol.Checkout
	access   middleware.Middleware
}

// NewModule returns a Module that will register routes using the
// given file sourcecontrol.
func NewModule(browse sourcecontrol.Browse, mutate sourcecontrol.Mutate, checkout sourcecontrol.Checkout, accessCfg ...middleware.FileAccessConfig) *Module {
	cfg := middleware.FileAccessConfig{}
	if len(accessCfg) > 0 {
		cfg = accessCfg[0]
	}
	return &Module{browse: browse, mutate: mutate, checkout: checkout, access: middleware.FileAccess(cfg)}
}

// Register implements [Module] by registering the file operation routes.
func (m *Module) Register(mux *http.ServeMux) {
	handle := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, m.access(h))
	}
	// Scope-rooted file browser. scope defaults to the workspace folder.
	handle("GET /api/workspaces/{ws}/files/capabilities", HandleFileCapabilities())
	handle("GET /api/workspaces/{ws}/files/tree", HandleScopedFileTree(m.browse))
	handle("GET /api/workspaces/{ws}/files/index", HandleScopedFileIndex(m.browse))
	handle("POST /api/workspaces/{ws}/files/search", HandleScopedFileSearch(m.browse))
	handle("GET /api/workspaces/{ws}/files/git-status", HandleScopedGitStatus(m.checkout))
	handle("GET /api/workspaces/{ws}/files/checkouts", HandleFileCheckouts(m.checkout))
	handle("POST /api/workspaces/{ws}/files/checkouts/repair", HandleFileCheckoutRepair(m.checkout))
	handle("GET /api/workspaces/{ws}/files/diff", HandleScopedFileDiff(m.browse))
	handle("GET /api/workspaces/{ws}/files/history", HandleScopedFileHistory(m.browse))
	handle("GET /api/workspaces/{ws}/files/blame", HandleScopedFileBlame(m.browse))
	handle("GET /api/workspaces/{ws}/files", HandleScopedFileRead(m.browse))
	handle("GET /api/workspaces/{ws}/files/stat", HandleScopedFileStat(m.browse))
	handle("PUT /api/workspaces/{ws}/files", HandleScopedFileWrite(m.mutate))
	handle("DELETE /api/workspaces/{ws}/files", HandleScopedFileDelete(m.mutate))
	handle("POST /api/workspaces/{ws}/files/mkdir", HandleScopedFileMkdir(m.mutate))
	handle("PATCH /api/workspaces/{ws}/files/move", HandleScopedFileMove(m.mutate))
}
