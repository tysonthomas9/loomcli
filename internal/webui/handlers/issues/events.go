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
	Error   string         `json:"error,omitempty"`
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

		events, err := svc.ListEvents(r.Context(), service.EventListParams{
			IssueID: issueID,
			Limit:   parseEventLimit(r),
		})
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, EventListResponse{
			Success: true,
			Data:    events,
		})
	}
}
