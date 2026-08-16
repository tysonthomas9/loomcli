package app

import (
	"context"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/app/query/sessionarchive"
	"github.com/tysonthomas9/loomcli/internal/webui/agentmodules"
	githandlers "github.com/tysonthomas9/loomcli/internal/webui/handlers/git"
	hterminal "github.com/tysonthomas9/loomcli/internal/webui/handlers/terminal"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

// buildModules conditionally constructs workspace-scoped route modules
// and assigns them to app.wsModules.
func (app *Server) buildModules() {
	storeBacked := app.config.ProjectionRecords != nil

	// Core workspace operations use owned capability ports.
	opsModule := NewWorkspaceOpsModule(app.workspaceSvc, nil).
		WithWorkItems(app.workItems).
		WithWorkItemStats(app.workItems).
		WithWorkItemGraph(app.workItems)
	if app.workspaceCatalog != nil && app.workspaceStore != nil && app.workspaceSvc != nil {
		workspaceProjection := NewWorkspaceHTTPProjection(app.workspaceStore, app.workspaceSvc)
		opsModule = opsModule.WithWorkspaceCatalog(app.workspaceCatalog, workspaceProjection)
	}
	if storeBacked {
		// Healing variant: when readyz finds no local path, attempt a one-shot
		// re-bind to an existing on-disk checkout before reporting "not ready".
		store := app.config.ProjectionRecords
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
		NewIssueModules(app.workItems, app.workItemMover, app.sessSvc)...)

	// Log module (always added — handles nil agentSvc gracefully)
	app.wsModules = append(app.wsModules, NewLogModule(app.agentSvc))

	// SSE subscription
	if app.hub != nil {
		app.wsModules = append(app.wsModules,
			NewSubscriptionModule(app.hub, app.getMutationsSince,
				middleware.WorkspaceFromContext, app.activateSSESubscriber, app.sseTokens))
	}

	app.buildTerminalModules()
	app.buildInfraModules()
}

// buildTerminalModules adds terminal and issue-tab modules when their
// dependencies are available.
func (app *Server) buildTerminalModules() {
	if app.termSvc != nil {
		var presentationState hterminal.PresentationState
		if app.tabMetaStore != nil {
			presentationState = hterminal.NewPresentationState(app.tabMetaStore.RedisClient())
		}
		app.wsModules = append(app.wsModules,
			NewTerminalModules(
				app.config.InteractionCapability,
				TerminalModuleDeps{
					TermSvc:           app.termSvc,
					AgentSvc:          app.agentSvc,
					TermAuth:          app.termAuth,
					CORSOrigins:       app.corsConfig.AllowedOrigins,
					SelfURL:           fmt.Sprintf("http://localhost:%d", app.actualPort),
					Workspace:         app.workspaceCatalog,
					PresentationState: presentationState,
					Hub:               app.hub,
					ServerStartedAt:   app.startedAt,
				},
			)...)
	}

	if app.issueTabStore != nil {
		app.wsModules = append(app.wsModules,
			NewIssueTabModule(app.issueTabStore, app.hub))
	}
}

// buildInfraModules adds fleet, diff, file, and agent control modules
// when their dependencies are available.
func (app *Server) buildInfraModules() {
	storeBacked := app.config.ProjectionRecords != nil

	if app.fleetRegistry != nil {
		app.wsModules = append(app.wsModules,
			NewFleetModule(app.fleetRegistry, app.tokenCfg,
				app.config.WorkItemsFn, app.claimMetrics, app.fleetRegCfg))
	}

	if app.sourceCheckout != nil && app.sourceBrowse != nil && app.issueDiff != nil {
		app.wsModules = append(app.wsModules, NewDiffModule(app.sourceCheckout, app.sourceBrowse, app.issueDiff))
	}

	if app.sourceBrowse != nil && app.sourceMutate != nil && app.sourceCheckout != nil {
		app.wsModules = append(app.wsModules, NewFileModule(app.sourceBrowse, app.sourceMutate, app.sourceCheckout, middleware.FileAccessConfig{
			RemoteAuth:      app.config.ExtAuthURL != "",
			ResolveRole:     app.config.WorkspaceRoleResolver,
			FrontendOrigins: app.config.FrontendOrigins,
			Logger:          app.config.Logger,
			GrantIssuer:     app.config.SourceControlAccessGrants,
		}))
	}

	if storeBacked {
		app.buildStoreBackedInfraModules()
		return
	}
	app.buildStorelessInfraModules()
}

func (app *Server) buildStoreBackedInfraModules() {
	app.connectorDispatcher, app.connectorManagement, app.connectorSealer = app.buildConnectorCapabilities()
	unifiedDeps := app.unifiedAgentModuleDeps()
	app.wsModules = append(app.wsModules, NewUnifiedAgentModules(unifiedDeps)...)
	app.buildPRReviewModule()
}

func (app *Server) unifiedAgentModuleDeps() UnifiedAgentModuleDeps {
	deps := UnifiedAgentModuleDeps{
		InteractiveAgentRuntime: app.agentRuntime,
		WorkItems:               app.workItems, Workspace: app.workspaceCatalog, Hub: app.hub,
		FleetBaseURL: app.config.FleetDBBaseURL, DriverAPIBaseURL: app.config.DriverAPIBaseURL,
		DriverRunTokenKey: app.config.DriverRunTokenKey,
		DaytonaProvider:   app.config.DaytonaProvider,
		LocalSettingsDir:  app.config.LocalSettingsDir, Dispatcher: app.connectorDispatcher,
		ConnectorBindingGrantLifecycle: app.connectorManagement,
		WorkflowCatalog:                app.config.WorkflowCatalogAPI,
		WorkflowCatalogAuthoring:       app.config.WorkflowCatalogAuthoring,
		WorkflowCatalogOperator:        app.config.WorkflowCatalogOperator,
		WorkflowTargetPreparation:      app.config.WorkflowTargetPreparation,
		SourceControl:                  app.config.SourceControl,
		TaskStackBindings:              app.config.TaskStackBindings,
		TaskOutcomes:                   app.config.TaskOutcomes,
	}
	if records := app.config.ProjectionRecords; records != nil {
		deps.WorkspaceTopology = records
		deps.ConnectorRecords = records
		deps.OrchestrationSessions = records
		deps.AwaitRecords = records.Awaits()
		deps.DriverRunRecords = records.DriverRuns()
		deps.TaskRunRecords = records.TaskRuns()
		deps.TriggerEventRecords = records.TriggerEvents()
	}
	if app.config.BackendOps != nil {
		deps.WorkflowBackendHealth = agentmodules.NewWorkflowBackendHealthQuery(
			func(name string) (bool, bool, bool, string, bool) {
				status, ok := app.config.BackendOps.BackendHealth(name)
				return status.Available, status.Installed, status.APIKeySet, status.Message, ok
			},
		)
	}
	if transcripts, ok := app.sessSvc.(sessionarchive.AgentSessionTranscriptService); ok {
		deps.AgentSessionTranscripts = transcripts
	}
	if capability := app.config.ArtifactsCapability; capability != nil {
		deps.Artifacts = capability.ArtifactsAPI()
	}
	PopulateUnifiedAgentCapabilityDeps(app.config, &deps)
	return deps
}

func (app *Server) buildPRReviewModule() {
	prReviewModule := NewPRReviewModule(
		app.config,
		app.workspaceCatalog,
		app.connectorManagement,
		app.connectorSealer,
		app.connectorDispatcher,
		app.agentRuntime,
	)
	app.prReviewCredentialSeeds = prReviewModule
	app.wsModules = append(app.wsModules, prReviewModule)
}

func (app *Server) buildStorelessInfraModules() {
	// Without a store there is no connector-backed prreview module, so
	// keep the gh-backed pull-request list route available.
	if app.sourceCheckout != nil {
		app.wsModules = append(app.wsModules, githandlers.NewPullRequestListModule(app.sourceCheckout))
	}
}
