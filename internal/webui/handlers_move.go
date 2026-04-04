package webui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

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

// workspaceValidatorImpl implements service.WorkspaceValidator using the webui workspace config.
type workspaceValidatorImpl struct {
	workspaceConfigFn func() (*service.WorkspaceData, error)
}

func (v *workspaceValidatorImpl) ValidateTarget(targetWorkspace string) (string, error) {
	if v.workspaceConfigFn == nil {
		return "", service.ErrValidation("workspace configuration not available")
	}

	wsData, err := v.workspaceConfigFn()
	if err != nil {
		return "", service.ErrInternal("failed to load workspace config", err)
	}

	found := false
	for _, ws := range wsData.Workspaces {
		if ws.Name == targetWorkspace {
			found = true
			break
		}
	}
	if !found {
		return "", service.ErrValidation(fmt.Sprintf("workspace %q not found", targetWorkspace))
	}

	if wsData.Name == targetWorkspace {
		return "", service.ErrValidation("cannot move issue to the same workspace")
	}

	// Resolve workspace name → ID
	for _, ws := range wsData.Workspaces {
		if ws.Name == targetWorkspace {
			if ws.ID != "" {
				return ws.ID, nil
			}
			return ws.Name, nil
		}
	}
	return targetWorkspace, nil
}

func (v *workspaceValidatorImpl) CurrentWorkspace() string {
	if v.workspaceConfigFn == nil {
		return ""
	}
	wsData, err := v.workspaceConfigFn()
	if err != nil {
		return ""
	}
	return wsData.Name
}

// handleMoveIssue returns a handler that moves an issue to a different workspace.
func handleMoveIssue(svc service.IssueService, workspaceConfigFn func() (*service.WorkspaceData, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		if issueID == "" {
			respondJSON(w, http.StatusBadRequest, MoveIssueResponse{Success: false, Error: "missing issue ID in path"})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		var req MoveIssueRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				respondJSON(w, http.StatusRequestEntityTooLarge, MoveIssueResponse{Success: false, Error: "request body too large (max 1MB)"})
				return
			}
			respondJSON(w, http.StatusBadRequest, MoveIssueResponse{Success: false, Error: "invalid request body"})
			return
		}

		targetWorkspace := strings.TrimSpace(req.TargetWorkspace)
		if targetWorkspace == "" {
			respondJSON(w, http.StatusBadRequest, MoveIssueResponse{Success: false, Error: "target_workspace is required"})
			return
		}

		validator := &workspaceValidatorImpl{workspaceConfigFn: workspaceConfigFn}

		result, err := svc.MoveIssue(r.Context(), service.MoveIssueParams{
			IssueID:         issueID,
			TargetWorkspace: targetWorkspace,
			Validator:       validator,
		})
		if err != nil {
			WriteServiceError(w, err)
			return
		}

		respondJSON(w, http.StatusOK, MoveIssueResponse{
			Success: true,
			Data: &MoveResult{
				SourceID: result.SourceID,
				TargetID: result.TargetID,
				Warnings: result.Warnings,
			},
		})
	}
}
