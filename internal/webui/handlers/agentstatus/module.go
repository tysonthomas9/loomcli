package agentstatus

import "net/http"

// Module registers the workspace-scoped agent status route. Constructed only
// when the underlying handler is non-nil — see app/server_modules.go for the
// gating logic (requires AgentStatusCollectFn + WsDaemonSupervisorFn +
// WorkspaceDaemonResolver).
type Module struct {
	handler http.HandlerFunc
}

// NewModule wraps the agent-status handler in a Module so it can be appended
// to the workspace-scoped module list.
func NewModule(h http.HandlerFunc) *Module {
	return &Module{handler: h}
}

// Register implements the wsModule interface used by buildInfraModules.
func (m *Module) Register(mux *http.ServeMux) {
	if m.handler == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/status", m.handler)
}
