package app

import (
	"fmt"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/appinfra"
	"github.com/tysonthomas9/loomcli/internal/webui/appstores"
	"github.com/tysonthomas9/loomcli/internal/webui/handlermux"
	"github.com/tysonthomas9/loomcli/internal/webui/modbuilder"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/svcimpl"
)

// buildModules conditionally constructs workspace-scoped route modules
// and assigns them to app.wsModules.
func (app *Server) buildModules() {
	var agentQueueH http.HandlerFunc
	if app.config.AgentQueueFn != nil {
		agentQueueH = webui.HandleAgentQueue(app.config.AgentQueueFn)
	}

	// Core modules
	app.wsModules = append(app.wsModules,
		handlermux.NewWorkspaceOpsModule(app.workspaceSvc, app.multiPool, agentQueueH),
	)

	// Issue + session modules
	app.wsModules = append(app.wsModules,
		modbuilder.NewIssueModules(app.issueSvc, app.sessSvc, app.config.WorkspaceConfigFn)...)

	// Log module (always added — handles nil agentSvc gracefully)
	app.wsModules = append(app.wsModules, svcimpl.NewLogModule(app.agentSvc))

	// SSE subscription
	if app.hub != nil {
		app.wsModules = append(app.wsModules,
			appstores.NewSubscriptionModule(app.hub, app.getMutationsSince,
				middleware.WorkspaceFromContext, app.sseTokens))
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
				TermSvc:      app.termSvc,
				AgentSvc:     app.agentSvc,
				PTYMgr:       app.ptyMgr,
				AgentTmuxMgr: app.agentTmuxMgr,
				TermAuth:     app.termAuth,
				CORSOrigins:  app.corsConfig.AllowedOrigins,
				SelfURL:      fmt.Sprintf("http://localhost:%d", app.actualPort),
				ConfigByIDFn: app.config.WorkspaceConfigByIDFn,
				TabMetaStore: app.tabMetaStore,
				Hub:          app.hub,
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
	if app.fleetRegistry != nil {
		app.wsModules = append(app.wsModules,
			appinfra.NewFleetModule(app.fleetRegistry, app.tokenCfg,
				app.multiPool, app.claimMetrics, app.fleetRegCfg))
	}

	if app.diffSvc != nil {
		app.wsModules = append(app.wsModules, modbuilder.NewDiffModule(app.agentSvc, app.diffSvc))
	}

	if app.fileSvc != nil {
		app.wsModules = append(app.wsModules, modbuilder.NewFileModule(app.fileSvc))
	}

	if app.config.AgentControlFn != nil {
		app.wsModules = append(app.wsModules, webui.NewAgentControlModule(app.config.AgentControlFn))
	}
}
