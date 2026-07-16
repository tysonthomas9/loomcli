package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

type WorkspaceDesignFormatPatchRequest struct {
	DesignFormat string `json:"design_format"`
}

// HandleWorkspaceDesignFormatPatch persists the format used by planning
// agents and by the issue-design renderer for this workspace.
func HandleWorkspaceDesignFormatPatch(svc service.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		if wsID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, WorkspaceResponse{Success: false, Error: "workspace ID is required"})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)
		var req WorkspaceDesignFormatPatchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				handler.WriteJSON(w, http.StatusRequestEntityTooLarge, WorkspaceResponse{Success: false, Error: "request body too large"})
				return
			}
			handler.WriteJSON(w, http.StatusBadRequest, WorkspaceResponse{Success: false, Error: "invalid request body"})
			return
		}
		if !service.ValidWorkspaceDesignFormat(req.DesignFormat) {
			handler.WriteJSON(w, http.StatusBadRequest, WorkspaceResponse{
				Success: false,
				Error:   fmt.Sprintf("invalid design format %q; valid options: markdown, html", req.DesignFormat),
			})
			return
		}

		data, err := svc.PatchWorkspaceDesignFormat(r.Context(), wsID, req.DesignFormat)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, WorkspaceResponse{Success: true, Data: data})
	}
}
