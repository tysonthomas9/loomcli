package app

import (
	"context"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/webui/app/capabilitycomposition"
	"github.com/tysonthomas9/loomcli/internal/webui/appinfra"
	"github.com/tysonthomas9/loomcli/internal/webui/appstores"
	"github.com/tysonthomas9/loomcli/internal/webui/handlermux"
	githandlers "github.com/tysonthomas9/loomcli/internal/webui/handlers/git"
	"github.com/tysonthomas9/loomcli/internal/webui/modbuilder"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
	"github.com/tysonthomas9/loomcli/internal/webui/svcimpl"
)

// buildModules conditionally constructs workspace-scoped route modules
// and assigns them to app.wsModules.
func (app *Server) buildModules() {
	storeBacked := app.config.Store != nil

	// Core workspace operations use the workflow-catalog IssueBackend port.
	opsModule := handlermux.NewWorkspaceOpsModule(app.workspaceSvc, nil)
	if app.workspaceCatalog != nil && app.workspaceStore != nil && app.workspaceSvc != nil {
		workspaceProjection := capabilitycomposition.NewWorkspaceHTTPProjection(app.workspaceStore, app.workspaceSvc)
		opsModule = opsModule.WithWorkspaceCatalog(app.workspaceCatalog, workspaceProjection)
	}
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
		modbuilder.NewIssueModules(app.workItems, app.workItemMover, app.sessSvc)...)

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
			capabilitycomposition.NewTerminalModules(
				app.config.AgentsCapability,
				app.config.InteractionCapability,
				modbuilder.TerminalModuleDeps{
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
				},
			)...)
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
				app.config.IssueBackendFn, app.claimMetrics, app.fleetRegCfg))
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
		app.buildStoreBackedInfraModules()
		return
	}
	app.buildStorelessInfraModules()
}

func (app *Server) buildStoreBackedInfraModules() {
	app.connectorDispatcher = app.buildConnectorDispatcher()
	unifiedDeps := app.unifiedAgentModuleDeps()
	app.wsModules = append(app.wsModules, modbuilder.NewUnifiedAgentModules(unifiedDeps)...)
	app.buildPRReviewModule()
}

func (app *Server) unifiedAgentModuleDeps() modbuilder.UnifiedAgentModuleDeps {
	deps := modbuilder.UnifiedAgentModuleDeps{
		Store: app.config.Store, InteractiveAgentRuntime: app.agentRuntime,
		WorkItems: app.workItems, Hub: app.hub,
		FleetBaseURL: app.config.FleetDBBaseURL, DriverAPIBaseURL: app.config.DriverAPIBaseURL,
		ExecutionIssueBackends: app.config.ExecutionIssueBackends,
		DriverAPIToken:         app.config.DriverAPIToken, DriverRunTokenKey: app.config.DriverRunTokenKey,
		DaytonaProvider:  app.config.DaytonaProvider,
		LocalSettingsDir: app.config.LocalSettingsDir, Dispatcher: app.connectorDispatcher,
		WorkflowCatalog:           app.config.WorkflowCatalogAPI,
		WorkflowCatalogAuthoring:  app.config.WorkflowCatalogAuthoring,
		WorkflowCatalogOperator:   app.config.WorkflowCatalogOperator,
		WorkflowTargetPreparation: app.config.WorkflowTargetPreparation,
		SourceControl:             app.config.SourceControl,
	}
	if transcripts, ok := app.sessSvc.(service.AgentSessionTranscriptService); ok {
		deps.AgentSessionTranscripts = transcripts
	}
	if capability := app.config.ArtifactsCapability; capability != nil {
		deps.Artifacts = capability.ArtifactsAPI()
	}
	capabilitycomposition.PopulateUnifiedAgentCapabilityDeps(app.config, &deps)
	return deps
}

func (app *Server) buildPRReviewModule() {
	prReviewModule := capabilitycomposition.NewPRReviewModule(
		app.config,
		app.connectorDispatcher,
		app.agentSvc,
		app.termSvc,
	)
	app.prReviewCredentialSeeds = prReviewModule
	app.wsModules = append(app.wsModules, prReviewModule)
}

func (app *Server) buildStorelessInfraModules() {
	// Without a store there is no connector-backed prreview module, so
	// keep the gh-backed pull-request list route available.
	app.wsModules = append(app.wsModules, githandlers.NewPullRequestListModule(app.agentSvc))
}
