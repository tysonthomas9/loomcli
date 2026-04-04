package webui

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// workspaceOrderRequest is the JSON body for PUT /api/workspaces/order.
type workspaceOrderRequest struct {
	Order []string `json:"order"`
}

// handleWorkspaceReorder returns a handler that persists a custom workspace display order.
func handleWorkspaceReorder(svc service.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)

		var req workspaceOrderRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				respondJSON(w, http.StatusRequestEntityTooLarge, workspaceResponse{Success: false, Error: "request body too large"})
				return
			}
			respondJSON(w, http.StatusBadRequest, workspaceResponse{Success: false, Error: "invalid request body"})
			return
		}

		data, err := svc.ReorderWorkspaces(r.Context(), req.Order)
		if err != nil {
			WriteServiceError(w, err)
			return
		}
		respondJSON(w, http.StatusOK, workspaceResponse{Success: true, Data: data})
	}
}
