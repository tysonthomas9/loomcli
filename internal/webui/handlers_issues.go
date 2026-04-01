package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

// IssueWithParent extends IssueWithCounts with parent info for the /api/issues response.
// This enables the frontend to display parent-child relationships (swim lanes in Kanban)
// without requiring additional API calls for each issue.
type IssueWithParent struct {
	*types.IssueWithCounts
	Parent      *string `json:"parent,omitempty"`       // Parent issue ID (null for root-level issues)
	ParentTitle *string `json:"parent_title,omitempty"` // Parent issue title for display
	Repo        *string `json:"repo,omitempty"`         // Repository that owns this issue
}

// KanbanIssue extends IssueWithParent with blocked dependency info.
// Returned when include_blocked=true is passed to /api/issues.
type KanbanIssue struct {
	*types.IssueWithCounts
	Parent           *string            `json:"parent,omitempty"`
	ParentTitle      *string            `json:"parent_title,omitempty"`
	IsBlocked        bool               `json:"is_blocked"`
	BlockedByCount   int                `json:"blocked_by_count"`
	BlockedBy        []string           `json:"blocked_by,omitempty"`
	BlockedByDetails []types.BlockerRef `json:"blocked_by_details,omitempty"`
	Repo             *string            `json:"repo,omitempty"`
}

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

// issueGetter is an internal interface for testing issue retrieval.
// The production code uses *rpc.Client which implements this interface.
type issueGetter interface {
	Show(args *rpc.ShowArgs) (*rpc.Response, error)
}

// connectionGetter is an internal interface for testing connection pool operations.
type connectionGetter interface {
	Get(ctx context.Context) (issueGetter, error)
	Put(client issueGetter)
}

// poolAdapter wraps daemon.Pool to implement connectionGetter.
type poolAdapter struct {
	pool daemon.Pool
}

func (p *poolAdapter) Get(ctx context.Context) (issueGetter, error) {
	return p.pool.Get(ctx)
}

func (p *poolAdapter) Put(client issueGetter) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Put(c)
	}
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

// validIssueTypes defines the valid issue types for validation.
var validIssueTypes = map[string]bool{
	"bug":     true,
	"feature": true,
	"task":    true,
	"epic":    true,
	"chore":   true,
}

// Limits for create request validation.
const (
	maxLabels       = 50
	maxDependencies = 100
)

// handleGetIssue returns a handler that retrieves a single issue by ID.
func handleGetIssue(pool daemon.Pool) http.HandlerFunc {
	return handleGetIssueWithPool(&poolAdapter{pool: pool})
}

// handleGetIssueWithPool is the internal implementation that accepts an interface for testing.
func handleGetIssueWithPool(pool connectionGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Extract issue ID from path parameter
		issueID := r.PathValue("id")
		if issueID == "" {
			respondError(w, http.StatusBadRequest, "missing issue ID")
			return
		}

		// Get connection from pool
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		client, err := pool.Get(ctx)
		if err != nil {
			respondError(w, http.StatusServiceUnavailable, "daemon not available")
			return
		}
		defer pool.Put(client)

		// Call Show RPC
		resp, err := client.Show(&rpc.ShowArgs{ID: issueID})
		if err != nil {
			// Check if it's a "not found" error
			if strings.Contains(err.Error(), "not found") {
				respondError(w, http.StatusNotFound, fmt.Sprintf("issue not found: %s", issueID))
				return
			}
			slog.Error("RPC error in handleGetIssue", "issue_id", issueID, "err", err)
			respondError(w, http.StatusInternalServerError, "internal server error")
			return
		}

		// Return the issue details wrapped in standard {success, data} envelope
		respondJSON(w, http.StatusOK, IssuesResponse{
			Success: true,
			Data:    resp.Data,
		})
	}
}

