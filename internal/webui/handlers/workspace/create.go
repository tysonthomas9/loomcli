package workspace

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// HandleWorkspaceCreate returns a handler that creates a new workspace.
func HandleWorkspaceCreate(svc service.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)

		var req service.WorkspaceCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				handler.WriteJSON(w, http.StatusRequestEntityTooLarge, WorkspaceResponse{
					Success: false,
					Error:   "request body too large",
				})
				return
			}
			handler.WriteJSON(w, http.StatusBadRequest, WorkspaceResponse{
				Success: false,
				Error:   "invalid request body",
			})
			return
		}

		// Async path for clone workspaces
		if req.Type == "clone" {
			jobID, err := svc.StartAsyncCreate(r.Context(), req)
			if err != nil {
				handler.HandleServiceError(w, err)
				return
			} else {
				handler.WriteJSON(w, http.StatusAccepted, map[string]any{"success": true, "job_id": jobID})
				return
			}
		}

		// Sync path
		data, warnings, err := svc.CreateWorkspace(r.Context(), req)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		resp := WorkspaceResponse{Success: true, Data: data}
		if len(warnings) > 0 {
			resp.Warnings = warnings
		}
		handler.WriteJSON(w, http.StatusCreated, resp)
	}
}
