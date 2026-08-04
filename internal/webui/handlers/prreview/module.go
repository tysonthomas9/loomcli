package prreview

import (
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/prreviewer"
	"github.com/tysonthomas9/loomcli/internal/infra/connectorscatalog"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

const (
	bindingID   = "webui-review"
	connectorID = "github-webui"

	webuiGitHubTokenEnv = "LOOM_WEBUI_GITHUB_TOKEN" //nolint:gosec // G101: env var name, not a credential
)

// Module serves connector-backed pull request review routes. Serve is assumed
// to have one operator: the GitHub PAT from local runtime settings (or the
// explicit env override) supplies the outer authority bound, while connector
// grants provide defense in depth. Read grants are seeded on read paths; write
// grants only on explicit review posts.
type Module struct {
	store                    store.Store
	connectorManagement      connectorsmodule.Management
	connectorManagementStore connectorsmodule.ManagementStore
	dispatcher               connectorsmodule.Dispatcher
	agentSvc                 service.AgentService
	terminalSvc              service.TerminalService
	reviewerProvisioning     prreviewer.Commands
	reviewerAgents           agents.IdentityQueries
	sourceControl            sourcecontrol.Materializer
	interactionChat          interaction.ChatAPI
	interactionMessenger     interaction.ChatMessenger
	interactionAuthority     workflowcataloghttp.OperatorAuthorityResolver
	localSettingsDir         string
	checkoutReviewerPRHead   reviewerCheckoutFunc
	recordReviewerPRContext  reviewerRecordContextFunc
	streamPollInterval       time.Duration
	streamHeartbeatInterval  time.Duration
	// seeded caches "connector+grants already ensured" by canonical resource
	// and action set so read and write authority cannot share a cache hit.
	seeded                     sync.Map
	credentialSeedMu           sync.Mutex
	credentialSeedGeneration   atomic.Uint64
	beforeCredentialSeedCommit func()
}

// NewModule constructs the pull request review route module. localSettingsDir
// supplies the desktop GitHub credential and connector vault fallback.
// terminalSvc may be nil (no PTY manager); backend migration then skips
// killing live reviewer terminals, which is safe because without a terminal
// service none exist. Interaction chat dependencies own all provider/session
// reads and message delivery; missing dependencies fail those routes closed.
func NewModule(
	st store.Store,
	disp connectorsmodule.Dispatcher,
	agentSvc service.AgentService,
	terminalSvc service.TerminalService,
	localSettingsDir string,
	reviewerProvisioning prreviewer.Commands,
	reviewerAgents agents.IdentityQueries,
	sourceControl sourcecontrol.Materializer,
	interactionChat interaction.ChatAPI,
	interactionMessenger interaction.ChatMessenger,
	interactionAuthority workflowcataloghttp.OperatorAuthorityResolver,
) *Module {
	if !connectorsmodule.DispatcherAvailable(disp) {
		disp = nil
	}
	module := &Module{
		store:                   st,
		dispatcher:              disp,
		agentSvc:                agentSvc,
		terminalSvc:             terminalSvc,
		reviewerProvisioning:    reviewerProvisioning,
		reviewerAgents:          reviewerAgents,
		sourceControl:           sourceControl,
		interactionChat:         interactionChat,
		interactionMessenger:    interactionMessenger,
		interactionAuthority:    interactionAuthority,
		localSettingsDir:        strings.TrimSpace(localSettingsDir),
		checkoutReviewerPRHead:  localworkspace.EnsureDetachedGitWorktreeAtFetchedPRHead,
		recordReviewerPRContext: localworkspace.RecordPRReviewContextFromFetchedBase,
		streamPollInterval:      reviewerStreamPollInterval,
		streamHeartbeatInterval: reviewerStreamHeartbeatInterval,
	}
	if st != nil {
		adapter, err := connectorscatalog.New(st.Connectors(), st.ConnectorGrants(), st.ConnectorCalls())
		if err == nil {
			module.connectorManagementStore = adapter
			module.connectorManagement, _ = connectorsmodule.NewManagement(adapter)
		}
	}
	return module
}

// InvalidateCredentialSeeds forces subsequent connector ensures to re-resolve
// the GitHub credential and synchronize the stored sealed value.
func (m *Module) InvalidateCredentialSeeds() {
	if m == nil {
		return
	}
	m.credentialSeedMu.Lock()
	defer m.credentialSeedMu.Unlock()
	m.seeded.Clear()
	m.credentialSeedGeneration.Add(1)
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
