package git

import (
	"context"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// Module registers workspace-scoped git operation and diff routes
// on a [*http.ServeMux].
//
// The module is only constructed when ops.GitOps is non-nil. All routes are
// unconditional within this module.
type Module struct {
	agentSvc    service.AgentService
	diffSvc     service.DiffService
	epicPublish func(context.Context, string, string) error
}

// NewModule returns a Module that will register routes using the given
// agent service and diff service.
func NewModule(agentSvc service.AgentService, diffSvc service.DiffService, epicPublish ...func(context.Context, string, string) error) *Module {
	publisher := publish.ReconcileEpicLead
	if len(epicPublish) > 0 && epicPublish[0] != nil {
		publisher = epicPublish[0]
	}
	return &Module{
		agentSvc:    agentSvc,
		diffSvc:     diffSvc,
		epicPublish: publisher,
	}
}

// Register implements [Module] by registering git and diff routes.
func (m *Module) Register(mux *http.ServeMux) {
	// Git operations (agent-scoped)
	mux.HandleFunc("POST /api/workspaces/{ws}/git/push-all", HandleGitPushAll(m.agentSvc))
	mux.HandleFunc("POST /api/workspaces/{ws}/git/apply", HandleGitApply(m.agentSvc))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/push", HandleGitPush(m.agentSvc))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/pull", HandleGitPull(m.agentSvc))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/sync", HandleGitSync(m.agentSvc))
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/pr", HandleGitPR(m.agentSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/git/merge-up-to", handleMergeUpTo)
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/merge-up-to", handleMergeUpTo)
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/git/merge-requests", handleMergeRequests)
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/merge-requests", handleMergeRequests)
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/merge-requests/{id}/confirm", handleConfirmMergeRequest)
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/reset", HandleGitReset(m.agentSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/git/reset-preview", HandleGitResetPreview(m.agentSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/git/status", HandleGitStatus(m.agentSvc))
	mux.HandleFunc("PATCH /api/workspaces/{ws}/agents/{name}/git/target", HandleGitTargetUpdate(m.agentSvc))

	// Diff stat
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}/git/diff-stat", HandleGetIssueDiffStat(m.diffSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/git/diff-stat", HandleAgentDiffStat(m.agentSvc))

	// Diff endpoints
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/diff/commits", HandleDiffCommits(m.diffSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/diff/files", HandleDiffFiles(m.diffSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/diff/file", HandleDiffFile(m.diffSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/changes/{change}/revisions/{r}/diff", HandleRevisionDiff(false))
	mux.HandleFunc("GET /api/workspaces/{ws}/changes/{change}/revisions/{r}/interdiff", HandleRevisionDiff(true))
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}/revisions", handleTaskRevisions)
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}/diff", HandleTaskDiff())
	mux.HandleFunc("POST /api/workspaces/{ws}/changes/{change}/revisions/{r}/verdict", func(w http.ResponseWriter, r *http.Request) {
		handleVerdictWithPublisher(w, r, m.epicPublish)
	})
	mux.HandleFunc("PUT /api/workspaces/{ws}/git/following/{lead}", handleFollowing)
}
