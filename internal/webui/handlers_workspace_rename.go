package webui

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// WorkspaceRenameRequest is the JSON body for PATCH /api/workspaces/{ws}/name.
type WorkspaceRenameRequest struct {
	NewName string `json:"new_name"`
}

// handleWorkspaceRename returns a handler that renames a workspace in the global config.
func handleWorkspaceRename(svc service.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		if wsID == "" {
			respondJSON(w, http.StatusBadRequest, workspaceResponse{Success: false, Error: "workspace ID is required"})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)

		var req WorkspaceRenameRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				respondJSON(w, http.StatusRequestEntityTooLarge, workspaceResponse{Success: false, Error: "request body too large"})
				return
			}
			respondJSON(w, http.StatusBadRequest, workspaceResponse{Success: false, Error: "invalid request body"})
			return
		}

		data, err := svc.RenameWorkspace(r.Context(), wsID, req.NewName)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		respondJSON(w, http.StatusOK, workspaceResponse{Success: true, Data: data})
	}
}
