package app

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
	reopenIssueFunc      func(ctx context.Context, params service.ReopenIssueParams) error
	archiveIssueFunc     func(ctx context.Context, params service.ArchiveIssueParams) error
	unarchiveIssueFunc   func(ctx context.Context, params service.UnarchiveIssueParams) error
	claimIssueFunc       func(ctx context.Context, params service.ClaimIssueParams) (json.RawMessage, error)
	deleteIssueFunc      func(ctx context.Context, issueID string) (json.RawMessage, error)
	addCommentFunc       func(ctx context.Context, params service.AddCommentParams) (*types.Comment, error)
	listCommentsFunc     func(ctx context.Context, issueID string) ([]*types.Comment, error)
	addDependencyFunc    func(ctx context.Context, params service.AddDependencyParams) error
	removeDependencyFunc func(ctx context.Context, params service.RemoveDependencyParams) error
	listDependenciesFunc func(ctx context.Context, issueID string) (json.RawMessage, error)
	listEventsFunc       func(ctx context.Context, params service.EventListParams) ([]*types.Event, error)
	getJourneyFunc       func(ctx context.Context, issueID string) (*service.Journey, error)
	moveIssueFunc        func(ctx context.Context, params service.MoveIssueParams) (*service.MoveIssueResult, error)
	searchIssuesFunc     func(ctx context.Context, params service.SearchIssuesParams) (json.RawMessage, error)
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

func (m *mockIssueService) ClaimIssue(ctx context.Context, params service.ClaimIssueParams) (json.RawMessage, error) {
	if m.claimIssueFunc != nil {
		return m.claimIssueFunc(ctx, params)
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

func (m *mockIssueService) ListComments(ctx context.Context, issueID string) ([]*types.Comment, error) {
	if m.listCommentsFunc != nil {
		return m.listCommentsFunc(ctx, issueID)
	}
	return nil, nil
}

func (m *mockIssueService) ArchiveIssue(ctx context.Context, params service.ArchiveIssueParams) error {
	if m.archiveIssueFunc != nil {
		return m.archiveIssueFunc(ctx, params)
	}
	return nil
}

func (m *mockIssueService) UnarchiveIssue(ctx context.Context, params service.UnarchiveIssueParams) error {
	if m.unarchiveIssueFunc != nil {
		return m.unarchiveIssueFunc(ctx, params)
	}
	return nil
}

func (m *mockIssueService) ReopenIssue(ctx context.Context, params service.ReopenIssueParams) error {
	if m.reopenIssueFunc != nil {
		return m.reopenIssueFunc(ctx, params)
	}
	return nil
}

func (m *mockIssueService) ListDependencies(ctx context.Context, issueID string) (json.RawMessage, error) {
	if m.listDependenciesFunc != nil {
		return m.listDependenciesFunc(ctx, issueID)
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

func (m *mockIssueService) ListEventHistory(ctx context.Context, params service.EventListParams) (*service.EventListResult, error) {
	events, err := m.ListEvents(ctx, params)
	if err != nil {
		return nil, err
	}
	return &service.EventListResult{Events: events}, nil
}

func (m *mockIssueService) GetJourney(ctx context.Context, issueID string) (*service.Journey, error) {
	if m.getJourneyFunc != nil {
		return m.getJourneyFunc(ctx, issueID)
	}
	return &service.Journey{}, nil
}

func (m *mockIssueService) MoveIssue(ctx context.Context, params service.MoveIssueParams) (*service.MoveIssueResult, error) {
	if m.moveIssueFunc != nil {
		return m.moveIssueFunc(ctx, params)
	}
	return nil, nil
}

func (m *mockIssueService) SearchIssues(ctx context.Context, params service.SearchIssuesParams) (json.RawMessage, error) {
	if m.searchIssuesFunc != nil {
		return m.searchIssuesFunc(ctx, params)
	}
	return nil, nil
}
