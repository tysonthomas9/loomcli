package webui

import (
	"context"
	"encoding/json"

	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// mockIssueService implements service.IssueService for handler-level testing.
type mockIssueService struct {
	getIssueFunc         func(ctx context.Context, issueID string) (json.RawMessage, error)
	listIssuesFunc       func(ctx context.Context, params service.ListIssuesParams) (*service.ListIssuesResult, error)
	createIssueFunc      func(ctx context.Context, params service.CreateIssueParams) (json.RawMessage, error)
	patchIssueFunc       func(ctx context.Context, params service.PatchIssueParams) error
	closeIssueFunc       func(ctx context.Context, params service.CloseIssueParams) (json.RawMessage, error)
	deleteIssueFunc      func(ctx context.Context, issueID string) (json.RawMessage, error)
	addCommentFunc       func(ctx context.Context, params service.AddCommentParams) (*types.Comment, error)
	addDependencyFunc    func(ctx context.Context, params service.AddDependencyParams) error
	removeDependencyFunc func(ctx context.Context, params service.RemoveDependencyParams) error
	listEventsFunc       func(ctx context.Context, params service.EventListParams) ([]*types.Event, error)
	moveIssueFunc        func(ctx context.Context, params service.MoveIssueParams) (*service.MoveIssueResult, error)
}

func (m *mockIssueService) GetIssue(ctx context.Context, issueID string) (json.RawMessage, error) {
	if m.getIssueFunc != nil {
		return m.getIssueFunc(ctx, issueID)
	}
	return nil, nil
}

func (m *mockIssueService) ListIssues(ctx context.Context, params service.ListIssuesParams) (*service.ListIssuesResult, error) {
	if m.listIssuesFunc != nil {
		return m.listIssuesFunc(ctx, params)
	}
	return &service.ListIssuesResult{Issues: []service.IssueWithParent{}}, nil
}

func (m *mockIssueService) CreateIssue(ctx context.Context, params service.CreateIssueParams) (json.RawMessage, error) {
	if m.createIssueFunc != nil {
		return m.createIssueFunc(ctx, params)
	}
	return nil, nil
}

func (m *mockIssueService) PatchIssue(ctx context.Context, params service.PatchIssueParams) error {
	if m.patchIssueFunc != nil {
		return m.patchIssueFunc(ctx, params)
	}
	return nil
}

func (m *mockIssueService) CloseIssue(ctx context.Context, params service.CloseIssueParams) (json.RawMessage, error) {
	if m.closeIssueFunc != nil {
		return m.closeIssueFunc(ctx, params)
	}
	return nil, nil
}

func (m *mockIssueService) DeleteIssue(ctx context.Context, issueID string) (json.RawMessage, error) {
	if m.deleteIssueFunc != nil {
		return m.deleteIssueFunc(ctx, issueID)
	}
	return nil, nil
}

func (m *mockIssueService) AddComment(ctx context.Context, params service.AddCommentParams) (*types.Comment, error) {
	if m.addCommentFunc != nil {
		return m.addCommentFunc(ctx, params)
	}
	return nil, nil
}

func (m *mockIssueService) AddDependency(ctx context.Context, params service.AddDependencyParams) error {
	if m.addDependencyFunc != nil {
		return m.addDependencyFunc(ctx, params)
	}
	return nil
}

func (m *mockIssueService) RemoveDependency(ctx context.Context, params service.RemoveDependencyParams) error {
	if m.removeDependencyFunc != nil {
		return m.removeDependencyFunc(ctx, params)
	}
	return nil
}

func (m *mockIssueService) ListEvents(ctx context.Context, params service.EventListParams) ([]*types.Event, error) {
	if m.listEventsFunc != nil {
		return m.listEventsFunc(ctx, params)
	}
	return nil, nil
}

func (m *mockIssueService) MoveIssue(ctx context.Context, params service.MoveIssueParams) (*service.MoveIssueResult, error) {
	if m.moveIssueFunc != nil {
		return m.moveIssueFunc(ctx, params)
	}
	return nil, nil
}
