package webui

import (
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
	githandlers "github.com/tysonthomas9/loomcli/internal/webui/handlers/git"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/issues"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/misc"
	hterminal "github.com/tysonthomas9/loomcli/internal/webui/handlers/terminal"
	webuilog "github.com/tysonthomas9/loomcli/internal/webui/log"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/subscription"
)

// buildModules conditionally constructs workspace-scoped route modules
// and assigns them to app.wsModules. Called from NewServer after
// buildHandlers() and before mux creation.
func (app *Server) buildModules() {
	workspaceConfigFn := app.config.WorkspaceConfigFn
	workspaceConfigByIDFn := app.config.WorkspaceConfigByIDFn

	// Always-constructed modules
	app.wsModules = append(app.wsModules,
		issues.NewIssueModule(app.issueSvc, workspaceConfigFn),
		NewWorkspaceOpsModule(app.workspaceSvc, app.multiPool),
		webuilog.NewModule(app.agentSvc),
		issues.NewSessionModule(app.sessSvc, issues.SessionModuleOpts{
			ListTaskSessions:     misc.HandleListTaskSessions(app.sessSvc),
			GetSession:           misc.HandleGetSession(app.sessSvc),
			GetSessionTranscript: misc.HandleGetSessionTranscript(app.sessSvc),
			GetSessionDiff:       misc.HandleGetSessionDiff(app.sessSvc),
		}),
	)

	// Conditionally-constructed modules
	if app.hub != nil {
		app.wsModules = append(app.wsModules,
			subscription.NewModule(app.hub, app.getMutationsSince,
				middleware.WorkspaceFromContext, app.sseTokens))
	}
	if app.termSvc != nil {
		app.wsModules = append(app.wsModules,
			hterminal.NewTabModule(app.termSvc))
	}
	if app.issueTabStore != nil {
		app.wsModules = append(app.wsModules,
			issues.NewIssueTabModule(app.issueTabStore, app.termMgr, app.hub))
	}
	if app.termSvc != nil {
		// Derive the server's own URL for terminal context banner fetches.
		// After server consolidation, the status endpoint lives on the same server.
		selfURL := fmt.Sprintf("http://localhost:%d", app.actualPort)
		app.wsModules = append(app.wsModules,
			hterminal.NewModule(app.termSvc, app.agentSvc, app.termMgr,
				app.termAuth, app.corsConfig.AllowedOrigins,
				selfURL, workspaceConfigByIDFn,
				app.tabMetaStore, app.hub))
	}
	if app.fleetRegistry != nil {
		app.wsModules = append(app.wsModules,
			fleet.NewModule(app.fleetRegistry.Get, app.tokenCfg,
				app.multiPool, app.claimMetrics, app.fleetRegCfg))
	}
	if app.diffSvc != nil {
		app.wsModules = append(app.wsModules,
			githandlers.NewModule(app.agentSvc, app.diffSvc))
	}
	if app.fileSvc != nil {
		app.wsModules = append(app.wsModules,
			misc.NewModule(app.fileSvc))
	}
}
