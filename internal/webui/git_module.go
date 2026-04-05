package webui

import "net/http"

// GitModule registers the 13 workspace-scoped git operation and diff routes
// on a [*http.ServeMux].
//
// The module is only constructed when GitOps is non-nil. All routes are
// unconditional within this module.
type GitModule struct {
	agentSvc AgentService
	diffSvc  DiffService
}

// NewGitModule returns a GitModule that will register routes using the given
// agent service and diff service.
func NewGitModule(agentSvc AgentService, diffSvc DiffService) *GitModule {
	return &GitModule{
		agentSvc: agentSvc,
		diffSvc:  diffSvc,
	}
}

// Register implements [Module] by registering 13 git and diff routes.
func (m *GitModule) Register(mux *http.ServeMux) {
	// Git operations (agent-scoped)
	mux.HandleFunc("POST /api/workspaces/{ws}/git/push-all", handleGitPushAll(m.agentSvc))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/push", handleGitPush(m.agentSvc))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/pull", handleGitPull(m.agentSvc))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/sync", handleGitSync(m.agentSvc))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/pr", handleGitPR(m.agentSvc))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/reset", handleGitReset(m.agentSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/git/status", handleGitStatus(m.agentSvc))
	mux.HandleFunc("PATCH /api/workspaces/{ws}/agents/{name}/git/target", handleGitTargetUpdate(m.agentSvc))

	// Diff stat
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}/git/diff-stat", handleGetIssueDiffStat(m.diffSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/git/diff-stat", handleAgentDiffStat(m.agentSvc))

	// Diff endpoints
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/diff/commits", handleDiffCommits(m.diffSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/diff/files", handleDiffFiles(m.diffSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/diff/file", handleDiffFile(m.diffSvc))
}
