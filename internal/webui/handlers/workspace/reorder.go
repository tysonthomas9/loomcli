package workspace

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// workspaceOrderRequest is the JSON body for PUT /api/workspaces/order.
type workspaceOrderRequest struct {
	Order []string `json:"order"`
}

// HandleWorkspaceReorder returns a handler that persists a custom workspace display order.
func HandleWorkspaceReorder(svc service.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)

		var req workspaceOrderRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				handler.WriteJSON(w, http.StatusRequestEntityTooLarge, WorkspaceResponse{Success: false, Error: "request body too large"})
				return
			}
			handler.WriteJSON(w, http.StatusBadRequest, WorkspaceResponse{Success: false, Error: "invalid request body"})
			return
		}

		data, err := svc.ReorderWorkspaces(r.Context(), req.Order)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, WorkspaceResponse{Success: true, Data: data})
	}
}
