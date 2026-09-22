package issues

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// JourneyResponse is the standard issue-handler envelope for a journey fold.
type JourneyResponse struct {
	Success bool             `json:"success"`
	Data    *service.Journey `json:"data,omitempty"`
	Error   string           `json:"error,omitempty"`
}

// HandleGetIssueJourney returns the durable stage and agent-work reconstruction
// for one issue.
func HandleGetIssueJourney(svc service.IssueService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		if issueID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, JourneyResponse{
				Success: false,
				Error:   "missing issue ID",
			})
			return
		}

		journey, err := svc.GetJourney(r.Context(), issueID)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		if journey == nil {
			handler.HandleServiceError(w, service.ErrInternal("journey result missing", nil))
			return
		}
		handler.WriteJSON(w, http.StatusOK, JourneyResponse{Success: true, Data: journey})
	}
}
