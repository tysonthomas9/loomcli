package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
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

// issueUpdater is an internal interface for testing issue updates.
// The production code uses *rpc.Client which implements this interface.
type issueUpdater interface {
	Update(args *rpc.UpdateArgs) (*rpc.Response, error)
}

// patchConnectionGetter is an internal interface for testing PATCH handler pool operations.
type patchConnectionGetter interface {
	Get(ctx context.Context) (issueUpdater, error)
	Put(client issueUpdater)
}

// patchPoolAdapter wraps daemon.Pool to implement patchConnectionGetter.
type patchPoolAdapter struct {
	pool daemon.Pool
}

func (p *patchPoolAdapter) Get(ctx context.Context) (issueUpdater, error) {
	return p.pool.Get(ctx)
}

func (p *patchPoolAdapter) Put(client issueUpdater) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Put(c)
	}
}

// issueCloser is an internal interface for testing issue close operations.
// The production code uses *rpc.Client which implements this interface.
type issueCloser interface {
	CloseIssue(args *rpc.CloseArgs) (*rpc.Response, error)
}

// closeConnectionGetter is an internal interface for testing close handler pool operations.
type closeConnectionGetter interface {
	Get(ctx context.Context) (issueCloser, error)
	Put(client issueCloser)
}

// closePoolAdapter wraps daemon.Pool to implement closeConnectionGetter.
type closePoolAdapter struct {
	pool daemon.Pool
}

func (p *closePoolAdapter) Get(ctx context.Context) (issueCloser, error) {
	return p.pool.Get(ctx)
}

func (p *closePoolAdapter) Put(client issueCloser) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Put(c)
	}
}

// issueCreator is an internal interface for testing issue creation.
// The production code uses *rpc.Client which implements this interface.
type issueCreator interface {
	Create(args *rpc.CreateArgs) (*rpc.Response, error)
}

// createConnectionGetter is an internal interface for testing connection pool operations for create.
type createConnectionGetter interface {
	Get(ctx context.Context) (issueCreator, error)
	Put(client issueCreator)
}

// createPoolAdapter wraps daemon.Pool to implement createConnectionGetter.
type createPoolAdapter struct {
	pool daemon.Pool
}

func (p *createPoolAdapter) Get(ctx context.Context) (issueCreator, error) {
	return p.pool.Get(ctx)
}

