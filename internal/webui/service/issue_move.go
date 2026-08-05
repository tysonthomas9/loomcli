package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/types"
)

func (s *issueServiceImpl) MoveIssue(ctx context.Context, params MoveIssueParams) (*MoveIssueResult, error) {
	return s.moveIssueViaBackend(ctx, params)
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
