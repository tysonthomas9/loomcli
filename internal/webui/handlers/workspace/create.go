package workspace

import (
	"encoding/json"
	"errors"
	"net/http"

	"go.opentelemetry.io/otel/attribute"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/workspacecoord"
)

// HandleWorkspaceCreate returns a handler that creates a new workspace.
func HandleWorkspaceCreate(svc workspacecoord.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, span := startSpan(r.Context(), "service.Workspace.Create")
		defer span.End()

		r.Body = http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)
		req, ok := decodeCreateRequest(w, r)
		if !ok {
			return
		}

		if req.Type == "clone" {
			span.SetAttributes(attribute.String("workspace.create.mode", "clone"))
			jobID, err := svc.StartAsyncCreate(ctx, req)
			if err != nil {
				recordErr(span, err)
				handler.HandleServiceError(w, err)
				return
			}
			handler.WriteJSON(w, http.StatusAccepted, map[string]any{"success": true, "job_id": jobID})
			return
		}

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

// decodeCreateRequest decodes the request body, writing an error response
// and returning ok=false on failure.
func decodeCreateRequest(w http.ResponseWriter, r *http.Request) (workspacecoord.WorkspaceCreateRequest, bool) {
	var req workspacecoord.WorkspaceCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxBytesErr *http.MaxBytesError
		status := http.StatusBadRequest
		msg := "invalid request body"
		if errors.As(err, &maxBytesErr) {
			status = http.StatusRequestEntityTooLarge
			msg = "request body too large"
		}
		handler.WriteJSON(w, status, WorkspaceResponse{Success: false, Error: msg})
		return req, false
	}
	return req, true
}
