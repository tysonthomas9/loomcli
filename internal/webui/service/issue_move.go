package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

func (s *issueServiceImpl) MoveIssue(ctx context.Context, params MoveIssueParams) (*MoveIssueResult, error) {
	if s.pool == nil {
		return nil, ErrUnavailable("connection pool not initialized")
	}
	if s.multiPool == nil {
		return nil, ErrValidation("cross-workspace move requires multi-workspace mode")
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
			return nil, ErrTimeout("timeout connecting to daemon")
		}
		slog.Error("pool error in MoveIssue", "err", err)
		return nil, ErrUnavailable("daemon not available")
	}
	defer s.pool.Put(client)

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
	if _, commentErr := client.AddComment(&rpc.CommentAddArgs{ID: params.IssueID, Author: "web-ui", Text: commentText}); commentErr != nil {
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

	return &MoveIssueResult{
		SourceID: params.IssueID,
		TargetID: newID,
		Warnings: warnings,
	}, nil
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
		return "", ErrUnavailable("target workspace daemon not available")
	}
	defer s.multiPool.Put(targetClient)

	createArgs := buildMoveCreateArgs(sourceIssue, sourceID)
	createResp, err := targetClient.Create(createArgs)
	if err != nil {
		slog.Error("RPC create error in MoveIssue", "err", err)
		return "", ErrInternal("failed to create issue in target workspace", err)
	}
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
