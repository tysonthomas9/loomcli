package readprojection_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/webui/readprojection"
)

type issueDiffWorkItemQuery struct {
	issue *workitems.IssueDetail
}

func TestIssueDiffProjectionFailsClosedWhenWorkItemsIsUnavailable(t *testing.T) {
	projection, err := readprojection.NewIssueDiffProjection(
		func(context.Context, string) readprojection.IssueDiffWorkItemQuery { return nil },
		&issueDiffBrowse{},
	)
	if err != nil {
		t.Fatalf("NewIssueDiffProjection() error = %v", err)
	}
	_, err = projection.GetIssueDiff(t.Context(), readprojection.IssueDiffQuery{
		WorkspaceKey: "WS-1", IssueID: "TASK-1",
	})
	if !errors.Is(err, readprojection.ErrIssueDiffUnavailable) {
		t.Fatalf("GetIssueDiff() error = %v, want ErrIssueDiffUnavailable", err)
	}
}

func TestIssueDiffProjectionRejectsWorkItemWithoutAssignee(t *testing.T) {
	projection, err := readprojection.NewIssueDiffProjection(
		func(context.Context, string) readprojection.IssueDiffWorkItemQuery {
			return issueDiffWorkItemQuery{issue: &workitems.IssueDetail{ID: "TASK-1"}}
		},
		&issueDiffBrowse{},
	)
	if err != nil {
		t.Fatalf("NewIssueDiffProjection() error = %v", err)
	}
	_, err = projection.GetIssueDiff(t.Context(), readprojection.IssueDiffQuery{
		WorkspaceKey: "WS-1", IssueID: "TASK-1",
	})
	if !errors.Is(err, readprojection.ErrIssueDiffNotFound) {
		t.Fatalf("GetIssueDiff() error = %v, want ErrIssueDiffNotFound", err)
	}
}

func (query issueDiffWorkItemQuery) Get(
	_ context.Context,
	input workitems.GetQuery,
) (*workitems.IssueDetail, error) {
	if input.IssueID != "TASK-1" {
		return nil, workitems.ErrNotFound
	}
	return query.issue, nil
}

type issueDiffBrowse struct {
	query sourcecontrol.AgentQuery
}

func (browse *issueDiffBrowse) DiffStat(
	_ context.Context,
	query sourcecontrol.AgentQuery,
) (sourcecontrol.AgentDiffStat, error) {
	browse.query = query
	return sourcecontrol.AgentDiffStat{
		Branch: "planner", FilesChanged: 2, LinesAdded: 17, LinesRemoved: 4,
	}, nil
}

func TestIssueDiffProjectionJoinsWorkItemAssigneeToSourceControl(t *testing.T) {
	browse := &issueDiffBrowse{}
	projection, err := readprojection.NewIssueDiffProjection(
		func(_ context.Context, workspaceKey string) readprojection.IssueDiffWorkItemQuery {
			if workspaceKey != "WS-1" {
				t.Fatalf("workspace = %q, want WS-1", workspaceKey)
			}
			return issueDiffWorkItemQuery{issue: &workitems.IssueDetail{
				ID: "TASK-1", Assignee: "planner",
			}}
		},
		browse,
	)
	if err != nil {
		t.Fatalf("NewIssueDiffProjection() error = %v", err)
	}

	got, err := projection.GetIssueDiff(t.Context(), readprojection.IssueDiffQuery{
		WorkspaceKey: "WS-1",
		IssueID:      "TASK-1",
	})
	if err != nil {
		t.Fatalf("GetIssueDiff() error = %v", err)
	}
	if got.Branch != "planner" || got.Added != 17 || got.Removed != 4 {
		t.Fatalf("GetIssueDiff() = %#v", got)
	}
	if browse.query != (sourcecontrol.AgentQuery{WorkspaceKey: "WS-1", AgentID: "planner"}) {
		t.Fatalf("Source Control query = %#v", browse.query)
	}
}
