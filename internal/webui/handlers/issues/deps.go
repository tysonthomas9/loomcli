package issues

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

// AddDependencyRequest represents the POST body for adding a dependency.
type AddDependencyRequest struct {
	DependsOnID string `json:"depends_on_id"`
	DepType     string `json:"dep_type,omitempty"` // defaults to "blocks"
}

// HandleListWorkItemDependencies is the Work Items-owned dependency query
// route. It preserves the existing JSON envelope and projection shape.
func HandleListWorkItemDependencies(api workitems.API) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		if issueID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, DependencyResponse{Success: false, Error: "missing issue ID"})
			return
		}
		if api == nil {
			handler.HandleWorkItemsError(w, workitems.ErrUnavailable)
			return
		}
		data, err := api.ListDependencies(r.Context(), workitems.ListDependenciesQuery{IssueID: issueID})
		if err != nil {
			handler.HandleWorkItemsError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, DependencyResponse{Success: true, Data: data})
	}
}

// HandleAddWorkItemDependency adapts one dependency create command.
func HandleAddWorkItemDependency(api workitems.API) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		if issueID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, DependencyResponse{Success: false, Error: "missing issue ID"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)
		var req AddDependencyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				handler.WriteJSON(w, http.StatusRequestEntityTooLarge, DependencyResponse{Success: false, Error: "request body too large (max 1MB)"})
				return
			}
			slog.Warn("invalid request body in HandleAddWorkItemDependency", "err", err)
			handler.WriteJSON(w, http.StatusBadRequest, DependencyResponse{Success: false, Error: "invalid request body"})
			return
		}
		if api == nil {
			handler.HandleWorkItemsError(w, workitems.ErrUnavailable)
			return
		}
		if err := api.AddDependency(r.Context(), workitems.AddDependencyCommand{IssueID: issueID, DependsOnID: req.DependsOnID, Type: req.DepType}); err != nil {
			handler.HandleWorkItemsError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, DependencyResponse{Success: true, Data: nil})
	}
}

// HandleRemoveWorkItemDependency adapts one dependency removal command.
func HandleRemoveWorkItemDependency(api workitems.API) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID, depID := r.PathValue("id"), r.PathValue("depId")
		if issueID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, DependencyResponse{Success: false, Error: "missing issue ID"})
			return
		}
		if depID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, DependencyResponse{Success: false, Error: "missing dependency ID"})
			return
		}
		if api == nil {
			handler.HandleWorkItemsError(w, workitems.ErrUnavailable)
			return
		}
		if err := api.RemoveDependency(r.Context(), workitems.RemoveDependencyCommand{IssueID: issueID, DependsOnID: depID}); err != nil {
			handler.HandleWorkItemsError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, DependencyResponse{Success: true, Data: nil})
	}
}

// DependencyResponse wraps the dependency operation result for JSON response.
// Follows the same structure as other API responses for consistency.
type DependencyResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}
