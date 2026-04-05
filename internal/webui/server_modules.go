package webui

import "github.com/tysonthomas9/loomcli/internal/webui/server/middleware"

// buildModules conditionally constructs workspace-scoped route modules
// and assigns them to app.wsModules. Called from NewServer after
// buildHandlers() and before mux creation.
func (app *Server) buildModules() {
	workspaceConfigFn := app.config.WorkspaceConfigFn
	workspaceConfigByIDFn := app.config.WorkspaceConfigByIDFn

	// Always-constructed modules
	app.wsModules = append(app.wsModules,
		NewIssueModule(app.issueSvc, workspaceConfigFn),
		NewWorkspaceOpsModule(app.workspaceSvc, app.multiPool),
		NewLogModule(app.agentSvc),
		NewSessionModule(app.sessSvc),
	)

	// Conditionally-constructed modules
	if app.hub != nil {
		app.wsModules = append(app.wsModules,
			NewSSEModule(app.hub, app.getMutationsSince,
				middleware.WorkspaceFromContext, app.sseTokens))
	}
	if app.termSvc != nil {
		app.wsModules = append(app.wsModules,
			NewTerminalTabModule(app.termSvc))
	}
	if app.issueTabStore != nil {
		app.wsModules = append(app.wsModules,
			NewIssueTabModule(app.issueTabStore, app.termMgr, app.hub))
	}
	if app.termSvc != nil {
		app.wsModules = append(app.wsModules,
			NewTerminalModule(app.termSvc, app.agentSvc, app.termMgr,
				app.termAuth, app.corsConfig.AllowedOrigins,
				app.config.LoomServerURL, workspaceConfigByIDFn,
				app.tabMetaStore, app.hub))
	}
	if app.fleetRegistry != nil {
		app.wsModules = append(app.wsModules,
			NewFleetModule(app.fleetRegistry.Get, app.tokenCfg,
				app.multiPool, app.claimMetrics, app.fleetRegCfg))
	}
	if app.diffSvc != nil {
		app.wsModules = append(app.wsModules,
			NewGitModule(app.agentSvc, app.diffSvc))
	}
	if app.fileSvc != nil {
		app.wsModules = append(app.wsModules,
			NewFileModule(app.fileSvc))
	}
}
