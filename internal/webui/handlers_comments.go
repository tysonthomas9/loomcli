package webui

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// CommentRequest represents the JSON body for creating a comment.
type CommentRequest struct {
	Text string `json:"text"`
}

// CommentResponse wraps the comment data for JSON response.
type CommentResponse struct {
	Success bool           `json:"success"`
	Data    *types.Comment `json:"data,omitempty"`
	Error   string         `json:"error,omitempty"`
}

// handleAddComment returns a handler that adds a comment to an issue.
func handleAddComment(svc service.IssueService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		if issueID == "" {
			respondJSON(w, http.StatusBadRequest, CommentResponse{
				Success: false,
				Error:   "missing issue ID",
			})
			return
		}

		var req CommentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			slog.Warn("invalid request body in handleAddComment", "err", err)
			respondJSON(w, http.StatusBadRequest, CommentResponse{
				Success: false,
				Error:   "invalid request body",
			})
			return
		}

		comment, err := svc.AddComment(r.Context(), service.AddCommentParams{
			IssueID: issueID,
			Author:  "web-ui",
			Text:    req.Text,
		})
		if err != nil {
			writeServiceError(w, err)
			return
		}

		respondJSON(w, http.StatusCreated, CommentResponse{
			Success: true,
			Data:    comment,
		})
	}
}
