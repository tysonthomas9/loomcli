package workspace

import (
	"context"
	"net/http"
	"strings"

	"go.opentelemetry.io/otel/attribute"

	loomworkspace "github.com/tysonthomas9/loomcli/internal/loomgit/workspace"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func HandleWorkspaceDeletePreview(svc service.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		previews, ok := svc.(interface {
			PreviewWorkspaceDeletion(context.Context, string) (loomworkspace.DeletePreview, error)
		})
		if !ok {
			handler.WriteJSON(w, http.StatusServiceUnavailable, WorkspaceResponse{Success: false, Error: "workspace deletion preview unavailable"})
			return
		}
		preview, err := previews.PreviewWorkspaceDeletion(r.Context(), wsID)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, preview)
	}
}

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
		fingerprint := strings.TrimSpace(r.Header.Get("X-Loom-Delete-Fingerprint"))
		data, err := svc.DeleteWorkspace(service.WithWorkspaceDeleteFingerprint(ctx, fingerprint), wsID)
		if err != nil {
			recordErr(span, err)
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, WorkspaceResponse{Success: true, Data: data})
	}
}
