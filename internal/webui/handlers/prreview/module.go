package prreview

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
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
	workspace               WorkspaceQueries
	connectorManagement     connectorsmodule.Management
	connectorSealer         connectorsmodule.CredentialSealer
	dispatcher              connectorsmodule.Dispatcher
	pullRequests            PullRequestLister
	reviewerIdentities      ReviewerIdentityCommands
	reviewerAgents          agents.IdentityQueries
	sourceControl           PullRequestCheckoutMaterializer
	interactionChat         interaction.ChatAPI
	interactionMessenger    interaction.ChatMessenger
	interactionAuthority    OperatorAuthorityResolver
	localSettingsDir        string
	checkoutReviewerPRHead  reviewerCheckoutFunc
	recordReviewerPRContext reviewerRecordContextFunc
	streamPollInterval      time.Duration
	streamHeartbeatInterval time.Duration
	// seeded caches "connector+grants already ensured" by canonical resource
	// and action set so read and write authority cannot share a cache hit.
	seeded                     sync.Map
	credentialSeedMu           sync.Mutex
	credentialSeedGeneration   atomic.Uint64
	beforeCredentialSeedCommit func()
}

// OperatorAuthorityResolver is the narrow request-authority port consumed by
// PR Review. Declaring the port at the consumer boundary avoids coupling this
// delivery adapter to Workflow Catalog's separate HTTP adapter.
type OperatorAuthorityResolver interface {
	ResolveOperatorAuthority(*http.Request, string, authority.Action) (authority.OperatorAuthority, error)
}

// WorkspaceQueries is the complete Workspace-owned information consumed by PR
// Review. The port lives with its consumer and deliberately excludes mutation,
// local checkout state, and the composite persistence store.
type WorkspaceQueries interface {
	Resolve(context.Context, workspacemodule.ResolveQuery) (*workspacemodule.Reference, error)
	ListRepositories(context.Context, workspacemodule.ListRepositoriesQuery) ([]workspacemodule.Repository, error)
}

// PullRequestLister is the narrow Source Control query consumed by the PR
// review fallback. It is separate from the connector path, which proves a
// different trust origin before returning the same delivery projection.
type PullRequestLister interface {
	ListPullRequests(context.Context, sourcecontrol.ListPullRequestsQuery) (*sourcecontrol.PullRequestList, error)
}

type PullRequestCheckoutMaterializer interface {
	PreparePullRequestCheckout(context.Context, sourcecontrol.PullRequestCheckoutCommand) (*sourcecontrol.PullRequestCheckout, error)
}

// ReviewerIdentityCommands is PR Review's purpose-specific Agents consumer
// port. Composition supplies an authority-deriving wrapper; the route cannot
// issue system authority or invoke generic Role/Agent mutations.
type ReviewerIdentityCommands interface {
	ConvergeReviewerIdentity(context.Context, agents.ManagedReviewerCommand) (*agents.ManagedReviewerResult, error)
}

// Config contains the owner interfaces and runtime adapters consumed by PR
// Review. Composition belongs to the application root; the route module never
// receives a repository collection or constructs an owner implementation.
type Config struct {
	Workspace            WorkspaceQueries
	ConnectorManagement  connectorsmodule.Management
	ConnectorSealer      connectorsmodule.CredentialSealer
	Dispatcher           connectorsmodule.Dispatcher
	PullRequests         PullRequestLister
	LocalSettingsDir     string
	ReviewerIdentities   ReviewerIdentityCommands
	ReviewerAgents       agents.IdentityQueries
	SourceControl        PullRequestCheckoutMaterializer
	InteractionChat      interaction.ChatAPI
	InteractionMessenger interaction.ChatMessenger
	InteractionAuthority OperatorAuthorityResolver
}

// NewModule constructs the pull request review route module. LocalSettingsDir
// supplies the desktop GitHub credential and connector vault location.
// Interaction chat dependencies own all provider/session reads and message
// delivery; missing dependencies fail those routes closed.
func NewModule(config Config) *Module {
	if !connectorsmodule.DispatcherAvailable(config.Dispatcher) {
		config.Dispatcher = nil
	}
	return &Module{
		workspace:               config.Workspace,
		connectorManagement:     config.ConnectorManagement,
		connectorSealer:         config.ConnectorSealer,
		dispatcher:              config.Dispatcher,
		pullRequests:            config.PullRequests,
		reviewerIdentities:      config.ReviewerIdentities,
		reviewerAgents:          config.ReviewerAgents,
		sourceControl:           config.SourceControl,
		interactionChat:         config.InteractionChat,
		interactionMessenger:    config.InteractionMessenger,
		interactionAuthority:    config.InteractionAuthority,
		localSettingsDir:        strings.TrimSpace(config.LocalSettingsDir),
		checkoutReviewerPRHead:  localworkspace.EnsureDetachedGitWorktreeAtFetchedPRHead,
		recordReviewerPRContext: localworkspace.RecordPRReviewContextFromFetchedBase,
		streamPollInterval:      reviewerStreamPollInterval,
		streamHeartbeatInterval: reviewerStreamHeartbeatInterval,
	}
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
	if m == nil || m.workspace == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces/{ws}/pull-requests", m.listPullRequests)
	mux.HandleFunc("GET /api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}", m.getPullRequest)
	mux.HandleFunc("GET /api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/diff", m.getPullRequestDiff)
	mux.HandleFunc("POST /api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/review", m.postReview)
	mux.HandleFunc("POST /api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/reviewer", m.ensureReviewer)
	mux.HandleFunc("DELETE /api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/reviewer", m.archiveReviewer)
	mux.HandleFunc("POST /api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/messages", m.postReviewerMessage)
	mux.HandleFunc("GET /api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/stream", m.streamReviewer)
	mux.HandleFunc("GET /api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/conversation", m.getReviewerConversation)
}
