package stacks

import (
	"errors"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

type stacksResponse struct {
	Success bool                           `json:"success"`
	Data    *service.WorkspaceStacksResult `json:"data,omitempty"`
	Error   string                         `json:"error,omitempty"`
}

func writeStacksError(w http.ResponseWriter, err error) {
	var svcErr *service.ServiceError
	if errors.As(err, &svcErr) {
		handler.WriteJSON(w, handler.StatusForKind(svcErr.Kind), stacksResponse{
			Success: false,
			Error:   svcErr.Message,
		})
		return
	}
	handler.WriteJSON(w, http.StatusInternalServerError, stacksResponse{
		Success: false,
		Error:   "internal server error",
	})
}

// HandleListStacks handles GET /api/workspaces/{ws}/stacks.
func HandleListStacks(svc service.StackService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeStacksError(w, service.ErrUnavailable("stack service unavailable"))
			return
		}
		wsID := middleware.WorkspaceFromContext(r.Context())
		result, err := svc.ListStacks(r.Context(), wsID)
		if err != nil {
			writeStacksError(w, err)
			return
		}
		if result == nil {
			result = &service.WorkspaceStacksResult{Stacks: []service.WorkspaceStack{}}
		}
		handler.WriteJSON(w, http.StatusOK, stacksResponse{Success: true, Data: result})
	}
}
