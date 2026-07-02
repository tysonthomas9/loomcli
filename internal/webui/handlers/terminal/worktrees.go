package terminal

import (
	"errors"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

type worktreeGroupListResponse struct {
	Groups any `json:"groups"`
}

type worktreeGroupErrorResponse struct {
	Error   string                `json:"error"`
	Kind    string                `json:"kind"`
	Results []WorktreeGroupResult `json:"results"`
}

// HandleListWorktreeGroups returns user-created terminal worktree groups.
func HandleListWorktreeGroups(svc *WorktreeGroupService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspace := middleware.WorkspaceFromContext(r.Context())
		groups, err := svc.List(r.Context(), workspace)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, worktreeGroupListResponse{Groups: groups})
	}
}

// HandleCreateWorktreeGroup creates a terminal worktree group.
func HandleCreateWorktreeGroup(svc *WorktreeGroupService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req CreateWorktreeGroupRequest
		if err := handler.ReadJSON(w, r, &req); err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		workspace := middleware.WorkspaceFromContext(r.Context())
		resp, err := svc.Create(r.Context(), workspace, req)
		if err != nil {
			if resp != nil {
				handleWorktreeGroupCreateError(w, err, resp.Results)
				return
			}
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusCreated, resp)
	}
}

func handleWorktreeGroupCreateError(w http.ResponseWriter, err error, results []WorktreeGroupResult) {
	var svcErr *service.ServiceError
	if errors.As(err, &svcErr) {
		handler.WriteJSON(w, handler.StatusForKind(svcErr.Kind), worktreeGroupErrorResponse{
			Error:   svcErr.Message,
			Kind:    string(svcErr.Kind),
			Results: results,
		})
		return
	}
	handler.WriteJSON(w, http.StatusInternalServerError, worktreeGroupErrorResponse{
		Error:   "internal server error",
		Kind:    string(service.KindInternal),
		Results: results,
	})
}
