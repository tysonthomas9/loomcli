package webui

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

// EventListResponse wraps the event list data for JSON response.
type EventListResponse struct {
	Success bool           `json:"success"`
	Data    []*types.Event `json:"data,omitempty"`
	Error   string         `json:"error,omitempty"`
}

// eventLister is an internal interface for testing event list operations.
type eventLister interface {
	ListEvents(args *rpc.EventListArgs) (*rpc.Response, error)
}

// eventConnectionGetter is an internal interface for testing connection pool operations.
type eventConnectionGetter interface {
	Get(ctx context.Context) (eventLister, error)
	Put(client eventLister)
	Discard(client eventLister)
}

// eventPoolAdapter wraps daemon.Pool to implement eventConnectionGetter.
type eventPoolAdapter struct {
	pool daemon.Pool
}

func (p *eventPoolAdapter) Get(ctx context.Context) (eventLister, error) {
	return p.pool.Get(ctx)
}

func (p *eventPoolAdapter) Put(client eventLister) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Put(c)
	}
}

func (p *eventPoolAdapter) Discard(client eventLister) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Discard(c)
	}
}

// parseEventLimit parses and clamps the limit query parameter.
func parseEventLimit(r *http.Request) int {
	const defaultLimit = 100
	const maxLimit = 500

	limitStr := r.URL.Query().Get("limit")
	if limitStr == "" {
		return defaultLimit
	}

	parsed, err := strconv.Atoi(limitStr)
	if err != nil || parsed <= 0 {
		return defaultLimit
	}
	if parsed > maxLimit {
		return maxLimit
	}
	return parsed
}

// httpStatusForError returns an appropriate HTTP status code based on an error message.
func httpStatusForError(msg string) int {
	if strings.Contains(msg, "not found") {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

// handleGetIssueEvents returns a handler that lists events for an issue.
func handleGetIssueEvents(pool daemon.Pool) http.HandlerFunc {
	if pool == nil {
		return handleGetIssueEventsWithPool(nil)
	}
	return handleGetIssueEventsWithPool(&eventPoolAdapter{pool: pool})
}

// handleGetIssueEventsWithPool is the internal implementation that accepts an interface for testing.
func handleGetIssueEventsWithPool(pool eventConnectionGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		if issueID == "" {
			respondJSON(w, http.StatusBadRequest, EventListResponse{
				Success: false,
				Error:   "missing issue ID",
			})
			return
		}

		if pool == nil {
			respondJSON(w, http.StatusServiceUnavailable, EventListResponse{
				Success: false,
				Error:   "connection pool not initialized",
			})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		client, err := pool.Get(ctx)
		if err != nil {
			status := http.StatusServiceUnavailable
			if errors.Is(err, context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
			}
			log.Printf("Pool error in handleGetIssueEvents: %v", err)
			respondJSON(w, status, EventListResponse{
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

		resp, err := client.ListEvents(&rpc.EventListArgs{
			ID:    issueID,
			Limit: parseEventLimit(r),
		})
		if err != nil {
			log.Printf("RPC error in handleGetIssueEvents: %v", err)
			respondJSON(w, httpStatusForError(err.Error()), EventListResponse{
				Success: false,
				Error:   "internal server error",
			})
			return
		}

		if !resp.Success {
			respondJSON(w, httpStatusForError(resp.Error), EventListResponse{
				Success: false,
				Error:   resp.Error,
			})
			return
		}

		var events []*types.Event
		if err := json.Unmarshal(resp.Data, &events); err != nil {
			respondJSON(w, http.StatusInternalServerError, EventListResponse{
				Success: false,
				Error:   "failed to parse events",
			})
			return
		}

		if events == nil {
			events = []*types.Event{}
		}

		rpcOK = true
		respondJSON(w, http.StatusOK, EventListResponse{
			Success: true,
			Data:    events,
		})
	}
}
