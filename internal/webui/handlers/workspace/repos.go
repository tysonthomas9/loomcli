package workspace

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"go.opentelemetry.io/otel/attribute"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/workspacecoord"
)

// HandleAddWorkspaceRepos attaches and materializes repositories through the
// remaining machine-local workspace coordinator.
func HandleAddWorkspaceRepos(svc workspacecoord.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := workspaceIDFromRequest(r)
		ctx, span := startSpan(r.Context(), "service.Repo.Add", attribute.String("loom.workspace", wsID))
		defer span.End()

		if wsID == "" {
			handler.RespondError(w, http.StatusBadRequest, "workspace ID is required")
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)
		var req workspacecoord.WorkspaceAddReposRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				handler.RespondError(w, http.StatusRequestEntityTooLarge, "request body too large")
				return
			}
			handler.RespondError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		req.WorkspaceID = wsID
		span.SetAttributes(attribute.Int("repo.count", len(req.Repos)+len(req.CloneURLs)))

		if workspacecoord.WorkspaceAddReposRequiresClone(req) {
			jobID, err := svc.StartAsyncAddRepos(ctx, req)
			if err != nil {
				recordErr(span, err)
				handler.HandleServiceError(w, err)
				return
			}
			handler.WriteJSON(w, http.StatusAccepted, map[string]any{"success": true, "job_id": jobID})
			return
		}

		data, err := svc.AddWorkspaceRepos(ctx, req)
		if err != nil {
			recordErr(span, err)
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusCreated, WorkspaceResponse{Success: true, Data: data})
	}
}

func workspaceIDFromRequest(r *http.Request) string {
	if wsID := strings.TrimSpace(middleware.WorkspaceFromContext(r.Context())); wsID != "" {
		return wsID
	}
	return strings.TrimSpace(r.PathValue("ws"))
}
