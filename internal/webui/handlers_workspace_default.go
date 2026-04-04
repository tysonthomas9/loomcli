package webui

import (
	"encoding/json"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// handleSetDefaultWorkspace handles PUT /api/workspaces/default.
func handleSetDefaultWorkspace(svc service.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			respondJSON(w, http.StatusBadRequest, workspaceResponse{Success: false, Error: "invalid request body"})
			return
		}
		if body.Name == "" {
			respondJSON(w, http.StatusBadRequest, workspaceResponse{Success: false, Error: "name is required"})
			return
		}

		data, err := svc.SetDefaultWorkspace(r.Context(), body.Name)
		if err != nil {
			WriteServiceError(w, err)
			return
		}
		respondJSON(w, http.StatusOK, workspaceResponse{Success: true, Data: data})
	}
}

// handleClearDefaultWorkspace handles DELETE /api/workspaces/default.
func handleClearDefaultWorkspace(svc service.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := svc.ClearDefaultWorkspace(r.Context())
		if err != nil {
			WriteServiceError(w, err)
			return
		}
		respondJSON(w, http.StatusOK, workspaceResponse{Success: true, Data: data})
	}
}
