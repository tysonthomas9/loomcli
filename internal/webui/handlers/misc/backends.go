package misc

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"

	"github.com/tysonthomas9/loomcli/internal/app/query/operationalview"
)

type backendsHealthResponse struct {
	Success bool                      `json:"success"`
	Data    []operationalview.Backend `json:"data"`
	Error   string                    `json:"error,omitempty"`
}

// HandleGetBackendsHealth returns a handler that lists registered backends with health status.
func HandleGetBackendsHealth(backendOps operationalview.BackendHealthQuery) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		backends, err := backendOps.ListBackendsHealth()
		if err != nil {
			handler.WriteJSON(w, http.StatusInternalServerError, backendsHealthResponse{
				Success: false,
				Error:   "failed to list backends",
			})
			return
		}

		// Ensure empty slice for JSON [] marshaling
		if backends == nil {
			backends = []operationalview.Backend{}
		}

		handler.WriteJSON(w, http.StatusOK, backendsHealthResponse{
			Success: true,
			Data:    backends,
		})
	}
}
