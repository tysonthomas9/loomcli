package misc

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"

	"github.com/tysonthomas9/loomcli/internal/ops"
)

// BackendSetupData is the wire shape for /api/backends. It embeds
// ops.BackendHealth (so the on-the-wire fields name/installed/available/
// api_key_set/version/message keep working for unchanged clients) and
// adds curated setup metadata plus the derived `authenticated` and
// `ready` booleans the spec calls for.
type BackendSetupData struct {
	ops.BackendHealth
	Description    string        `json:"description,omitempty"`
	Authenticated  bool          `json:"authenticated"`
	Ready          bool          `json:"ready"`
	InstallActions []SetupAction `json:"install_actions,omitempty"`
	LoginActions   []SetupAction `json:"login_actions,omitempty"`
	EnvVars        []EnvVarHint  `json:"env_vars,omitempty"`
}

type backendsHealthResponse struct {
	Success bool               `json:"success"`
	Data    []BackendSetupData `json:"data"`
	Error   string             `json:"error,omitempty"`
}

// HandleGetBackendsHealth returns a handler that lists registered backends
// with health status and curated setup metadata. See
// docs/product/web-onboarding-spec.md for the wire contract.
func HandleGetBackendsHealth(backendOps ops.BackendOps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		backends, err := backendOps.ListBackendsHealth()
		if err != nil {
			handler.WriteJSON(w, http.StatusInternalServerError, backendsHealthResponse{
				Success: false,
				Error:   "failed to list backends",
			})
			return
		}

		out := make([]BackendSetupData, 0, len(backends))
		for _, b := range backends {
			out = append(out, decorateBackend(b))
		}

		handler.WriteJSON(w, http.StatusOK, backendsHealthResponse{
			Success: true,
			Data:    out,
		})
	}
}

// decorateBackend joins live health data with curated setup metadata.
//
// Authenticated is currently aliased to APIKeySet because that is the
// only auth signal the CLI plumbing surfaces today. Once the CLI
// exposes a separate "auth file present" or `<backend> status` signal,
// this is the seam to thread it through.
func decorateBackend(h ops.BackendHealth) BackendSetupData {
	authenticated := h.APIKeySet
	ready := h.Installed && authenticated
	out := BackendSetupData{
		BackendHealth: h,
		Authenticated: authenticated,
		Ready:         ready,
	}
	if meta, ok := LookupBackendSetupMetadata(h.Name); ok {
		out.Description = meta.Description
		out.InstallActions = meta.InstallActions
		out.LoginActions = meta.LoginActions
		out.EnvVars = meta.EnvVars
	}
	return out
}
