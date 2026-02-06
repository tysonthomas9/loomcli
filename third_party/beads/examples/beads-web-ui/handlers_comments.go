package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/steveyegge/beads/examples/beads-web-ui/daemon"
	"github.com/steveyegge/beads/internal/rpc"
	"github.com/steveyegge/beads/internal/types"
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

// commentAdder is an internal interface for testing comment operations.
// The production code uses *rpc.Client which implements this interface.
type commentAdder interface {
	AddComment(args *rpc.CommentAddArgs) (*rpc.Response, error)
}

// commentConnectionGetter is an internal interface for testing connection pool operations.
type commentConnectionGetter interface {
	Get(ctx context.Context) (commentAdder, error)
	Put(client commentAdder)
}

// commentPoolAdapter wraps *daemon.ConnectionPool to implement commentConnectionGetter.
type commentPoolAdapter struct {
	pool *daemon.ConnectionPool
}

func (p *commentPoolAdapter) Get(ctx context.Context) (commentAdder, error) {
	return p.pool.Get(ctx)
}

func (p *commentPoolAdapter) Put(client commentAdder) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Put(c)
	}
}

// handleAddComment returns a handler that adds a comment to an issue.
func handleAddComment(pool *daemon.ConnectionPool) http.HandlerFunc {
	if pool == nil {
		return handleAddCommentWithPool(nil)
	}
	return handleAddCommentWithPool(&commentPoolAdapter{pool: pool})
}

// handleAddCommentWithPool is the internal implementation that accepts an interface for testing.
func handleAddCommentWithPool(pool commentConnectionGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Extract issue ID from path parameter
		issueID := r.PathValue("id")
		if issueID == "" {
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(CommentResponse{
				Success: false,
				Error:   "missing issue ID",
			}); err != nil {
				log.Printf("Failed to encode comment response: %v", err)
			}
			return
		}

		// Check pool availability
		if pool == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			if err := json.NewEncoder(w).Encode(CommentResponse{
				Success: false,
				Error:   "connection pool not initialized",
			}); err != nil {
				log.Printf("Failed to encode comment response: %v", err)
			}
			return
		}

		// Parse request body
		var req CommentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(CommentResponse{
				Success: false,
				Error:   fmt.Sprintf("invalid request body: %v", err),
			}); err != nil {
				log.Printf("Failed to encode comment response: %v", err)
			}
			return
		}

		// Validate text is not empty
		text := strings.TrimSpace(req.Text)
		if text == "" {
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(CommentResponse{
				Success: false,
				Error:   "comment text is required",
			}); err != nil {
				log.Printf("Failed to encode comment response: %v", err)
			}
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
			w.WriteHeader(status)
			if err := json.NewEncoder(w).Encode(CommentResponse{
				Success: false,
				Error:   err.Error(),
			}); err != nil {
				log.Printf("Failed to encode comment response: %v", err)
			}
			return
		}
		defer pool.Put(client)

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
			w.WriteHeader(status)
			if err := json.NewEncoder(w).Encode(CommentResponse{
				Success: false,
				Error:   fmt.Sprintf("rpc error: %v", err),
			}); err != nil {
				log.Printf("Failed to encode comment response: %v", err)
			}
			return
		}

		if !resp.Success {
			status := http.StatusInternalServerError
			if strings.Contains(resp.Error, "not found") {
				status = http.StatusNotFound
			}
			w.WriteHeader(status)
			if err := json.NewEncoder(w).Encode(CommentResponse{
				Success: false,
				Error:   resp.Error,
			}); err != nil {
				log.Printf("Failed to encode comment response: %v", err)
			}
			return
		}

		// Parse the created comment from response
		var comment types.Comment
		if err := json.Unmarshal(resp.Data, &comment); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(CommentResponse{
				Success: false,
				Error:   fmt.Sprintf("failed to parse comment: %v", err),
			}); err != nil {
				log.Printf("Failed to encode comment response: %v", err)
			}
			return
		}

		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(CommentResponse{
			Success: true,
			Data:    &comment,
		}); err != nil {
			log.Printf("Failed to encode comment response: %v", err)
		}
	}
}
