// Package epics registers epic-scoped HTTP routes that the UI uses to
// orchestrate work. The first endpoint is POST /epics/{id}/run, the
// shared backend command path the lead-agent epic-runner spec promises
// for both the UI "Run Epic" button and the CLI `loom epic run`.
package epics

import (
	"context"
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

type Module struct {
	store          store.Store
	hub            *realtime.Hub
	issueBackendFn func(context.Context) backend.IssueBackend
	runs           *runRegistry
	runInterval    time.Duration
	backgroundRuns bool
}

func NewModule(st store.Store, hub *realtime.Hub) *Module {
	return &Module{
		store:          st,
		hub:            hub,
		runs:           newRunRegistry(),
		runInterval:    5 * time.Second,
		backgroundRuns: true,
	}
}

func (m *Module) WithIssueBackendFn(fn func(context.Context) backend.IssueBackend) *Module {
	m.issueBackendFn = fn
	return m
}

func (m *Module) WithBackgroundRuns(enabled bool) *Module {
	m.backgroundRuns = enabled
	return m
}

// Register implements webui.Module. POST /epics/{id}/run binds a lead
// to an epic and enforces the "one lead per epic" + "one epic per lead"
// invariants the spec mandates.
func (m *Module) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/workspaces/{ws}/epics/{id}/run", handleRunEpic(m))
}
