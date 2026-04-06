package workspace

import (
	"encoding/json"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// HandleSetDefaultWorkspace handles PUT /api/workspaces/default.
func HandleSetDefaultWorkspace(svc service.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			handler.WriteJSON(w, http.StatusBadRequest, WorkspaceResponse{Success: false, Error: "invalid request body"})
			return
		}
		if body.Name == "" {
			handler.WriteJSON(w, http.StatusBadRequest, WorkspaceResponse{Success: false, Error: "name is required"})
			return
		}

		data, err := svc.SetDefaultWorkspace(r.Context(), body.Name)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, WorkspaceResponse{Success: true, Data: data})
	}
}

// HandleClearDefaultWorkspace handles DELETE /api/workspaces/default.
func HandleClearDefaultWorkspace(svc service.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := svc.ClearDefaultWorkspace(r.Context())
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, WorkspaceResponse{Success: true, Data: data})
	}
}
