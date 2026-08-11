package git

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

type narrowWorkItemQueries struct {
	list    *workitems.ListResult
	blocked []workitems.IssueSummary
	detail  map[string]*workitems.IssueDetail
}

func (q *narrowWorkItemQueries) List(context.Context, workitems.ListQuery) (*workitems.ListResult, error) {
	return q.list, nil
}

func (q *narrowWorkItemQueries) Get(_ context.Context, query workitems.GetQuery) (*workitems.IssueDetail, error) {
	return q.detail[query.IssueID], nil
}

func (q *narrowWorkItemQueries) Blocked(context.Context, workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
	return q.blocked, nil
}

func TestHandleGraphUsesWorkItemsOwnerQueries(t *testing.T) {
	queries := &narrowWorkItemQueries{
		list:   &workitems.ListResult{Issues: []workitems.ListItem{{IssueSummary: workitems.IssueSummary{ID: "TASK-1", Title: "task", Status: "open"}}}},
		detail: map[string]*workitems.IssueDetail{"TASK-1": {ID: "TASK-1"}},
	}
	handler := HandleGraph(queries)
	req := httptest.NewRequest(http.MethodGet, "/api/issues/graph", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleBlocked tests
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// handleGraph tests
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// parseBlockedParams tests
// ---------------------------------------------------------------------------

func TestParseBlockedParams(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		wantErr   bool
		checkArgs func(t *testing.T, args *blockedFilter)
	}{
		{
			name: "no params returns defaults",
			url:  "/api/blocked",
			checkArgs: func(t *testing.T, args *blockedFilter) {
				if args.ParentID != "" {
					t.Errorf("ParentID = %q, want empty", args.ParentID)
				}
				if args.Assignee != "" {
					t.Errorf("Assignee = %q, want empty", args.Assignee)
				}
				if args.Priority != nil {
					t.Errorf("Priority = %v, want nil", args.Priority)
				}
				if args.Limit != 0 {
					t.Errorf("Limit = %d, want 0", args.Limit)
				}
			},
		},
		{
			name:    "invalid priority (not a number)",
			url:     "/api/blocked?priority=high",
			wantErr: true,
		},
		{
			name:    "priority out of range (negative)",
			url:     "/api/blocked?priority=-1",
			wantErr: true,
		},
		{
			name:    "priority out of range (too high)",
			url:     "/api/blocked?priority=5",
			wantErr: true,
		},
		{
			name:    "negative limit",
			url:     "/api/blocked?limit=-10",
			wantErr: true,
		},
		{
			name: "limit exceeding MaxListLimit is capped",
			url:  "/api/blocked?limit=5000",
			checkArgs: func(t *testing.T, args *blockedFilter) {
				if args.Limit != MaxListLimit {
					t.Errorf("Limit = %d, want %d (capped at MaxListLimit)", args.Limit, MaxListLimit)
				}
			},
		},
		{
			name: "all valid params",
			url:  "/api/blocked?parent_id=loom-root&assignee=bob&type=feature&priority=3&limit=25",
			checkArgs: func(t *testing.T, args *blockedFilter) {
				if args.ParentID != "loom-root" {
					t.Errorf("ParentID = %q, want %q", args.ParentID, "loom-root")
				}
				if args.Assignee != "bob" {
					t.Errorf("Assignee = %q, want %q", args.Assignee, "bob")
				}
				if args.Type != "feature" {
					t.Errorf("Type = %q, want %q", args.Type, "feature")
				}
				if args.Priority == nil || *args.Priority != 3 {
					t.Errorf("Priority = %v, want 3", args.Priority)
				}
				if args.Limit != 25 {
					t.Errorf("Limit = %d, want 25", args.Limit)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			args, err := parseBlockedParams(req)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.checkArgs != nil {
				tt.checkArgs(t, args)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// parseGraphParams tests
// ---------------------------------------------------------------------------

func TestParseGraphParams_TableDriven(t *testing.T) {
	tests := []struct {
		name              string
		url               string
		wantStatus        string
		wantIncludeClosed bool
		wantSourceRepos   []string
	}{
		{
			name:              "defaults",
			url:               "/api/issues/graph",
			wantStatus:        "all",
			wantIncludeClosed: true,
			wantSourceRepos:   nil,
		},
		{
			name:              "status=open",
			url:               "/api/issues/graph?status=open",
			wantStatus:        "open",
			wantIncludeClosed: true,
		},
		{
			name:              "include_closed=false",
			url:               "/api/issues/graph?include_closed=false",
			wantStatus:        "all",
			wantIncludeClosed: false,
		},
		{
			name:              "source_repos param",
			url:               "/api/issues/graph?source_repos=repo-a,repo-b",
			wantStatus:        "all",
			wantIncludeClosed: true,
			wantSourceRepos:   []string{"repo-a", "repo-b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			status, includeClosed, sourceRepos := parseGraphParams(req)

			if status != tt.wantStatus {
				t.Errorf("status = %q, want %q", status, tt.wantStatus)
			}
			if includeClosed != tt.wantIncludeClosed {
				t.Errorf("includeClosed = %v, want %v", includeClosed, tt.wantIncludeClosed)
			}
			if tt.wantSourceRepos != nil {
				if len(sourceRepos) != len(tt.wantSourceRepos) {
					t.Fatalf("sourceRepos = %v, want %v", sourceRepos, tt.wantSourceRepos)
				}
				for i, v := range tt.wantSourceRepos {
					if sourceRepos[i] != v {
						t.Errorf("sourceRepos[%d] = %q, want %q", i, sourceRepos[i], v)
					}
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Work Items owner-query tests — HandleGraph
// ---------------------------------------------------------------------------

func TestHandleGraph_WorkItemsOwnerQueries(t *testing.T) {
	queries := &narrowWorkItemQueries{
		list: &workitems.ListResult{Issues: []workitems.ListItem{
			{IssueSummary: workitems.IssueSummary{ID: "PARITY-1", Title: "Child A", Status: "open", Priority: 1, IssueType: "bug", Parent: "EPIC-1"}},
			{IssueSummary: workitems.IssueSummary{ID: "EPIC-1", Title: "Epic", Status: "open", Priority: 0, IssueType: "epic"}},
		}},
		detail: map[string]*workitems.IssueDetail{
			"PARITY-1": {ID: "PARITY-1", Dependencies: []workitems.Dependency{{ID: "BLOCKER-1", DependencyType: "blocks"}}},
			"EPIC-1":   {ID: "EPIC-1"},
		},
	}
	handler := HandleGraph(queries)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/graph", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var resp GraphResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success=true; error=%q", resp.Error)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("issues = %d, want 2", len(resp.Data))
	}
	var found *GraphIssue
	for _, gi := range resp.Data {
		if gi.ID == "PARITY-1" {
			found = gi
		}
	}
	if found == nil {
		t.Fatal("missing PARITY-1 node")
	}
	// Should contain BOTH the backend-reported "blocks" dependency AND the
	// synthesized parent-child edge (Parent field).
	sawBlocker, sawParent := false, false
	for _, dep := range found.Dependencies {
		if dep.DependsOnID == "BLOCKER-1" && dep.Type == "blocks" {
			sawBlocker = true
		}
		if dep.DependsOnID == "EPIC-1" && dep.Type == "parent-child" {
			sawParent = true
		}
	}
	if !sawBlocker {
		t.Error("expected blocks dependency in child node")
	}
	if !sawParent {
		t.Error("expected synthesized parent-child edge to EPIC-1")
	}
}

func TestHandleGraph_WorkItemsStatusFilter(t *testing.T) {
	queries := &narrowWorkItemQueries{
		list: &workitems.ListResult{Issues: []workitems.ListItem{
			{IssueSummary: workitems.IssueSummary{ID: "OPEN-1", Status: "open"}},
			{IssueSummary: workitems.IssueSummary{ID: "CLOSED-1", Status: "closed"}},
			{IssueSummary: workitems.IssueSummary{ID: "TOMB-1", Status: "tombstone"}},
		}},
		detail: map[string]*workitems.IssueDetail{},
	}
	handler := HandleGraph(queries)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/graph?status=open", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	var resp GraphResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 1 || resp.Data[0].ID != "OPEN-1" {
		ids := make([]string, 0, len(resp.Data))
		for _, gi := range resp.Data {
			ids = append(ids, gi.ID)
		}
		t.Errorf("status=open returned IDs %v; want [OPEN-1]", ids)
	}
}

func TestHandleGraph_NoPoolNoBackendReturns503(t *testing.T) {
	handler := HandleGraph(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/issues/graph", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Work Items owner-query tests — HandleBlocked
// ---------------------------------------------------------------------------

func TestHandleBlocked_WorkItemsOwnerQueries(t *testing.T) {
	queries := &narrowWorkItemQueries{
		blocked: []workitems.IssueSummary{{ID: "FLEET-1", Title: "fleet-only"}},
	}
	handler := HandleBlocked(queries)

	req := httptest.NewRequest(http.MethodGet, "/api/blocked", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var env struct {
		Success bool                     `json:"success"`
		Data    []workitems.IssueSummary `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !env.Success || len(env.Data) != 1 || env.Data[0].ID != "FLEET-1" {
		t.Errorf("expected fleet-only data (FLEET-1), got %+v", env)
	}
}

func TestHandleBlocked_NoPoolNoBackendReturns503(t *testing.T) {
	handler := HandleBlocked(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/blocked", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}
