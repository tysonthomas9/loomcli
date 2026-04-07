package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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
	Discard(client issueGetter)
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

func (p *poolAdapter) Discard(client issueGetter) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Discard(c)
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
		rpcOK := false
		defer func() {
			if rpcOK {
				pool.Put(client)
			} else {
				pool.Discard(client)
			}
		}()

		// Call Show RPC
		resp, err := client.Show(&rpc.ShowArgs{ID: issueID})
		if err != nil {
			// Check if it's a "not found" error
			if strings.Contains(err.Error(), "not found") {
				respondError(w, http.StatusNotFound, fmt.Sprintf("issue not found: %s", issueID))
				return
			}
			log.Printf("RPC error in handleGetIssue for %s: %v", issueID, err)
			respondError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		rpcOK = true

		// Return the issue details wrapped in standard {success, data} envelope
		respondJSON(w, http.StatusOK, IssuesResponse{
			Success: true,
			Data:    resp.Data,
		})
	}
}

// handleListIssues returns a handler that lists issues from the daemon.
// Uses the composite ListKanban RPC (1 round-trip) when available,
// falling back to the legacy 3-call path (List + GetParentIDs + Blocked)
// for old daemons.
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
		args.Lightweight = true

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
			log.Printf("Connection pool error: %v", err)
			writeIssuesError(w, status, message, code)
			return
		}
		rpcOK := false
		defer func() {
			if rpcOK {
				pool.Put(client)
			} else {
				pool.Discard(client)
			}
		}()

		// Try composite ListKanban RPC (single round-trip)
		kanbanArgs := &rpc.ListKanbanArgs{
			ListArgs:       *args,
			IncludeBlocked: kp.IncludeBlocked,
			ExcludeStatus:  kp.ExcludeStatus,
		}
		kanbanResp, kanbanErr := client.ListKanban(kanbanArgs)
		if kanbanErr != nil && (strings.Contains(kanbanErr.Error(), "unknown operation") || strings.Contains(kanbanErr.Error(), "unknown:")) {
			// Old daemon: fall back to legacy 3-call path
			handleListIssuesLegacy(w, client, args, kp, &rpcOK)
			return
		}
		if kanbanErr != nil {
			log.Printf("ListKanban RPC error: %v", kanbanErr)
			writeIssuesError(w, http.StatusInternalServerError, "failed to list issues", "RPC_ERROR")
			return
		}
		rpcOK = true

		// Convert KanbanIssueRPC → KanbanIssue/IssueWithParent for the HTTP response
		if kp.IncludeBlocked {
			// Build unclosed sets for client-side blocker refinement
			iwcSlice := make([]*types.IssueWithCounts, len(kanbanResp.Issues))
			for i, ki := range kanbanResp.Issues {
				iwcSlice[i] = ki.IssueWithCounts
			}
			unclosedIDs, issueMap := buildUnclosedSetsFromFetched(iwcSlice)

			kanbanIssues := make([]*KanbanIssue, len(kanbanResp.Issues))
			for i, ki := range kanbanResp.Issues {
				out := &KanbanIssue{
					IssueWithCounts: ki.IssueWithCounts,
				}
				if ki.ParentID != "" {
					out.Parent = &ki.ParentID
					out.ParentTitle = &ki.ParentTitle
				}
				if ki.Repo != "" {
					out.Repo = &ki.Repo
				}
				// Client-side blocker refinement: check unclosed deps first
				refs := getUnclosedBlockerRefs(ki.Issue.Dependencies, unclosedIDs, issueMap)
				if len(refs) > 0 {
					out.IsBlocked = true
					out.BlockedByCount = len(refs)
					out.BlockedBy = extractBlockerIDs(refs)
					out.BlockedByDetails = refs
				} else if ki.IsBlocked {
					out.IsBlocked = true
					out.BlockedByCount = ki.BlockedByCount
					out.BlockedBy = ki.BlockedBy
					out.BlockedByDetails = ki.BlockedByDetails
				}
				kanbanIssues[i] = out
			}

			data, err := json.Marshal(kanbanIssues)
			if err != nil {
				writeIssuesError(w, http.StatusInternalServerError, "failed to encode response", "ENCODE_ERROR")
				return
			}
			respondJSON(w, http.StatusOK, IssuesResponse{Success: true, Data: data})
		} else {
			issuesWithParent := make([]*IssueWithParent, len(kanbanResp.Issues))
			for i, ki := range kanbanResp.Issues {
				iwp := &IssueWithParent{
					IssueWithCounts: ki.IssueWithCounts,
				}
				if ki.ParentID != "" {
					iwp.Parent = &ki.ParentID
					iwp.ParentTitle = &ki.ParentTitle
				}
				if ki.Repo != "" {
					iwp.Repo = &ki.Repo
				}
				issuesWithParent[i] = iwp
			}

			data, err := json.Marshal(issuesWithParent)
			if err != nil {
				writeIssuesError(w, http.StatusInternalServerError, "failed to encode response", "ENCODE_ERROR")
				return
			}
			respondJSON(w, http.StatusOK, IssuesResponse{Success: true, Data: data})
		}
	}
}

