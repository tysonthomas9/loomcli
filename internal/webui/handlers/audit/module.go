// Package audit exposes the workspace mutation and daemon runtime audit trail.
package audit

import (
	"context"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// Service is the narrow seam between the locked HTTP contract and its fleet-db
// plus local-daemon implementation.
type Service interface {
	ListAuditEvents(ctx context.Context, workspaceKey, since string, limit int, entityID, actor string) ([]store.AuditEvent, string, error)
}

// Module registers the workspace audit route.
type Module struct {
	service       Service
	workspaceWrap middleware.Middleware
}

func NewModule(service Service) *Module {
	return &Module{service: service}
}

func (m *Module) WithWorkspaceMiddleware(wrap middleware.Middleware) *Module {
	m.workspaceWrap = wrap
	return m
}

func (m *Module) Register(mux *http.ServeMux) {
	if m.service == nil {
		return
	}
	h := http.Handler(HandleList(m.service))
	if m.workspaceWrap != nil {
		h = m.workspaceWrap(h)
	}
	mux.Handle("GET /api/workspaces/{ws}/audit", h)
}
