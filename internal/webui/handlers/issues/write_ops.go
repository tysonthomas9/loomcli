package issues

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"unicode"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// handleCreateIssue returns a handler that creates a new issue.
func HandleCreateIssue(svc service.IssueService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)

		var req IssueCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				writeIssuesError(w, http.StatusRequestEntityTooLarge, "request body too large (max 1MB)", "REQUEST_TOO_LARGE")
				return
			}
			slog.Warn("invalid JSON body in handleCreateIssue", "err", err)
			writeIssuesError(w, http.StatusBadRequest, "invalid request body", "INVALID_JSON")
			return
		}

		data, err := svc.CreateIssue(r.Context(), createParamsFromRequest(r, &req))
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusCreated, IssuesResponse{
			Success: true,
			Data:    data,
		})
	}
}

// createParamsFromRequest maps the decoded create request body plus the
// idempotency headers onto service.CreateIssueParams. The idempotency values
// are header-only end to end: fleet-db's strict JSON decode rejects unknown
// body fields.
func createParamsFromRequest(r *http.Request, req *IssueCreateRequest) service.CreateIssueParams {
	return service.CreateIssueParams{
		Title:              req.Title,
		IssueType:          req.IssueType,
		Priority:           req.Priority,
		ID:                 req.ID,
		Parent:             req.Parent,
		Description:        req.Description,
		Status:             req.Status,
		Design:             req.Design,
		AcceptanceCriteria: req.AcceptanceCriteria,
		Notes:              req.Notes,
		Assignee:           req.Assignee,
		Owner:              req.Owner,
		CreatedBy:          req.CreatedBy,
		ExternalRef:        req.ExternalRef,
		EstimatedMinutes:   req.EstimatedMinutes,
		Labels:             req.Labels,
		Dependencies:       req.Dependencies,
		DueAt:              req.DueAt,
		DeferUntil:         req.DeferUntil,
		SourceRepo:         req.SourceRepo,
		IdempotencyKey:     r.Header.Get("X-Idempotency-Key"),
		Force:              r.Header.Get("X-Idempotency-Force") == "true",
	}
}

// handleCloseIssue returns a handler that closes an issue by ID.
func HandleCloseIssue(svc service.IssueService) http.HandlerFunc {
	fallbackActor := resolveOperatorActor()
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		if issueID == "" {
			handler.RespondError(w, http.StatusBadRequest, "missing issue ID")
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)

		var req CloseRequest
		if r.Body != nil && r.ContentLength > 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				var maxBytesErr *http.MaxBytesError
				if errors.As(err, &maxBytesErr) {
					handler.RespondError(w, http.StatusRequestEntityTooLarge, "request body too large (max 1MB)")
					return
				}
				slog.Warn("invalid request body in handleCloseIssue", "err", err)
				handler.RespondError(w, http.StatusBadRequest, "invalid request body")
				return
			}
		}

		params := service.CloseIssueParams{
			IssueID:     issueID,
			Actor:       operatorActor(r.Context(), fallbackActor),
			Reason:      req.ResolvedReason(),
			Session:     req.Session,
			SuggestNext: req.SuggestNext,
			Force:       req.Force,
		}

		data, err := svc.CloseIssue(r.Context(), params)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, CloseResponse{
			Success: true,
			Data:    data,
		})
	}
}

// maxActorHeaderLen bounds the X-Actor header before it is forwarded to the
// backing store. fleet-db records the actor on the lock and on every event it
// emits, so an unbounded value is written back many times over.
const maxActorHeaderLen = 128

