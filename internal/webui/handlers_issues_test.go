package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
)

// --- Mock types for issue handler tests ---

// issuesMockClient implements issueGetter for handleGetIssue tests.
type issuesMockClient struct {
	showFunc func(args *rpc.ShowArgs) (*rpc.Response, error)
}

func (m *issuesMockClient) Show(args *rpc.ShowArgs) (*rpc.Response, error) {
	if m.showFunc != nil {
		return m.showFunc(args)
	}
	return nil, errors.New("showFunc not implemented")
}

// issuesMockPool implements connectionGetter for handleGetIssueWithPool tests.
type issuesMockPool struct {
	getFunc func(ctx context.Context) (issueGetter, error)
	putFunc func(client issueGetter)
}

func (m *issuesMockPool) Get(ctx context.Context) (issueGetter, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx)
	}
	return nil, errors.New("getFunc not implemented")
}

func (m *issuesMockPool) Put(client issueGetter) {
	if m.putFunc != nil {
		m.putFunc(client)
	}
}

func (m *issuesMockPool) Discard(client issueGetter) {}

// --- handleGetIssue tests ---

func TestHandleGetIssue_Success_ResponseShape(t *testing.T) {
	issueData := map[string]interface{}{
		"id":         "proj-abc12",
		"title":      "Fix login page",
		"status":     "open",
		"priority":   2,
		"issue_type": "bug",
	}
	issueBytes, err := json.Marshal(issueData)
	if err != nil {
		t.Fatalf("marshal issue data: %v", err)
	}

	client := &issuesMockClient{
		showFunc: func(args *rpc.ShowArgs) (*rpc.Response, error) {
			if args.ID != "proj-abc12" {
				t.Errorf("expected ID %q, got %q", "proj-abc12", args.ID)
			}
			return &rpc.Response{
				Success: true,
				Data:    json.RawMessage(issueBytes),
			}, nil
		},
	}

	pool := &issuesMockPool{
		getFunc: func(ctx context.Context) (issueGetter, error) {
			return client, nil
		},
	}

	handler := handleGetIssueWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/proj-abc12", nil)
	req.SetPathValue("id", "proj-abc12")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := assertJSONResponse(t, rec)
	assertEnvelopeSuccess(t, body)

	// Verify data field contains the issue
	dataRaw, ok := body["data"]
	if !ok {
		t.Fatal("missing 'data' field in response")
	}
	dataMap, ok := dataRaw.(map[string]interface{})
	if !ok {
		t.Fatalf("expected data to be object, got %T", dataRaw)
	}
	if dataMap["id"] != "proj-abc12" {
		t.Errorf("expected id %q, got %v", "proj-abc12", dataMap["id"])
	}
	if dataMap["title"] != "Fix login page" {
		t.Errorf("expected title %q, got %v", "Fix login page", dataMap["title"])
	}
}

func TestHandleGetIssue_NotFound_404(t *testing.T) {
	client := &issuesMockClient{
		showFunc: func(args *rpc.ShowArgs) (*rpc.Response, error) {
			return nil, fmt.Errorf("issue not found: %s", args.ID)
		},
	}

	pool := &issuesMockPool{
		getFunc: func(ctx context.Context) (issueGetter, error) {
			return client, nil
		},
	}

	handler := handleGetIssueWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/nonexistent-99", nil)
	req.SetPathValue("id", "nonexistent-99")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	body := assertJSONResponse(t, rec)
	assertPlainError(t, body)
}

func TestHandleGetIssue_MissingID_400(t *testing.T) {
	pool := &issuesMockPool{
		getFunc: func(ctx context.Context) (issueGetter, error) {
			t.Fatal("pool.Get should not be called for empty ID")
			return nil, nil
		},
	}

	handler := handleGetIssueWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/", nil)
	// Do NOT set path value to simulate missing ID
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	body := assertJSONResponse(t, rec)
	assertPlainError(t, body)
}

