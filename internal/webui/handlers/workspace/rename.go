package workspace

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// WorkspaceRenameRequest is the JSON body for PATCH /api/workspaces/{ws}/name.
type WorkspaceRenameRequest struct {
	NewName string `json:"new_name"`
}

// HandleWorkspaceRename returns a handler that renames a workspace in the global config.
func HandleWorkspaceRename(svc service.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		if wsID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, WorkspaceResponse{Success: false, Error: "workspace ID is required"})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)

		var req WorkspaceRenameRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				handler.WriteJSON(w, http.StatusRequestEntityTooLarge, WorkspaceResponse{Success: false, Error: "request body too large"})
				return
			}
			handler.WriteJSON(w, http.StatusBadRequest, WorkspaceResponse{Success: false, Error: "invalid request body"})
			return
		}

		data, err := svc.RenameWorkspace(r.Context(), wsID, req.NewName)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, WorkspaceResponse{Success: true, Data: data})
	}
}
