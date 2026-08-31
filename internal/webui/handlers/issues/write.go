package issues

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/backend/advisoryactor"
	"github.com/tysonthomas9/loomcli/internal/webui/operatorid"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// envOperatorActor / defaultOperatorActor mirror internal/webui/operatorid so
// this package's tests and messages keep their local names. operatorid is the
// single source of truth; `loom doctor` reads the same values.
const (
	envOperatorActor     = operatorid.EnvOperatorActor
	defaultOperatorActor = operatorid.DefaultOperatorActor
)

// handlePatchIssue returns a handler that performs partial updates on an issue.
func HandlePatchIssue(svc service.IssueService) http.HandlerFunc {
	fallbackActor := resolveOperatorActor()
	return func(w http.ResponseWriter, r *http.Request) {
		issueID, req, ok := validatePatchRequest(w, r)
		if !ok {
			return
		}

		ctx := operatorActorContext(r, fallbackActor)
		params := service.PatchIssueParams{
			IssueID:            issueID,
			Actor:              advisoryactor.From(ctx),
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

		if err := svc.PatchIssue(ctx, params); err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		data, err := svc.GetIssue(ctx, issueID)
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
func resolveOperatorActor() string { return operatorid.Resolve() }

// operatorActorContext resolves the operator identity for this request and
// returns a context carrying it as the *advisory* actor.
//
// This is the only way to obtain the operator actor — read it back with
// advisoryactor.From(ctx) and hand the same ctx to the service call, so the
// backend can tell "attribute this to the operator if you can" apart from an
// actor override it must honor exactly (claim/release). Resolution and
// stamping are inseparable on purpose: a handler cannot get the actor without
// also getting the context that makes it safe.
//
// A future handler that forgets this and reads advisoryactor.From(r.Context())
// gets "", which the fleet backend treats as "keep the process identity" — the
// write still lands, losing attribution but never the board.
func operatorActorContext(r *http.Request, fallback string) context.Context {
	actor := fallback
	if verified, _, ok := middleware.VerifiedUserActorFromContext(r.Context()); ok {
		actor = verified
	}
	return advisoryactor.With(r.Context(), actor)
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
