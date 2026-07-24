package execution

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

func (service *Service) HandoffDriverRunReviewWorkItem(
	ctx context.Context,
	auth authority.ExecutionAuthority,
	command HandoffDriverRunReviewWorkItemCommand,
) (DriverRunWorkItemMutationResult, error) {
	if err := service.requireOwner(ActionHandoffDriverRunReviewWorkItem, command.WorkspaceKey, command.Owner, auth); err != nil {
		return DriverRunWorkItemMutationResult{}, err
	}
	command = normalizeDriverRunReviewHandoffCommand(command)
	if !validDriverRunReviewHandoffCommand(command) {
		return DriverRunWorkItemMutationResult{}, ErrInvalid
	}
	port := service.dependencies.DriverRuns.WorkItems
	if port == nil {
		return DriverRunWorkItemMutationResult{}, ErrUnavailable
	}
	result, err := port.HandoffDriverRunReviewWorkItem(ctx, command)
	if err != nil {
		return DriverRunWorkItemMutationResult{}, err
	}
	if !validDriverRunReviewHandoffResult(command, result) {
		return DriverRunWorkItemMutationResult{}, fmt.Errorf("%w: review handoff escaped DriverRun owner envelope", ErrConflict)
	}
	return cloneDriverRunWorkItemMutationResult(result), nil
}

func normalizeDriverRunReviewHandoffCommand(
	command HandoffDriverRunReviewWorkItemCommand,
) HandoffDriverRunReviewWorkItemCommand {
	command.WorkItemID = strings.TrimSpace(command.WorkItemID)
	command.TaskRunID = strings.TrimSpace(command.TaskRunID)
	command.TargetStatus = strings.TrimSpace(command.TargetStatus)
	command.Reason = strings.TrimSpace(command.Reason)
	command.Labels = normalizeDriverRunReviewHandoffLabels(command.Labels)
	return command
}

func validDriverRunReviewHandoffCommand(command HandoffDriverRunReviewWorkItemCommand) bool {
	claimRequestID := ClaimDriverRunWorkItemRequestID(command.Owner.ResourceID, command.WorkItemID)
	return command.Owner.ResourceKind == ResourceDriverRun &&
		command.WorkItemID != "" &&
		command.TaskRunID != "" &&
		command.RequestID == HandoffDriverRunReviewWorkItemRequestID(command.Owner.ResourceID, command.WorkItemID, command.TaskRunID) &&
		command.ClaimActionID == DriverRunWorkItemClaimActionID(claimRequestID) &&
		validDriverRunReviewHandoffTargetStatus(command.TargetStatus) &&
		validDriverRunReviewHandoffAnnotations(command) &&
		!command.HandedOffAt.IsZero()
}

func validDriverRunReviewHandoffTargetStatus(status string) bool {
	return status == DriverRunWorkItemRestoreOpen ||
		status == DriverRunWorkItemRestoreReview ||
		status == "closed"
}

func validDriverRunReviewHandoffResult(
	command HandoffDriverRunReviewWorkItemCommand,
	result DriverRunWorkItemMutationResult,
) bool {
	workItem, action := result.WorkItem, result.Action
	actor := "driver-run:" + command.Owner.ResourceID
	return workItem != nil &&
		workItem.WorkspaceKey == command.WorkspaceKey &&
		workItem.WorkItemID == command.WorkItemID &&
		workItem.Status == command.TargetStatus &&
		workItem.Assignee == "" &&
		validDriverRunReviewHandoffWorkItem(command, workItem) &&
		!workItem.UpdatedAt.IsZero() &&
		validDriverRunWorkItemActionReceipt(
			action, command.WorkspaceKey, command.WorkItemID, actor, "handoff_review_work_item",
			DriverRunReviewWorkItemHandoffActionID(command.RequestID),
			"issue://"+command.WorkItemID+"#handed-off", command.HandedOffAt, "", result.Replay,
		) &&
		workItem.UpdatedAt.Equal(action.CreatedAt) &&
		validDriverRunReviewHandoffComment(command, result.Comment, action.CreatedAt)
}

func validDriverRunReviewHandoffAnnotations(command HandoffDriverRunReviewWorkItemCommand) bool {
	if command.TargetStatus != DriverRunWorkItemRestoreReview {
		return command.Priority == nil && len(command.Labels) == 0 && strings.TrimSpace(command.CommentBody) == ""
	}
	if command.Priority == nil ||
		*command.Priority < DriverRunReviewWorkItemPriorityMin ||
		*command.Priority > DriverRunReviewWorkItemPriorityMax ||
		strings.TrimSpace(command.CommentBody) == "" ||
		len(command.CommentBody) > DriverRunReviewWorkItemMaxCommentBytes ||
		len(command.Labels) > DriverRunReviewWorkItemMaxLabels {
		return false
	}
	for _, label := range command.Labels {
		if label == "" || !utf8.ValidString(label) || len(label) > DriverRunReviewWorkItemMaxLabelBytes ||
			strings.ContainsAny(label, ",;") || strings.IndexFunc(label, unicode.IsControl) >= 0 {
			return false
		}
	}
	return true
}

func normalizeDriverRunReviewHandoffLabels(labels []string) []string {
	if labels == nil {
		return nil
	}
	normalized := make([]string, 0, len(labels))
	for _, label := range labels {
		label = strings.TrimSpace(label)
		if !slices.Contains(normalized, label) {
			normalized = append(normalized, label)
		}
	}
	return normalized
}

func validDriverRunReviewHandoffWorkItem(
	command HandoffDriverRunReviewWorkItemCommand,
	workItem *DriverRunWorkItem,
) bool {
	if command.TargetStatus != DriverRunWorkItemRestoreReview {
		return true
	}
	if command.Priority == nil || workItem.Priority != *command.Priority {
		return false
	}
	for _, label := range command.Labels {
		if !slices.Contains(workItem.Labels, label) {
			return false
		}
	}
	return true
}

func validDriverRunReviewHandoffComment(
	command HandoffDriverRunReviewWorkItemCommand,
	comment *DriverRunWorkItemComment,
	receiptTime time.Time,
) bool {
	if command.TargetStatus != DriverRunWorkItemRestoreReview {
		return comment == nil
	}
	actor := "driver-run:" + command.Owner.ResourceID
	return comment != nil &&
		strings.TrimSpace(comment.CommentID) != "" &&
		comment.WorkItemID == command.WorkItemID &&
		comment.Author == actor &&
		comment.Body == command.CommentBody &&
		!comment.CreatedAt.IsZero() &&
		comment.CreatedAt.Equal(receiptTime)
}
