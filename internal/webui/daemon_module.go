package webui

import (
	"encoding/json"
	"net/http"
)

// DaemonModule registers workspace-scoped daemon inspection routes
// (/api/workspaces/{ws}/daemon/supervisor and /api/workspaces/{ws}/daemon/config).
// Routes are registered only when their backing closure is non-nil — fleet
// mode (no WorkspaceDaemonResolver) leaves both nil and skips registration,
// so the workspace catch-all returns 404 rather than 503.
type DaemonModule struct {
	supervisorFn func(wsID string) (*DaemonSupervisorData, error)
	configFn     func(wsID string) (json.RawMessage, error)
}

// NewDaemonModule returns a DaemonModule bound to the given per-workspace
// closures. A nil closure disables the corresponding route.
func NewDaemonModule(
	supervisorFn func(wsID string) (*DaemonSupervisorData, error),
	configFn func(wsID string) (json.RawMessage, error),
) *DaemonModule {
	return &DaemonModule{supervisorFn: supervisorFn, configFn: configFn}
}

// Register implements the wsModule interface used by buildInfraModules.
func (m *DaemonModule) Register(mux *http.ServeMux) {
	if m.supervisorFn != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/daemon/supervisor", HandleWsDaemonSupervisor(m.supervisorFn))
	}
	if m.configFn != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/daemon/config", HandleWsDaemonConfig(m.configFn))
	}
}
