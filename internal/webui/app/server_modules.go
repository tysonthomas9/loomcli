package app

import (
	"context"
	"fmt"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/appinfra"
	"github.com/tysonthomas9/loomcli/internal/webui/appstores"
	"github.com/tysonthomas9/loomcli/internal/webui/handlermux"
	githandlers "github.com/tysonthomas9/loomcli/internal/webui/handlers/git"
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
	if app.config.WorkflowCatalogModule != nil {
		app.wsModules = append(app.wsModules, app.config.WorkflowCatalogModule)
	}

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
func (app *Server) buildInfraModules() {
	storeBacked := app.config.Store != nil

	if app.fleetRegistry != nil {
		app.wsModules = append(app.wsModules,
			appinfra.NewFleetModule(app.fleetRegistry, app.tokenCfg,
				app.multiPool, app.claimMetrics, app.fleetRegCfg))
	}

	if app.diffSvc != nil {
		app.wsModules = append(app.wsModules, modbuilder.NewDiffModule(app.agentSvc, app.diffSvc))
	}

	if app.fileSvc != nil {
		app.wsModules = append(app.wsModules, modbuilder.NewFileModule(app.fileSvc, middleware.FileAccessConfig{
			RemoteAuth:      app.config.ExtAuthURL != "",
			ResolveRole:     app.config.WorkspaceRoleResolver,
			FrontendOrigins: app.config.FrontendOrigins,
			Logger:          app.config.Logger,
		}))
	}

	if storeBacked {
		app.connectorDispatcher = app.buildConnectorDispatcher()
		app.wsModules = append(app.wsModules, modbuilder.NewUnifiedAgentModules(modbuilder.UnifiedAgentModuleDeps{
			Store: app.config.Store, AgentSvc: app.agentSvc, IssueSvc: app.issueSvc, Hub: app.hub,
			FleetBaseURL: app.config.FleetDBBaseURL, DriverAPIBaseURL: app.config.DriverAPIBaseURL,
			DriverAPIToken: app.config.DriverAPIToken, DriverRunTokenKey: app.config.DriverRunTokenKey,
			LocalSettingsDir: app.config.LocalSettingsDir, Dispatcher: app.connectorDispatcher,
		})...)
		prReviewModule := modbuilder.NewPRReviewModule(
			app.config.Store, app.connectorDispatcher, app.agentSvc, app.termSvc, app.config.LocalSettingsDir,
		)
		app.prReviewCredentialSeeds = prReviewModule
		app.wsModules = append(app.wsModules, prReviewModule)
	} else {
		// Without a store there is no connector-backed prreview module, so
		// keep the gh-backed pull-request list route available.
		app.wsModules = append(app.wsModules, githandlers.NewPullRequestListModule(app.agentSvc))
		if app.config.AgentControlFn != nil {
			app.wsModules = append(app.wsModules, webui.NewAgentControlModule(app.config.AgentControlFn, app.config.AgentInputFn))
		}
	}
}