func (p *createPoolAdapter) Put(client issueCreator) {
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
			log.Printf("RPC error in handleGetIssue for %s: %v", issueID, err)
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
			log.Printf("Connection pool error: %v", err)
			writeIssuesError(w, status, message, code)
			return
		}
		defer pool.Put(client)

		// Execute List RPC call
		resp, err := client.List(args)
		if err != nil {
			log.Printf("RPC error: %v", err)
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
			log.Printf("Failed to parse issues: %v", err)
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
				log.Printf("Failed to marshal empty response: %v", err)
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

		// Get parent info for all issues
		parentResp, err := client.GetParentIDs(&rpc.GetParentIDsArgs{IssueIDs: issueIDs})
		if err != nil {
			// Non-fatal: log and continue without parent info
			log.Printf("Failed to get parent IDs: %v", err)
			parentResp = &rpc.GetParentIDsResponse{Parents: make(map[string]*rpc.ParentInfo)}
		}

		if kp.IncludeBlocked {
			// Build blocked info map
			blockedMap := make(map[string]*types.BlockedIssue)
			blockedResp, blockedErr := client.Blocked(&rpc.BlockedArgs{})
			if blockedErr != nil {
				// Non-fatal: log and continue without blocked info
				log.Printf("Failed to get blocked issues: %v", blockedErr)
			} else if blockedResp.Success {
				var blockedIssues []*types.BlockedIssue
				if jsonErr := json.Unmarshal(blockedResp.Data, &blockedIssues); jsonErr != nil {
					log.Printf("Failed to parse blocked issues: %v", jsonErr)
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
				log.Printf("Failed to marshal kanban issues: %v", err)
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
			issuesWithParent[i] = iwp
		}

		data, err := json.Marshal(issuesWithParent)
		if err != nil {
			log.Printf("Failed to marshal issues: %v", err)
			writeIssuesError(w, http.StatusInternalServerError, "failed to encode response", "ENCODE_ERROR")
			return
		}
		respondJSON(w, http.StatusOK, IssuesResponse{
			Success: true,
			Data:    data,
		})
	}
}

// handlePatchIssue returns a handler that performs partial updates on an issue.
func handlePatchIssue(pool daemon.Pool) http.HandlerFunc {
	if pool == nil {
		return handlePatchIssueWithPool(nil)
	}
	return handlePatchIssueWithPool(&patchPoolAdapter{pool: pool})
}

// handlePatchIssueWithPool is the internal implementation that accepts an interface for testing.
func handlePatchIssueWithPool(pool patchConnectionGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Extract and validate issue ID from path
		issueID := r.PathValue("id")
		if issueID == "" {
			respondJSON(w, http.StatusBadRequest, PatchIssueResponse{
				Success: false,
				Error:   "missing issue ID in path",
			})
			return
		}

		// Check pool availability
		if pool == nil {
			respondJSON(w, http.StatusServiceUnavailable, PatchIssueResponse{
				Success: false,
				Error:   "connection pool not initialized",
			})
			return
		}

		// Limit request body size to prevent DoS attacks
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)

		// Parse request body
		var req PatchIssueRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				respondJSON(w, http.StatusRequestEntityTooLarge, PatchIssueResponse{
					Success: false,
					Error:   "request body too large (max 1MB)",
				})
				return
			}
			log.Printf("Invalid request body in handlePatchIssue: %v", err)
			respondJSON(w, http.StatusBadRequest, PatchIssueResponse{
				Success: false,
				Error:   "invalid request body",
			})
			return
		}

		// Acquire connection with timeout
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		client, err := pool.Get(ctx)
		if err != nil {
			status := http.StatusServiceUnavailable
			if errors.Is(err, context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
			}
			log.Printf("Pool error in handlePatchIssue: %v", err)
			respondJSON(w, status, PatchIssueResponse{
				Success: false,
				Error:   "daemon not available",
			})
			return
		}
		defer pool.Put(client)

		// Build UpdateArgs from request
		updateArgs := &rpc.UpdateArgs{
			ID:                 issueID,
			Title:              req.Title,
			Description:        req.Description,
			Status:             req.Status,
			Priority:           req.Priority,
			Assignee:           req.Assignee,
			Design:             req.Design,
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
		}

		// Execute update
		resp, err := client.Update(updateArgs)
		if err != nil {
			errMsg := err.Error()
			status := http.StatusInternalServerError
			if strings.Contains(errMsg, "not found") {
				status = http.StatusNotFound
			}
			log.Printf("RPC error in handlePatchIssue for %s: %v", issueID, err)
			respondJSON(w, status, PatchIssueResponse{
				Success: false,
				Error:   "internal server error",
			})
			return
		}

		if !resp.Success {
			status := http.StatusInternalServerError
			if strings.Contains(resp.Error, "not found") {
				status = http.StatusNotFound
			} else if strings.Contains(resp.Error, "cannot update template") {
				status = http.StatusConflict
			}
			respondJSON(w, status, PatchIssueResponse{
				Success: false,
				Error:   resp.Error,
			})
			return
		}

		respondJSON(w, http.StatusOK, PatchIssueResponse{
			Success: true,
			Data:    map[string]string{"id": issueID, "status": "updated"},
		})
	}
}

// handleCreateIssue returns a handler that creates a new issue.
func handleCreateIssue(pool daemon.Pool) http.HandlerFunc {
	if pool == nil {
		return func(w http.ResponseWriter, r *http.Request) {
			writeIssuesError(w, http.StatusServiceUnavailable, "connection pool not initialized", "POOL_NOT_INITIALIZED")
		}
	}
	return handleCreateIssueWithPool(&createPoolAdapter{pool: pool})
}

