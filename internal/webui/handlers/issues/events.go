package issues

import (
	"net/http"
	"strconv"

	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// EventListResponse wraps the event list data for JSON response.
type EventListResponse struct {
	Success bool           `json:"success"`
	Data    []*types.Event `json:"data,omitempty"`
	// Cursor is present only for a since-paged response. A newest-tail response
	// has no cursor; clients page forward from since="" to retrieve its history.
	Cursor  string `json:"cursor,omitempty"`
	HasMore bool   `json:"has_more"`
	// TotalEvents is the full event count when the backend knows it. Zero means
	// unknown and is omitted rather than fabricated from the returned page.
	TotalEvents int    `json:"total_events,omitempty"`
	Error       string `json:"error,omitempty"`
}

// parseEventLimit parses and clamps the limit query parameter. Tail requests
// may ask Loom to aggregate up to 500 events, while since-paged requests are
// limited to fleet-db's 200-event per-page maximum and are never aggregated.
func parseEventLimit(r *http.Request) int {
	const defaultLimit = 100
	maxLimit := 500
	if r.URL.Query().Has("since") {
		maxLimit = 200
	}

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

// handleGetIssueEvents returns a handler that lists events for an issue.
func HandleGetIssueEvents(svc service.IssueService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		if issueID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, EventListResponse{
				Success: false,
				Error:   "missing issue ID",
			})
			return
		}

		params := service.EventListParams{
			IssueID: issueID,
			Limit:   parseEventLimit(r),
		}
		if r.URL.Query().Has("since") {
			// A bare ?since= starts with fleet-db's first history page and uses
			// the 200-item page ceiling selected by parseEventLimit.
			since := r.URL.Query().Get("since")
			params.Since = &since
		}
		result, err := svc.ListEventHistory(r.Context(), params)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		if result == nil {
			handler.HandleServiceError(w, service.ErrInternal("event history result missing", nil))
			return
		}
		handler.WriteJSON(w, http.StatusOK, EventListResponse{
			Success:     true,
			Data:        result.Events,
			Cursor:      result.Cursor,
			HasMore:     result.HasMore,
			TotalEvents: result.TotalEvents,
		})
	}
}
