package issues

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/workitemmove"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// HandleMoveWorkItem routes cross-workspace movement through the named
// coordinator, which composes Workspace queries with Work Items commands.
func HandleMoveWorkItem(mover handler.WorkItemMover) http.HandlerFunc {
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
		requestID := strings.TrimSpace(req.RequestID)
		if req.ExpectedSourceRevision.IsZero() || requestID == "" || len(requestID) > 200 {
			handler.WriteJSON(w, http.StatusBadRequest, MoveIssueResponse{Success: false, Error: "expected_source_revision and a request_id of at most 200 bytes are required"})
			return
		}
		if mover == nil {
			writeMoveIssueError(w, workitems.ErrUnavailable)
			return
		}
		currentWorkspace := middleware.WorkspaceFromContext(r.Context())
		if currentWorkspace == "" {
			currentWorkspace = r.PathValue("ws")
		}
		result, err := mover.Move(r.Context(), workitemmove.Command{
			IssueID: issueID, SourceWorkspace: currentWorkspace, TargetWorkspace: targetWorkspace,
			ExpectedSourceRevision: req.ExpectedSourceRevision, RequestID: requestID,
		})
		if err != nil {
			writeMoveIssueError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, MoveIssueResponse{Success: true, Data: result})
	}
}

func writeMoveIssueError(w http.ResponseWriter, err error) {
	if errors.Is(err, workitemmove.ErrForbidden) {
		handler.WriteJSON(w, http.StatusForbidden, MoveIssueResponse{Success: false, Error: "source or target workspace access was denied"})
		return
	}
	status, message := handler.WorkItemsHTTPError(err)
	handler.WriteJSON(w, status, MoveIssueResponse{Success: false, Error: message})
}

// MoveIssueRequest is the JSON body for POST /api/issues/{id}/move.
type MoveIssueRequest struct {
	TargetWorkspace        string    `json:"target_workspace"`
	ExpectedSourceRevision time.Time `json:"expected_source_revision"`
	RequestID              string    `json:"request_id"`
}

// MoveIssueResponse is the JSON response for the move endpoint.
type MoveIssueResponse struct {
	Success bool                 `json:"success"`
	Data    *workitemmove.Result `json:"data,omitempty"`
	Error   string               `json:"error,omitempty"`
}

func decodeMoveIssueRequest(w http.ResponseWriter, r *http.Request) (MoveIssueRequest, bool) {
	var req MoveIssueRequest
	if err := handler.DecodeOneJSON(w, r, &req, handler.JSONDecodeOptions{
		MaxBytes: handler.MaxRequestBody, DisallowUnknownFields: true,
	}); err != nil {
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
