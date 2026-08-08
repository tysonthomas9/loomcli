package issues

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

func decodeOptionalIssueJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if r.Body == nil {
		return nil
	}
	err := handler.DecodeOneJSON(w, r, dst, handler.JSONDecodeOptions{MaxBytes: handler.MaxRequestBody})
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

// HandleReopenWorkItem routes reopening through the Work Items owner command.
func HandleReopenWorkItem(api workitems.API) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		if issueID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, ReopenResponse{Success: false, Error: "missing issue ID"})
			return
		}
		var req ReopenRequest
		if err := decodeOptionalIssueJSON(w, r, &req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				handler.WriteJSON(w, http.StatusRequestEntityTooLarge, ReopenResponse{Success: false, Error: "request body too large (max 1MB)"})
				return
			}
			slog.Warn("invalid request body in HandleReopenWorkItem", "err", err)
			handler.WriteJSON(w, http.StatusBadRequest, ReopenResponse{Success: false, Error: "invalid request body"})
			return
		}
		if api == nil {
			handler.HandleWorkItemsError(w, workitems.ErrUnavailable)
			return
		}
		if err := api.Reopen(r.Context(), workitems.ReopenCommand{IssueID: issueID, Reason: req.Reason}); err != nil {
			handler.HandleWorkItemsError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, ReopenResponse{Success: true})
	}
}

// HandleDeleteWorkItem routes permanent deletion through the Work Items owner
// command and preserves the legacy deletion result envelope.
func HandleDeleteWorkItem(api workitems.API) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		if issueID == "" {
			handler.RespondError(w, http.StatusBadRequest, "missing issue ID")
			return
		}
		if api == nil {
			handler.HandleWorkItemsError(w, workitems.ErrUnavailable)
			return
		}
		result, err := api.Delete(r.Context(), workitems.DeleteCommand{IssueID: issueID})
		if err != nil {
			handler.HandleWorkItemsError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "data": result})
	}
}

// handleCreateIssue returns a handler that creates a new issue.
func HandleCreateWorkItem(api workitems.API) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req IssueCreateRequest
		if err := handler.DecodeOneJSON(w, r, &req, handler.JSONDecodeOptions{MaxBytes: handler.MaxRequestBody}); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				writeIssuesError(w, http.StatusRequestEntityTooLarge, "request body too large (max 1MB)", "REQUEST_TOO_LARGE")
				return
			}
			slog.Warn("invalid JSON body in HandleCreateWorkItem", "err", err)
			writeIssuesError(w, http.StatusBadRequest, "invalid request body", "INVALID_JSON")
			return
		}
		if api == nil {
			handler.HandleWorkItemsError(w, workitems.ErrUnavailable)
			return
		}
		value, err := api.Create(r.Context(), createWorkItemCommand(r, &req))
		if err != nil {
			handler.HandleWorkItemsError(w, err)
			return
		}
		data, err := json.Marshal(value)
		if err != nil {
			writeIssuesError(w, http.StatusInternalServerError, "failed to encode response", "ENCODE_ERROR")
			return
		}
		handler.WriteJSON(w, http.StatusCreated, IssuesResponse{Success: true, Data: data})
	}
}

func createWorkItemCommand(r *http.Request, req *IssueCreateRequest) workitems.CreateCommand {
	return workitems.CreateCommand{
		Title: req.Title, IssueType: req.IssueType, Priority: req.Priority,
		ID: req.ID, Parent: req.Parent, Description: req.Description, Status: req.Status,
		Design: req.Design, AcceptanceCriteria: req.AcceptanceCriteria, Notes: req.Notes,
		Assignee: req.Assignee, Owner: req.Owner, CreatedBy: req.CreatedBy,
		ExternalRef: req.ExternalRef, EstimatedMinutes: req.EstimatedMinutes,
		Labels: req.Labels, Dependencies: req.Dependencies, DueAt: req.DueAt,
		DeferUntil: req.DeferUntil, SourceRepo: req.SourceRepo,
		IdempotencyKey: r.Header.Get("X-Idempotency-Key"),
		Force:          r.Header.Get("X-Idempotency-Force") == "true",
	}
}

func HandleCloseWorkItem(api workitems.API) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		if issueID == "" {
			handler.RespondError(w, http.StatusBadRequest, "missing issue ID")
			return
		}
		req, ok := decodeCloseRequest(w, r)
		if !ok {
			return
		}
		if api == nil {
			handler.HandleWorkItemsError(w, workitems.ErrUnavailable)
			return
		}
		value, err := api.Close(r.Context(), workitems.CloseCommand{
			IssueID: issueID, Reason: req.ResolvedReason(), Session: req.Session,
			SuggestNext: req.SuggestNext, Force: req.Force,
		})
		if err != nil {
			handler.HandleWorkItemsError(w, err)
			return
		}
		data, err := json.Marshal(value)
		if err != nil {
			writeIssuesError(w, http.StatusInternalServerError, "failed to encode response", "ENCODE_ERROR")
			return
		}
		handler.WriteJSON(w, http.StatusOK, CloseResponse{Success: true, Data: data})
	}
}

func decodeCloseRequest(w http.ResponseWriter, r *http.Request) (CloseRequest, bool) {
	var req CloseRequest
	if err := decodeOptionalIssueJSON(w, r, &req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			handler.RespondError(w, http.StatusRequestEntityTooLarge, "request body too large (max 1MB)")
			return CloseRequest{}, false
		}
		slog.Warn("invalid request body in close work item", "err", err)
		handler.RespondError(w, http.StatusBadRequest, "invalid request body")
		return CloseRequest{}, false
	}
	return req, true
}

// HandleClaimWorkItem invokes the Work Items owner command. FleetDB performs
// lock acquisition, assignment, and in_progress transition atomically; this
// adapter only marshals the returned aggregate projection.
func HandleClaimWorkItem(api workitems.API) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		if issueID == "" {
			handler.RespondError(w, http.StatusBadRequest, "missing issue ID")
			return
		}
		if api == nil {
			handler.HandleWorkItemsError(w, workitems.ErrUnavailable)
			return
		}
		value, err := api.Claim(r.Context(), workitems.ClaimCommand{IssueID: issueID})
		if err != nil {
			handler.HandleWorkItemsError(w, err)
			return
		}
		data, err := json.Marshal(value)
		if err != nil {
			writeIssuesError(w, http.StatusInternalServerError, "failed to encode response", "ENCODE_ERROR")
			return
		}
		handler.WriteJSON(w, http.StatusOK, IssuesResponse{Success: true, Data: data})
	}
}

// ReopenRequest represents the JSON body for reopening a closed issue. All
// fields optional; an empty body is valid and yields a status-only reopen.
type ReopenRequest struct {
	Reason string `json:"reason,omitempty"`
}

// ReopenResponse wraps the reopen operation result.
type ReopenResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}
