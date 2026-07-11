package prreview

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/connector"
	"github.com/tysonthomas9/loomcli/internal/leadcontrol"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

const (
	bindingID   = "webui-review"
	connectorID = "github-webui"

	webuiGitHubTokenEnv = "LOOM_WEBUI_GITHUB_TOKEN" //nolint:gosec // G101: env var name, not a credential
)

// Module serves connector-backed, read-only pull request review routes.
type Module struct {
	store                   store.Store
	dispatcher              *connector.Dispatcher
	agentSvc                service.AgentService
	terminalSvc             service.TerminalService
	dialCodex               func(ctx context.Context, endpoint string) (codexThreadReader, error)
	streamPollInterval      time.Duration
	streamHeartbeatInterval time.Duration
	// seeded caches "connector+grants already ensured" per canonical
	// ws|owner/repo so a polled read API does not re-seal + re-Create on
	// every request. Key is the canonical resource; value struct{}{}.
	seeded sync.Map
}

type codexThreadReader interface {
	ReadThreadWithTurns(ctx context.Context, threadID string) (*leadcontrol.CodexThread, error)
	Close(reason string) error
}

// NewModule constructs the pull request review route module. terminalSvc may
// be nil (no PTY manager); backend migration then skips killing live reviewer
// terminals, which is safe because without a terminal service none exist.
func NewModule(st store.Store, disp *connector.Dispatcher, agentSvc service.AgentService, terminalSvc service.TerminalService) *Module {
	return &Module{
		store:                   st,
		dispatcher:              disp,
		agentSvc:                agentSvc,
		terminalSvc:             terminalSvc,
		streamPollInterval:      reviewerStreamPollInterval,
		streamHeartbeatInterval: reviewerStreamHeartbeatInterval,
		dialCodex: func(ctx context.Context, endpoint string) (codexThreadReader, error) {
			return leadcontrol.DialCodexAppServer(ctx, endpoint)
		},
	}
}

// Register adds the workspace-scoped pull request review routes.
func (m *Module) Register(mux *http.ServeMux) {
	if m == nil || m.store == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces/{ws}/pull-requests", m.listPullRequests)
	mux.HandleFunc("GET /api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}", m.getPullRequest)
	mux.HandleFunc("GET /api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/diff", m.getPullRequestDiff)
	mux.HandleFunc("POST /api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/review", m.postReview)
	mux.HandleFunc("POST /api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/reviewer", m.ensureReviewer)
	mux.HandleFunc("POST /api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/messages", m.postReviewerMessage)
	mux.HandleFunc("GET /api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/stream", m.streamReviewer)
	mux.HandleFunc("GET /api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/conversation", m.getReviewerConversation)
}
