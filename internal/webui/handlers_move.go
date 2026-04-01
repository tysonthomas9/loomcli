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

// MoveIssueRequest is the JSON body for POST /api/issues/{id}/move.
type MoveIssueRequest struct {
	TargetWorkspace string `json:"target_workspace"`
}

// MoveIssueResponse is the JSON response for the move endpoint.
type MoveIssueResponse struct {
	Success bool        `json:"success"`
	Data    *MoveResult `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// MoveResult contains the outcome of a move operation.
type MoveResult struct {
	SourceID string   `json:"source_id"`
	TargetID string   `json:"target_id"`
	Warnings []string `json:"warnings,omitempty"`
}

// issueMover is an internal interface for testing move operations.
// The production code uses *rpc.Client which implements this interface.
type issueMover interface {
	Show(args *rpc.ShowArgs) (*rpc.Response, error)
	Create(args *rpc.CreateArgs) (*rpc.Response, error)
	AddComment(args *rpc.CommentAddArgs) (*rpc.Response, error)
	CloseIssue(args *rpc.CloseArgs) (*rpc.Response, error)
}

// moveConnectionGetter is an internal interface for testing connection pool operations.
type moveConnectionGetter interface {
	Get(ctx context.Context) (issueMover, error)
	Put(client issueMover)
}

// movePoolAdapter wraps daemon.Pool to implement moveConnectionGetter.
type movePoolAdapter struct {
	pool daemon.Pool
}

func (p *movePoolAdapter) Get(ctx context.Context) (issueMover, error) {
	return p.pool.Get(ctx)
}

func (p *movePoolAdapter) Put(client issueMover) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Put(c)
	} else if client != nil {
		slog.Warn("movePoolAdapter.Put: unexpected client type — connection leaked", "client_type", fmt.Sprintf("%T", client))
	}
}

// handleMoveIssue returns a handler that moves an issue to a different workspace.
// multiPool, when non-nil, is used to acquire a connection to the target workspace
// so that Create routes to the correct daemon. When nil, cross-workspace moves
// are rejected with 400.
func handleMoveIssue(pool daemon.Pool, multiPool *daemon.MultiPool, workspaceConfigFn func() (*WorkspaceData, error)) http.HandlerFunc {
	var sourcePool moveConnectionGetter
	if pool != nil {
		sourcePool = &movePoolAdapter{pool: pool}
	}
	var targetPool moveConnectionGetter
	if multiPool != nil {
		targetPool = &movePoolAdapter{pool: multiPool}
	}
	return handleMoveIssueWithPool(sourcePool, targetPool, workspaceConfigFn)
}

// validateMoveRequest parses and validates the move request.
// Returns the issue ID, target workspace, workspace data, and whether validation passed.
func validateMoveRequest(w http.ResponseWriter, r *http.Request, workspaceConfigFn func() (*WorkspaceData, error)) (string, string, *WorkspaceData, bool) {
	issueID := r.PathValue("id")
	if issueID == "" {
		respondJSON(w, http.StatusBadRequest, MoveIssueResponse{Success: false, Error: "missing issue ID in path"})
		return "", "", nil, false
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	var req MoveIssueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			respondJSON(w, http.StatusRequestEntityTooLarge, MoveIssueResponse{Success: false, Error: "request body too large (max 1MB)"})
			return "", "", nil, false
		}
		respondJSON(w, http.StatusBadRequest, MoveIssueResponse{Success: false, Error: "invalid request body"})
		return "", "", nil, false
	}

	targetWorkspace := strings.TrimSpace(req.TargetWorkspace)
	if targetWorkspace == "" {
		respondJSON(w, http.StatusBadRequest, MoveIssueResponse{Success: false, Error: "target_workspace is required"})
		return "", "", nil, false
	}

	if workspaceConfigFn == nil {
		respondJSON(w, http.StatusBadRequest, MoveIssueResponse{Success: false, Error: "workspace configuration not available"})
		return "", "", nil, false
	}

	wsData, err := workspaceConfigFn()
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, MoveIssueResponse{Success: false, Error: "failed to load workspace config"})
		return "", "", nil, false
	}

	if !workspaceExists(wsData.Workspaces, targetWorkspace) {
		respondJSON(w, http.StatusBadRequest, MoveIssueResponse{Success: false, Error: fmt.Sprintf("workspace %q not found", targetWorkspace)})
		return "", "", nil, false
	}

	if wsData.Name == targetWorkspace {
		respondJSON(w, http.StatusBadRequest, MoveIssueResponse{Success: false, Error: "cannot move issue to the same workspace"})
		return "", "", nil, false
	}

	return issueID, targetWorkspace, wsData, true
}

// workspaceExists checks if a workspace name exists in the list.
func workspaceExists(workspaces []WorkspaceSummary, name string) bool {
	for _, ws := range workspaces {
		if ws.Name == name {
			return true
		}
	}
	return false
}

// resolveWorkspaceID maps a workspace name to its stable UUID.
// Returns the UUID from WorkspaceSummary.ID when available, otherwise
// falls back to the name (pre-UUID migration compatibility).
func resolveWorkspaceID(wsData *WorkspaceData, name string) string {
	for _, ws := range wsData.Workspaces {
		if ws.Name == name {
			if ws.ID != "" {
				return ws.ID
			}
			return ws.Name
		}
	}
	return name
}

// fetchSourceIssue retrieves and validates the source issue via RPC.
func fetchSourceIssue(w http.ResponseWriter, client issueMover, issueID string) (*types.Issue, bool) {
	showResp, err := client.Show(&rpc.ShowArgs{ID: issueID})
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			respondJSON(w, http.StatusNotFound, MoveIssueResponse{Success: false, Error: fmt.Sprintf("issue not found: %s", issueID)})
			return nil, false
		}
		slog.Error("RPC show error in handleMoveIssue", "err", err)
		respondJSON(w, http.StatusInternalServerError, MoveIssueResponse{Success: false, Error: "failed to get source issue"})
		return nil, false
	}
	if !showResp.Success {
		status := http.StatusInternalServerError
		if strings.Contains(showResp.Error, "not found") {
			status = http.StatusNotFound
		}
		respondJSON(w, status, MoveIssueResponse{Success: false, Error: showResp.Error})
		return nil, false
	}

	var sourceIssue types.Issue
	if err := json.Unmarshal(showResp.Data, &sourceIssue); err != nil {
		slog.Error("failed to parse source issue in handleMoveIssue", "err", err)
		respondJSON(w, http.StatusInternalServerError, MoveIssueResponse{Success: false, Error: "failed to parse source issue"})
		return nil, false
	}

	if sourceIssue.Status == types.StatusClosed {
		respondJSON(w, http.StatusBadRequest, MoveIssueResponse{Success: false, Error: "cannot move a closed issue"})
		return nil, false
	}

	return &sourceIssue, true
}

// buildCreateArgs converts a source issue into RPC CreateArgs for the move target.
func buildCreateArgs(source *types.Issue, issueID string) *rpc.CreateArgs {
	description := source.Description
	if description != "" {
		description += "\n\n"
	}
	description += fmt.Sprintf("(Moved from %s)", issueID)

	externalRef := ""
	if source.ExternalRef != nil {
		externalRef = *source.ExternalRef
	}

	return &rpc.CreateArgs{
		Title:              source.Title,
		Description:        description,
		IssueType:          string(source.IssueType),
		Priority:           source.Priority,
		Design:             source.Design,
		AcceptanceCriteria: source.AcceptanceCriteria,
		Notes:              source.Notes,
		Assignee:           source.Assignee,
		ExternalRef:        externalRef,
		EstimatedMinutes:   source.EstimatedMinutes,
		Labels:             source.Labels,
		Owner:              source.Owner,
		CreatedBy:          "web-ui",
		DueAt:              formatTimePtr(source.DueAt),
		DeferUntil:         formatTimePtr(source.DeferUntil),
	}
}

// createIssueInTarget acquires a client from the target pool and creates the
// issue there. On error it writes the HTTP response and returns an error.
// On success it returns the new issue ID.
func createIssueInTarget(w http.ResponseWriter, targetPool moveConnectionGetter, targetCtx context.Context, targetWorkspace string, sourceIssue *types.Issue, sourceID string) (string, error) {
	targetClient, err := targetPool.Get(targetCtx)
	if err != nil {
		if errors.Is(err, daemon.ErrWorkspaceNotRegistered) {
			respondJSON(w, http.StatusBadRequest, MoveIssueResponse{Success: false, Error: fmt.Sprintf("target workspace %q not registered", targetWorkspace)})
			return "", err
		}
		slog.Error("target pool error in handleMoveIssue", "workspace", targetWorkspace, "err", err)
		respondJSON(w, http.StatusBadGateway, MoveIssueResponse{Success: false, Error: "target workspace daemon not available"})
		return "", err
	}
	defer targetPool.Put(targetClient)

	createResp, err := targetClient.Create(buildCreateArgs(sourceIssue, sourceID))
	if err != nil {
		slog.Error("RPC create error in handleMoveIssue", "err", err)
		respondJSON(w, http.StatusInternalServerError, MoveIssueResponse{Success: false, Error: "failed to create issue in target workspace"})
		return "", err
	}
	if !createResp.Success {
		respondJSON(w, http.StatusInternalServerError, MoveIssueResponse{Success: false, Error: fmt.Sprintf("failed to create issue: %s", createResp.Error)})
		return "", fmt.Errorf("create failed: %s", createResp.Error)
	}

	var createdIssue types.Issue
	if err := json.Unmarshal(createResp.Data, &createdIssue); err != nil {
		slog.Error("failed to parse created issue in handleMoveIssue", "err", err)
		respondJSON(w, http.StatusInternalServerError, MoveIssueResponse{Success: false, Error: "issue created but failed to parse response"})
		return "", err
	}

	return createdIssue.ID, nil
}

// handleMoveIssueWithPool is the internal implementation that accepts interfaces for testing.
// targetPool is used to acquire a connection to the target workspace for Create.
// When targetPool is nil, cross-workspace moves are rejected.
func handleMoveIssueWithPool(pool moveConnectionGetter, targetPool moveConnectionGetter, workspaceConfigFn func() (*WorkspaceData, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if pool == nil {
			respondJSON(w, http.StatusServiceUnavailable, MoveIssueResponse{Success: false, Error: "connection pool not initialized"})
			return
		}

		issueID, targetWorkspace, wsData, ok := validateMoveRequest(w, r, workspaceConfigFn)
		if !ok {
			return
		}

		if targetPool == nil {
			respondJSON(w, http.StatusBadRequest, MoveIssueResponse{Success: false, Error: "cross-workspace move requires multi-workspace mode"})
			return
		}

		// Resolve target workspace name → ID before any RPC calls.
		targetWsID := resolveWorkspaceID(wsData, targetWorkspace)

		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		// Source client — used for Show, AddComment, CloseIssue
		client, err := pool.Get(ctx)
		if err != nil {
			status := http.StatusServiceUnavailable
			if errors.Is(err, context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
			}
			slog.Error("pool error in handleMoveIssue", "err", err)
			respondJSON(w, status, MoveIssueResponse{Success: false, Error: "daemon not available"})
			return
		}
		defer pool.Put(client)

		sourceIssue, ok := fetchSourceIssue(w, client, issueID)
		if !ok {
			return
		}

		var warnings []string
		if sourceIssue.Assignee != "" {
			warnings = append(warnings, fmt.Sprintf("Active agent %q assigned to this issue. Moving will not stop the agent.", sourceIssue.Assignee))
		}

		// Create issue in target workspace via the target pool.
		targetCtx := WithWorkspace(ctx, targetWsID)
		newID, err := createIssueInTarget(w, targetPool, targetCtx, targetWorkspace, sourceIssue, issueID)
		if err != nil {
			return // createIssueInTarget already wrote the HTTP response
		}

		// Add comment on source issue noting the move (via source client)
		commentText := fmt.Sprintf("Moved to %s in workspace %q", newID, targetWorkspace)
		if _, err := client.AddComment(&rpc.CommentAddArgs{ID: issueID, Author: "web-ui", Text: commentText}); err != nil {
			slog.Error("failed to add move comment on source", "issue_id", issueID, "err", err)
			warnings = append(warnings, "Failed to add comment on source issue")
		}

		// Close the source issue (via source client)
		closeResp, closeErr := client.CloseIssue(&rpc.CloseArgs{ID: issueID, Reason: fmt.Sprintf("Moved to %s", newID), Force: true})
		if closeErr != nil {
			slog.Error("failed to close source", "issue_id", issueID, "err", closeErr)
			warnings = append(warnings, "Source issue could not be closed")
		} else if !closeResp.Success {
			slog.Error("close failed for source", "issue_id", issueID, "err", closeResp.Error)
			warnings = append(warnings, "Source issue could not be closed")
		}

		respondJSON(w, http.StatusOK, MoveIssueResponse{
			Success: true,
			Data:    &MoveResult{SourceID: issueID, TargetID: newID, Warnings: warnings},
		})
	}
}

// formatTimePtr converts a *time.Time to ISO string, or empty if nil.
func formatTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.RFC3339)
}
