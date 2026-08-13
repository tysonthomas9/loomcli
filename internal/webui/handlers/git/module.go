package git

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/webui/readprojection"
)

// Module registers the 13 workspace-scoped git operation and diff routes
// on a [*http.ServeMux].
//
// The module is constructed only when Source Control Browse and Checkout are
// composed. All routes are unconditional within this module.
type Module struct {
	checkout  sourcecontrol.Checkout
	browse    DiffBrowse
	issueDiff readprojection.IssueDiffProjection
}

// NewModule returns a Module that will register routes using the given
// agent service and diff service.
func NewModule(
	checkout sourcecontrol.Checkout,
	browse DiffBrowse,
	issueDiff readprojection.IssueDiffProjection,
) *Module {
	return &Module{
		checkout:  checkout,
		browse:    browse,
		issueDiff: issueDiff,
	}
}

// Register implements [Module] by registering 13 git and diff routes.
func (m *Module) Register(mux *http.ServeMux) {
	// Git operations (agent-scoped)
	mux.HandleFunc("POST /api/workspaces/{ws}/git/push-all", HandleGitPushAll(m.checkout))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/push", HandleGitPush(m.checkout))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/pull", HandleGitPull(m.checkout))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/sync", HandleGitSync(m.checkout))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/pr", HandleGitPR(m.checkout))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/reset", HandleGitReset(m.checkout))
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/git/status", HandleGitStatus(m.checkout))
	mux.HandleFunc("PATCH /api/workspaces/{ws}/agents/{name}/git/target", HandleGitTargetUpdate(m.checkout))

	// Diff stat
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}/git/diff-stat", HandleGetIssueDiffStat(m.issueDiff))
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/git/diff-stat", HandleAgentDiffStat(m.browse))

	// Diff endpoints
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/diff/commits", HandleDiffCommits(m.browse))
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/diff/files", HandleDiffFiles(m.browse))
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/diff/file", HandleDiffFile(m.browse))
}
