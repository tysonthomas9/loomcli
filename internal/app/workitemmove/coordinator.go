// Package workitemmove coordinates the recoverable cross-workspace move
// workflow without taking ownership of either Workspace or Work Item records.
package workitemmove

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/modules/workspace"
)

const moveTimeout = 30 * time.Second

type WorkItems interface {
	Get(context.Context, workitems.GetQuery) (*workitems.IssueDetail, error)
	Create(context.Context, workitems.CreateCommand) (*workitems.CreatedIssue, error)
	AddComment(context.Context, workitems.AddCommentCommand) (*workitems.Comment, error)
	Close(context.Context, workitems.CloseCommand) (*workitems.CloseResult, error)
}

type WorkspaceCatalog interface {
	Resolve(context.Context, workspace.ResolveQuery) (*workspace.Reference, error)
}

type WorkspaceScope func(context.Context, string) context.Context

type Command struct {
	IssueID         string
	SourceWorkspace string
	TargetWorkspace string
}

type Result struct {
	SourceID string
	TargetID string
	Warnings []string
}

type Commands interface {
	Move(context.Context, Command) (*Result, error)
}

type Coordinator struct {
	workItems  WorkItems
	workspaces WorkspaceCatalog
	scope      WorkspaceScope
}

var _ Commands = (*Coordinator)(nil)

func New(workItems WorkItems, workspaces WorkspaceCatalog, scope WorkspaceScope) (*Coordinator, error) {
	if workItems == nil || workspaces == nil || scope == nil {
		return nil, fmt.Errorf("compose work item move: work items, workspace catalog, and scope are required: %w", workitems.ErrUnavailable)
	}
	return &Coordinator{workItems: workItems, workspaces: workspaces, scope: scope}, nil
}

func (c *Coordinator) Move(ctx context.Context, command Command) (*Result, error) {
	issueID := strings.TrimSpace(command.IssueID)
	sourceRef := strings.TrimSpace(command.SourceWorkspace)
	targetRef := strings.TrimSpace(command.TargetWorkspace)
	if issueID == "" || sourceRef == "" || targetRef == "" {
		return nil, fmt.Errorf("issue id, source workspace, and target workspace are required: %w", workitems.ErrInvalid)
	}
	ctx, cancel := context.WithTimeout(ctx, moveTimeout)
	defer cancel()
	sourceWorkspace, err := c.resolveWorkspace(ctx, sourceRef)
	if err != nil {
		return nil, err
	}
	targetWorkspace, err := c.resolveWorkspace(ctx, targetRef)
	if err != nil {
		return nil, err
	}
	if targetWorkspace.Key == sourceWorkspace.Key || targetWorkspace.Name == sourceWorkspace.Name {
		return nil, fmt.Errorf("cannot move issue to the same workspace: %w", workitems.ErrInvalid)
	}
	sourceCtx := c.scope(ctx, sourceWorkspace.Key)
	source, err := c.workItems.Get(sourceCtx, workitems.GetQuery{IssueID: issueID})
	if err != nil {
		return nil, err
	}
	if source.Status == "closed" {
		return nil, fmt.Errorf("cannot move a closed issue: %w", workitems.ErrInvalid)
	}
	warnings := make([]string, 0, 2)
	if source.Assignee != "" {
		warnings = append(warnings, fmt.Sprintf("Active agent %q assigned to this issue. Moving will not stop the agent.", source.Assignee))
	}
	targetCtx := c.scope(ctx, targetWorkspace.Key)
	created, err := c.workItems.Create(targetCtx, moveCreateCommand(source))
	if err != nil {
		return nil, err
	}
	targetID := createdIssueID(created)
	if targetID == "" {
		return nil, fmt.Errorf("move created an issue without an id: %w", workitems.ErrInvalidPersistedState)
	}
	if _, err := c.workItems.AddComment(sourceCtx, workitems.AddCommentCommand{
		IssueID: issueID, Author: "web-ui",
		Text: fmt.Sprintf("Moved to %s in workspace %q", targetID, command.TargetWorkspace),
	}); err != nil {
		warnings = append(warnings, "Failed to add comment on source issue")
	}
	if _, err := c.workItems.Close(sourceCtx, workitems.CloseCommand{
		IssueID: issueID, Reason: fmt.Sprintf("Moved to %s", targetID), Force: true,
	}); err != nil {
		warnings = append(warnings, "Source issue could not be closed")
	}
	return &Result{SourceID: issueID, TargetID: targetID, Warnings: warnings}, nil
}

func (c *Coordinator) resolveWorkspace(ctx context.Context, reference string) (*workspace.Reference, error) {
	value, err := c.workspaces.Resolve(ctx, workspace.ResolveQuery{Reference: reference})
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) {
			return nil, fmt.Errorf("workspace %q not found: %w", reference, workitems.ErrInvalid)
		}
		if errors.Is(err, workspace.ErrInvalid) {
			return nil, fmt.Errorf("invalid workspace %q: %w", reference, workitems.ErrInvalid)
		}
		if errors.Is(err, workspace.ErrUnavailable) {
			return nil, fmt.Errorf("workspace catalog unavailable: %w", workitems.ErrUnavailable)
		}
		return nil, err
	}
	return value, nil
}

func moveCreateCommand(source *workitems.IssueDetail) workitems.CreateCommand {
	description := source.Description
	if description != "" {
		description += "\n\n"
	}
	description += fmt.Sprintf("(Moved from %s)", source.ID)
	return workitems.CreateCommand{
		Title: source.Title, Description: description, IssueType: source.IssueType,
		Priority: source.Priority, Design: source.Design,
		AcceptanceCriteria: source.AcceptanceCriteria, Notes: source.Notes,
		Assignee: source.Assignee, Owner: source.Owner, CreatedBy: "web-ui",
		ExternalRef: source.ExternalRef, EstimatedMinutes: source.EstimatedMinutes,
		Labels: append([]string(nil), source.Labels...),
		DueAt:  formatTime(source.DueAt), DeferUntil: formatTime(source.DeferUntil),
	}
}

func createdIssueID(value *workitems.CreatedIssue) string {
	if value == nil {
		return ""
	}
	if value.Detail != nil {
		return value.Detail.ID
	}
	if value.Summary != nil {
		return value.Summary.ID
	}
	return ""
}

func formatTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.Format(time.RFC3339)
}