func TestHandleGetIssue_PoolUnavailable_503(t *testing.T) {
	pool := &issuesMockPool{
		getFunc: func(ctx context.Context) (issueGetter, error) {
			return nil, errors.New("connection refused")
		},
	}

	handler := handleGetIssueWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/proj-1", nil)
	req.SetPathValue("id", "proj-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}

	body := assertJSONResponse(t, rec)
	assertPlainError(t, body)
}

func TestHandleGetIssue_InternalError_500(t *testing.T) {
	client := &issuesMockClient{
		showFunc: func(args *rpc.ShowArgs) (*rpc.Response, error) {
			return nil, errors.New("disk I/O error")
		},
	}

	pool := &issuesMockPool{
		getFunc: func(ctx context.Context) (issueGetter, error) {
			return client, nil
		},
	}

	handler := handleGetIssueWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/proj-1", nil)
	req.SetPathValue("id", "proj-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}

	body := assertJSONResponse(t, rec)
	assertPlainError(t, body)
}

func TestHandleGetIssue_ClientReturnedToPoolOnSuccess(t *testing.T) {
	putCalled := false

	client := &issuesMockClient{
		showFunc: func(args *rpc.ShowArgs) (*rpc.Response, error) {
			return &rpc.Response{
				Success: true,
				Data:    json.RawMessage(`{"id":"proj-1","title":"Test"}`),
			}, nil
		},
	}

	pool := &issuesMockPool{
		getFunc: func(ctx context.Context) (issueGetter, error) {
			return client, nil
		},
		putFunc: func(c issueGetter) {
			putCalled = true
			if c != client {
				t.Error("pool.Put called with wrong client")
			}
		},
	}

	handler := handleGetIssueWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/proj-1", nil)
	req.SetPathValue("id", "proj-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !putCalled {
		t.Error("expected pool.Put to be called (connection returned to pool)")
	}
}

func TestHandleGetIssue_TableDriven(t *testing.T) {
	tests := []struct {
		name       string
		issueID    string
		showResp   *rpc.Response
		showErr    error
		poolErr    error
		wantStatus int
	}{
		{
			name:    "success with full issue details",
			issueID: "proj-full",
			showResp: &rpc.Response{
				Success: true,
				Data:    json.RawMessage(`{"id":"proj-full","title":"Full Details","status":"in_progress","priority":1,"issue_type":"feature","labels":[],"dependencies":[],"dependents":[],"comments":[]}`),
			},
			wantStatus: http.StatusOK,
		},
		{
			name:       "not found error",
			issueID:    "proj-missing",
			showErr:    fmt.Errorf("issue not found: proj-missing"),
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "generic RPC error",
			issueID:    "proj-err",
			showErr:    errors.New("timeout"),
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "pool unavailable",
			issueID:    "proj-down",
			poolErr:    errors.New("pool closed"),
			wantStatus: http.StatusServiceUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &issuesMockClient{
				showFunc: func(args *rpc.ShowArgs) (*rpc.Response, error) {
					return tt.showResp, tt.showErr
				},
			}

			pool := &issuesMockPool{
				getFunc: func(ctx context.Context) (issueGetter, error) {
					if tt.poolErr != nil {
						return nil, tt.poolErr
					}
					return client, nil
				},
			}

			handler := handleGetIssueWithPool(pool)

			req := httptest.NewRequest(http.MethodGet, "/api/issues/"+tt.issueID, nil)
			req.SetPathValue("id", tt.issueID)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", tt.wantStatus, rec.Code, rec.Body.String())
			}

			// All responses must be valid JSON
			_ = assertJSONResponse(t, rec)
		})
	}
}

// --- handleListIssues tests ---

func TestHandleListIssues_NilPool_503(t *testing.T) {
	handler := handleListIssues(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}

	body := assertJSONResponse(t, rec)
	if body["success"] != false {
		t.Error("expected success=false for nil pool")
	}
	if code, ok := body["code"].(string); !ok || code != "POOL_NOT_INITIALIZED" {
		t.Errorf("expected code POOL_NOT_INITIALIZED, got %v", body["code"])
	}
}

// --- parseListParams tests ---