// handleCreateIssueWithPool is the internal implementation that accepts an interface for testing.
func handleCreateIssueWithPool(pool createConnectionGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Limit request body size to prevent DoS attacks
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)

		// Parse request body
		var req IssueCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			// Check if it's a request body too large error
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				writeIssuesError(w, http.StatusRequestEntityTooLarge, "request body too large (max 1MB)", "REQUEST_TOO_LARGE")
				return
			}
			log.Printf("Invalid JSON body in handleCreateIssue: %v", err)
			writeIssuesError(w, http.StatusBadRequest, "invalid request body", "INVALID_JSON")
			return
		}

		// Validate required fields
		if err := validateCreateRequest(&req); err != nil {
			writeIssuesError(w, http.StatusBadRequest, err.Error(), "VALIDATION_ERROR")
			return
		}

		// Acquire connection with 30-second timeout for create operations
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
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
		defer pool.Put(client)

		// Convert request to RPC args and call daemon
		createArgs := toCreateArgs(&req)
		resp, err := client.Create(createArgs)
		if err != nil {
			log.Printf("RPC error: %v", err)
			writeIssuesError(w, http.StatusInternalServerError, "failed to create issue", "RPC_ERROR")
			return
		}

		if !resp.Success {
			writeIssuesError(w, http.StatusInternalServerError, resp.Error, "DAEMON_ERROR")
			return
		}

		// Return success with created issue
		respondJSON(w, http.StatusCreated, IssuesResponse{
			Success: true,
			Data:    resp.Data,
		})
	}
}

// handleCloseIssue returns a handler that closes an issue by ID.
func handleCloseIssue(pool daemon.Pool) http.HandlerFunc {
	if pool == nil {
		return handleCloseIssueWithPool(nil)
	}
	return handleCloseIssueWithPool(&closePoolAdapter{pool: pool})
}

// handleCloseIssueWithPool is the internal implementation that accepts an interface for testing.
func handleCloseIssueWithPool(pool closeConnectionGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Extract issue ID from path parameter
		issueID := r.PathValue("id")
		if issueID == "" {
			respondError(w, http.StatusBadRequest, "missing issue ID")
			return
		}

		// Limit request body size to prevent DoS attacks
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)

		// Parse optional JSON body
		var req CloseRequest
		if r.Body != nil && r.ContentLength > 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				var maxBytesErr *http.MaxBytesError
				if errors.As(err, &maxBytesErr) {
					respondError(w, http.StatusRequestEntityTooLarge, "request body too large (max 1MB)")
					return
				}
				log.Printf("Invalid request body in handleCloseIssue: %v", err)
				respondError(w, http.StatusBadRequest, "invalid request body")
				return
			}
		}

		// Check pool availability
		if pool == nil {
			respondError(w, http.StatusServiceUnavailable, "connection pool not initialized")
			return
		}

		// Get connection from pool
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		client, err := pool.Get(ctx)
		if err != nil {
			status := http.StatusServiceUnavailable
			if errors.Is(err, context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
			}
			respondError(w, status, "daemon not available")
			return
		}
		defer pool.Put(client)

		// Build CloseArgs from path and body
		args := &rpc.CloseArgs{
			ID:          issueID,
			Reason:      req.Reason,
			Session:     req.Session,
			SuggestNext: req.SuggestNext,
			Force:       req.Force,
		}

		// Call CloseIssue RPC
		resp, err := client.CloseIssue(args)
		if err != nil {
			// Check if it's a "not found" error
			if strings.Contains(err.Error(), "not found") {
				respondError(w, http.StatusNotFound, fmt.Sprintf("issue not found: %s", issueID))
				return
			}
			// Check for "has open blockers" error (when force=false)
			if strings.Contains(err.Error(), "blocker") {
				respondError(w, http.StatusConflict, err.Error())
				return
			}
			log.Printf("RPC error in handleCloseIssue for %s: %v", issueID, err)
			respondError(w, http.StatusInternalServerError, "internal server error")
			return
		}

		if !resp.Success {
			respondError(w, http.StatusInternalServerError, resp.Error)
			return
		}

		// Return success response with closed issue data
		respondJSON(w, http.StatusOK, CloseResponse{
			Success: true,
			Data:    resp.Data,
		})
	}
}