// handleListIssues returns a handler that lists issues from the daemon.
func handleListIssues(pool daemon.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if pool == nil {
			writeIssuesError(w, http.StatusServiceUnavailable, "connection pool not initialized", "POOL_NOT_INITIALIZED")
			return
		}

		// Parse query parameters into ListArgs
		args, err := parseListParams(r)
		if err != nil {
			writeIssuesError(w, http.StatusBadRequest, err.Error(), "INVALID_PARAMS")
			return
		}

		// Parse kanban-specific parameters
		kp, err := parseKanbanParams(r)
		if err != nil {
			writeIssuesError(w, http.StatusBadRequest, err.Error(), "INVALID_PARAMS")
			return
		}

		// Acquire connection with timeout
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		client, err := pool.Get(ctx)
		if err != nil {
			status := http.StatusServiceUnavailable
			code := "DAEMON_UNAVAILABLE"
			message := "daemon unavailable"
			if errors.Is(err, context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
				code = "CONNECTION_TIMEOUT"
				message = "timeout connecting to daemon"
			}
			slog.Error("connection pool error", "err", err)
			writeIssuesError(w, status, message, code)
			return
		}
		defer pool.Put(client)

		// Execute List RPC call
		resp, err := client.List(args)
		if err != nil {
			slog.Error("RPC error", "err", err)
			writeIssuesError(w, http.StatusInternalServerError, "failed to list issues", "RPC_ERROR")
			return
		}

		if !resp.Success {
			writeIssuesError(w, http.StatusInternalServerError, resp.Error, "DAEMON_ERROR")
			return
		}

		// Parse IssueWithCounts from response to extract issue IDs
		var issuesWithCounts []*types.IssueWithCounts
		if err := json.Unmarshal(resp.Data, &issuesWithCounts); err != nil {
			slog.Error("failed to parse issues", "err", err)
			writeIssuesError(w, http.StatusInternalServerError, "failed to parse issues", "PARSE_ERROR")
			return
		}

		// Filter out excluded statuses (server-side, since ListArgs.Status only supports one value)
		if len(kp.ExcludeStatus) > 0 {
			excludeSet := make(map[types.Status]bool, len(kp.ExcludeStatus))
			for _, s := range kp.ExcludeStatus {
				excludeSet[types.Status(s)] = true
			}
			filtered := make([]*types.IssueWithCounts, 0, len(issuesWithCounts))
			for _, iwc := range issuesWithCounts {
				if !excludeSet[iwc.Issue.Status] {
					filtered = append(filtered, iwc)
				}
			}
			issuesWithCounts = filtered
		}

		// If no issues, return empty response
		if len(issuesWithCounts) == 0 {
			var data []byte
			if kp.IncludeBlocked {
				data, err = json.Marshal([]*KanbanIssue{})
			} else {
				data, err = json.Marshal([]*IssueWithParent{})
			}
			if err != nil {
				slog.Error("failed to marshal empty response", "err", err)
				writeIssuesError(w, http.StatusInternalServerError, "failed to encode response", "ENCODE_ERROR")
				return
			}
			respondJSON(w, http.StatusOK, IssuesResponse{
				Success: true,
				Data:    data,
			})
			return
		}

		// Extract issue IDs for parent lookup
		issueIDs := make([]string, len(issuesWithCounts))
		for i, iwc := range issuesWithCounts {
			issueIDs[i] = iwc.Issue.ID
		}

		// Get parent info for all issues (batched to stay within RPC limit of 1000)
		parentResp := &rpc.GetParentIDsResponse{Parents: make(map[string]*rpc.ParentInfo)}
		const parentBatchSize = 1000
		for i := 0; i < len(issueIDs); i += parentBatchSize {
			end := i + parentBatchSize
			if end > len(issueIDs) {
				end = len(issueIDs)
			}
			batch, err := client.GetParentIDs(&rpc.GetParentIDsArgs{IssueIDs: issueIDs[i:end]})
			if err != nil {
				slog.Error("failed to get parent IDs", "batch_start", i, "batch_end", end, "err", err)
				continue
			}
			for k, v := range batch.Parents {
				parentResp.Parents[k] = v
			}
		}

		if kp.IncludeBlocked {
			// Build blocked info map
			blockedMap := make(map[string]*types.BlockedIssue)
			blockedResp, blockedErr := client.Blocked(&rpc.BlockedArgs{})
			if blockedErr != nil {
				// Non-fatal: log and continue without blocked info
				slog.Error("failed to get blocked issues", "err", blockedErr)
			} else if blockedResp.Success {
				var blockedIssues []*types.BlockedIssue
				if jsonErr := json.Unmarshal(blockedResp.Data, &blockedIssues); jsonErr != nil {
					slog.Error("failed to parse blocked issues", "err", jsonErr)
				} else {
					for _, bi := range blockedIssues {
						blockedMap[bi.Issue.ID] = bi
					}
				}
			}

			// Fetch unfiltered issue list for accurate blocker detection.
			// The daemon's Blocked() RPC considers in_progress/review as resolved,
			// but blockers should only clear when closed.
			unclosedIDs, issueMap := fetchUnclosedIDSetAndMap(client)

			// Build KanbanIssue response with blocked info merged
			kanbanIssues := make([]*KanbanIssue, len(issuesWithCounts))
			for i, iwc := range issuesWithCounts {
				ki := &KanbanIssue{
					IssueWithCounts: iwc,
				}
				if parentInfo, ok := parentResp.Parents[iwc.Issue.ID]; ok {
					ki.Parent = &parentInfo.ParentID
					ki.ParentTitle = &parentInfo.ParentTitle
				}
				if iwc.Issue.SourceRepo != "" {
					ki.Repo = &iwc.Issue.SourceRepo
				}
				// Client-side blocker check is authoritative (considers only closed
				// blockers as resolved). Falls back to daemon data on error.
				if unclosedIDs != nil {
					refs := getUnclosedBlockerRefs(iwc.Issue.Dependencies, unclosedIDs, issueMap)
					if len(refs) > 0 {
						ki.IsBlocked = true
						ki.BlockedByCount = len(refs)
						ki.BlockedBy = extractBlockerIDs(refs)
						ki.BlockedByDetails = refs
					}
				} else if bi, ok := blockedMap[iwc.Issue.ID]; ok {
					ki.IsBlocked = true
					ki.BlockedByCount = bi.BlockedByCount
					ki.BlockedBy = bi.BlockedBy
					ki.BlockedByDetails = bi.BlockedByDetails
				}
				kanbanIssues[i] = ki
			}

			data, err := json.Marshal(kanbanIssues)
			if err != nil {
				slog.Error("failed to marshal kanban issues", "err", err)
				writeIssuesError(w, http.StatusInternalServerError, "failed to encode response", "ENCODE_ERROR")
				return
			}
			respondJSON(w, http.StatusOK, IssuesResponse{
				Success: true,
				Data:    data,
			})
			return
		}

		// Standard path: Build response with parent info
		issuesWithParent := make([]*IssueWithParent, len(issuesWithCounts))
		for i, iwc := range issuesWithCounts {
			iwp := &IssueWithParent{
				IssueWithCounts: iwc,
			}
			if parentInfo, ok := parentResp.Parents[iwc.Issue.ID]; ok {
				iwp.Parent = &parentInfo.ParentID
				iwp.ParentTitle = &parentInfo.ParentTitle
			}
			if iwc.Issue.SourceRepo != "" {
				iwp.Repo = &iwc.Issue.SourceRepo
			}
			issuesWithParent[i] = iwp
		}

		data, err := json.Marshal(issuesWithParent)
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