func TestHandleListIssues_ParseListParams_Filters(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		validate func(t *testing.T, args *rpc.ListArgs)
	}{
		{
			name: "no params returns empty args",
			url:  "/api/issues",
			validate: func(t *testing.T, args *rpc.ListArgs) {
				if args.Status != "" {
					t.Errorf("expected empty status, got %q", args.Status)
				}
				if args.IssueType != "" {
					t.Errorf("expected empty type, got %q", args.IssueType)
				}
				if args.Priority != nil {
					t.Errorf("expected nil priority, got %v", args.Priority)
				}
			},
		},
		{
			name: "status filter",
			url:  "/api/issues?status=open",
			validate: func(t *testing.T, args *rpc.ListArgs) {
				if args.Status != "open" {
					t.Errorf("expected status %q, got %q", "open", args.Status)
				}
			},
		},
		{
			name: "type filter",
			url:  "/api/issues?type=bug",
			validate: func(t *testing.T, args *rpc.ListArgs) {
				if args.IssueType != "bug" {
					t.Errorf("expected type %q, got %q", "bug", args.IssueType)
				}
			},
		},
		{
			name: "priority filter",
			url:  "/api/issues?priority=1",
			validate: func(t *testing.T, args *rpc.ListArgs) {
				if args.Priority == nil || *args.Priority != 1 {
					t.Errorf("expected priority 1, got %v", args.Priority)
				}
			},
		},
		{
			name: "assignee filter",
			url:  "/api/issues?assignee=alice",
			validate: func(t *testing.T, args *rpc.ListArgs) {
				if args.Assignee != "alice" {
					t.Errorf("expected assignee %q, got %q", "alice", args.Assignee)
				}
			},
		},
		{
			name: "query filter",
			url:  "/api/issues?q=login",
			validate: func(t *testing.T, args *rpc.ListArgs) {
				if args.Query != "login" {
					t.Errorf("expected query %q, got %q", "login", args.Query)
				}
			},
		},
		{
			name: "limit filter",
			url:  "/api/issues?limit=50",
			validate: func(t *testing.T, args *rpc.ListArgs) {
				if args.Limit != 50 {
					t.Errorf("expected limit 50, got %d", args.Limit)
				}
			},
		},
		{
			name: "limit capped at MaxListLimit",
			url:  "/api/issues?limit=99999",
			validate: func(t *testing.T, args *rpc.ListArgs) {
				if args.Limit != MaxListLimit {
					t.Errorf("expected limit %d (MaxListLimit), got %d", MaxListLimit, args.Limit)
				}
			},
		},
		{
			name: "labels comma-separated",
			url:  "/api/issues?labels=urgent,backend",
			validate: func(t *testing.T, args *rpc.ListArgs) {
				if len(args.Labels) != 2 || args.Labels[0] != "urgent" || args.Labels[1] != "backend" {
					t.Errorf("expected labels [urgent, backend], got %v", args.Labels)
				}
			},
		},
		{
			name: "title_contains filter",
			url:  "/api/issues?title_contains=login",
			validate: func(t *testing.T, args *rpc.ListArgs) {
				if args.TitleContains != "login" {
					t.Errorf("expected title_contains %q, got %q", "login", args.TitleContains)
				}
			},
		},
		{
			name: "pinned=true filter",
			url:  "/api/issues?pinned=true",
			validate: func(t *testing.T, args *rpc.ListArgs) {
				if args.Pinned == nil || *args.Pinned != true {
					t.Errorf("expected pinned=true, got %v", args.Pinned)
				}
			},
		},
		{
			name: "pinned=false filter",
			url:  "/api/issues?pinned=false",
			validate: func(t *testing.T, args *rpc.ListArgs) {
				if args.Pinned == nil || *args.Pinned != false {
					t.Errorf("expected pinned=false, got %v", args.Pinned)
				}
			},
		},
		{
			name: "multiple filters combined",
			url:  "/api/issues?status=open&type=feature&priority=2&assignee=bob&limit=25",
			validate: func(t *testing.T, args *rpc.ListArgs) {
				if args.Status != "open" {
					t.Errorf("expected status %q, got %q", "open", args.Status)
				}
				if args.IssueType != "feature" {
					t.Errorf("expected type %q, got %q", "feature", args.IssueType)
				}
				if args.Priority == nil || *args.Priority != 2 {
					t.Errorf("expected priority 2, got %v", args.Priority)
				}
				if args.Assignee != "bob" {
					t.Errorf("expected assignee %q, got %q", "bob", args.Assignee)
				}
				if args.Limit != 25 {
					t.Errorf("expected limit 25, got %d", args.Limit)
				}
			},
		},
		{
			name: "empty_description flag",
			url:  "/api/issues?empty_description=true",
			validate: func(t *testing.T, args *rpc.ListArgs) {
				if !args.EmptyDescription {
					t.Error("expected empty_description=true")
				}
			},
		},
		{
			name: "no_assignee flag",
			url:  "/api/issues?no_assignee=true",
			validate: func(t *testing.T, args *rpc.ListArgs) {
				if !args.NoAssignee {
					t.Error("expected no_assignee=true")
				}
			},
		},
		{
			name: "source_repos filter",
			url:  "/api/issues?source_repos=repo-a,repo-b",
			validate: func(t *testing.T, args *rpc.ListArgs) {
				if len(args.SourceRepos) != 2 || args.SourceRepos[0] != "repo-a" || args.SourceRepos[1] != "repo-b" {
					t.Errorf("expected source_repos [repo-a, repo-b], got %v", args.SourceRepos)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			args, err := parseListParams(req)
			if err != nil {
				t.Fatalf("parseListParams error: %v", err)
			}
			tt.validate(t, args)
		})
	}
}