// handleListIssuesLegacy is the pre-ListKanban 3-call path for old daemons.
func handleListIssuesLegacy(w http.ResponseWriter, client *rpc.Client, args *rpc.ListArgs, kp *kanbanParams, rpcOK *bool) {
	resp, err := client.List(args)
	if err != nil {
		log.Printf("RPC error: %v", err)
		writeIssuesError(w, http.StatusInternalServerError, "failed to list issues", "RPC_ERROR")
		return
	}
	*rpcOK = true

	if !resp.Success {
		writeIssuesError(w, http.StatusInternalServerError, resp.Error, "DAEMON_ERROR")
		return
	}

	var issuesWithCounts []*types.IssueWithCounts
	if err := json.Unmarshal(resp.Data, &issuesWithCounts); err != nil {
		writeIssuesError(w, http.StatusInternalServerError, "failed to parse issues", "PARSE_ERROR")
		return
	}

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

	if len(issuesWithCounts) == 0 {
		var data []byte
		if kp.IncludeBlocked {
			data, _ = json.Marshal([]*KanbanIssue{})
		} else {
			data, _ = json.Marshal([]*IssueWithParent{})
		}
		respondJSON(w, http.StatusOK, IssuesResponse{Success: true, Data: data})
		return
	}

	issueIDs := make([]string, len(issuesWithCounts))
	for i, iwc := range issuesWithCounts {
		issueIDs[i] = iwc.Issue.ID
	}

	parentResp := &rpc.GetParentIDsResponse{Parents: make(map[string]*rpc.ParentInfo)}
	const parentBatchSize = 1000
	for i := 0; i < len(issueIDs); i += parentBatchSize {
		end := i + parentBatchSize
		if end > len(issueIDs) {
			end = len(issueIDs)
		}
		batch, err := client.GetParentIDs(&rpc.GetParentIDsArgs{IssueIDs: issueIDs[i:end]})
		if err != nil {
			log.Printf("Failed to get parent IDs (batch %d-%d): %v", i, end, err)
			*rpcOK = false
			continue
		}
		for k, v := range batch.Parents {
			parentResp.Parents[k] = v
		}
	}

	if kp.IncludeBlocked {
		blockedMap := make(map[string]*types.BlockedIssue)
		blockedResp, blockedErr := client.Blocked(&rpc.BlockedArgs{})
		if blockedErr != nil {
			*rpcOK = false
			log.Printf("Failed to get blocked issues: %v", blockedErr)
		} else if blockedResp.Success {
			var blockedIssues []*types.BlockedIssue
			if jsonErr := json.Unmarshal(blockedResp.Data, &blockedIssues); jsonErr == nil {
				for _, bi := range blockedIssues {
					blockedMap[bi.Issue.ID] = bi
				}
			}
		}

		unclosedIDs, issueMap := buildUnclosedSetsFromFetched(issuesWithCounts)
		kanbanIssues := make([]*KanbanIssue, len(issuesWithCounts))
		for i, iwc := range issuesWithCounts {
			ki := &KanbanIssue{IssueWithCounts: iwc}
			if parentInfo, ok := parentResp.Parents[iwc.Issue.ID]; ok {
				ki.Parent = &parentInfo.ParentID
				ki.ParentTitle = &parentInfo.ParentTitle
			}
			if iwc.Issue.SourceRepo != "" {
				ki.Repo = &iwc.Issue.SourceRepo
			}
			refs := getUnclosedBlockerRefs(iwc.Issue.Dependencies, unclosedIDs, issueMap)
			if len(refs) > 0 {
				ki.IsBlocked = true
				ki.BlockedByCount = len(refs)
				ki.BlockedBy = extractBlockerIDs(refs)
				ki.BlockedByDetails = refs
			} else if bi, ok := blockedMap[iwc.Issue.ID]; ok {
				ki.IsBlocked = true
				ki.BlockedByCount = bi.BlockedByCount
				ki.BlockedBy = bi.BlockedBy
				ki.BlockedByDetails = bi.BlockedByDetails
			}
			kanbanIssues[i] = ki
		}

		data, _ := json.Marshal(kanbanIssues)
		respondJSON(w, http.StatusOK, IssuesResponse{Success: true, Data: data})
		return
	}

	issuesWithParent := make([]*IssueWithParent, len(issuesWithCounts))
	for i, iwc := range issuesWithCounts {
		iwp := &IssueWithParent{IssueWithCounts: iwc}
		if parentInfo, ok := parentResp.Parents[iwc.Issue.ID]; ok {
			iwp.Parent = &parentInfo.ParentID
			iwp.ParentTitle = &parentInfo.ParentTitle
		}
		if iwc.Issue.SourceRepo != "" {
			iwp.Repo = &iwc.Issue.SourceRepo
		}
		issuesWithParent[i] = iwp
	}

	data, _ := json.Marshal(issuesWithParent)
	respondJSON(w, http.StatusOK, IssuesResponse{Success: true, Data: data})
}
