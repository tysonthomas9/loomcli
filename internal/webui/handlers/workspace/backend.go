package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	webuiterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
	"github.com/tysonthomas9/loomcli/internal/webui/workspacecoord"
)

// BackendConfigResponse wraps the backend config data for JSON response.
type BackendConfigResponse struct {
	Success bool                              `json:"success"`
	Data    *workspacecoord.BackendConfigData `json:"data,omitempty"`
	Error   string                            `json:"error,omitempty"`
}

// isValidBackend checks if the backend name is in the allowed list.
func isValidBackend(name string) bool {
	for _, b := range webuiterminal.ValidBackends {
		if b == name {
			return true
		}
	}
	return false
}

// WorkspaceBackendPatchRequest is the JSON body for PATCH /api/workspaces/{ws}/config/backend.
type WorkspaceBackendPatchRequest struct {
	Backend string `json:"backend"`
}

// HandleWorkspaceBackendGet returns the store-backed workspace backend config.
func HandleWorkspaceBackendGet(svc workspacecoord.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		if wsID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, BackendConfigResponse{Success: false, Error: "workspace ID is required"})
			return
		}
		data, err := svc.GetWorkspaceBackend(r.Context(), wsID)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, BackendConfigResponse{Success: true, Data: data})
	}
}

// HandleWorkspaceBackendPatch returns a handler that updates a workspace's backend
// in the FleetDB-backed workspace config.
func HandleWorkspaceBackendPatch(svc workspacecoord.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		if wsID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, WorkspaceResponse{Success: false, Error: "workspace ID is required"})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)

		var req WorkspaceBackendPatchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				handler.WriteJSON(w, http.StatusRequestEntityTooLarge, WorkspaceResponse{Success: false, Error: "request body too large"})
				return
			}
			handler.WriteJSON(w, http.StatusBadRequest, WorkspaceResponse{Success: false, Error: "invalid request body"})
			return
		}

		if req.Backend == "" {
			handler.WriteJSON(w, http.StatusBadRequest, WorkspaceResponse{Success: false, Error: "backend is required"})
			return
		}

		if !isValidBackend(req.Backend) {
			handler.WriteJSON(w, http.StatusBadRequest, WorkspaceResponse{
				Success: false,
				Error:   fmt.Sprintf("invalid backend %q; valid options: %v", req.Backend, webuiterminal.ValidBackends),
			})
			return
		}

		data, err := svc.PatchWorkspaceBackend(r.Context(), wsID, req.Backend)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, WorkspaceResponse{Success: true, Data: data})
	}
}
