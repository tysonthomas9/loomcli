// Package teamtemplates exposes the built-in Team Template catalog and apply
// operation over the web UI API.
package teamtemplates

import (
	"context"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/teamtemplate"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// TeamTemplateService is the narrow service seam used by the HTTP handlers.
type TeamTemplateService interface {
	CatalogTeamTemplates(ctx context.Context) ([]teamtemplate.TeamTemplate, error)
	ApplyTeamTemplate(ctx context.Context, workspaceKey, teamTemplateID string, dryRun bool) (teamtemplate.ApplyReport, error)
}

// Module registers Team Template routes.
type Module struct {
	service       TeamTemplateService
	workspaceWrap middleware.Middleware
}

func NewModule(service TeamTemplateService) *Module {
	return &Module{service: service}
}

// WithWorkspaceMiddleware applies the server's canonical workspace resolver
// to the workspace-scoped apply route. The registry-only catalog stays global.
func (m *Module) WithWorkspaceMiddleware(wrap middleware.Middleware) *Module {
	m.workspaceWrap = wrap
	return m
}

func (m *Module) Register(mux *http.ServeMux) {
	if m.service == nil {
		return
	}
	mux.HandleFunc("GET /api/team-templates", HandleCatalog(m.service))
	apply := http.Handler(HandleApply(m.service))
	if m.workspaceWrap != nil {
		apply = m.workspaceWrap(apply)
	}
	mux.Handle("POST /api/workspaces/{ws}/team-templates/{id}/apply", apply)
}
