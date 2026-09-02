package issues

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/backend/advisoryactor"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// ArchiveRequest represents the JSON body for archiving an issue. The reason
// is optional; an empty body is valid and yields a status-only archive.
type ArchiveRequest struct {
	Reason string `json:"reason,omitempty"`
}

// ArchiveResponse wraps the archive/unarchive operation result.
type ArchiveResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// HandleArchiveIssue returns a handler that archives (tombstones) an issue by
// ID. This exists as a dedicated route because tombstone is not a settable
// status on PATCH — the workspace tree's Archive action used to PATCH it and
// got a 400 from fleet-db's status validator on every click.
func HandleArchiveIssue(svc service.IssueService) http.HandlerFunc {
	fallbackActor := resolveOperatorActor()
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		if issueID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, ArchiveResponse{
				Success: false,
				Error:   "missing issue ID",
			})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)

		var req ArchiveRequest
		if r.Body != nil && r.ContentLength > 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				var maxBytesErr *http.MaxBytesError
				if errors.As(err, &maxBytesErr) {
					handler.WriteJSON(w, http.StatusRequestEntityTooLarge, ArchiveResponse{
						Success: false,
						Error:   "request body too large (max 1MB)",
					})
					return
				}
				slog.Warn("invalid request body in handleArchiveIssue", "err", err)
				handler.WriteJSON(w, http.StatusBadRequest, ArchiveResponse{
					Success: false,
					Error:   "invalid request body",
				})
				return
			}
		}

		ctx := operatorActorContext(r, fallbackActor)
		err := svc.ArchiveIssue(ctx, service.ArchiveIssueParams{
			IssueID: issueID,
			Actor:   advisoryactor.From(ctx),
			Reason:  req.Reason,
		})
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, ArchiveResponse{Success: true})
	}
}

// HandleUnarchiveIssue returns a handler that restores an archived issue.
// It takes no body: unarchive has nothing to record.
func HandleUnarchiveIssue(svc service.IssueService) http.HandlerFunc {
	fallbackActor := resolveOperatorActor()
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		if issueID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, ArchiveResponse{
				Success: false,
				Error:   "missing issue ID",
			})
			return
		}

		ctx := operatorActorContext(r, fallbackActor)
		err := svc.UnarchiveIssue(ctx, service.UnarchiveIssueParams{
			IssueID: issueID,
			Actor:   advisoryactor.From(ctx),
		})
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, ArchiveResponse{Success: true})
	}
}
