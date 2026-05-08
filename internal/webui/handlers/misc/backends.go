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
// `Ready` follows the backend's own Available signal — that is the
// canonical "this backend will work right now" boolean each
// HealthCheck implementation produces. Backends that need an env var
// (claude, codex) flip Available true only when APIKeySet is true;
// keyless backends (opencode) flip it on Installed alone. Trust the
// backend, not a synthetic two-field check.
//
// `Authenticated` reports whether the auth requirement specific to
// this backend is satisfied: APIKeySet for env-var backends, the
// Available signal for keyless ones.
func decorateBackend(h ops.BackendHealth) BackendSetupData {
	meta, hasMeta := LookupBackendSetupMetadata(h.Name)
	requiresEnvVar := hasMeta && len(meta.EnvVars) > 0

	authenticated := h.APIKeySet
	if !requiresEnvVar {
		authenticated = h.Available
	}
	out := BackendSetupData{
		BackendHealth: h,
		Authenticated: authenticated,
		Ready:         h.Available,
	}
	if hasMeta {
		out.Description = meta.Description
		out.InstallActions = meta.InstallActions
		out.LoginActions = meta.LoginActions
		out.EnvVars = meta.EnvVars
	}
	return out
}
