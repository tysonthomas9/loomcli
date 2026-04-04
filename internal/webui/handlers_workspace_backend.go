package webui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// WorkspaceBackendPatchRequest is the JSON body for PATCH /api/workspaces/{ws}/config/backend.
type WorkspaceBackendPatchRequest struct {
	Backend string `json:"backend"`
}

// handleWorkspaceBackendPatch returns a handler that updates a workspace's backend
// in the global config.
func handleWorkspaceBackendPatch(svc service.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		if wsID == "" {
			respondJSON(w, http.StatusBadRequest, workspaceResponse{Success: false, Error: "workspace ID is required"})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)

		var req WorkspaceBackendPatchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				respondJSON(w, http.StatusRequestEntityTooLarge, workspaceResponse{Success: false, Error: "request body too large"})
				return
			}
			respondJSON(w, http.StatusBadRequest, workspaceResponse{Success: false, Error: "invalid request body"})
			return
		}

		if req.Backend == "" {
			respondJSON(w, http.StatusBadRequest, workspaceResponse{Success: false, Error: "backend is required"})
			return
		}

		if !isValidBackend(req.Backend) {
			respondJSON(w, http.StatusBadRequest, workspaceResponse{
				Success: false,
				Error:   fmt.Sprintf("invalid backend %q; valid options: %v", req.Backend, validBackends),
			})
			return
		}

		data, err := svc.PatchWorkspaceBackend(r.Context(), wsID, req.Backend)
		if err != nil {
			WriteServiceError(w, err)
			return
		}
		respondJSON(w, http.StatusOK, workspaceResponse{Success: true, Data: data})
	}
}
