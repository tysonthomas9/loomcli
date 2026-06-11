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

		params := service.CreateIssueParams{
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
		}

		data, err := svc.CreateIssue(r.Context(), params)
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

// handleCloseIssue returns a handler that closes an issue by ID.
func HandleCloseIssue(svc service.IssueService) http.HandlerFunc {
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
// backing store.
const maxActorHeaderLen = 128

// actorFromRequest extracts and validates the optional X-Actor header that
// carries the true worker identity through serve-mediated claim/release. An
// absent header returns ("", nil) — the legacy server-side actor applies.
// Over-long values or control characters are rejected.
//
// Trust model: serve forwards the client-supplied actor verbatim (matching
// fleet-db's dev-mode X-Actor handling). Like the rest of the serve API, this
// assumes a trusted network — there is no binding of actor to caller.
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

		actor, err := actorFromRequest(r)
		if err != nil {
			handler.RespondError(w, http.StatusBadRequest, err.Error())
			return
		}

		data, err := svc.ClaimIssue(r.Context(), service.ClaimIssueParams{IssueID: issueID, Actor: actor})
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
// open. The optional X-Actor header scopes the release to the calling worker:
// releasing a lock held by a different actor returns 409 instead of silently
// un-claiming it.
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

		if err := svc.ReleaseIssue(r.Context(), service.ReleaseIssueParams{IssueID: issueID, Actor: actor}); err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, IssuesResponse{
			Success: true,
		})
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