// validateCreateRequest validates the required fields in a create request.
func validateCreateRequest(req *IssueCreateRequest) error {
	// Validate title
	if strings.TrimSpace(req.Title) == "" {
		return fmt.Errorf("title is required")
	}

	// Validate issue_type
	if req.IssueType == "" {
		return fmt.Errorf("issue_type is required")
	}
	if !validIssueTypes[req.IssueType] {
		return fmt.Errorf("invalid issue_type: %s (must be bug, feature, task, epic, or chore)", req.IssueType)
	}

	// Validate priority (0-4 are valid, where 0 is P0/critical)
	if req.Priority < 0 || req.Priority > 4 {
		return fmt.Errorf("priority must be between 0 and 4 (got %d)", req.Priority)
	}

	// Validate array lengths to prevent resource exhaustion
	if len(req.Labels) > maxLabels {
		return fmt.Errorf("too many labels (max %d, got %d)", maxLabels, len(req.Labels))
	}
	if len(req.Dependencies) > maxDependencies {
		return fmt.Errorf("too many dependencies (max %d, got %d)", maxDependencies, len(req.Dependencies))
	}

	return nil
}

// toCreateArgs converts an IssueCreateRequest to rpc.CreateArgs.
func toCreateArgs(req *IssueCreateRequest) *rpc.CreateArgs {
	return &rpc.CreateArgs{
		ID:                 req.ID,
		Parent:             req.Parent,
		Title:              req.Title,
		Description:        req.Description,
		IssueType:          req.IssueType,
		Priority:           req.Priority,
		Design:             req.Design,
		AcceptanceCriteria: req.AcceptanceCriteria,
		Notes:              req.Notes,
		Assignee:           req.Assignee,
		ExternalRef:        req.ExternalRef,
		EstimatedMinutes:   req.EstimatedMinutes,
		Labels:             req.Labels,
		Dependencies:       req.Dependencies,
		CreatedBy:          req.CreatedBy,
		Owner:              req.Owner,
		DueAt:              req.DueAt,
		DeferUntil:         req.DeferUntil,
	}
}

// parseListParams extracts ListArgs from HTTP query parameters.
func parseListParams(r *http.Request) (*rpc.ListArgs, error) {
	query := r.URL.Query()
	args := &rpc.ListArgs{}

	// Basic filters
	if v := query.Get("status"); v != "" {
		args.Status = v
	}
	if v := query.Get("type"); v != "" {
		args.IssueType = v
	}
	if v := query.Get("assignee"); v != "" {
		args.Assignee = v
	}
	if v := query.Get("q"); v != "" {
		args.Query = v
	}

	// Priority (integer)
	if v := query.Get("priority"); v != "" {
		if priority, err := strconv.Atoi(v); err == nil {
			args.Priority = &priority
		}
	}

	// Labels (comma-separated)
	if v := query.Get("labels"); v != "" {
		args.Labels = splitAndTrim(v)
	}

	// Limit (capped at MaxListLimit to prevent DoS)
	if v := query.Get("limit"); v != "" {
		if limit, err := strconv.Atoi(v); err == nil && limit > 0 {
			if limit > MaxListLimit {
				limit = MaxListLimit
			}
			args.Limit = limit
		}
	}

	// Pattern matching
	if v := query.Get("title_contains"); v != "" {
		args.TitleContains = v
	}
	if v := query.Get("description_contains"); v != "" {
		args.DescriptionContains = v
	}
	if v := query.Get("notes_contains"); v != "" {
		args.NotesContains = v
	}

	// Date ranges (validated as RFC3339 or date-only)
	dateParams := []struct {
		param string
		dest  *string
	}{
		{"created_after", &args.CreatedAfter},
		{"created_before", &args.CreatedBefore},
		{"updated_after", &args.UpdatedAfter},
		{"updated_before", &args.UpdatedBefore},
	}
	for _, dp := range dateParams {
		if v := query.Get(dp.param); v != "" {
			if _, err := time.Parse(time.RFC3339, v); err != nil {
				if _, err2 := time.Parse("2006-01-02", v); err2 != nil {
					return nil, fmt.Errorf("invalid %s: expected RFC3339 format (e.g., 2024-01-15T00:00:00Z) or date (2024-01-15)", dp.param)
				}
			}
			*dp.dest = v
		}
	}

	// Empty/null checks
	if v := query.Get("empty_description"); v == "true" {
		args.EmptyDescription = true
	}
	if v := query.Get("no_assignee"); v == "true" {
		args.NoAssignee = true
	}
	if v := query.Get("no_labels"); v == "true" {
		args.NoLabels = true
	}

	// Pinned filtering
	if v := query.Get("pinned"); v != "" {
		pinned := v == "true"
		args.Pinned = &pinned
	}

	return args, nil
}

