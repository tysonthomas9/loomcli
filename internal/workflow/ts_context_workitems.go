package workflow

import (
	"context"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func appendWorkItemReadEvent(ctx context.Context, st store.Store, run *domain.WorkflowRun, params map[string]any) {
	data := copyAnyMap(params)
	data["workflow_run_id"] = run.RunID
	if id := firstString(params, "workItemId", "work_item_id", "id"); id != "" {
		data["work_item_id"] = id
	}
	_, _ = st.RunEvents().Append(ctx, store.RunEventAppend{
		WorkspaceKey:  run.WorkspaceKey,
		WorkflowRunID: run.RunID,
		Type:          "work_item_read",
		Message:       "workflow work item projection read",
		Data:          mustJSON(data),
	})
}

func applyWorkItemCommentOperation(ctx context.Context, st store.Store, ib backend.IssueBackend, run *domain.WorkflowRun, params map[string]any) error {
	if ib == nil {
		return fmt.Errorf("workItems.comment requires issue backend")
	}
	workItemID := firstString(params, "workItemId", "work_item_id", "id")
	if workItemID == "" {
		return fmt.Errorf("workItems.comment requires workItemId")
	}
	text := firstString(params, "body", "text", "comment")
	if text == "" {
		return fmt.Errorf("workItems.comment requires body")
	}
	author := firstString(params, "author", "actor")
	if author == "" {
		author = run.LeaseOwner
	}
	if author == "" {
		author = "workflow"
	}
	comment, err := ib.AddComment(ctx, backend.CommentAddParams{
		IssueID: workItemID,
		Author:  author,
		Text:    text,
	})
	if err != nil {
		return fmt.Errorf("comment on work item %s: %w", workItemID, err)
	}
	data := copyAnyMap(params)
	data["work_item_id"] = workItemID
	data["workflow_run_id"] = run.RunID
	data["author"] = author
	if comment != nil {
		data["comment_id"] = comment.ID
	}
	_, _ = st.RunEvents().Append(ctx, store.RunEventAppend{
		WorkspaceKey:  run.WorkspaceKey,
		WorkflowRunID: run.RunID,
		Type:          "work_item_comment_added",
		Message:       "workflow added work item comment",
		Data:          mustJSON(data),
	})
	return nil
}
