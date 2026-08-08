package issues

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

// HandleAssignWorkItemRepository delegates the atomic repository assignment
// and conditional reopen to the Work Items owner.
func HandleAssignWorkItemRepository(api workitems.API) http.HandlerFunc {
	return handleSetIssueRepository(func(ctx context.Context, issueID, repo string) (json.RawMessage, error) {
		if api == nil {
			return nil, workitems.ErrUnavailable
		}
		value, err := api.AssignRepository(ctx, workitems.AssignRepositoryCommand{IssueID: issueID, Repository: repo})
		if err != nil {
			return nil, err
		}
		return json.Marshal(value)
	}, true)
}

// SetIssueRepositoryRequest is the Loom HTTP request body for assigning the
// canonical source repository to an issue.
type SetIssueRepositoryRequest struct {
	Repo string `json:"repo"`
}

func handleSetIssueRepository(command func(context.Context, string, string) (json.RawMessage, error), capabilityErrors bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if command == nil {
			handler.WriteJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "repository assignment service not configured",
				"kind":  "unavailable",
			})
			return
		}

		issueID := r.PathValue("id")
		if issueID == "" {
			writeIssuesError(w, http.StatusBadRequest, "missing issue ID", "INVALID_REQUEST")
			return
		}

		var req SetIssueRepositoryRequest
		if err := handler.DecodeOneJSON(w, r, &req, handler.JSONDecodeOptions{
			MaxBytes:              handler.MaxRequestBody,
			DisallowUnknownFields: true,
		}); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				writeIssuesError(w, http.StatusRequestEntityTooLarge, "request body too large (max 1MB)", "REQUEST_TOO_LARGE")
				return
			}
			slog.Warn("invalid request body in HandleSetIssueRepository", "err", err)
			writeIssuesError(w, http.StatusBadRequest, "invalid request body", "INVALID_JSON")
			return
		}

		data, err := command(r.Context(), issueID, req.Repo)
		if err != nil {
			if capabilityErrors {
				handler.HandleWorkItemsError(w, err)
			} else {
				handler.HandleServiceError(w, err)
			}
			return
		}

		handler.WriteJSON(w, http.StatusOK, IssuesResponse{Success: true, Data: data})
	}
}
