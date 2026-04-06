package workspace

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// HandleWorkspaceDelete returns a handler for DELETE /api/workspaces/{ws}.
func HandleWorkspaceDelete(svc service.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		if wsID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, WorkspaceResponse{Success: false, Error: "workspace ID is required"})
			return
		}
		data, err := svc.DeleteWorkspace(r.Context(), wsID)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, WorkspaceResponse{Success: true, Data: data})
	}
}
