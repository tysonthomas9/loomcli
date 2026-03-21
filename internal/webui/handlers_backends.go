package webui

import "net/http"

type backendsHealthResponse struct {
	Success bool            `json:"success"`
	Data    []BackendHealth `json:"data"`
	Error   string          `json:"error,omitempty"`
}

// handleGetBackendsHealth returns a handler that lists registered backends with health status.
func handleGetBackendsHealth(ops BackendOps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		backends, err := ops.ListBackendsHealth()
		if err != nil {
			respondJSON(w, http.StatusInternalServerError, backendsHealthResponse{
				Success: false,
				Error:   "failed to list backends",
			})
			return
		}

		// Ensure empty slice for JSON [] marshaling
		if backends == nil {
			backends = []BackendHealth{}
		}

		respondJSON(w, http.StatusOK, backendsHealthResponse{
			Success: true,
			Data:    backends,
		})
	}
}
