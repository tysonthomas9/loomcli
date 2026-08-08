// Package reviewcomposition constructs source review, file, and local
// settings modules used by the web UI composition root.
package reviewcomposition

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/app/prreviewer"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/agentcoord"
	"github.com/tysonthomas9/loomcli/internal/webui/filecoord"
	githandlers "github.com/tysonthomas9/loomcli/internal/webui/handlers/git"
	locsettings "github.com/tysonthomas9/loomcli/internal/webui/handlers/localsettings"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/misc"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/prreview"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/sourcecontrolcoord"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// PRReviewModule is the route module plus its credential-cache invalidation
// surface used by local settings wiring.
type PRReviewModule interface {
	Register(*http.ServeMux)
	InvalidateCredentialSeeds()
}

// CredentialSeedInvalidator is the cross-module notification surface needed
// when a persisted GitHub runtime credential changes.
type CredentialSeedInvalidator interface {
	InvalidateCredentialSeeds()
}

// LocalSettingsHandlers contains the non-workspace local settings routes.
type LocalSettingsHandlers struct {
	Get                        http.HandlerFunc
	Patch                      http.HandlerFunc
	RuntimeCredentialPreflight http.HandlerFunc
}

// NewDiffModule creates the git diff module.
func NewDiffModule(agentSvc agentcoord.AgentService, diffSvc sourcecontrolcoord.DiffService) interface{ Register(*http.ServeMux) } {
	return githandlers.NewModule(agentSvc, diffSvc)
}

// NewFileModule creates the file operations module.
func NewFileModule(fileSvc filecoord.FileService, accessCfg ...middleware.FileAccessConfig) interface{ Register(*http.ServeMux) } {
	return misc.NewModule(fileSvc, accessCfg...)
}

// NewPRReviewModule creates the connector-backed pull request review module.
// terminalSvc may be nil (no PTY manager); reviewer backend migration then
// skips killing live reviewer terminals. localSettingsDir supplies the shared
// GitHub credential and connector vault key location. Interaction owns all
// reviewer conversation reads and message delivery.
func NewPRReviewModule(
	st store.Store,
	dispatcher connectorsmodule.Dispatcher,
	agentSvc agentcoord.AgentService,
	terminalSvc terminal.TerminalService,
	localSettingsDir string,
	reviewerProvisioning prreviewer.Commands,
	reviewerAgents agents.IdentityQueries,
	sourceControl sourcecontrol.Materializer,
	interactionChat interaction.ChatAPI,
	interactionMessenger interaction.ChatMessenger,
	interactionAuthority workflowcataloghttp.OperatorAuthorityResolver,
) PRReviewModule {
	return prreview.NewModule(
		st,
		dispatcher,
		agentSvc,
		terminalSvc,
		localSettingsDir,
		reviewerProvisioning,
		reviewerAgents,
		sourceControl,
		interactionChat,
		interactionMessenger,
		interactionAuthority,
	)
}

// NewLocalSettingsHandlers wires GitHub credential changes to the PR-review
// seed cache without coupling either handler package to the other.
func NewLocalSettingsHandlers(dataDir string, invalidator CredentialSeedInvalidator) LocalSettingsHandlers {
	options := locsettings.PatchOptions{}
	if invalidator != nil {
		options.OnGitHubRuntimeCredentialChanged = invalidator.InvalidateCredentialSeeds
	}
	return LocalSettingsHandlers{
		Get:                        locsettings.HandleGet(dataDir),
		Patch:                      locsettings.HandlePatch(dataDir, options),
		RuntimeCredentialPreflight: locsettings.HandleRuntimeCredentialPreflight(dataDir),
	}
}
