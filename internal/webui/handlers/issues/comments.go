package issues

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// CommentRequest represents the JSON body for creating a comment.
//
// Accepts both "text" and "body" keys on the wire. The backends diverge
// on field naming (beads uses "text", fleet-db uses "body") and clients
// have historically sent whichever matched their backend. Covering both
// here keeps the handler dialect-agnostic; Content() returns whichever
// was set, with "text" winning when both are present.
type CommentRequest struct {
	Text string `json:"text,omitempty"`
	Body string `json:"body,omitempty"`
}

// Content returns the comment text regardless of which JSON key the
// caller supplied. "text" wins if both are set (beads' canonical name).
func (r CommentRequest) Content() string {
	if r.Text != "" {
		return r.Text
	}
	return r.Body
}

// CommentResponse wraps the comment data for JSON response.
type CommentResponse struct {
	Success bool           `json:"success"`
	Data    *types.Comment `json:"data,omitempty"`
	Error   string         `json:"error,omitempty"`
}

// CommentListResponse wraps the comment list data for JSON response.
// Data is always a (possibly empty) slice so the FE sees a stable shape.
type CommentListResponse struct {
	Success bool             `json:"success"`
	Data    []*types.Comment `json:"data"`
	Error   string           `json:"error,omitempty"`
}

// HandleListComments returns a handler that lists comments for an issue.
// Always returns {success:true, data:[]} for issues with no comments rather
// than nil, matching the parity test's expectation of a list shape.
func HandleListComments(svc service.IssueService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		if issueID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, CommentListResponse{
				Success: false,
				Data:    []*types.Comment{},
				Error:   "missing issue ID",
			})
			return
		}

		comments, err := svc.ListComments(r.Context(), issueID)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		if comments == nil {
			comments = []*types.Comment{}
		}

		handler.WriteJSON(w, http.StatusOK, CommentListResponse{
			Success: true,
			Data:    comments,
		})
	}
}

// handleAddComment returns a handler that adds a comment to an issue.
func HandleAddComment(svc service.IssueService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		if issueID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, CommentResponse{
				Success: false,
				Error:   "missing issue ID",
			})
			return
		}

		var req CommentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			slog.Warn("invalid request body in handleAddComment", "err", err)
			handler.WriteJSON(w, http.StatusBadRequest, CommentResponse{
				Success: false,
				Error:   "invalid request body",
			})
			return
		}

		comment, err := svc.AddComment(r.Context(), service.AddCommentParams{
			IssueID: issueID,
			Author:  "web-ui",
			Text:    req.Content(),
		})
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusCreated, CommentResponse{
			Success: true,
			Data:    comment,
		})
	}
}
