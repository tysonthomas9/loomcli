package workflows

import (
	"context"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// Module registers store-backed workflow definition and run routes.
type Module struct {
	store          store.Store
	issueBackendFn func(context.Context) backend.IssueBackend
}

func NewModule(st store.Store) *Module {
	return &Module{store: st}
}

func (m *Module) WithIssueBackendFn(fn func(context.Context) backend.IssueBackend) *Module {
	m.issueBackendFn = fn
	return m
}

func (m *Module) Register(mux *http.ServeMux) {
	if m == nil || m.store == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces/{ws}/workflows", HandleList(m.store))
	mux.HandleFunc("POST /api/workspaces/{ws}/workflows/{name}/runs", HandleRun(m.store, m.issueBackendFn))
	mux.HandleFunc("GET /api/workspaces/{ws}/workflow-runs/{runID}", HandleShow(m.store))
	mux.HandleFunc("GET /api/workspaces/{ws}/workflow-runs/{runID}/events", HandleEvents(m.store))
	mux.HandleFunc("POST /api/workspaces/{ws}/workflow-runs/{runID}/cancel", HandleCancel(m.store))
}
