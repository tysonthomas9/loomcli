package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

type WorkspaceTaskDeliveryPatchRequest struct {
	Repository              string                         `json:"repository,omitempty"`
	TaskDeliveryRequirement domain.TaskDeliveryRequirement `json:"task_delivery_requirement"`
}

// HandleWorkspaceTaskDeliveryPatch persists a workspace requirement or one
// repository override. An empty repository requirement means inherit.
func HandleWorkspaceTaskDeliveryPatch(svc service.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		if wsID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, WorkspaceResponse{Success: false, Error: "workspace ID is required"})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)
		var req WorkspaceTaskDeliveryPatchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				handler.WriteJSON(w, http.StatusRequestEntityTooLarge, WorkspaceResponse{Success: false, Error: "request body too large"})
				return
			}
			handler.WriteJSON(w, http.StatusBadRequest, WorkspaceResponse{Success: false, Error: "invalid request body"})
			return
		}
		if !req.TaskDeliveryRequirement.Valid() || (req.Repository == "" && req.TaskDeliveryRequirement == "") {
			handler.WriteJSON(w, http.StatusBadRequest, WorkspaceResponse{
				Success: false,
				Error:   fmt.Sprintf("invalid task delivery requirement %q", req.TaskDeliveryRequirement),
			})
			return
		}

		data, err := svc.PatchWorkspaceTaskDelivery(r.Context(), wsID, req.Repository, req.TaskDeliveryRequirement)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, WorkspaceResponse{Success: true, Data: data})
	}
}
