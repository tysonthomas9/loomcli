package workitemmove

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/modules/workspace"
)

type fakeWorkItems struct {
	source  *workitems.IssueDetail
	created *workitems.IssueSummary
	create  workitems.CreateCommand
	comment workitems.AddCommentCommand
	close   workitems.CloseCommand
}

func (f *fakeWorkItems) Get(context.Context, workitems.GetQuery) (*workitems.IssueDetail, error) {
	return f.source, nil
}
func (f *fakeWorkItems) Create(_ context.Context, command workitems.CreateCommand) (*workitems.IssueSummary, error) {
	f.create = command
	return f.created, nil
}
func (f *fakeWorkItems) AddComment(_ context.Context, command workitems.AddCommentCommand) (*workitems.Comment, error) {
	f.comment = command
	return &workitems.Comment{IssueID: command.IssueID, Text: command.Text}, nil
}
func (f *fakeWorkItems) Close(_ context.Context, command workitems.CloseCommand) (*workitems.CloseResult, error) {
	f.close = command
	return &workitems.CloseResult{Closed: &workitems.IssueSummary{ID: command.IssueID}}, nil
}

type fakeWorkspaces struct{}

func (fakeWorkspaces) Resolve(_ context.Context, query workspace.ResolveQuery) (*workspace.Reference, error) {
	if query.Reference == "Source" {
		return &workspace.Reference{Key: "SOURCE", Name: "Source"}, nil
	}
	return &workspace.Reference{Key: "TARGET", Name: "Target"}, nil
}

func TestMoveCoordinatesOwnerAPIs(t *testing.T) {
	items := &fakeWorkItems{
		source:  &workitems.IssueDetail{ID: "TASK-1", Title: "Proof", IssueType: "task", Priority: 2, Description: "body", Labels: []string{"proof"}},
		created: &workitems.IssueSummary{ID: "TARGET-1"},
	}
	var scopes []string
	coordinator, err := New(items, fakeWorkspaces{}, func(ctx context.Context, key string) context.Context {
		scopes = append(scopes, key)
		return ctx
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := coordinator.Move(context.Background(), Command{IssueID: "TASK-1", SourceWorkspace: "Source", TargetWorkspace: "Target"})
	if err != nil {
		t.Fatal(err)
	}
	if result.TargetID != "TARGET-1" || items.create.Description != "body\n\n(Moved from TASK-1)" || items.comment.IssueID != "TASK-1" || !items.close.Force {
		t.Fatalf("unexpected move result=%#v create=%#v comment=%#v close=%#v", result, items.create, items.comment, items.close)
	}
	if len(scopes) != 2 || scopes[0] != "SOURCE" || scopes[1] != "TARGET" {
		t.Fatalf("unexpected workspace scopes: %#v", scopes)
	}
}
