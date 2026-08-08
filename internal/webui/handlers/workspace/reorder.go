package workspace

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

// workspaceOrderRequest is the JSON body for PUT /api/workspaces/order.
type workspaceOrderRequest struct {
	Order []string `json:"order"`
}

// HandleWorkspaceReorder returns a handler that persists a custom workspace display order.
func HandleWorkspaceReorder() http.HandlerFunc {
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

		handler.WriteJSON(w, http.StatusNotImplemented, WorkspaceResponse{Success: false, Error: "workspace ordering is not implemented"})
	}
}