// --- parseKanbanParams tests ---

func TestHandleListIssues_ParseKanbanParams(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		wantBlocked bool
		wantExclude []string
		wantErr     bool
	}{
		{
			name:        "no params",
			url:         "/api/issues",
			wantBlocked: false,
			wantExclude: nil,
		},
		{
			name:        "include_blocked=true",
			url:         "/api/issues?include_blocked=true",
			wantBlocked: true,
			wantExclude: nil,
		},
		{
			name:        "include_blocked=false (any non-true)",
			url:         "/api/issues?include_blocked=false",
			wantBlocked: false,
			wantExclude: nil,
		},
		{
			name:        "exclude_status single",
			url:         "/api/issues?exclude_status=closed",
			wantBlocked: false,
			wantExclude: []string{"closed"},
		},
		{
			name:        "exclude_status multiple",
			url:         "/api/issues?exclude_status=closed,tombstone",
			wantBlocked: false,
			wantExclude: []string{"closed", "tombstone"},
		},
		{
			name:        "both include_blocked and exclude_status",
			url:         "/api/issues?include_blocked=true&exclude_status=closed",
			wantBlocked: true,
			wantExclude: []string{"closed"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			params, err := parseKanbanParams(req)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if params.IncludeBlocked != tt.wantBlocked {
				t.Errorf("IncludeBlocked = %v, want %v", params.IncludeBlocked, tt.wantBlocked)
			}
			if len(params.ExcludeStatus) != len(tt.wantExclude) {
				t.Errorf("ExcludeStatus = %v, want %v", params.ExcludeStatus, tt.wantExclude)
			} else {
				for i, s := range params.ExcludeStatus {
					if s != tt.wantExclude[i] {
						t.Errorf("ExcludeStatus[%d] = %q, want %q", i, s, tt.wantExclude[i])
					}
				}
			}
		})
	}
}

// --- KanbanIssue enrichment tests ---

