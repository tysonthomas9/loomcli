package issues

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

// CommentRequest represents the JSON body for creating a comment.
//
// Accepts both "text" and "body" keys on the wire. Covering both here keeps
// the handler dialect-agnostic; Content returns whichever was set, with
// "text" winning when both are present.
type CommentRequest struct {
	Text string `json:"text,omitempty"`
	Body string `json:"body,omitempty"`
}

// HandleListWorkItemComments is the Work Items-owned route adapter.
func HandleListWorkItemComments(api workitems.API) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		if issueID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, CommentListResponse{Success: false, Data: []*types.Comment{}, Error: "missing issue ID"})
			return
		}
		if api == nil {
			handler.HandleWorkItemsError(w, workitems.ErrUnavailable)
			return
		}
		comments, err := api.ListComments(r.Context(), workitems.ListCommentsQuery{IssueID: issueID})
		if err != nil {
			handler.HandleWorkItemsError(w, err)
			return
		}
		if comments == nil {
			comments = []*workitems.Comment{}
		}
		handler.WriteJSON(w, http.StatusOK, CommentListResponse{Success: true, Data: comments})
	}
}

// HandleAddWorkItemComment adapts the HTTP dialect to the Work Items command.
func HandleAddWorkItemComment(api workitems.API) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		if issueID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, CommentResponse{Success: false, Error: "missing issue ID"})
			return
		}
		var req CommentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			slog.Warn("invalid request body in HandleAddWorkItemComment", "err", err)
			handler.WriteJSON(w, http.StatusBadRequest, CommentResponse{Success: false, Error: "invalid request body"})
			return
		}
		if api == nil {
			handler.HandleWorkItemsError(w, workitems.ErrUnavailable)
			return
		}
		comment, err := api.AddComment(r.Context(), workitems.AddCommentCommand{IssueID: issueID, Author: "web-ui", Text: req.Content()})
		if err != nil {
			handler.HandleWorkItemsError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusCreated, CommentResponse{Success: true, Data: comment})
	}
}

// Content returns the comment text regardless of which JSON key the
// caller supplied. "text" wins if both are set.
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
