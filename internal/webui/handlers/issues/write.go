package issues

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

const (
	envOperatorActor     = "LOOM_OPERATOR_ACTOR"
	defaultOperatorActor = "operator@local"
)

// handlePatchIssue returns a handler that performs partial updates on an issue.
func HandlePatchIssue(svc service.IssueService) http.HandlerFunc {
	fallbackActor := resolveOperatorActor()
	return func(w http.ResponseWriter, r *http.Request) {
		issueID, req, ok := validatePatchRequest(w, r)
		if !ok {
			return
		}

		params := service.PatchIssueParams{
			IssueID:            issueID,
			Actor:              operatorActor(r.Context(), fallbackActor),
			Title:              req.Title,
			Description:        req.Description,
			Status:             req.Status,
			Priority:           req.Priority,
			Assignee:           req.Assignee,
			Owner:              req.Owner,
			Design:             req.Design,
			DesignFormat:       req.DesignFormat,
			AcceptanceCriteria: req.AcceptanceCriteria,
			Notes:              req.Notes,
			ExternalRef:        req.ExternalRef,
			EstimatedMinutes:   req.EstimatedMinutes,
			IssueType:          req.IssueType,
			AddLabels:          req.AddLabels,
			RemoveLabels:       req.RemoveLabels,
			SetLabels:          req.SetLabels,
			Pinned:             req.Pinned,
			Parent:             req.Parent,
			DueAt:              req.DueAt,
			DeferUntil:         req.DeferUntil,
			AgentState:         req.AgentState,
		}

		if err := svc.PatchIssue(r.Context(), params); err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		data, err := svc.GetIssue(r.Context(), issueID)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, PatchIssueResponse{
			Success: true,
			Data:    data,
		})
	}
}

// resolveOperatorActor is called when the route is constructed, so the
// open-mode fallback is stable for the lifetime of the server.
func resolveOperatorActor() string {
	if actor := strings.TrimSpace(os.Getenv(envOperatorActor)); actor != "" {
		return actor
	}
	return defaultOperatorActor
}

func operatorActor(ctx context.Context, fallback string) string {
	if actor, _, ok := middleware.VerifiedUserActorFromContext(ctx); ok {
		return actor
	}
	return fallback
}

// validatePatchRequest extracts the issue ID and parses the JSON body from an HTTP request.
func validatePatchRequest(w http.ResponseWriter, r *http.Request) (string, *PatchIssueRequest, bool) {
	issueID := r.PathValue("id")
	if issueID == "" {
		handler.WriteJSON(w, http.StatusBadRequest, PatchIssueResponse{
			Success: false,
			Error:   "missing issue ID in path",
		})
		return "", nil, false
	}

	r.Body = http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)

	var req PatchIssueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			handler.WriteJSON(w, http.StatusRequestEntityTooLarge, PatchIssueResponse{
				Success: false,
				Error:   "request body too large (max 1MB)",
			})
			return "", nil, false
		}
		slog.Warn("invalid request body in handlePatchIssue", "err", err)
		handler.WriteJSON(w, http.StatusBadRequest, PatchIssueResponse{
			Success: false,
			Error:   "invalid request body",
		})
		return "", nil, false
	}

	return issueID, &req, true
}
