package workspace

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"go.opentelemetry.io/otel/attribute"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// HandleListWorkspaces returns GET /api/workspaces — a list of all registered
// workspaces with basic status information.
func HandleListWorkspaces(svc service.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, span := startSpan(r.Context(), "service.Workspace.List")
		defer span.End()

		items, err := svc.ListWorkspaces(ctx)
		if err != nil {
			recordErr(span, err)
			handler.HandleServiceError(w, err)
			return
		}
		span.SetAttributes(attribute.Int("result.count", len(items)))
		handler.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success":    true,
			"workspaces": items,
		})
	}
}

// HandleGetWorkspace returns GET /api/workspaces/{ws} — full WorkspaceData
// (same shape as /api/workspaces/active) so the frontend uses the same
// unwrap<WorkspaceData>() logic for both endpoints.
func HandleGetWorkspace(svc service.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := strings.TrimSpace(r.PathValue("ws"))
		ctx, span := startSpan(r.Context(), "service.Workspace.Get",
			attribute.String("loom.workspace", wsID))
		defer span.End()

		if wsID == "" {
			handler.RespondError(w, http.StatusBadRequest, "workspace ID is required")
			return
		}
		data, err := svc.GetWorkspace(ctx, wsID)
		if err != nil {
			recordErr(span, err)
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, WorkspaceResponse{Success: true, Data: data})
	}
}

// HandleListWorkspaceRepos returns GET /api/workspaces/{ws}/repos — the
// workspace's repo list as a lightweight endpoint for clients that do not need
// the full WorkspaceData payload.
func HandleListWorkspaceRepos(svc service.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := strings.TrimSpace(r.PathValue("ws"))
		if wsID == "" {
			handler.RespondError(w, http.StatusBadRequest, "workspace ID is required")
			return
		}
		data, err := svc.GetWorkspace(r.Context(), wsID)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"repos":   data.Repos,
		})
	}
}

// HandleAddWorkspaceRepos returns POST /api/workspaces/{ws}/repos.
func HandleAddWorkspaceRepos(svc service.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := strings.TrimSpace(r.PathValue("ws"))
		ctx, span := startSpan(r.Context(), "service.Repo.Add",
			attribute.String("loom.workspace", wsID))
		defer span.End()

		if wsID == "" {
			handler.RespondError(w, http.StatusBadRequest, "workspace ID is required")
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)
		var req service.WorkspaceAddReposRequest
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
		// Annotate the span with how many repos this call adds. Names/URLs of
		// repos are user-supplied free-form strings (per redaction policy) so
		// we only emit the count here.
		span.SetAttributes(attribute.Int("repo.count", len(req.Repos)+len(req.CloneURLs)))

		data, err := svc.AddWorkspaceRepos(ctx, req)
		if err != nil {
			recordErr(span, err)
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusCreated, WorkspaceResponse{Success: true, Data: data})
	}
}
