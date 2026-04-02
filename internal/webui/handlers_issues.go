package webui

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// IssuesResponse represents the response structure for the issues endpoint.
type IssuesResponse struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
	Code    string          `json:"code,omitempty"`
}

// CloseRequest represents the JSON body for the close endpoint.
type CloseRequest struct {
	Reason      string `json:"reason,omitempty"`
	Session     string `json:"session,omitempty"`
	SuggestNext bool   `json:"suggest_next,omitempty"`
	Force       bool   `json:"force,omitempty"`
}

// CloseResponse wraps the close result for JSON response.
type CloseResponse struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// PatchIssueRequest represents the PATCH /api/issues/:id request body.
// All fields are optional pointers to support partial updates.
type PatchIssueRequest struct {
	Title              *string  `json:"title,omitempty"`
	Description        *string  `json:"description,omitempty"`
	Status             *string  `json:"status,omitempty"`
	Priority           *int     `json:"priority,omitempty"`
	Assignee           *string  `json:"assignee,omitempty"`
	Owner              *string  `json:"owner,omitempty"`
	Design             *string  `json:"design,omitempty"`
	AcceptanceCriteria *string  `json:"acceptance_criteria,omitempty"`
	Notes              *string  `json:"notes,omitempty"`
	ExternalRef        *string  `json:"external_ref,omitempty"`
	EstimatedMinutes   *int     `json:"estimated_minutes,omitempty"`
	IssueType          *string  `json:"issue_type,omitempty"`
	AddLabels          []string `json:"add_labels,omitempty"`
	RemoveLabels       []string `json:"remove_labels,omitempty"`
	SetLabels          []string `json:"set_labels,omitempty"`
	Pinned             *bool    `json:"pinned,omitempty"`
	Parent             *string  `json:"parent,omitempty"`
	DueAt              *string  `json:"due_at,omitempty"`
	DeferUntil         *string  `json:"defer_until,omitempty"`
}

// PatchIssueResponse wraps the patch response for JSON output.
type PatchIssueResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// IssueCreateRequest represents the JSON request body for creating an issue.
type IssueCreateRequest struct {
	// Required fields
	Title     string `json:"title"`
	IssueType string `json:"issue_type"`
	Priority  int    `json:"priority"`

	// Optional fields - match rpc.CreateArgs
	ID                 string   `json:"id,omitempty"`
	Parent             string   `json:"parent,omitempty"`
	Description        string   `json:"description,omitempty"`
	Design             string   `json:"design,omitempty"`
	AcceptanceCriteria string   `json:"acceptance_criteria,omitempty"`
	Notes              string   `json:"notes,omitempty"`
	Assignee           string   `json:"assignee,omitempty"`
	Owner              string   `json:"owner,omitempty"`
	CreatedBy          string   `json:"created_by,omitempty"`
	ExternalRef        string   `json:"external_ref,omitempty"`
	EstimatedMinutes   *int     `json:"estimated_minutes,omitempty"`
	Labels             []string `json:"labels,omitempty"`
	Dependencies       []string `json:"dependencies,omitempty"`
	DueAt              string   `json:"due_at,omitempty"`
	DeferUntil         string   `json:"defer_until,omitempty"`
}

// handleGetIssue returns a handler that retrieves a single issue by ID.
func handleGetIssue(svc service.IssueService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		if issueID == "" {
			respondError(w, http.StatusBadRequest, "missing issue ID")
			return
		}
		data, err := svc.GetIssue(r.Context(), issueID)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		respondJSON(w, http.StatusOK, IssuesResponse{
			Success: true,
			Data:    data,
		})
	}
}

// handleListIssues returns a handler that lists issues from the daemon.
func handleListIssues(svc service.IssueService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		args, err := parseListParams(r)
		if err != nil {
			writeIssuesError(w, http.StatusBadRequest, err.Error(), "INVALID_PARAMS")
			return
		}

		kp, err := parseKanbanParams(r)
		if err != nil {
			writeIssuesError(w, http.StatusBadRequest, err.Error(), "INVALID_PARAMS")
			return
		}

		result, svcErr := svc.ListIssues(r.Context(), service.ListIssuesParams{
			Args:           args,
			ExcludeStatus:  kp.ExcludeStatus,
			IncludeBlocked: kp.IncludeBlocked,
		})
		if svcErr != nil {
			writeServiceError(w, svcErr)
			return
		}

		var data []byte
		if result.KanbanIssues != nil {
			data, err = json.Marshal(result.KanbanIssues)
		} else {
			data, err = json.Marshal(result.Issues)
		}
		if err != nil {
			slog.Error("failed to marshal issues", "err", err)
			writeIssuesError(w, http.StatusInternalServerError, "failed to encode response", "ENCODE_ERROR")
			return
		}
		respondJSON(w, http.StatusOK, IssuesResponse{
			Success: true,
			Data:    data,
		})
	}
}
