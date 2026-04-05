package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

const maxCommentLength = 64 * 1024 // 64KB

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

// commentAdder is an internal interface for testing comment operations.
// The production code uses *rpc.Client which implements this interface.
type commentAdder interface {
	AddComment(args *rpc.CommentAddArgs) (*rpc.Response, error)
}

// commentConnectionGetter is an internal interface for testing connection pool operations.
type commentConnectionGetter interface {
	Get(ctx context.Context) (commentAdder, error)
	Put(client commentAdder)
	Discard(client commentAdder)
}

// commentPoolAdapter wraps daemon.Pool to implement commentConnectionGetter.
type commentPoolAdapter struct {
	pool daemon.Pool
}

func (p *commentPoolAdapter) Get(ctx context.Context) (commentAdder, error) {
	return p.pool.Get(ctx)
}

func (p *commentPoolAdapter) Put(client commentAdder) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Put(c)
	}
}

func (p *commentPoolAdapter) Discard(client commentAdder) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Discard(c)
	}
}

// handleAddComment returns a handler that adds a comment to an issue.
func handleAddComment(pool daemon.Pool) http.HandlerFunc {
	if pool == nil {
		return handleAddCommentWithPool(nil)
	}
	return handleAddCommentWithPool(&commentPoolAdapter{pool: pool})
}

// handleAddCommentWithPool is the internal implementation that accepts an interface for testing.
func handleAddCommentWithPool(pool commentConnectionGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Extract issue ID from path parameter
		issueID := r.PathValue("id")
		if issueID == "" {
			respondJSON(w, http.StatusBadRequest, CommentResponse{
				Success: false,
				Error:   "missing issue ID",
			})
			return
		}

		// Check pool availability
		if pool == nil {
			respondJSON(w, http.StatusServiceUnavailable, CommentResponse{
				Success: false,
				Error:   "connection pool not initialized",
			})
			return
		}

		// Parse request body
		var req CommentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			log.Printf("Invalid request body in handleAddComment: %v", err)
			respondJSON(w, http.StatusBadRequest, CommentResponse{
				Success: false,
				Error:   "invalid request body",
			})
			return
		}

		// Validate text is not empty
		text := strings.TrimSpace(req.Text)
		if text == "" {
			respondJSON(w, http.StatusBadRequest, CommentResponse{
				Success: false,
				Error:   "comment text is required",
			})
			return
		}

		// Validate text length
		if len(text) > maxCommentLength {
			respondJSON(w, http.StatusBadRequest, CommentResponse{
				Success: false,
				Error:   fmt.Sprintf("comment text too long (%d bytes, max %d)", len(text), maxCommentLength),
			})
			return
		}

		// Acquire connection with timeout
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		client, err := pool.Get(ctx)
		if err != nil {
			status := http.StatusServiceUnavailable
			if errors.Is(err, context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
			}
			log.Printf("Pool error in handleAddComment: %v", err)
			respondJSON(w, status, CommentResponse{
				Success: false,
				Error:   "daemon not available",
			})
			return
		}
		rpcOK := false
		defer func() {
			if rpcOK {
				pool.Put(client)
			} else {
				pool.Discard(client)
			}
		}()

		// Build CommentAddArgs and call RPC
		args := &rpc.CommentAddArgs{
			ID:     issueID,
			Author: "web-ui", // Default author for web UI comments
			Text:   text,
		}

		resp, err := client.AddComment(args)
		if err != nil {
			errMsg := err.Error()
			status := http.StatusInternalServerError
			if strings.Contains(errMsg, "not found") {
				status = http.StatusNotFound
			}
			log.Printf("RPC error in handleAddComment: %v", err)
			respondJSON(w, status, CommentResponse{
				Success: false,
				Error:   "internal server error",
			})
			return
		}
		rpcOK = true

		if !resp.Success {
			status := http.StatusInternalServerError
			if strings.Contains(resp.Error, "not found") {
				status = http.StatusNotFound
			}
			respondJSON(w, status, CommentResponse{
				Success: false,
				Error:   resp.Error,
			})
			return
		}

		// Parse the created comment from response
		var comment types.Comment
		if err := json.Unmarshal(resp.Data, &comment); err != nil {
			respondJSON(w, http.StatusInternalServerError, CommentResponse{
				Success: false,
				Error:   fmt.Sprintf("failed to parse comment: %v", err),
			})
			return
		}

		respondJSON(w, http.StatusCreated, CommentResponse{
			Success: true,
			Data:    &comment,
		})
	}
}
