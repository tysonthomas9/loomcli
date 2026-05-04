package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

func (s *issueServiceImpl) MoveIssue(ctx context.Context, params MoveIssueParams) (*MoveIssueResult, error) {
	if s.backendFn != nil {
		return s.moveIssueViaBackend(ctx, params)
	}
	return s.moveIssueViaPool(ctx, params)
}

func (s *issueServiceImpl) moveIssueViaPool(ctx context.Context, params MoveIssueParams) (*MoveIssueResult, error) {
	if s.pool == nil {
		return nil, ErrUnavailable("connection pool not initialized")
	}
	if s.multiPool == nil {
		return nil, ErrValidation("cross-workspace move requires multi-workspace mode")
	}
	if params.Validator == nil {
		return nil, ErrValidation("workspace validator is required")
	}

	targetWsID, err := params.Validator.ValidateTarget(params.TargetWorkspace)
	if err != nil {
		return nil, err // Validator returns ServiceError
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Source client
	client, err := s.pool.Get(ctx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, ErrTimeout("timeout connecting to issue backend")
		}
		slog.Error("pool error in MoveIssue", "err", err)
		return nil, ErrUnavailable("issue backend unavailable")
	}
	rpcOK := false
	defer s.releaseClient(client, &rpcOK)

	// Fetch and validate source issue
	sourceIssue, err := s.fetchSourceIssue(client, params.IssueID)
	if err != nil {
		return nil, err
	}

	var warnings []string
	if sourceIssue.Assignee != "" {
		warnings = append(warnings, fmt.Sprintf("Active agent %q assigned to this issue. Moving will not stop the agent.", sourceIssue.Assignee))
	}

	// Create issue in target workspace
	newID, err := s.createIssueInTarget(ctx, targetWsID, params.TargetWorkspace, sourceIssue, params.IssueID)
	if err != nil {
		return nil, err
	}

	// Add comment on source noting the move (non-fatal)
	commentText := fmt.Sprintf("Moved to %s in workspace %q", newID, params.TargetWorkspace)
	_, commentErr := client.AddComment(&rpc.CommentAddArgs{ID: params.IssueID, Author: "web-ui", Text: commentText})
	if commentErr != nil {
		slog.Error("failed to add move comment on source", "issue_id", params.IssueID, "err", commentErr)
		warnings = append(warnings, "Failed to add comment on source issue")
	}

	// Close source issue
	closeResp, closeErr := client.CloseIssue(&rpc.CloseArgs{ID: params.IssueID, Reason: fmt.Sprintf("Moved to %s", newID), Force: true})
	if closeErr != nil {
		slog.Error("failed to close source", "issue_id", params.IssueID, "err", closeErr)
		warnings = append(warnings, "Source issue could not be closed")
	} else if !closeResp.Success {
		slog.Error("close failed for source", "issue_id", params.IssueID, "err", closeResp.Error)
		warnings = append(warnings, "Source issue could not be closed")
	}
	// All RPCs over `client` succeeded at the transport level; safe to return
	// the connection. Any transport error keeps rpcOK false so the connection
	// is discarded (its read buffer may hold stale bytes).
	if commentErr == nil && closeErr == nil {
		rpcOK = true
	}

	return &MoveIssueResult{
		SourceID: params.IssueID,
		TargetID: newID,
		Warnings: warnings,
	}, nil
}

func (s *issueServiceImpl) moveIssueViaBackend(ctx context.Context, params MoveIssueParams) (*MoveIssueResult, error) {
	if strings.TrimSpace(params.IssueID) == "" {
		return nil, ErrValidation("issue ID is required")
	}
	if params.Validator == nil {
		return nil, ErrValidation("workspace validator is required")
	}

	targetWsID, err := params.Validator.ValidateTarget(params.TargetWorkspace)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	sourceBackend, svcErr := s.resolveBackend(ctx)
	if svcErr != nil {
		return nil, svcErr
	}

	sourceIssue, err := sourceBackend.Get(ctx, params.IssueID)
	if err != nil {
		slog.Error("backend get error in MoveIssue", "issue_id", params.IssueID, "err", err)
		return nil, translateBackendError(err)
	}
	if sourceIssue == nil {
		return nil, ErrNotFound(fmt.Sprintf("issue not found: %s", params.IssueID))
	}
	if sourceIssue.Status == string(types.StatusClosed) {
		return nil, ErrValidation("cannot move a closed issue")
	}

	var warnings []string
	if sourceIssue.Assignee != "" {
		warnings = append(warnings, fmt.Sprintf("Active agent %q assigned to this issue. Moving will not stop the agent.", sourceIssue.Assignee))
	}

	targetCtx := ctx
	if s.withWorkspaceFn != nil {
		targetCtx = s.withWorkspaceFn(ctx, targetWsID)
	}
	targetBackend, svcErr := s.resolveBackend(targetCtx)
	if svcErr != nil {
		return nil, svcErr
	}

	created, err := targetBackend.Create(targetCtx, buildMoveCreateBackendParams(sourceIssue, params.IssueID))
	if err != nil {
		slog.Error("backend create error in MoveIssue", "source_issue_id", params.IssueID, "target_workspace", params.TargetWorkspace, "err", err)
		return nil, translateBackendError(err)
	}
	if created == nil || created.ID == "" {
		return nil, ErrInternal("issue created but backend returned no issue ID", nil)
	}

	commentText := fmt.Sprintf("Moved to %s in workspace %q", created.ID, params.TargetWorkspace)
	if _, commentErr := sourceBackend.AddComment(ctx, backend.CommentAddParams{
		IssueID: params.IssueID,
		Author:  "web-ui",
		Text:    commentText,
	}); commentErr != nil {
		slog.Error("failed to add move comment on source", "issue_id", params.IssueID, "err", commentErr)
		warnings = append(warnings, "Failed to add comment on source issue")
	}

	if _, closeErr := sourceBackend.Close(ctx, params.IssueID, backend.CloseParams{
		Reason: fmt.Sprintf("Moved to %s", created.ID),
		Force:  true,
	}); closeErr != nil {
		slog.Error("failed to close source", "issue_id", params.IssueID, "err", closeErr)
		warnings = append(warnings, "Source issue could not be closed")
	}

	return &MoveIssueResult{
		SourceID: params.IssueID,
		TargetID: created.ID,
		Warnings: warnings,
	}, nil
}