func TestHandleListIssues_GetUnclosedBlockerRefs(t *testing.T) {
	now := time.Now()

	unclosedIDs := map[string]bool{
		"blocker-1": true,
		"blocker-2": true,
		// "resolved-1" is not in unclosedIDs (closed)
	}

	issueMap := map[string]*types.IssueWithCounts{
		"blocker-1": {
			Issue: &types.Issue{
				ID:        "blocker-1",
				Title:     "Database migration",
				Priority:  1,
				Status:    types.StatusOpen,
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
		"blocker-2": {
			Issue: &types.Issue{
				ID:        "blocker-2",
				Title:     "Auth service",
				Priority:  0,
				Status:    types.StatusInProgress,
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
	}

	tests := []struct {
		name    string
		deps    []*types.Dependency
		wantLen int
		wantIDs []string
	}{
		{
			name:    "no dependencies",
			deps:    nil,
			wantLen: 0,
		},
		{
			name: "one unclosed blocker",
			deps: []*types.Dependency{
				{IssueID: "child-1", DependsOnID: "blocker-1", Type: types.DepBlocks},
			},
			wantLen: 1,
			wantIDs: []string{"blocker-1"},
		},
		{
			name: "resolved blocker excluded",
			deps: []*types.Dependency{
				{IssueID: "child-1", DependsOnID: "resolved-1", Type: types.DepBlocks},
			},
			wantLen: 0,
		},
		{
			name: "multiple unclosed blockers",
			deps: []*types.Dependency{
				{IssueID: "child-1", DependsOnID: "blocker-1", Type: types.DepBlocks},
				{IssueID: "child-1", DependsOnID: "blocker-2", Type: types.DepBlocks},
			},
			wantLen: 2,
			wantIDs: []string{"blocker-1", "blocker-2"},
		},
		{
			name: "non-blocking dependency type excluded",
			deps: []*types.Dependency{
				{IssueID: "child-1", DependsOnID: "blocker-1", Type: types.DepRelated},
			},
			wantLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			refs := getUnclosedBlockerRefs(tt.deps, unclosedIDs, issueMap)
			if len(refs) != tt.wantLen {
				t.Fatalf("expected %d refs, got %d: %+v", tt.wantLen, len(refs), refs)
			}
			for i, wantID := range tt.wantIDs {
				if refs[i].ID != wantID {
					t.Errorf("refs[%d].ID = %q, want %q", i, refs[i].ID, wantID)
				}
			}
			// Check that title and priority are populated from issueMap
			for _, ref := range refs {
				if iwc, ok := issueMap[ref.ID]; ok {
					if ref.Title != iwc.Issue.Title {
						t.Errorf("ref %q title = %q, want %q", ref.ID, ref.Title, iwc.Issue.Title)
					}
					if ref.Priority != iwc.Issue.Priority {
						t.Errorf("ref %q priority = %d, want %d", ref.ID, ref.Priority, iwc.Issue.Priority)
					}
				}
			}
		})
	}
}

func TestHandleListIssues_ExtractBlockerIDs(t *testing.T) {
	tests := []struct {
		name    string
		refs    []types.BlockerRef
		wantIDs []string
	}{
		{
			name:    "empty refs",
			refs:    []types.BlockerRef{},
			wantIDs: []string{},
		},
		{
			name: "single ref",
			refs: []types.BlockerRef{
				{ID: "blocker-1", Title: "Fix DB", Priority: 1},
			},
			wantIDs: []string{"blocker-1"},
		},
		{
			name: "multiple refs",
			refs: []types.BlockerRef{
				{ID: "blocker-1", Title: "Fix DB", Priority: 1},
				{ID: "blocker-2", Title: "Fix Auth", Priority: 0},
				{ID: "blocker-3", Title: "Fix Cache", Priority: 2},
			},
			wantIDs: []string{"blocker-1", "blocker-2", "blocker-3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ids := extractBlockerIDs(tt.refs)
			if len(ids) != len(tt.wantIDs) {
				t.Fatalf("expected %d IDs, got %d", len(tt.wantIDs), len(ids))
			}
			for i, wantID := range tt.wantIDs {
				if ids[i] != wantID {
					t.Errorf("ids[%d] = %q, want %q", i, ids[i], wantID)
				}
			}
		})
	}
}

// --- IssueWithParent / KanbanIssue JSON shape tests ---

func TestHandleListIssues_IssueWithParent_JSONShape(t *testing.T) {
	now := time.Now()
	parentID := "parent-epic"
	parentTitle := "Epic: User Auth"
	repo := "core-repo"

	iwp := &IssueWithParent{
		IssueWithCounts: &types.IssueWithCounts{
			Issue: &types.Issue{
				ID:        "child-1",
				Title:     "Implement login",
				Status:    types.StatusOpen,
				Priority:  2,
				IssueType: types.TypeFeature,
				CreatedAt: now,
				UpdatedAt: now,
			},
			DependencyCount: 1,
			DependentCount:  0,
		},
		Parent:      &parentID,
		ParentTitle: &parentTitle,
		Repo:        &repo,
	}

	data, err := json.Marshal(iwp)
	if err != nil {
		t.Fatalf("marshal IssueWithParent: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal IssueWithParent: %v", err)
	}

	// Verify expected fields
	if result["id"] != "child-1" {
		t.Errorf("expected id %q, got %v", "child-1", result["id"])
	}
	if result["parent"] != "parent-epic" {
		t.Errorf("expected parent %q, got %v", "parent-epic", result["parent"])
	}
	if result["parent_title"] != "Epic: User Auth" {
		t.Errorf("expected parent_title %q, got %v", "Epic: User Auth", result["parent_title"])
	}
	if result["repo"] != "core-repo" {
		t.Errorf("expected repo %q, got %v", "core-repo", result["repo"])
	}
	// dependency_count should be present from IssueWithCounts
	if dc, ok := result["dependency_count"].(float64); !ok || dc != 1 {
		t.Errorf("expected dependency_count 1, got %v", result["dependency_count"])
	}
}

func TestHandleListIssues_IssueWithParent_NilOptionalFields(t *testing.T) {
	now := time.Now()
	iwp := &IssueWithParent{
		IssueWithCounts: &types.IssueWithCounts{
			Issue: &types.Issue{
				ID:        "orphan-1",
				Title:     "Standalone task",
				Status:    types.StatusOpen,
				Priority:  3,
				IssueType: types.TypeTask,
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
		// No parent, no repo
	}

	data, err := json.Marshal(iwp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// parent and parent_title should be absent (omitempty)
	if _, ok := result["parent"]; ok {
		t.Error("expected 'parent' to be omitted for nil value")
	}
	if _, ok := result["parent_title"]; ok {
		t.Error("expected 'parent_title' to be omitted for nil value")
	}
	if _, ok := result["repo"]; ok {
		t.Error("expected 'repo' to be omitted for nil value")
	}
}

func TestHandleListIssues_KanbanIssue_JSONShape(t *testing.T) {
	now := time.Now()
	parentID := "epic-1"
	parentTitle := "Auth Epic"

	ki := &KanbanIssue{
		IssueWithCounts: &types.IssueWithCounts{
			Issue: &types.Issue{
				ID:        "task-1",
				Title:     "Login page",
				Status:    types.StatusOpen,
				Priority:  2,
				IssueType: types.TypeTask,
				CreatedAt: now,
				UpdatedAt: now,
			},
			DependencyCount: 2,
			DependentCount:  1,
		},
		Parent:         &parentID,
		ParentTitle:    &parentTitle,
		IsBlocked:      true,
		BlockedByCount: 2,
		BlockedBy:      []string{"dep-1", "dep-2"},
		BlockedByDetails: []types.BlockerRef{
			{ID: "dep-1", Title: "Database setup", Priority: 1},
			{ID: "dep-2", Title: "Cache layer", Priority: 2},
		},
	}

	data, err := json.Marshal(ki)
	if err != nil {
		t.Fatalf("marshal KanbanIssue: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal KanbanIssue: %v", err)
	}

	// Core issue fields
	if result["id"] != "task-1" {
		t.Errorf("expected id %q, got %v", "task-1", result["id"])
	}

	// Blocked fields
	if result["is_blocked"] != true {
		t.Errorf("expected is_blocked=true, got %v", result["is_blocked"])
	}
	if bc, ok := result["blocked_by_count"].(float64); !ok || bc != 2 {
		t.Errorf("expected blocked_by_count 2, got %v", result["blocked_by_count"])
	}

	blockedBy, ok := result["blocked_by"].([]interface{})
	if !ok || len(blockedBy) != 2 {
		t.Fatalf("expected blocked_by array of len 2, got %v", result["blocked_by"])
	}
	if blockedBy[0] != "dep-1" || blockedBy[1] != "dep-2" {
		t.Errorf("expected blocked_by [dep-1, dep-2], got %v", blockedBy)
	}

	details, ok := result["blocked_by_details"].([]interface{})
	if !ok || len(details) != 2 {
		t.Fatalf("expected blocked_by_details array of len 2, got %v", result["blocked_by_details"])
	}
	detail0 := details[0].(map[string]interface{})
	if detail0["id"] != "dep-1" {
		t.Errorf("expected detail[0].id %q, got %v", "dep-1", detail0["id"])
	}
	if detail0["title"] != "Database setup" {
		t.Errorf("expected detail[0].title %q, got %v", "Database setup", detail0["title"])
	}

	// Parent fields
	if result["parent"] != "epic-1" {
		t.Errorf("expected parent %q, got %v", "epic-1", result["parent"])
	}
}

func TestHandleListIssues_KanbanIssue_NotBlocked(t *testing.T) {
	now := time.Now()
	ki := &KanbanIssue{
		IssueWithCounts: &types.IssueWithCounts{
			Issue: &types.Issue{
				ID:        "task-2",
				Title:     "Free task",
				Status:    types.StatusOpen,
				Priority:  3,
				IssueType: types.TypeTask,
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
		IsBlocked:      false,
		BlockedByCount: 0,
	}

	data, err := json.Marshal(ki)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if result["is_blocked"] != false {
		t.Errorf("expected is_blocked=false, got %v", result["is_blocked"])
	}
	if bc, ok := result["blocked_by_count"].(float64); !ok || bc != 0 {
		t.Errorf("expected blocked_by_count 0, got %v", result["blocked_by_count"])
	}
	// blocked_by and blocked_by_details should be omitted when empty
	if _, ok := result["blocked_by"]; ok {
		t.Error("expected 'blocked_by' to be omitted when empty")
	}
	if _, ok := result["blocked_by_details"]; ok {
		t.Error("expected 'blocked_by_details' to be omitted when empty")
	}
}

// --- IssuesResponse envelope tests ---

func TestHandleListIssues_IssuesResponse_SuccessEnvelope(t *testing.T) {
	issueList := []map[string]string{
		{"id": "proj-1", "title": "First"},
		{"id": "proj-2", "title": "Second"},
	}
	data, _ := json.Marshal(issueList)

	resp := IssuesResponse{
		Success: true,
		Data:    json.RawMessage(data),
	}

	out, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if result["success"] != true {
		t.Error("expected success=true")
	}
	dataArr, ok := result["data"].([]interface{})
	if !ok || len(dataArr) != 2 {
		t.Fatalf("expected data array of len 2, got %v", result["data"])
	}
	// error and code should be omitted
	if _, ok := result["error"]; ok {
		t.Error("unexpected 'error' field in success response")
	}
	if _, ok := result["code"]; ok {
		t.Error("unexpected 'code' field in success response")
	}
}

func TestHandleListIssues_IssuesResponse_ErrorEnvelope(t *testing.T) {
	resp := IssuesResponse{
		Success: false,
		Error:   "failed to list issues",
		Code:    "RPC_ERROR",
	}

	out, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if result["success"] != false {
		t.Error("expected success=false")
	}
	if result["error"] != "failed to list issues" {
		t.Errorf("expected error message, got %v", result["error"])
	}
	if result["code"] != "RPC_ERROR" {
		t.Errorf("expected code %q, got %v", "RPC_ERROR", result["code"])
	}
}

func TestHandleListIssues_IssuesResponse_EmptyDataEnvelope(t *testing.T) {
	// Empty list case -- what handleListIssues returns for empty results
	data, _ := json.Marshal([]*IssueWithParent{})

	resp := IssuesResponse{
		Success: true,
		Data:    json.RawMessage(data),
	}

	out, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if result["success"] != true {
		t.Error("expected success=true")
	}
	dataArr, ok := result["data"].([]interface{})
	if !ok {
		t.Fatalf("expected data to be array, got %T", result["data"])
	}
	if len(dataArr) != 0 {
		t.Errorf("expected empty array, got len %d", len(dataArr))
	}
}
