package git

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// Module registers the 13 workspace-scoped git operation and diff routes
// on a [*http.ServeMux].
//
// The module is only constructed when ops.GitOps is non-nil. All routes are
// unconditional within this module.
type Module struct {
	agentSvc service.AgentService
	diffSvc  service.DiffService
}

// NewModule returns a Module that will register routes using the given
// agent service and diff service.
func NewModule(agentSvc service.AgentService, diffSvc service.DiffService) *Module {
	return &Module{
		agentSvc: agentSvc,
		diffSvc:  diffSvc,
	}
}

// Register implements [Module] by registering 13 git and diff routes.
func (m *Module) Register(mux *http.ServeMux) {
	// Git operations (agent-scoped)
	mux.HandleFunc("POST /api/workspaces/{ws}/git/push-all", HandleGitPushAll(m.agentSvc))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/push", HandleGitPush(m.agentSvc))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/pull", HandleGitPull(m.agentSvc))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/sync", HandleGitSync(m.agentSvc))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/pr", HandleGitPR(m.agentSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/pull-requests", HandleListPullRequests(m.agentSvc))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/reset", HandleGitReset(m.agentSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/git/status", HandleGitStatus(m.agentSvc))
	mux.HandleFunc("PATCH /api/workspaces/{ws}/agents/{name}/git/target", HandleGitTargetUpdate(m.agentSvc))

	// Diff stat
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}/git/diff-stat", HandleGetIssueDiffStat(m.diffSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/git/diff-stat", HandleAgentDiffStat(m.agentSvc))

	// Diff endpoints
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/diff/commits", HandleDiffCommits(m.diffSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/diff/files", HandleDiffFiles(m.diffSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/diff/file", HandleDiffFile(m.diffSvc))
}
