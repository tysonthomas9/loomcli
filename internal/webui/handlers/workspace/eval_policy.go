package workspace

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

type WorkspaceEvalPolicyPatchRequest struct {
	EvalSamplingPercent *int `json:"eval_sampling_percent,omitempty"`
	EvalBatchSize       *int `json:"eval_batch_size,omitempty"`
	EvalLookbackDays    *int `json:"eval_lookback_days,omitempty"`
}

func HandleWorkspaceEvalPolicyPatch(svc service.WorkspaceService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		if wsID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, WorkspaceResponse{Success: false, Error: "workspace ID is required"})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)
		var req WorkspaceEvalPolicyPatchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				handler.WriteJSON(w, http.StatusRequestEntityTooLarge, WorkspaceResponse{Success: false, Error: "request body too large"})
				return
			}
			handler.WriteJSON(w, http.StatusBadRequest, WorkspaceResponse{Success: false, Error: "invalid request body"})
			return
		}
		patch := service.WorkspaceEvalPolicyPatch{
			EvalSamplingPercent: req.EvalSamplingPercent,
			EvalBatchSize:       req.EvalBatchSize,
			EvalLookbackDays:    req.EvalLookbackDays,
		}
		if err := service.ValidateWorkspaceEvalPolicyPatch(patch); err != nil {
			handler.WriteJSON(w, http.StatusBadRequest, WorkspaceResponse{Success: false, Error: err.Error()})
			return
		}

		data, err := svc.PatchWorkspaceEvalPolicy(r.Context(), wsID, patch)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, WorkspaceResponse{Success: true, Data: data})
	}
}
