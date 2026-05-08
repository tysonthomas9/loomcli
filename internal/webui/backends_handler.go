package webui

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/misc"
)

// HandleBackendsHealth returns an HTTP handler that lists registered backends
// with health status and curated setup metadata. Thin wrapper that delegates
// to the misc package so the response decoration (description,
// authenticated, ready, install_actions, login_actions, env_vars) lives in
// one place — see internal/webui/handlers/misc/backends.go.
func HandleBackendsHealth(backendOps ops.BackendOps) http.HandlerFunc {
	return misc.HandleGetBackendsHealth(backendOps)
}
