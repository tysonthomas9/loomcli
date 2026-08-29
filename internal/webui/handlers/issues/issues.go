package issues

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
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
//
// Accepts both "reason" and "close_reason" keys. Decoding both keeps the
// endpoint dialect-agnostic; the service layer only sees the resolved Reason
// string.
type CloseRequest struct {
	Reason      string `json:"reason,omitempty"`
	CloseReason string `json:"close_reason,omitempty"`
	Session     string `json:"session,omitempty"`
	SuggestNext bool   `json:"suggest_next,omitempty"`
	Force       bool   `json:"force,omitempty"`
}

// ResolvedReason returns the close reason from whichever JSON key was
// supplied. "reason" wins when both are set.
func (r CloseRequest) ResolvedReason() string {
	if r.Reason != "" {
		return r.Reason
	}
	return r.CloseReason
}

// CloseResponse wraps the close result for JSON response.
type CloseResponse struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// ClaimIssueRequest represents the optional JSON body for claiming an issue.
type ClaimIssueRequest struct {
	LockTTL    int    `json:"lock_ttl,omitempty"`
	OwnerActor string `json:"owner_actor,omitempty"`
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
	DesignFormat       *string  `json:"design_format,omitempty"`
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
	AgentState         *string  `json:"agent_state,omitempty"`
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
	Status             string   `json:"status,omitempty"`
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
	SourceRepo         string   `json:"source_repo,omitempty"`
}

// writeIssuesError writes a JSON error response for the issues endpoint.
func writeIssuesError(w http.ResponseWriter, status int, message, code string) {
	handler.WriteJSON(w, status, IssuesResponse{Success: false, Error: message, Code: code})
}

// handleGetIssue returns a handler that retrieves a single issue by ID.
func HandleGetIssue(svc service.IssueService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		if issueID == "" {
			handler.RespondError(w, http.StatusBadRequest, "missing issue ID")
			return
		}
		data, err := svc.GetIssue(r.Context(), issueID)
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

// handleListIssues returns a handler that lists issues from the daemon.
func HandleListIssues(svc service.IssueService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		args, err := parseListParams(r)
		if err != nil {
			writeIssuesError(w, http.StatusBadRequest, err.Error(), "INVALID_PARAMS")
			return
		}
		// List views don't need full issue bodies — use lightweight mode
		// to avoid allocating multi-KB description/design/notes per issue.
		args.Lightweight = true

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
			handler.HandleServiceError(w, svcErr)
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
		handler.WriteJSON(w, http.StatusOK, IssuesResponse{
			Success: true,
			Data:    data,
		})
	}
}
