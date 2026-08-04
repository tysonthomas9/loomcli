package issues

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// MoveIssueRequest is the JSON body for POST /api/issues/{id}/move.
type MoveIssueRequest struct {
	TargetWorkspace string `json:"target_workspace"`
}

// MoveIssueResponse is the JSON response for the move endpoint.
type MoveIssueResponse struct {
	Success bool        `json:"success"`
	Data    *MoveResult `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// MoveResult contains the outcome of a move operation.
type MoveResult struct {
	SourceID string   `json:"source_id"`
	TargetID string   `json:"target_id"`
	Warnings []string `json:"warnings,omitempty"`
}

// workspaceValidatorImpl implements service.WorkspaceValidator using FleetDB.
type workspaceValidatorImpl struct {
	store            store.Store
	currentWorkspace string
}

func (v *workspaceValidatorImpl) ValidateTarget(targetWorkspace string) (string, error) {
	if v.store == nil {
		return "", service.ErrValidation("workspace store not available")
	}

	targetKey, targetName, err := resolveWorkspaceRef(context.Background(), v.store, targetWorkspace)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return "", service.ErrValidation(fmt.Sprintf("workspace %q not found", targetWorkspace))
		}
		return "", service.ErrInternal("failed to load workspace", err)
	}

	if v.currentWorkspace != "" {
		currentKey, currentName, currentErr := resolveWorkspaceRef(context.Background(), v.store, v.currentWorkspace)
		if currentErr == nil {
			if targetKey == currentKey || (targetName != "" && targetName == currentName) {
				return "", service.ErrValidation("cannot move issue to the same workspace")
			}
		}
	}

	return targetKey, nil
}

func (v *workspaceValidatorImpl) CurrentWorkspace() string {
	return v.currentWorkspace
}

// handleMoveIssue returns a handler that moves an issue to a different workspace.
func HandleMoveIssue(svc service.IssueService, st store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		if issueID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, MoveIssueResponse{Success: false, Error: "missing issue ID in path"})
			return
		}

		req, ok := decodeMoveIssueRequest(w, r)
		if !ok {
			return
		}

		targetWorkspace := strings.TrimSpace(req.TargetWorkspace)
		if targetWorkspace == "" {
			handler.WriteJSON(w, http.StatusBadRequest, MoveIssueResponse{Success: false, Error: "target_workspace is required"})
			return
		}

		currentWorkspace := middleware.WorkspaceFromContext(r.Context())
		if currentWorkspace == "" {
			currentWorkspace = r.PathValue("ws")
		}
		validator := &workspaceValidatorImpl{store: st, currentWorkspace: currentWorkspace}

		result, err := svc.MoveIssue(r.Context(), service.MoveIssueParams{
			IssueID:         issueID,
			TargetWorkspace: targetWorkspace,
			Validator:       validator,
		})
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		if result == nil {
			handler.HandleServiceError(w, service.ErrInternal("move issue returned no result", nil))
			return
		}

		handler.WriteJSON(w, http.StatusOK, MoveIssueResponse{
			Success: true,
			Data: &MoveResult{
				SourceID: result.SourceID,
				TargetID: result.TargetID,
				Warnings: result.Warnings,
			},
		})
	}
}

func decodeMoveIssueRequest(w http.ResponseWriter, r *http.Request) (MoveIssueRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)
	var req MoveIssueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			handler.WriteJSON(w, http.StatusRequestEntityTooLarge, MoveIssueResponse{Success: false, Error: "request body too large (max 1MB)"})
			return MoveIssueRequest{}, false
		}
		handler.WriteJSON(w, http.StatusBadRequest, MoveIssueResponse{Success: false, Error: "invalid request body"})
		return MoveIssueRequest{}, false
	}
	return req, true
}

func resolveWorkspaceRef(ctx context.Context, st store.Store, ref string) (string, string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", "", domain.ErrNotFound
	}
	ws, getErr := st.Workspaces().Get(ctx, ref)
	if getErr == nil && ws != nil {
		return ws.Key, ws.Name, nil
	}
	ws, err := st.Workspaces().GetByName(ctx, ref)
	if err != nil {
		return "", "", err
	}
	return ws.Key, ws.Name, nil
}