func buildMoveCreateBackendParams(source *backend.IssueDetailData, sourceID string) backend.CreateParams {
	description := source.Description
	if description != "" {
		description += "\n\n"
	}
	description += fmt.Sprintf("(Moved from %s)", sourceID)

	return backend.CreateParams{
		Title:              source.Title,
		Description:        description,
		IssueType:          source.IssueType,
		Priority:           source.Priority,
		Design:             source.Design,
		AcceptanceCriteria: source.AcceptanceCriteria,
		Notes:              source.Notes,
		Assignee:           source.Assignee,
		Owner:              source.Owner,
		CreatedBy:          "web-ui",
		ExternalRef:        source.ExternalRef,
		EstimatedMinutes:   source.EstimatedMinutes,
		Labels:             source.Labels,
		DueAt:              formatTimePtr(source.DueAt),
		DeferUntil:         formatTimePtr(source.DeferUntil),
	}
}

func (s *issueServiceImpl) fetchSourceIssue(client *rpc.Client, issueID string) (*types.Issue, error) {
	showResp, err := client.Show(&rpc.ShowArgs{ID: issueID})
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, ErrNotFound(fmt.Sprintf("issue not found: %s", issueID))
		}
		slog.Error("RPC show error in MoveIssue", "err", err)
		return nil, ErrInternal("failed to get source issue", err)
	}
	if !showResp.Success {
		if strings.Contains(showResp.Error, "not found") {
			return nil, ErrNotFound(showResp.Error)
		}
		return nil, ErrInternal(showResp.Error, nil)
	}

	var sourceIssue types.Issue
	if err := json.Unmarshal(showResp.Data, &sourceIssue); err != nil {
		slog.Error("failed to parse source issue in MoveIssue", "err", err)
		return nil, ErrInternal("failed to parse source issue", err)
	}

	if sourceIssue.Status == types.StatusClosed {
		return nil, ErrValidation("cannot move a closed issue")
	}

	return &sourceIssue, nil
}

func (s *issueServiceImpl) createIssueInTarget(ctx context.Context, targetWsID, targetWorkspace string, sourceIssue *types.Issue, sourceID string) (string, error) {
	targetCtx := s.withWorkspaceFn(ctx, targetWsID)
	targetClient, err := s.multiPool.Get(targetCtx)
	if err != nil {
		if errors.Is(err, daemon.ErrWorkspaceNotRegistered) {
			return "", ErrValidation(fmt.Sprintf("target workspace %q not registered", targetWorkspace))
		}
		slog.Error("target pool error in MoveIssue", "workspace", targetWorkspace, "err", err)
		return "", ErrUnavailable("target workspace issue backend unavailable")
	}
	rpcOK := false
	defer func() {
		if rpcOK {
			s.multiPool.Put(targetClient)
		} else {
			s.multiPool.Discard(targetClient)
		}
	}()

	createArgs := buildMoveCreateArgs(sourceIssue, sourceID)
	createResp, err := targetClient.Create(createArgs)
	if err != nil {
		slog.Error("RPC create error in MoveIssue", "err", err)
		return "", ErrInternal("failed to create issue in target workspace", err)
	}
	rpcOK = true
	if !createResp.Success {
		return "", ErrInternal(fmt.Sprintf("failed to create issue: %s", createResp.Error), nil)
	}

	var createdIssue types.Issue
	if err := json.Unmarshal(createResp.Data, &createdIssue); err != nil {
		slog.Error("failed to parse created issue in MoveIssue", "err", err)
		return "", ErrInternal("issue created but failed to parse response", err)
	}

	return createdIssue.ID, nil
}