// kanbanParams holds the additional query parameters for Kanban-enriched responses.
type kanbanParams struct {
	ExcludeStatus  []string // Statuses to exclude from results
	IncludeBlocked bool     // Whether to include blocked dependency info
}

// MaxExcludeStatuses caps the number of exclude_status values to prevent abuse.
const MaxExcludeStatuses = 1000

func parseKanbanParams(r *http.Request) (*kanbanParams, error) {
	params := &kanbanParams{}
	q := r.URL.Query()

	if v := q.Get("exclude_status"); v != "" {
		statuses := splitAndTrim(v)
		if len(statuses) > MaxExcludeStatuses {
			return nil, fmt.Errorf("too many exclude_status values (max %d)", MaxExcludeStatuses)
		}
		params.ExcludeStatus = statuses
	}

	if v := q.Get("include_blocked"); v == "true" {
		params.IncludeBlocked = true
	}

	return params, nil
}

// fetchUnclosedIDSetAndMap fetches all issues via client.List and returns:
//   - unclosedIDs: set of issue IDs with status != closed
//   - issueMap: lookup map for populating blocker details (title, priority)
//
// Returns nil, nil on error (non-fatal — caller falls back to daemon data).
func fetchUnclosedIDSetAndMap(client *rpc.Client) (map[string]bool, map[string]*types.IssueWithCounts) {
	resp, err := client.List(&rpc.ListArgs{Limit: MaxListLimit})
	if err != nil {
		log.Printf("Failed to fetch issues for blocker detection: %v", err)
		return nil, nil
	}
	if !resp.Success {
		log.Printf("List RPC failed for blocker detection: %s", resp.Error)
		return nil, nil
	}

	var allIssues []*types.IssueWithCounts
	if err := json.Unmarshal(resp.Data, &allIssues); err != nil {
		log.Printf("Failed to parse issues for blocker detection: %v", err)
		return nil, nil
	}

	unclosedIDs := make(map[string]bool, len(allIssues))
	issueMap := make(map[string]*types.IssueWithCounts, len(allIssues))
	for _, iwc := range allIssues {
		issueMap[iwc.Issue.ID] = iwc
		if iwc.Issue.Status != types.StatusClosed {
			unclosedIDs[iwc.Issue.ID] = true
		}
	}
	return unclosedIDs, issueMap
}

// getUnclosedBlockerRefs returns BlockerRef entries for each "blocks" dependency
// that points to an unclosed issue. Populates title/priority from issueMap.
func getUnclosedBlockerRefs(deps []*types.Dependency, unclosedIDs map[string]bool, issueMap map[string]*types.IssueWithCounts) []types.BlockerRef {
	var refs []types.BlockerRef
	for _, dep := range deps {
		if dep.Type == types.DepBlocks && unclosedIDs[dep.DependsOnID] {
			ref := types.BlockerRef{ID: dep.DependsOnID}
			if blocker, ok := issueMap[dep.DependsOnID]; ok {
				ref.Title = blocker.Issue.Title
				ref.Priority = blocker.Issue.Priority
			}
			refs = append(refs, ref)
		}
	}
	return refs
}

// extractBlockerIDs extracts issue IDs from a slice of BlockerRefs.
func extractBlockerIDs(refs []types.BlockerRef) []string {
	ids := make([]string, len(refs))
	for i, ref := range refs {
		ids[i] = ref.ID
	}
	return ids
}
