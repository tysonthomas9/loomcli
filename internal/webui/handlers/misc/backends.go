package misc

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"

	"github.com/tysonthomas9/loomcli/internal/ops"
)

type backendsHealthResponse struct {
	Success bool                `json:"success"`
	Data    []ops.BackendHealth `json:"data"`
	Error   string              `json:"error,omitempty"`
}

// HandleGetBackendsHealth returns a handler that lists registered backends with health status.
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

		// Ensure empty slice for JSON [] marshaling
		if backends == nil {
			backends = []ops.BackendHealth{}
		}

		handler.WriteJSON(w, http.StatusOK, backendsHealthResponse{
			Success: true,
			Data:    backends,
		})
	}
}
