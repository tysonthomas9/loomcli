package webui

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// AddDependencyRequest represents the POST body for adding a dependency.
type AddDependencyRequest struct {
	DependsOnID string `json:"depends_on_id"`
	DepType     string `json:"dep_type,omitempty"` // defaults to "blocks"
}

// DependencyResponse wraps the dependency operation result for JSON response.
// Follows the same structure as other API responses for consistency.
type DependencyResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// handleAddDependency creates a dependency from the issue to another issue.
func handleAddDependency(svc service.IssueService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		if issueID == "" {
			respondJSON(w, http.StatusBadRequest, DependencyResponse{
				Success: false,
				Error:   "missing issue ID",
			})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)

		var req AddDependencyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				respondJSON(w, http.StatusRequestEntityTooLarge, DependencyResponse{
					Success: false,
					Error:   "request body too large (max 1MB)",
				})
				return
			}
			logger.Warn("invalid request body in handleAddDependency", "err", err)
			respondJSON(w, http.StatusBadRequest, DependencyResponse{
				Success: false,
				Error:   "invalid request body",
			})
			return
		}

		err := svc.AddDependency(r.Context(), service.AddDependencyParams{
			IssueID:     issueID,
			DependsOnID: req.DependsOnID,
			DepType:     req.DepType,
		})
		if err != nil {
			WriteServiceError(w, err)
			return
		}

		respondJSON(w, http.StatusOK, DependencyResponse{
			Success: true,
			Data:    nil,
		})
	}
}

// handleRemoveDependency removes a dependency from the issue.
func handleRemoveDependency(svc service.IssueService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		depID := r.PathValue("depId")

		if issueID == "" {
			respondJSON(w, http.StatusBadRequest, DependencyResponse{
				Success: false,
				Error:   "missing issue ID",
			})
			return
		}

		if depID == "" {
			respondJSON(w, http.StatusBadRequest, DependencyResponse{
				Success: false,
				Error:   "missing dependency ID",
			})
			return
		}

		err := svc.RemoveDependency(r.Context(), service.RemoveDependencyParams{
			IssueID: issueID,
			DepID:   depID,
		})
		if err != nil {
			WriteServiceError(w, err)
			return
		}

		respondJSON(w, http.StatusOK, DependencyResponse{
			Success: true,
			Data:    nil,
		})
	}
}
