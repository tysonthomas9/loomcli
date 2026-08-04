package issues

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// makeCovIssue creates a test issue with the given parameters.
func makeCovIssue(id, title string, status types.Status, priority int) *types.IssueWithCounts {
	now := time.Now()
	return &types.IssueWithCounts{
		Issue: &types.Issue{
			ID:        id,
			Title:     title,
			Status:    status,
			Priority:  priority,
			IssueType: types.TypeTask,
			CreatedAt: now,
			UpdatedAt: now,
		},
	}
}

// --- handleListIssues coverage tests ---

func TestHandleListIssues_BasicSuccess_ListMode(t *testing.T) {
	issue1 := makeCovIssue("issue-1", "First Issue", types.StatusOpen, 2)
	issue2 := makeCovIssue("issue-2", "Second Issue", types.StatusInProgress, 1)

	svc := &mockIssueService{
		listIssuesFunc: func(ctx context.Context, params service.ListIssuesParams) (*service.ListIssuesResult, error) {
			return &service.ListIssuesResult{
				Issues: []service.IssueWithParent{
					{IssueWithCounts: issue1},
					{IssueWithCounts: issue2},
				},
			}, nil
		},
	}

	handler := handleListIssues(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/issues?status=open", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp IssuesResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true, got false; error=%s", resp.Error)
	}

	var resultIssues []json.RawMessage
	if err := json.Unmarshal(resp.Data, &resultIssues); err != nil {
		t.Fatalf("failed to unmarshal data: %v", err)
	}
	if len(resultIssues) != 2 {
		t.Errorf("expected 2 issues, got %d", len(resultIssues))
	}
}

func TestHandleListIssues_EmptyResultListMode(t *testing.T) {
	svc := &mockIssueService{
		listIssuesFunc: func(ctx context.Context, params service.ListIssuesParams) (*service.ListIssuesResult, error) {
			return &service.ListIssuesResult{
				Issues: []service.IssueWithParent{},
			}, nil
		},
	}

	handler := handleListIssues(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp IssuesResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true")
	}

	var resultIssues []json.RawMessage
	if err := json.Unmarshal(resp.Data, &resultIssues); err != nil {
		t.Fatalf("failed to unmarshal data: %v", err)
	}
	if len(resultIssues) != 0 {
		t.Errorf("expected 0 issues, got %d", len(resultIssues))
	}
}

func TestHandleListIssues_WithExcludeStatus(t *testing.T) {
	issue1 := makeCovIssue("issue-1", "Open", types.StatusOpen, 2)
	issue3 := makeCovIssue("issue-3", "In Progress", types.StatusInProgress, 0)

	svc := &mockIssueService{
		listIssuesFunc: func(ctx context.Context, params service.ListIssuesParams) (*service.ListIssuesResult, error) {
			// Service layer handles exclude filtering
			return &service.ListIssuesResult{
				Issues: []service.IssueWithParent{
					{IssueWithCounts: issue1},
					{IssueWithCounts: issue3},
				},
			}, nil
		},
	}

	handler := handleListIssues(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/issues?exclude_status=closed", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp IssuesResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true, error=%s", resp.Error)
	}

	var resultIssues []map[string]interface{}
	if err := json.Unmarshal(resp.Data, &resultIssues); err != nil {
		t.Fatalf("failed to unmarshal data: %v", err)
	}
	if len(resultIssues) != 2 {
		t.Errorf("expected 2 issues (closed excluded), got %d", len(resultIssues))
	}
	for _, issue := range resultIssues {
		if issue["status"] == "closed" {
			t.Error("closed issue should have been excluded")
		}
	}
}

func TestHandleListIssues_WithParentInfo(t *testing.T) {
	child := makeCovIssue("child-1", "Child Issue", types.StatusOpen, 2)
	parentID := "parent-1"
	parentTitle := "Parent Epic"

	svc := &mockIssueService{
		listIssuesFunc: func(ctx context.Context, params service.ListIssuesParams) (*service.ListIssuesResult, error) {
			return &service.ListIssuesResult{
				Issues: []service.IssueWithParent{
					{
						IssueWithCounts: child,
						Parent:          &parentID,
						ParentTitle:     &parentTitle,
					},
				},
			}, nil
		},
	}

	handler := handleListIssues(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp IssuesResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	var resultIssues []map[string]interface{}
	if err := json.Unmarshal(resp.Data, &resultIssues); err != nil {
		t.Fatalf("failed to unmarshal data: %v", err)
	}
	if len(resultIssues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(resultIssues))
	}

	if resultIssues[0]["parent"] != "parent-1" {
		t.Errorf("expected parent %q, got %v", "parent-1", resultIssues[0]["parent"])
	}
	if resultIssues[0]["parent_title"] != "Parent Epic" {
		t.Errorf("expected parent_title %q, got %v", "Parent Epic", resultIssues[0]["parent_title"])
	}
}

