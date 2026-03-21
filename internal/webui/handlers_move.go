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
	}
}

// handleMoveIssue returns a handler that moves an issue to a different workspace.
func handleMoveIssue(pool daemon.Pool, workspaceConfigFn func() (*WorkspaceData, error)) http.HandlerFunc {
	if pool == nil {
		return handleMoveIssueWithPool(nil, workspaceConfigFn)
	}
	return handleMoveIssueWithPool(&movePoolAdapter{pool: pool}, workspaceConfigFn)
}

// validateMoveRequest parses and validates the move request.
// Returns the issue ID, target workspace, and whether validation passed.
func validateMoveRequest(w http.ResponseWriter, r *http.Request, workspaceConfigFn func() (*WorkspaceData, error)) (string, string, bool) {
	issueID := r.PathValue("id")
	if issueID == "" {
		respondJSON(w, http.StatusBadRequest, MoveIssueResponse{Success: false, Error: "missing issue ID in path"})
		return "", "", false
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	var req MoveIssueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			respondJSON(w, http.StatusRequestEntityTooLarge, MoveIssueResponse{Success: false, Error: "request body too large (max 1MB)"})
			return "", "", false
		}
		respondJSON(w, http.StatusBadRequest, MoveIssueResponse{Success: false, Error: "invalid request body"})
		return "", "", false
	}

	targetWorkspace := strings.TrimSpace(req.TargetWorkspace)
	if targetWorkspace == "" {
		respondJSON(w, http.StatusBadRequest, MoveIssueResponse{Success: false, Error: "target_workspace is required"})
		return "", "", false
	}

	if workspaceConfigFn == nil {
		respondJSON(w, http.StatusBadRequest, MoveIssueResponse{Success: false, Error: "workspace configuration not available"})
		return "", "", false
	}

	wsData, err := workspaceConfigFn()
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, MoveIssueResponse{Success: false, Error: "failed to load workspace config"})
		return "", "", false
	}

	if !workspaceExists(wsData.Workspaces, targetWorkspace) {
		respondJSON(w, http.StatusBadRequest, MoveIssueResponse{Success: false, Error: fmt.Sprintf("workspace %q not found", targetWorkspace)})
		return "", "", false
	}

	if wsData.Name == targetWorkspace {
		respondJSON(w, http.StatusBadRequest, MoveIssueResponse{Success: false, Error: "cannot move issue to the same workspace"})
		return "", "", false
	}

	return issueID, targetWorkspace, true
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

// fetchSourceIssue retrieves and validates the source issue via RPC.
func fetchSourceIssue(w http.ResponseWriter, client issueMover, issueID string) (*types.Issue, bool) {
	showResp, err := client.Show(&rpc.ShowArgs{ID: issueID})
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			respondJSON(w, http.StatusNotFound, MoveIssueResponse{Success: false, Error: fmt.Sprintf("issue not found: %s", issueID)})
			return nil, false
		}
		log.Printf("RPC Show error in handleMoveIssue: %v", err)
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
		log.Printf("Failed to parse source issue in handleMoveIssue: %v", err)
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

// handleMoveIssueWithPool is the internal implementation that accepts interfaces for testing.
func handleMoveIssueWithPool(pool moveConnectionGetter, workspaceConfigFn func() (*WorkspaceData, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if pool == nil {
			respondJSON(w, http.StatusServiceUnavailable, MoveIssueResponse{Success: false, Error: "connection pool not initialized"})
			return
		}

		issueID, targetWorkspace, ok := validateMoveRequest(w, r, workspaceConfigFn)
		if !ok {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		client, err := pool.Get(ctx)
		if err != nil {
			status := http.StatusServiceUnavailable
			if errors.Is(err, context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
			}
			log.Printf("Pool error in handleMoveIssue: %v", err)
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

		// Create new issue in target workspace
		createResp, err := client.Create(buildCreateArgs(sourceIssue, issueID))
		if err != nil {
			log.Printf("RPC Create error in handleMoveIssue: %v", err)
			respondJSON(w, http.StatusInternalServerError, MoveIssueResponse{Success: false, Error: "failed to create issue in target workspace"})
			return
		}
		if !createResp.Success {
			respondJSON(w, http.StatusInternalServerError, MoveIssueResponse{Success: false, Error: fmt.Sprintf("failed to create issue: %s", createResp.Error)})
			return
		}

		var createdIssue types.Issue
		if err := json.Unmarshal(createResp.Data, &createdIssue); err != nil {
			log.Printf("Failed to parse created issue in handleMoveIssue: %v", err)
			respondJSON(w, http.StatusInternalServerError, MoveIssueResponse{Success: false, Error: "issue created but failed to parse response"})
			return
		}

		newID := createdIssue.ID

		// Add comment on source issue noting the move
		commentText := fmt.Sprintf("Moved to %s in workspace %q", newID, targetWorkspace)
		if _, err := client.AddComment(&rpc.CommentAddArgs{ID: issueID, Author: "web-ui", Text: commentText}); err != nil {
			log.Printf("Failed to add move comment on source %s: %v", issueID, err)
			warnings = append(warnings, "Failed to add comment on source issue")
		}

		// Close the source issue
		closeResp, closeErr := client.CloseIssue(&rpc.CloseArgs{ID: issueID, Reason: fmt.Sprintf("Moved to %s", newID), Force: true})
		if closeErr != nil {
			log.Printf("Failed to close source %s: %v", issueID, closeErr)
			warnings = append(warnings, "Source issue could not be closed")
		} else if !closeResp.Success {
			log.Printf("Close failed for source %s: %s", issueID, closeResp.Error)
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
