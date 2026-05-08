package workspace

import (
	"net/http"

	"go.opentelemetry.io/otel/attribute"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// HandleWorkspaceDelete returns a handler for DELETE /api/workspaces/{ws}.
func HandleWorkspaceDelete(svc service.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		ctx, span := startSpan(r.Context(), "service.Workspace.Delete",
			attribute.String("loom.workspace", wsID))
		defer span.End()

		if wsID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, WorkspaceResponse{Success: false, Error: "workspace ID is required"})
			return
		}
		data, err := svc.DeleteWorkspace(ctx, wsID)
		if err != nil {
			recordErr(span, err)
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, WorkspaceResponse{Success: true, Data: data})
	}
}
