package issues

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// handleCreateIssue returns a handler that creates a new issue.
func HandleCreateIssue(svc service.IssueService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)

		var req IssueCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				writeIssuesError(w, http.StatusRequestEntityTooLarge, "request body too large (max 1MB)", "REQUEST_TOO_LARGE")
				return
			}
			slog.Warn("invalid JSON body in handleCreateIssue", "err", err)
			writeIssuesError(w, http.StatusBadRequest, "invalid request body", "INVALID_JSON")
			return
		}

		params := service.CreateIssueParams{
			Title:              req.Title,
			IssueType:          req.IssueType,
			Priority:           req.Priority,
			ID:                 req.ID,
			Parent:             req.Parent,
			Description:        req.Description,
			Design:             req.Design,
			AcceptanceCriteria: req.AcceptanceCriteria,
			Notes:              req.Notes,
			Assignee:           req.Assignee,
			Owner:              req.Owner,
			CreatedBy:          req.CreatedBy,
			ExternalRef:        req.ExternalRef,
			EstimatedMinutes:   req.EstimatedMinutes,
			Labels:             req.Labels,
			Dependencies:       req.Dependencies,
			DueAt:              req.DueAt,
			DeferUntil:         req.DeferUntil,
			SourceRepo:         req.SourceRepo,
		}

		data, err := svc.CreateIssue(r.Context(), params)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusCreated, IssuesResponse{
			Success: true,
			Data:    data,
		})
	}
}

// handleCloseIssue returns a handler that closes an issue by ID.
func HandleCloseIssue(svc service.IssueService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		if issueID == "" {
			handler.RespondError(w, http.StatusBadRequest, "missing issue ID")
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)

		var req CloseRequest
		if r.Body != nil && r.ContentLength > 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				var maxBytesErr *http.MaxBytesError
				if errors.As(err, &maxBytesErr) {
					handler.RespondError(w, http.StatusRequestEntityTooLarge, "request body too large (max 1MB)")
					return
				}
				slog.Warn("invalid request body in handleCloseIssue", "err", err)
				handler.RespondError(w, http.StatusBadRequest, "invalid request body")
				return
			}
		}

		params := service.CloseIssueParams{
			IssueID:     issueID,
			Reason:      req.Reason,
			Session:     req.Session,
			SuggestNext: req.SuggestNext,
			Force:       req.Force,
		}

		data, err := svc.CloseIssue(r.Context(), params)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, CloseResponse{
			Success: true,
			Data:    data,
		})
	}
}

// handleDeleteIssue returns a handler that permanently deletes an issue by ID.
func HandleDeleteIssue(svc service.IssueService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		if issueID == "" {
			handler.RespondError(w, http.StatusBadRequest, "missing issue ID")
			return
		}

		data, err := svc.DeleteIssue(r.Context(), issueID)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"data":    data,
		})
	}
}
