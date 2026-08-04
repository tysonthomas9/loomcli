package issues

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// AddDependencyRequest represents the POST body for adding a dependency.
type AddDependencyRequest struct {
	DependsOnID string `json:"depends_on_id"`
	DepType     string `json:"dep_type,omitempty"` // defaults to "blocks"
}

// HandleListDependencies returns a handler that lists dependencies for an
// issue. The wire shape mirrors IssueDetailData.dependencies so the FE consumes
// it with no translation: each entry is {id, title, status, priority,
// dependency_type, created_at, ...}.
func HandleListDependencies(svc service.IssueService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		if issueID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, DependencyResponse{
				Success: false,
				Error:   "missing issue ID",
			})
			return
		}

		data, err := svc.ListDependencies(r.Context(), issueID)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, DependencyResponse{
			Success: true,
			Data:    data,
		})
	}
}

// DependencyResponse wraps the dependency operation result for JSON response.
// Follows the same structure as other API responses for consistency.
type DependencyResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// handleAddDependency creates a dependency from the issue to another issue.
func HandleAddDependency(svc service.IssueService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		if issueID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, DependencyResponse{
				Success: false,
				Error:   "missing issue ID",
			})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)

		var req AddDependencyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				handler.WriteJSON(w, http.StatusRequestEntityTooLarge, DependencyResponse{
					Success: false,
					Error:   "request body too large (max 1MB)",
				})
				return
			}
			slog.Warn("invalid request body in handleAddDependency", "err", err)
			handler.WriteJSON(w, http.StatusBadRequest, DependencyResponse{
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
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, DependencyResponse{
			Success: true,
			Data:    nil,
		})
	}
}

// handleRemoveDependency removes a dependency from the issue.
func HandleRemoveDependency(svc service.IssueService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		depID := r.PathValue("depId")

		if issueID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, DependencyResponse{
				Success: false,
				Error:   "missing issue ID",
			})
			return
		}

		if depID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, DependencyResponse{
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
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, DependencyResponse{
			Success: true,
			Data:    nil,
		})
	}
}
