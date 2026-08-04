package issues

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// HandlePatchWorkItem routes a partial aggregate update through the Work
// Items owner and returns the canonical post-update projection.
func HandlePatchWorkItem(api workitems.API) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID, req, ok := validatePatchRequest(w, r)
		if !ok {
			return
		}
		if api == nil {
			handler.HandleWorkItemsError(w, workitems.ErrUnavailable)
			return
		}
		value, err := api.Patch(r.Context(), workitems.PatchCommand{
			IssueID: issueID, Title: req.Title, Description: req.Description,
			Status: req.Status, Priority: req.Priority, Assignee: req.Assignee,
			Owner: req.Owner, Design: req.Design, DesignFormat: req.DesignFormat,
			AcceptanceCriteria: req.AcceptanceCriteria, Notes: req.Notes,
			ExternalRef: req.ExternalRef, EstimatedMinutes: req.EstimatedMinutes,
			IssueType: req.IssueType, AddLabels: req.AddLabels,
			RemoveLabels: req.RemoveLabels, SetLabels: req.SetLabels,
			Pinned: req.Pinned, Parent: req.Parent, DueAt: req.DueAt,
			DeferUntil: req.DeferUntil, AgentState: req.AgentState,
		})
		if err != nil {
			handler.HandleWorkItemsError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, PatchIssueResponse{Success: true, Data: value})
	}
}

// handlePatchIssue returns a handler that performs partial updates on an issue.
func HandlePatchIssue(svc service.IssueService) http.HandlerFunc {
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
			DesignFormat:       req.DesignFormat,
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
			handler.HandleServiceError(w, err)
			return
		}

		data, err := svc.GetIssue(r.Context(), issueID)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, PatchIssueResponse{
			Success: true,
			Data:    data,
		})
	}
}

// validatePatchRequest extracts the issue ID and parses the JSON body from an HTTP request.
func validatePatchRequest(w http.ResponseWriter, r *http.Request) (string, *PatchIssueRequest, bool) {
	issueID := r.PathValue("id")
	if issueID == "" {
		handler.WriteJSON(w, http.StatusBadRequest, PatchIssueResponse{
			Success: false,
			Error:   "missing issue ID in path",
		})
		return "", nil, false
	}

	r.Body = http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)

	var req PatchIssueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			handler.WriteJSON(w, http.StatusRequestEntityTooLarge, PatchIssueResponse{
				Success: false,
				Error:   "request body too large (max 1MB)",
			})
			return "", nil, false
		}
		slog.Warn("invalid request body in handlePatchIssue", "err", err)
		handler.WriteJSON(w, http.StatusBadRequest, PatchIssueResponse{
			Success: false,
			Error:   "invalid request body",
		})
		return "", nil, false
	}

	return issueID, &req, true
}
