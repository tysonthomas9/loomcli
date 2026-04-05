package webui

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// handlePatchIssue returns a handler that performs partial updates on an issue.
func handlePatchIssue(svc service.IssueService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID, req, ok := validatePatchRequest(w, r)
		if !ok {
			return
		}

		params := service.PatchIssueParams{
			IssueID:            issueID,
			Title:              req.Title,
			Description:        req.Description,
			Status:             req.Status,
			Priority:           req.Priority,
			Assignee:           req.Assignee,
			Owner:              req.Owner,
			Design:             req.Design,
			AcceptanceCriteria: req.AcceptanceCriteria,
			Notes:              req.Notes,
			ExternalRef:        req.ExternalRef,
			EstimatedMinutes:   req.EstimatedMinutes,
			IssueType:          req.IssueType,
			AddLabels:          req.AddLabels,
			RemoveLabels:       req.RemoveLabels,
			SetLabels:          req.SetLabels,
			Pinned:             req.Pinned,
			Parent:             req.Parent,
			DueAt:              req.DueAt,
			DeferUntil:         req.DeferUntil,
			AgentState:         req.AgentState,
		}

		if err := svc.PatchIssue(r.Context(), params); err != nil {
			WriteServiceError(w, err)
			return
		}

		respondJSON(w, http.StatusOK, PatchIssueResponse{
			Success: true,
			Data:    map[string]string{"id": issueID, "status": "updated"},
		})
	}
}

// validatePatchRequest extracts the issue ID and parses the JSON body from an HTTP request.
func validatePatchRequest(w http.ResponseWriter, r *http.Request) (string, *PatchIssueRequest, bool) {
	issueID := r.PathValue("id")
	if issueID == "" {
		respondJSON(w, http.StatusBadRequest, PatchIssueResponse{
			Success: false,
			Error:   "missing issue ID in path",
		})
		return "", nil, false
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)

	var req PatchIssueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			respondJSON(w, http.StatusRequestEntityTooLarge, PatchIssueResponse{
				Success: false,
				Error:   "request body too large (max 1MB)",
			})
			return "", nil, false
		}
		logger.Warn("invalid request body in handlePatchIssue", "err", err)
		respondJSON(w, http.StatusBadRequest, PatchIssueResponse{
			Success: false,
			Error:   "invalid request body",
		})
		return "", nil, false
	}

	return issueID, &req, true
}