// actorFromRequest extracts and validates the optional X-Actor header carrying
// the calling worker's identity. An absent or blank header returns ("", nil):
// the legacy path, where the operation is recorded against serve's own
// configured actor.
//
// The value is forwarded verbatim to fleet-db, where it lands in the lock's
// owner field and in the event stream — so bound its length and reject control
// characters here, at the edge, rather than letting caller-controlled bytes
// into log lines and audit records.
//
// Trust model unchanged from the claim path this extends: serve forwards a
// client-supplied actor without binding it to the caller, matching fleet-db's
// own X-Actor handling. Like the rest of the serve API it assumes a trusted
// network.
func actorFromRequest(r *http.Request) (string, error) {
	actor := strings.TrimSpace(r.Header.Get("X-Actor"))
	if actor == "" {
		return "", nil
	}
	if len(actor) > maxActorHeaderLen {
		return "", fmt.Errorf("X-Actor header exceeds %d characters", maxActorHeaderLen)
	}
	for _, c := range actor {
		if unicode.IsControl(c) {
			return "", errors.New("X-Actor header contains control characters")
		}
	}
	return actor, nil
}

// HandleClaimIssue returns a handler that atomically claims an issue by ID.
// The optional X-Actor header scopes the claim to the calling worker; without
// it the claim is recorded against the server-side actor. Returns 409 if the
// issue is already claimed by another agent.
func HandleClaimIssue(svc service.IssueService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		if issueID == "" {
			handler.RespondError(w, http.StatusBadRequest, "missing issue ID")
			return
		}

		// X-Actor is the established convention for carrying a worker identity
		// toward fleet-db; the serve-mediated claim was the one path that
		// dropped it, so every sibling claimed as serve itself. A malformed
		// header is rejected rather than dropped: falling back to serve's own
		// actor is the exact collapse this path exists to prevent.
		actor, err := actorFromRequest(r)
		if err != nil {
			handler.RespondError(w, http.StatusBadRequest, err.Error())
			return
		}

		data, err := svc.ClaimIssue(r.Context(), service.ClaimIssueParams{
			IssueID: issueID,
			Actor:   actor,
		})
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, IssuesResponse{
			Success: true,
			Data:    data,
		})
	}
}

// HandleReleaseIssue returns a handler that releases a claimed issue back to
// open — the counterpart to HandleClaimIssue, and the route the serve-mediated
// release path had no way to call.
//
// The optional X-Actor header scopes the release to the calling worker:
// releasing a lock held by a different actor returns 409 rather than silently
// un-claiming the worker still running on it.
func HandleReleaseIssue(svc service.IssueService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		if issueID == "" {
			handler.RespondError(w, http.StatusBadRequest, "missing issue ID")
			return
		}

		actor, err := actorFromRequest(r)
		if err != nil {
			handler.RespondError(w, http.StatusBadRequest, err.Error())
			return
		}

		if err := svc.ReleaseIssue(r.Context(), service.ReleaseIssueParams{
			IssueID: issueID,
			Actor:   actor,
		}); err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, IssuesResponse{Success: true})
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

// HandleReopenIssue returns a handler that transitions a closed issue back
// to open status. An empty body or {} is valid.
func HandleReopenIssue(svc service.IssueService) http.HandlerFunc {
	fallbackActor := resolveOperatorActor()
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		if issueID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, ReopenResponse{
				Success: false,
				Error:   "missing issue ID",
			})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)

		var req ReopenRequest
		if r.Body != nil && r.ContentLength > 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				var maxBytesErr *http.MaxBytesError
				if errors.As(err, &maxBytesErr) {
					handler.WriteJSON(w, http.StatusRequestEntityTooLarge, ReopenResponse{
						Success: false,
						Error:   "request body too large (max 1MB)",
					})
					return
				}
				slog.Warn("invalid request body in handleReopenIssue", "err", err)
				handler.WriteJSON(w, http.StatusBadRequest, ReopenResponse{
					Success: false,
					Error:   "invalid request body",
				})
				return
			}
		}

		err := svc.ReopenIssue(r.Context(), service.ReopenIssueParams{
			IssueID: issueID,
			Actor:   operatorActor(r.Context(), fallbackActor),
			Reason:  req.Reason,
		})
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, ReopenResponse{
			Success: true,
		})
	}
}

// handleDeleteIssue returns a handler that permanently deletes an issue by ID.
func HandleDeleteIssue(svc service.IssueService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		if issueID == "" {
			handler.RespondError(w, http.StatusBadRequest, "missing issue ID")
			return
		}

		data, err := svc.DeleteIssue(r.Context(), issueID)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"data":    data,
		})
	}
}