func TestHandleListIssues_KanbanMode_IncludeBlocked(t *testing.T) {
	task1 := makeCovIssue("task-1", "Blocked Task", types.StatusOpen, 2)
	task2 := makeCovIssue("task-2", "Free Task", types.StatusOpen, 3)

	svc := &mockIssueService{
		listIssuesFunc: func(ctx context.Context, params service.ListIssuesParams) (*service.ListIssuesResult, error) {
			return &service.ListIssuesResult{
				KanbanIssues: []service.KanbanIssue{
					{
						IssueWithCounts: task1,
						IsBlocked:       true,
						BlockedByCount:  1,
					},
					{
						IssueWithCounts: task2,
						IsBlocked:       false,
						BlockedByCount:  0,
					},
				},
			}, nil
		},
	}

	handler := handleListIssues(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/issues?include_blocked=true", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp IssuesResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success=true, got false; error=%s", resp.Error)
	}

	var kanbanIssues []map[string]interface{}
	if err := json.Unmarshal(resp.Data, &kanbanIssues); err != nil {
		t.Fatalf("failed to unmarshal kanban data: %v", err)
	}
	if len(kanbanIssues) != 2 {
		t.Fatalf("expected 2 kanban issues, got %d", len(kanbanIssues))
	}

	for _, ki := range kanbanIssues {
		if ki["id"] == "task-1" {
			if ki["is_blocked"] != true {
				t.Errorf("task-1 should be blocked")
			}
			if bc, ok := ki["blocked_by_count"].(float64); !ok || bc != 1 {
				t.Errorf("task-1 blocked_by_count = %v, want 1", ki["blocked_by_count"])
			}
		}
		if ki["id"] == "task-2" {
			if ki["is_blocked"] != false {
				t.Errorf("task-2 should not be blocked")
			}
		}
	}
}

func TestHandleListIssues_KanbanMode_EmptyResult(t *testing.T) {
	svc := &mockIssueService{
		listIssuesFunc: func(ctx context.Context, params service.ListIssuesParams) (*service.ListIssuesResult, error) {
			return &service.ListIssuesResult{
				KanbanIssues: []service.KanbanIssue{},
			}, nil
		},
	}

	handler := handleListIssues(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/issues?include_blocked=true", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp IssuesResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true for empty kanban result")
	}

	var resultIssues []json.RawMessage
	if err := json.Unmarshal(resp.Data, &resultIssues); err != nil {
		t.Fatalf("failed to unmarshal data: %v", err)
	}
	if len(resultIssues) != 0 {
		t.Errorf("expected 0 kanban issues, got %d", len(resultIssues))
	}
}

func TestHandleListIssues_ServiceError(t *testing.T) {
	svc := &mockIssueService{
		listIssuesFunc: func(ctx context.Context, params service.ListIssuesParams) (*service.ListIssuesResult, error) {
			return nil, service.ErrInternal("internal daemon error", nil)
		},
	}

	handler := handleListIssues(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleListIssues_WithRepoField(t *testing.T) {
	issue := makeCovIssue("repo-issue", "Multi-repo Issue", types.StatusOpen, 2)
	issue.Issue.SourceRepo = "backend-repo"
	repo := "backend-repo"

	svc := &mockIssueService{
		listIssuesFunc: func(ctx context.Context, params service.ListIssuesParams) (*service.ListIssuesResult, error) {
			return &service.ListIssuesResult{
				Issues: []service.IssueWithParent{
					{IssueWithCounts: issue, Repo: &repo},
				},
			}, nil
		},
	}

	handler := handleListIssues(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp IssuesResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	var resultIssues []map[string]interface{}
	if err := json.Unmarshal(resp.Data, &resultIssues); err != nil {
		t.Fatalf("failed to unmarshal data: %v", err)
	}
	if len(resultIssues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(resultIssues))
	}
	if resultIssues[0]["repo"] != "backend-repo" {
		t.Errorf("expected repo %q, got %v", "backend-repo", resultIssues[0]["repo"])
	}
}

func TestHandleListIssues_MultipleFilters(t *testing.T) {
	issue := makeCovIssue("filtered-1", "Filtered Result", types.StatusOpen, 1)

	svc := &mockIssueService{
		listIssuesFunc: func(ctx context.Context, params service.ListIssuesParams) (*service.ListIssuesResult, error) {
			return &service.ListIssuesResult{
				Issues: []service.IssueWithParent{
					{IssueWithCounts: issue},
				},
			}, nil
		},
	}

	handler := handleListIssues(svc)

	req := httptest.NewRequest(http.MethodGet,
		"/api/issues?status=open&type=bug&assignee=alice&priority=1&limit=50&labels=urgent,backend", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp IssuesResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true for multi-filter query, error=%s", resp.Error)
	}
}

func TestHandleListIssues_ExcludeAllStatuses(t *testing.T) {
	svc := &mockIssueService{
		listIssuesFunc: func(ctx context.Context, params service.ListIssuesParams) (*service.ListIssuesResult, error) {
			// After excluding all statuses, no issues remain
			return &service.ListIssuesResult{
				Issues: []service.IssueWithParent{},
			}, nil
		},
	}

	handler := handleListIssues(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/issues?exclude_status=open,in_progress", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp IssuesResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true")
	}

	var resultIssues []json.RawMessage
	if err := json.Unmarshal(resp.Data, &resultIssues); err != nil {
		t.Fatalf("failed to unmarshal data: %v", err)
	}
	if len(resultIssues) != 0 {
		t.Errorf("expected 0 issues (all excluded), got %d", len(resultIssues))
	}
}

func TestHandleListIssues_ServiceUnavailable(t *testing.T) {
	svc := &mockIssueService{
		listIssuesFunc: func(ctx context.Context, params service.ListIssuesParams) (*service.ListIssuesResult, error) {
			return nil, service.ErrUnavailable("daemon not available")
		},
	}

	handler := handleListIssues(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
}
