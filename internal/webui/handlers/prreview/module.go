package prreview

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tysonthomas9/loomcli/internal/connector"
	"github.com/tysonthomas9/loomcli/internal/leadcontrol"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// deliveryGroupBackend is the durable FleetDB DeliveryGroup API. Optional:
// when nil, group reads return empty pages and writes return 503.
type deliveryGroupBackend = store.DeliveryGroupStore

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
	store                   store.Store
	dispatcher              *connector.Dispatcher
	agentSvc                service.AgentService
	terminalSvc             service.TerminalService
	localSettingsDir        string
	checkoutReviewerPRHead  reviewerCheckoutFunc
	dialCodex               func(ctx context.Context, endpoint string) (codexThreadReader, error)
	streamPollInterval      time.Duration
	streamHeartbeatInterval time.Duration
	// rollouts memoizes the codex reviewer's on-disk rollout across polls.
	rollouts codexRolloutCache
	// seeded caches "connector+grants already ensured" by canonical resource
	// and action set so read and write authority cannot share a cache hit.
	seeded                     sync.Map
	credentialSeedMu           sync.Mutex
	credentialSeedGeneration   atomic.Uint64
	beforeCredentialSeedCommit func()
	// readiness caches the last-known PR readiness snapshots.
	readiness readinessCache
	// deliveryGroups is the optional FleetDB DeliveryGroup store. Nil when
	// the backing store does not implement store.OptionalDeliveryGroups.
	deliveryGroups deliveryGroupBackend
	// now and readinessBackoff are test seams (fake clock, no sleeps).
	now              func() time.Time
	readinessBackoff []time.Duration
}

type codexThreadReader interface {
	ReadThreadWithTurns(ctx context.Context, threadID string) (*leadcontrol.CodexThread, error)
	Close(reason string) error
}

// NewModule constructs the pull request review route module. localSettingsDir
// supplies the desktop GitHub credential and connector vault fallback.
// terminalSvc may be nil (no PTY manager); backend migration then skips
// killing live reviewer terminals, which is safe because without a terminal
// service none exist.
func NewModule(
	st store.Store,
	disp *connector.Dispatcher,
	agentSvc service.AgentService,
	terminalSvc service.TerminalService,
	localSettingsDir string,
) *Module {
	m := &Module{
		store:                   st,
		dispatcher:              disp,
		agentSvc:                agentSvc,
		terminalSvc:             terminalSvc,
		localSettingsDir:        strings.TrimSpace(localSettingsDir),
		checkoutReviewerPRHead:  localworkspace.EnsureDetachedGitWorktreeAtPRHead,
		streamPollInterval:      reviewerStreamPollInterval,
		streamHeartbeatInterval: reviewerStreamHeartbeatInterval,
		dialCodex: func(ctx context.Context, endpoint string) (codexThreadReader, error) {
			return leadcontrol.DialCodexAppServer(ctx, endpoint)
		},
	}
	if opt, ok := st.(store.OptionalDeliveryGroups); ok {
		m.deliveryGroups = opt.DeliveryGroups()
	}
	return m
}

// SetDeliveryGroups overrides the delivery-group backend (tests).
func (m *Module) SetDeliveryGroups(backend store.DeliveryGroupStore) {
	if m == nil {
		return
	}
	m.deliveryGroups = backend
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
	m.readiness.clear()
}

// Register adds the workspace-scoped pull request review routes.
func (m *Module) Register(mux *http.ServeMux) {
	if m == nil || m.store == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces/{ws}/pull-requests", m.listPullRequests)
	mux.HandleFunc("GET /api/workspaces/{ws}/pull-requests/readiness", m.getPullRequestReadiness)
	mux.HandleFunc("GET /api/workspaces/{ws}/pull-requests/readiness/preview", m.getPullRequestReadinessPreview)
	mux.HandleFunc("GET /api/workspaces/{ws}/delivery-groups", m.listDeliveryGroups)
	mux.HandleFunc("POST /api/workspaces/{ws}/delivery-groups", m.createDeliveryGroup)
	mux.HandleFunc("GET /api/workspaces/{ws}/delivery-groups/{group_id}", m.getDeliveryGroup)
	mux.HandleFunc("PATCH /api/workspaces/{ws}/delivery-groups/{group_id}", m.updateDeliveryGroup)
	mux.HandleFunc("PUT /api/workspaces/{ws}/delivery-groups/{group_id}/members", m.setDeliveryGroupMembers)
	mux.HandleFunc("POST /api/workspaces/{ws}/delivery-groups/{group_id}/archive", m.archiveDeliveryGroup)
	mux.HandleFunc("GET /api/workspaces/{ws}/delivery-groups/{group_id}/preview", m.previewDeliveryGroup)
	mux.HandleFunc("GET /api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}", m.getPullRequest)
	mux.HandleFunc("GET /api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/diff", m.getPullRequestDiff)
	mux.HandleFunc("POST /api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/review", m.postReview)
	mux.HandleFunc("POST /api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/reviewer", m.ensureReviewer)
	mux.HandleFunc("POST /api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/messages", m.postReviewerMessage)
	mux.HandleFunc("GET /api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/stream", m.streamReviewer)
	mux.HandleFunc("GET /api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/conversation", m.getReviewerConversation)
}
