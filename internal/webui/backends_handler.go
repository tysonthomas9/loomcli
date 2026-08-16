package webui

import (
	"encoding/json"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/app/query/operationalview"
)

type backendsHealthResp struct {
	Success bool                      `json:"success"`
	Data    []operationalview.Backend `json:"data"`
	Error   string                    `json:"error,omitempty"`
}

// HandleBackendsHealth returns an HTTP handler that lists registered backends
// with health status. This thin wrapper exists so that the app package can
// build the handler without importing handlers/misc directly.
func HandleBackendsHealth(backendOps operationalview.BackendHealthQuery) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		backends, err := backendOps.ListBackendsHealth()
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(backendsHealthResp{Success: false, Error: "failed to list backends"})
			return
		}
		if backends == nil {
			backends = []operationalview.Backend{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(backendsHealthResp{Success: true, Data: backends})
	}
}
