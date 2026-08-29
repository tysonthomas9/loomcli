package app

import (
	"context"
	"fmt"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/appinfra"
	"github.com/tysonthomas9/loomcli/internal/webui/appstores"
	"github.com/tysonthomas9/loomcli/internal/webui/handlermux"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/agents"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/driverapi"
	githandlers "github.com/tysonthomas9/loomcli/internal/webui/handlers/git"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/onboarding"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/skills"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/webhooks"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/workflows"
	"github.com/tysonthomas9/loomcli/internal/webui/modbuilder"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
	"github.com/tysonthomas9/loomcli/internal/webui/svcimpl"
)

// buildModules conditionally constructs workspace-scoped route modules
// and assigns them to app.wsModules.
func (app *Server) buildModules() {
	storeBacked := app.config.Store != nil
	poollessIssueBackend := app.config.FleetClient || storeBacked

	var agentQueueH http.HandlerFunc
	if app.config.AgentQueueFn != nil && !storeBacked && !app.config.FleetClient {
		agentQueueH = webui.HandleAgentQueue(app.config.AgentQueueFn)
	}

	// Core modules. FleetDB-backed serve opens the unified store instead of
	// per-workspace daemons, so issue ops must use IssueBackendFn even when
	// a daemon pool object was constructed during startup.
	opsPool := app.multiPool
	if poollessIssueBackend {
		opsPool = nil
	}
	opsModule := handlermux.NewWorkspaceOpsModule(app.workspaceSvc, opsPool, agentQueueH).
		WithDaemonExpected(!poollessIssueBackend)
	if app.config.IssueBackendFn != nil {
		opsModule = opsModule.WithIssueBackendFn(app.config.IssueBackendFn)
	}
	if storeBacked {
		// Healing variant: when readyz finds no local path, attempt a one-shot
		// re-bind to an existing on-disk checkout before reporting "not ready".
		store := app.config.Store
		opsModule = opsModule.WithLocalWorkspacePathFn(func(wsKey string) string {
			return storeadapter.ResolveOrHealWorkspacePath(context.Background(), store, wsKey)
		})
	}
	app.wsModules = append(app.wsModules, opsModule)

	// Issue + session modules
	app.wsModules = append(app.wsModules,
		modbuilder.NewIssueModules(app.issueSvc, app.sessSvc, app.config.Store)...)

	// Log module (always added — handles nil agentSvc gracefully)
	app.wsModules = append(app.wsModules, svcimpl.NewLogModule(app.agentSvc))

	// SSE subscription
	if app.hub != nil {
		app.wsModules = append(app.wsModules,
			appstores.NewSubscriptionModule(app.hub, app.getMutationsSince,
				middleware.WorkspaceFromContext, app.activateSSESubscriber, app.sseTokens))
	}

	app.buildTerminalModules()
	app.buildInfraModules()
}

// buildTerminalModules adds terminal and issue-tab modules when their
// dependencies are available.
func (app *Server) buildTerminalModules() {
	if app.termSvc != nil {
		app.wsModules = append(app.wsModules,
			modbuilder.NewTerminalModules(modbuilder.TerminalModuleDeps{
				TermSvc:         app.termSvc,
				AgentSvc:        app.agentSvc,
				PTYMgr:          app.ptyMgr,
				AgentTmuxMgr:    app.agentTmuxMgr,
				TermAuth:        app.termAuth,
				CORSOrigins:     app.corsConfig.AllowedOrigins,
				SelfURL:         fmt.Sprintf("http://localhost:%d", app.actualPort),
				Store:           app.config.Store,
				TabMetaStore:    app.tabMetaStore,
				Hub:             app.hub,
				ServerStartedAt: app.startedAt,
			})...)
	}

	if app.issueTabStore != nil {
		app.wsModules = append(app.wsModules,
			modbuilder.NewIssueTabModule(app.issueTabStore, app.hub))
	}
}

// buildInfraModules adds fleet, diff, file, and agent control modules
// when their dependencies are available.
//
//nolint:funlen // One module registration per line; splitting hides the wiring order.
func (app *Server) buildInfraModules() {
	storeBacked := app.config.Store != nil
	fileAccessCfg := middleware.FileAccessConfig{
		RemoteAuth:      app.config.ExtAuthURL != "",
		ResolveRole:     app.config.WorkspaceRoleResolver,
		FrontendOrigins: app.config.FrontendOrigins,
		Logger:          app.config.Logger,
	}

	if app.fleetRegistry != nil {
		app.wsModules = append(app.wsModules,
			appinfra.NewFleetModule(app.fleetRegistry, app.tokenCfg,
				app.multiPool, app.claimMetrics, app.fleetRegCfg))
	}

	if app.diffSvc != nil {
		app.wsModules = append(app.wsModules, modbuilder.NewDiffModule(app.agentSvc, app.diffSvc))
	}

	if app.fileSvc != nil {
		app.wsModules = append(app.wsModules, modbuilder.NewFileModule(app.fileSvc, fileAccessCfg))
	}

	if storeBacked {
		app.connectorDispatcher = app.buildConnectorDispatcher()
		app.wsModules = append(app.wsModules, agents.NewModule(app.agentSvc, app.hub))
		app.wsModules = append(app.wsModules, skills.NewModule(app.config.Store, fileAccessCfg))
		app.wsModules = append(app.wsModules, onboarding.NewModule(app.issueSvc, app.agentSvc))
		app.wsModules = append(app.wsModules, workflows.NewModule(app.config.Store))
		app.wsModules = append(app.wsModules, webhooks.NewModule(app.config.Store))
		prReviewModule := modbuilder.NewPRReviewModule(
			app.config.Store, app.connectorDispatcher, app.agentSvc, app.termSvc, app.config.LocalSettingsDir,
		)
		app.prReviewCredentialSeeds = prReviewModule
		app.wsModules = append(app.wsModules, prReviewModule)
		app.wsModules = append(app.wsModules, modbuilder.NewApprovalsModule(app.config.Store))
		app.wsModules = append(app.wsModules, modbuilder.NewTaskRunAPIModule(app.config.Store, app.config.FleetDBBaseURL, app.config.LocalSettingsDir))
		app.wsModules = append(app.wsModules, driverapi.NewModule(driverapi.Config{
			Store:            app.config.Store,
			FleetBaseURL:     app.config.FleetDBBaseURL,
			APIBaseURL:       app.config.DriverAPIBaseURL,
			APIToken:         app.config.DriverAPIToken,
			RunTokenKey:      app.config.DriverRunTokenKey,
			LocalSettingsDir: app.config.LocalSettingsDir,
			Dispatcher:       app.connectorDispatcher,
		}))
	} else {
		// Without a store there is no connector-backed prreview module, so
		// keep the gh-backed pull-request list route available.
		app.wsModules = append(app.wsModules, githandlers.NewPullRequestListModule(app.agentSvc))
		if app.config.AgentControlFn != nil {
			app.wsModules = append(app.wsModules, webui.NewAgentControlModule(app.config.AgentControlFn, app.config.AgentInputFn))
		}
	}
}
