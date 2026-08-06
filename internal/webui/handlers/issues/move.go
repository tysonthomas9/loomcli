package issues

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/app/workitemmove"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// HandleMoveWorkItem routes cross-workspace movement through the named
// coordinator, which composes Workspace queries with Work Items commands.
func HandleMoveWorkItem(mover workitemmove.Commands) http.HandlerFunc {
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
		if mover == nil {
			handler.HandleWorkItemsError(w, workitems.ErrUnavailable)
			return
		}
		currentWorkspace := middleware.WorkspaceFromContext(r.Context())
		if currentWorkspace == "" {
			currentWorkspace = r.PathValue("ws")
		}
		result, err := mover.Move(r.Context(), workitemmove.Command{
			IssueID: issueID, SourceWorkspace: currentWorkspace, TargetWorkspace: targetWorkspace,
		})
		if err != nil {
			handler.HandleWorkItemsError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, MoveIssueResponse{Success: true, Data: &MoveResult{
			SourceID: result.SourceID, TargetID: result.TargetID, Warnings: result.Warnings,
		}})
	}
}

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
