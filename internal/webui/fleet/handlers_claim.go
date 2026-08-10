package fleet

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

// FleetClaimRequest represents the optional JSON body for POST /api/fleet/claim.
type FleetClaimRequest struct {
	IssueID     string `json:"issue_id,omitempty"`
	Status      string `json:"status,omitempty"`
	IssueType   string `json:"issue_type,omitempty"`
	MaxPriority *int   `json:"max_priority,omitempty"`
}

type FleetClaimResponse struct {
	Success bool                `json:"success"`
	Payload *WorkHandoffPayload `json:"payload,omitempty"`
	Error   string              `json:"error,omitempty"`
}

// WorkHandoffPayload is the adapter-private Fleet compatibility response.
// Keeping the transport DTO here preserves the legacy JSON contract without
// reintroducing a public horizontal product model.
type WorkHandoffPayload struct {
	Issue            *fleetClaimIssue       `json:"issue"`
	Labels           []string               `json:"labels,omitempty"`
	Dependencies     []fleetClaimDependency `json:"dependencies,omitempty"`
	Reason           string                 `json:"reason,omitempty"`
	Deadline         *time.Time             `json:"deadline,omitempty"`
	PriorityOverride *int                   `json:"priority_override,omitempty"`
}

type fleetClaimIssue struct {
	ID                 string     `json:"id"`
	Title              string     `json:"title"`
	Description        string     `json:"description,omitempty"`
	Design             string     `json:"design,omitempty"`
	DesignArtifactID   string     `json:"design_artifact_id,omitempty"`
	DesignFormat       string     `json:"design_format,omitempty"`
	HasDesign          bool       `json:"has_design"`
	AcceptanceCriteria string     `json:"acceptance_criteria,omitempty"`
	Notes              string     `json:"notes,omitempty"`
	Status             string     `json:"status,omitempty"`
	Priority           int        `json:"priority"`
	IssueType          string     `json:"issue_type,omitempty"`
	Assignee           string     `json:"assignee,omitempty"`
	Owner              string     `json:"owner,omitempty"`
	EstimatedMinutes   *int       `json:"estimated_minutes,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	CreatedBy          string     `json:"created_by,omitempty"`
	UpdatedAt          time.Time  `json:"updated_at"`
	ClosedAt           *time.Time `json:"closed_at,omitempty"`
	CloseReason        string     `json:"close_reason,omitempty"`
	ClosedBySession    string     `json:"closed_by_session,omitempty"`
	DueAt              *time.Time `json:"due_at,omitempty"`
	DeferUntil         *time.Time `json:"defer_until,omitempty"`
	ExternalRef        string     `json:"external_ref,omitempty"`
	SourceRepo         string     `json:"source_repo,omitempty"`
	Labels             []string   `json:"labels,omitempty"`
}

type fleetClaimDependency struct {
	IssueID     string    `json:"issue_id"`
	DependsOnID string    `json:"depends_on_id"`
	Type        string    `json:"type"`
	CreatedAt   time.Time `json:"created_at"`
	CreatedBy   string    `json:"created_by,omitempty"`
	Metadata    string    `json:"metadata,omitempty"`
	ThreadID    string    `json:"thread_id,omitempty"`
}

type IssueBackendFn func(context.Context) backend.IssueBackend

func handleFleetClaim(backendFn IssueBackendFn, claimMetrics *ClaimMetrics) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		be := resolveFleetClaimBackend(r.Context(), backendFn)
		if be == nil {
			handler.WriteJSON(w, http.StatusServiceUnavailable, FleetClaimResponse{Error: "issue backend not configured"})
			return
		}
		var req FleetClaimRequest
		if r.Body != nil {
			err := handler.DecodeOneJSON(w, r, &req, handler.JSONDecodeOptions{MaxBytes: handler.MaxRequestBody})
			if err != nil && !errors.Is(err, io.EOF) {
				var maxBytesErr *http.MaxBytesError
				if errors.As(err, &maxBytesErr) {
					handler.WriteJSON(w, http.StatusRequestEntityTooLarge, FleetClaimResponse{Error: "request body too large (max 1MB)"})
					return
				}
				handler.WriteJSON(w, http.StatusBadRequest, FleetClaimResponse{Error: "invalid request body"})
				return
			}
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if req.IssueID != "" {
			claimIssueWithBackend(w, ctx, be, req.IssueID, claimMetrics, true)
			return
		}
		ready, err := be.Ready(ctx, backend.ReadyOpts{Type: req.IssueType, Priority: req.MaxPriority, Limit: 10})
		if err != nil {
			recordClaim(claimMetrics, ClaimResultTimeout)
			handler.WriteJSON(w, http.StatusServiceUnavailable, FleetClaimResponse{Error: "issue backend unavailable"})
			return
		}
		for i := range ready {
			if claimIssueWithBackend(w, ctx, be, ready[i].ID, claimMetrics, false) {
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func resolveFleetClaimBackend(ctx context.Context, backendFn IssueBackendFn) backend.IssueBackend {
	if backendFn == nil {
		return nil
	}
	return backendFn(ctx)
}

func claimIssueWithBackend(w http.ResponseWriter, ctx context.Context, be backend.IssueBackend, issueID string, metrics *ClaimMetrics, writeConflict bool) bool {
	if err := be.ClaimIssue(ctx, issueID, 0); err != nil {
		var backendErr *backend.BackendError
		if errors.As(err, &backendErr) && backendErr.Kind == backend.KindConflict {
			recordClaim(metrics, ClaimResultCollision)
			if writeConflict {
				handler.WriteJSON(w, http.StatusConflict, FleetClaimResponse{Error: "task already claimed by another worker"})
			}
			return false
		}
		if writeConflict {
			handler.WriteJSON(w, http.StatusInternalServerError, FleetClaimResponse{Error: "failed to claim task"})
		}
		return false
	}
	detail, err := be.Get(ctx, issueID)
	if err != nil || detail == nil {
		if writeConflict {
			handler.WriteJSON(w, http.StatusInternalServerError, FleetClaimResponse{Error: "failed to read claimed task"})
		}
		return false
	}
	issue := issueDetailToWorkHandoff(detail)
	recordClaim(metrics, ClaimResultSuccess)
	handler.WriteJSON(w, http.StatusOK, FleetClaimResponse{
		Success: true,
		Payload: &WorkHandoffPayload{Issue: issue, Labels: issue.Labels},
	})
	return true
}

func issueDetailToWorkHandoff(d *backend.IssueDetailData) *fleetClaimIssue {
	return &fleetClaimIssue{
		ID: d.ID, Title: d.Title, Description: d.Description, Design: d.Design,
		DesignArtifactID: d.DesignArtifactID, DesignFormat: d.DesignFormat,
		HasDesign: d.HasDesign, AcceptanceCriteria: d.AcceptanceCriteria, Notes: d.Notes,
		Status: d.Status, Priority: d.Priority, IssueType: d.IssueType,
		Assignee: d.Assignee, Owner: d.Owner, EstimatedMinutes: d.EstimatedMinutes,
		CreatedAt: d.CreatedAt, CreatedBy: d.CreatedBy, UpdatedAt: d.UpdatedAt,
		ClosedAt: d.ClosedAt, CloseReason: d.CloseReason, ClosedBySession: d.ClosedBySession,
		DueAt: d.DueAt, DeferUntil: d.DeferUntil, SourceRepo: d.SourceRepo,
		ExternalRef: d.ExternalRef, Labels: append([]string(nil), d.Labels...),
	}
}

func recordClaim(m *ClaimMetrics, result string) {
	if m != nil {
		m.RecordClaim(result)
	}
}
