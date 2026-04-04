package webui

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// handleWorkspaceCreate returns a handler that creates a new workspace.
func handleWorkspaceCreate(svc service.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)

		var req service.WorkspaceCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				respondJSON(w, http.StatusRequestEntityTooLarge, workspaceResponse{
					Success: false,
					Error:   "request body too large",
				})
				return
			}
			respondJSON(w, http.StatusBadRequest, workspaceResponse{
				Success: false,
				Error:   "invalid request body",
			})
			return
		}

		// Async path for clone workspaces
		if req.Type == "clone" {
			jobID, err := svc.StartAsyncCreate(r.Context(), req)
			if err != nil {
				// If Unavailable, fall through to sync path
				var svcErr *service.ServiceError
				if errors.As(err, &svcErr) && svcErr.Kind == service.KindUnavailable {
					// fall through to sync creation below
				} else {
					WriteServiceError(w, err)
					return
				}
			} else {
				respondJSON(w, http.StatusAccepted, map[string]any{"success": true, "job_id": jobID})
				return
			}
		}

		// Sync path
		data, warnings, err := svc.CreateWorkspace(r.Context(), req)
		if err != nil {
			WriteServiceError(w, err)
			return
		}
		resp := workspaceResponse{Success: true, Data: data}
		if len(warnings) > 0 {
			resp.Warnings = warnings
		}
		respondJSON(w, http.StatusCreated, resp)
	}
}
