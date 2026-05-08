package workspace

import (
	"encoding/json"
	"errors"
	"net/http"

	"go.opentelemetry.io/otel/attribute"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// HandleWorkspaceCreate returns a handler that creates a new workspace.
func HandleWorkspaceCreate(svc service.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, span := startSpan(r.Context(), "service.Workspace.Create")
		defer span.End()

		r.Body = http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)

		var req service.WorkspaceCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				handler.WriteJSON(w, http.StatusRequestEntityTooLarge, WorkspaceResponse{
					Success: false,
					Error:   "request body too large",
				})
				return
			}
			handler.WriteJSON(w, http.StatusBadRequest, WorkspaceResponse{
				Success: false,
				Error:   "invalid request body",
			})
			return
		}

		// Async path for clone workspaces
		if req.Type == "clone" {
			span.SetAttributes(attribute.String("workspace.create.mode", "clone"))
			jobID, err := svc.StartAsyncCreate(ctx, req)
			if err != nil {
				recordErr(span, err)
				handler.HandleServiceError(w, err)
				return
			} else {
				handler.WriteJSON(w, http.StatusAccepted, map[string]any{"success": true, "job_id": jobID})
				return
			}
		}

		// Sync path
		span.SetAttributes(attribute.String("workspace.create.mode", "sync"))
		data, warnings, err := svc.CreateWorkspace(ctx, req)
		if err != nil {
			recordErr(span, err)
			handler.HandleServiceError(w, err)
			return
		}
		if data != nil {
			span.SetAttributes(attribute.String("loom.workspace", data.ID))
		}
		resp := WorkspaceResponse{Success: true, Data: data}
		if len(warnings) > 0 {
			resp.Warnings = warnings
		}
		handler.WriteJSON(w, http.StatusCreated, resp)
	}
}
