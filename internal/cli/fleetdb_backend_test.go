package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
)

// mockFleetDBClient implements fleetDBClient for testing.
type mockFleetDBClient struct {
	readyFn   func(args *rpc.ReadyArgs) (*rpc.Response, error)
	listFn    func(args *rpc.ListArgs) (*rpc.Response, error)
	showFn    func(args *rpc.ShowArgs) (*rpc.Response, error)
	blockedFn func(args *rpc.BlockedArgs) (*rpc.Response, error)
	statsFn   func() (*rpc.Response, error)
	updateFn  func(args *rpc.UpdateArgs) (*rpc.Response, error)
	closeFn   func(args *rpc.CloseArgs) (*rpc.Response, error)
}

func (m *mockFleetDBClient) Ready(args *rpc.ReadyArgs) (*rpc.Response, error) {
	if m.readyFn != nil {
		return m.readyFn(args)
	}
	return &rpc.Response{Success: true, Data: json.RawMessage(`[]`)}, nil
}

func (m *mockFleetDBClient) List(args *rpc.ListArgs) (*rpc.Response, error) {
	if m.listFn != nil {
		return m.listFn(args)
	}
	return &rpc.Response{Success: true, Data: json.RawMessage(`[]`)}, nil
}

func (m *mockFleetDBClient) Show(args *rpc.ShowArgs) (*rpc.Response, error) {
	if m.showFn != nil {
		return m.showFn(args)
	}
	return &rpc.Response{Success: true, Data: json.RawMessage("null")}, nil
}

func (m *mockFleetDBClient) Blocked(args *rpc.BlockedArgs) (*rpc.Response, error) {
	if m.blockedFn != nil {
		return m.blockedFn(args)
	}
	return &rpc.Response{Success: true, Data: json.RawMessage(`[]`)}, nil
}

func (m *mockFleetDBClient) Stats() (*rpc.Response, error) {
	if m.statsFn != nil {
		return m.statsFn()
	}
	return &rpc.Response{Success: true, Data: json.RawMessage("null")}, nil
}

func (m *mockFleetDBClient) Update(args *rpc.UpdateArgs) (*rpc.Response, error) {
	if m.updateFn != nil {
		return m.updateFn(args)
	}
	return &rpc.Response{Success: true, Data: json.RawMessage("null")}, nil
}

func (m *mockFleetDBClient) CloseIssue(args *rpc.CloseArgs) (*rpc.Response, error) {
	if m.closeFn != nil {
		return m.closeFn(args)
	}
	return &rpc.Response{Success: true, Data: json.RawMessage("null")}, nil
}

// helper: marshal data into a successful Response
func successResp(data interface{}) *rpc.Response {
	raw, _ := json.Marshal(data)
	return &rpc.Response{Success: true, Data: raw}
}

func TestMockFleetDBClient_DefaultsDoNotPanic(t *testing.T) {
	mock := &mockFleetDBClient{} // no fn callbacks set
	b := newFleetDBBackend(mock, "test")

	t.Run("Ready", func(t *testing.T) {
		got, err := b.Ready(context.Background(), ReadyOpts{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == nil {
			t.Fatal("expected non-nil result")
		}
	})

	t.Run("List", func(t *testing.T) {
		got, err := b.List(context.Background(), ListOpts{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == nil {
			t.Fatal("expected non-nil result")
		}
	})

	t.Run("Show", func(t *testing.T) {
		got, err := b.GetIssue(context.Background(), "X")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == nil {
			t.Fatal("expected non-nil result")
		}
	})

	t.Run("Blocked", func(t *testing.T) {
		got, err := b.Blocked(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == nil {
			t.Fatal("expected non-nil result")
		}
	})

	t.Run("Stats", func(t *testing.T) {
		got, err := b.Stats(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == nil {
			t.Fatal("expected non-nil result")
		}
	})

	t.Run("Update", func(t *testing.T) {
		err := b.UpdateIssue(context.Background(), "X", UpdateOpts{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("CloseIssue", func(t *testing.T) {
		err := b.CloseIssue(context.Background(), "X", "done")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

// (RunCommand dispatch tests removed — RunCommand and dispatch table no longer exist.
//  Type conversion is tested via TestFleetDBBackend_DependencyConversion and
//  TestFleetDBBackend_IssuesToBdIssues. Typed method tests below cover the RPC path.)

func TestFleetDBBackend_DependencyConversion(t *testing.T) {
	// CRITICAL test: verify time.Time -> RFC3339 string, DependencyType -> string.
	createdAt := time.Date(2026, 3, 14, 15, 30, 0, 0, time.UTC)
	dep := &types.Dependency{
		IssueID:     "a",
		DependsOnID: "b",
		Type:        types.DepParentChild,
		CreatedAt:   createdAt,
		CreatedBy:   "user1",
		Metadata:    `{"some":"data"}`, // Should be dropped
		ThreadID:    "thread-1",        // Should be dropped
	}

	result := convertDependency(dep)

	if result.IssueID != "a" {
		t.Errorf("expected IssueID 'a', got %q", result.IssueID)
	}
	if result.DependsOnID != "b" {
		t.Errorf("expected DependsOnID 'b', got %q", result.DependsOnID)
	}
	if result.Type != "parent-child" {
		t.Errorf("expected Type 'parent-child', got %q", result.Type)
	}
	expectedTime := "2026-03-14T15:30:00Z"
	if result.CreatedAt != expectedTime {
		t.Errorf("expected CreatedAt %q, got %q", expectedTime, result.CreatedAt)
	}
	if result.CreatedBy != "user1" {
		t.Errorf("expected CreatedBy 'user1', got %q", result.CreatedBy)
	}

	// Verify Metadata and ThreadID are not in the output by marshaling to JSON
	outBytes, _ := json.Marshal(result)
	outStr := string(outBytes)
	if jsonContainsField(outStr, "metadata") {
		t.Error("Metadata should not be present in converted dependency")
	}
	if jsonContainsField(outStr, "thread_id") {
		t.Error("ThreadID should not be present in converted dependency")
	}
}

// jsonContainsField checks if a JSON string contains a specific field name
func jsonContainsField(jsonStr, field string) bool {
	return strings.Contains(jsonStr, `"`+field+`"`)
}

// --- IssueTracker typed method tests ---

func TestFleetDBBackend_BackendName(t *testing.T) {
	b := newFleetDBBackend(&mockFleetDBClient{}, "test")
	if name := b.BackendName(); name != "fleet-db" {
		t.Errorf("got %q, want fleet-db", name)
	}
}

func TestFleetDBBackend_Ready(t *testing.T) {
	mock := &mockFleetDBClient{
		readyFn: func(args *rpc.ReadyArgs) (*rpc.Response, error) {
			if args.Limit != 10 {
				t.Errorf("expected limit 10, got %d", args.Limit)
			}
			if args.ParentID != "epic-1" {
				t.Errorf("expected parent 'epic-1', got %q", args.ParentID)
			}
			issues := []*types.Issue{
				{ID: "T-1", Title: "Task 1", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask},
				{ID: "T-2", Title: "Task 2", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask},
			}
			return successResp(issues), nil
		},
	}

	b := newFleetDBBackend(mock, "test")
	got, err := b.Ready(context.Background(), ReadyOpts{Limit: 10, ParentID: "epic-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(got))
	}
	if got[0].ID != "T-1" {
		t.Errorf("got[0].ID = %q, want T-1", got[0].ID)
	}
	if got[1].ID != "T-2" {
		t.Errorf("got[1].ID = %q, want T-2", got[1].ID)
	}
}

func TestFleetDBBackend_Ready_ForwardsLabelsAndSourceRepos(t *testing.T) {
	mock := &mockFleetDBClient{
		readyFn: func(args *rpc.ReadyArgs) (*rpc.Response, error) {
			if args.Limit != 10 {
				t.Errorf("expected limit 10, got %d", args.Limit)
			}
			if len(args.Labels) != 1 || args.Labels[0] != "repo:backend" {
				t.Errorf("expected labels [repo:backend], got %v", args.Labels)
			}
			if len(args.SourceRepos) != 2 || args.SourceRepos[0] != "repo-a" || args.SourceRepos[1] != "repo-b" {
				t.Errorf("expected source_repos [repo-a repo-b], got %v", args.SourceRepos)
			}
			issues := []*types.Issue{
				{ID: "T-1", Title: "Task 1", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask},
			}
			return successResp(issues), nil
		},
	}

	b := newFleetDBBackend(mock, "test")
	got, err := b.Ready(context.Background(), ReadyOpts{
		Limit:       10,
		Labels:      []string{"repo:backend"},
		SourceRepos: []string{"repo-a", "repo-b"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "T-1" {
		t.Errorf("expected [T-1], got %v", got)
	}
}

func TestFleetDBBackend_Ready_NoOpts(t *testing.T) {
	mock := &mockFleetDBClient{
		readyFn: func(args *rpc.ReadyArgs) (*rpc.Response, error) {
			if args.Limit != 0 {
				t.Errorf("expected limit 0, got %d", args.Limit)
			}
			if args.ParentID != "" {
				t.Errorf("expected empty parent, got %q", args.ParentID)
			}
			return successResp([]*types.Issue{}), nil
		},
	}

	b := newFleetDBBackend(mock, "test")
	got, err := b.Ready(context.Background(), ReadyOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %v", got)
	}
}

func TestFleetDBBackend_Ready_RPCError(t *testing.T) {
	mock := &mockFleetDBClient{
		readyFn: func(_ *rpc.ReadyArgs) (*rpc.Response, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}

	b := newFleetDBBackend(mock, "test")
	_, err := b.Ready(context.Background(), ReadyOpts{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error = %q, want 'connection refused'", err.Error())
	}
}

func TestFleetDBBackend_Ready_ServerError(t *testing.T) {
	mock := &mockFleetDBClient{
		readyFn: func(_ *rpc.ReadyArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: false, Error: "database unavailable"}, nil
		},
	}

	b := newFleetDBBackend(mock, "test")
	_, err := b.Ready(context.Background(), ReadyOpts{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "database unavailable") {
		t.Errorf("error = %q, want 'database unavailable'", err.Error())
	}
}

func TestFleetDBBackend_List(t *testing.T) {
	mock := &mockFleetDBClient{
		listFn: func(args *rpc.ListArgs) (*rpc.Response, error) {
			if args.Status != "in_progress" {
				t.Errorf("expected status 'in_progress', got %q", args.Status)
			}
			if args.Assignee != "bot" {
				t.Errorf("expected assignee 'bot', got %q", args.Assignee)
			}
			if args.IssueType != "task" {
				t.Errorf("expected type 'task', got %q", args.IssueType)
			}
			if args.ParentID != "epic-1" {
				t.Errorf("expected parent_id 'epic-1', got %q", args.ParentID)
			}
			if args.Limit != 5 {
				t.Errorf("expected limit 5, got %d", args.Limit)
			}
			issues := []*types.Issue{
				{ID: "T-2", Title: "In Progress", Status: types.StatusInProgress, IssueType: types.TypeTask, Assignee: "bot"},
			}
			return successResp(issues), nil
		},
	}

	b := newFleetDBBackend(mock, "test")
	got, err := b.List(context.Background(), ListOpts{Status: "in_progress", Assignee: "bot", Type: "task", ParentID: "epic-1", Limit: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "T-2" {
		t.Errorf("got %v", got)
	}
}

func TestFleetDBBackend_List_RPCError(t *testing.T) {
	mock := &mockFleetDBClient{
		listFn: func(_ *rpc.ListArgs) (*rpc.Response, error) {
			return nil, fmt.Errorf("timeout")
		},
	}

	b := newFleetDBBackend(mock, "test")
	_, err := b.List(context.Background(), ListOpts{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestFleetDBBackend_List_ServerError(t *testing.T) {
	mock := &mockFleetDBClient{
		listFn: func(_ *rpc.ListArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: false, Error: "bad filter"}, nil
		},
	}

	b := newFleetDBBackend(mock, "test")
	_, err := b.List(context.Background(), ListOpts{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "bad filter") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestFleetDBBackend_Blocked(t *testing.T) {
	mock := &mockFleetDBClient{
		blockedFn: func(_ *rpc.BlockedArgs) (*rpc.Response, error) {
			issues := []*types.Issue{
				{ID: "T-3", Title: "Blocked task", Status: types.StatusBlocked, IssueType: types.TypeTask},
			}
			return successResp(issues), nil
		},
	}

	b := newFleetDBBackend(mock, "test")
	got, err := b.Blocked(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "T-3" {
		t.Errorf("got %v", got)
	}
}

func TestFleetDBBackend_Blocked_RPCError(t *testing.T) {
	mock := &mockFleetDBClient{
		blockedFn: func(_ *rpc.BlockedArgs) (*rpc.Response, error) {
			return nil, fmt.Errorf("connection reset")
		},
	}

	b := newFleetDBBackend(mock, "test")
	_, err := b.Blocked(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "connection reset") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestFleetDBBackend_Blocked_ServerError(t *testing.T) {
	mock := &mockFleetDBClient{
		blockedFn: func(_ *rpc.BlockedArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: false, Error: "internal error"}, nil
		},
	}

	b := newFleetDBBackend(mock, "test")
	_, err := b.Blocked(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "internal error") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestFleetDBBackend_Stats(t *testing.T) {
	mock := &mockFleetDBClient{
		statsFn: func() (*rpc.Response, error) {
			stats := types.Statistics{
				TotalIssues:      100,
				OpenIssues:       40,
				ClosedIssues:     30,
				InProgressIssues: 15,
				BlockedIssues:    5,
				DeferredIssues:   3,
				TombstoneIssues:  2,
				PinnedIssues:     5,
			}
			return successResp(stats), nil
		},
	}

	b := newFleetDBBackend(mock, "test")
	got, err := b.Stats(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil stats")
	}
	if got.Summary.TotalIssues != 100 {
		t.Errorf("TotalIssues = %d, want 100", got.Summary.TotalIssues)
	}
	if got.Summary.OpenIssues != 40 {
		t.Errorf("OpenIssues = %d, want 40", got.Summary.OpenIssues)
	}
	if got.Summary.ClosedIssues != 30 {
		t.Errorf("ClosedIssues = %d, want 30", got.Summary.ClosedIssues)
	}
	if got.Summary.InProgressIssues != 15 {
		t.Errorf("InProgressIssues = %d, want 15", got.Summary.InProgressIssues)
	}
	if got.Summary.BlockedIssues != 5 {
		t.Errorf("BlockedIssues = %d, want 5", got.Summary.BlockedIssues)
	}
	if got.Summary.DeferredIssues != 3 {
		t.Errorf("DeferredIssues = %d, want 3", got.Summary.DeferredIssues)
	}
	if got.Summary.TombstoneIssues != 2 {
		t.Errorf("TombstoneIssues = %d, want 2", got.Summary.TombstoneIssues)
	}
	if got.Summary.PinnedIssues != 5 {
		t.Errorf("PinnedIssues = %d, want 5", got.Summary.PinnedIssues)
	}
}

func TestFleetDBBackend_Stats_RPCError(t *testing.T) {
	mock := &mockFleetDBClient{
		statsFn: func() (*rpc.Response, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}

	b := newFleetDBBackend(mock, "test")
	_, err := b.Stats(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestFleetDBBackend_Stats_ServerError(t *testing.T) {
	mock := &mockFleetDBClient{
		statsFn: func() (*rpc.Response, error) {
			return &rpc.Response{Success: false, Error: "stats unavailable"}, nil
		},
	}

	b := newFleetDBBackend(mock, "test")
	_, err := b.Stats(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "stats unavailable") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestFleetDBBackend_GetIssue(t *testing.T) {
	mock := &mockFleetDBClient{
		showFn: func(args *rpc.ShowArgs) (*rpc.Response, error) {
			if args.ID != "T-4" {
				t.Errorf("expected ID 'T-4', got %q", args.ID)
			}
			details := types.IssueDetails{
				Issue: types.Issue{
					ID:        "T-4",
					Title:     "Detail task",
					Status:    types.StatusOpen,
					Priority:  2,
					IssueType: types.TypeBug,
					Assignee:  "drift",
					Design:    "some design",
					CreatedAt: time.Date(2026, 3, 14, 12, 0, 0, 0, time.UTC),
				},
				Labels: []string{"critical", "backend"},
				Dependencies: []*types.IssueWithDependencyMetadata{
					{
						Issue: types.Issue{
							ID:        "T-3",
							Title:     "Prereq",
							CreatedAt: time.Date(2026, 3, 13, 10, 0, 0, 0, time.UTC),
							CreatedBy: "admin",
						},
						DependencyType: types.DepBlocks,
					},
				},
			}
			return successResp(details), nil
		},
	}

	b := newFleetDBBackend(mock, "test")
	got, err := b.GetIssue(context.Background(), "T-4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil issue")
	}
	if got.ID != "T-4" {
		t.Errorf("ID = %q, want T-4", got.ID)
	}
	if got.Title != "Detail task" {
		t.Errorf("Title = %q", got.Title)
	}
	if got.Status != "open" {
		t.Errorf("Status = %q, want open", got.Status)
	}
	if got.IssueType != "bug" {
		t.Errorf("IssueType = %q, want bug", got.IssueType)
	}
	if got.Assignee != "drift" {
		t.Errorf("Assignee = %q, want drift", got.Assignee)
	}
	if got.Design != "some design" {
		t.Errorf("Design = %q", got.Design)
	}
	if len(got.Labels) != 2 || got.Labels[0] != "critical" {
		t.Errorf("Labels = %v", got.Labels)
	}
	if len(got.Dependencies) != 1 {
		t.Fatalf("expected 1 dep, got %d", len(got.Dependencies))
	}
	dep := got.Dependencies[0]
	if dep.IssueID != "T-4" {
		t.Errorf("dep.IssueID = %q, want T-4", dep.IssueID)
	}
	if dep.DependsOnID != "T-3" {
		t.Errorf("dep.DependsOnID = %q, want T-3", dep.DependsOnID)
	}
	if dep.Type != "blocks" {
		t.Errorf("dep.Type = %q, want blocks", dep.Type)
	}
}

func TestFleetDBBackend_GetIssue_RPCError(t *testing.T) {
	mock := &mockFleetDBClient{
		showFn: func(_ *rpc.ShowArgs) (*rpc.Response, error) {
			return nil, fmt.Errorf("not found")
		},
	}

	b := newFleetDBBackend(mock, "test")
	_, err := b.GetIssue(context.Background(), "MISSING")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestFleetDBBackend_GetIssue_ServerError(t *testing.T) {
	mock := &mockFleetDBClient{
		showFn: func(_ *rpc.ShowArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: false, Error: "issue not found"}, nil
		},
	}

	b := newFleetDBBackend(mock, "test")
	_, err := b.GetIssue(context.Background(), "MISSING")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "issue not found") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestFleetDBBackend_GetIssueText(t *testing.T) {
	mock := &mockFleetDBClient{
		showFn: func(args *rpc.ShowArgs) (*rpc.Response, error) {
			if args.ID != "T-5" {
				t.Errorf("expected ID 'T-5', got %q", args.ID)
			}
			// GetIssueText returns raw resp.Data as string
			return &rpc.Response{Success: true, Data: json.RawMessage(`"Human-readable output"`)}, nil
		},
	}

	b := newFleetDBBackend(mock, "test")
	got, err := b.GetIssueText(context.Background(), "T-5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != `"Human-readable output"` {
		t.Errorf("got %q", got)
	}
}

func TestFleetDBBackend_GetIssueText_RPCError(t *testing.T) {
	mock := &mockFleetDBClient{
		showFn: func(_ *rpc.ShowArgs) (*rpc.Response, error) {
			return nil, fmt.Errorf("timeout")
		},
	}

	b := newFleetDBBackend(mock, "test")
	_, err := b.GetIssueText(context.Background(), "X")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestFleetDBBackend_GetIssueText_ServerError(t *testing.T) {
	mock := &mockFleetDBClient{
		showFn: func(_ *rpc.ShowArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: false, Error: "not found"}, nil
		},
	}

	b := newFleetDBBackend(mock, "test")
	_, err := b.GetIssueText(context.Background(), "X")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestFleetDBBackend_UpdateIssue(t *testing.T) {
	assignee := "drift"
	mock := &mockFleetDBClient{
		updateFn: func(args *rpc.UpdateArgs) (*rpc.Response, error) {
			if args.ID != "T-6" {
				t.Errorf("expected ID 'T-6', got %q", args.ID)
			}
			if args.Status == nil || *args.Status != "in_progress" {
				t.Errorf("expected status 'in_progress', got %v", args.Status)
			}
			if args.Assignee == nil || *args.Assignee != "drift" {
				t.Errorf("expected assignee 'drift', got %v", args.Assignee)
			}
			if args.Design == nil || *args.Design != "plan text" {
				t.Errorf("expected design 'plan text', got %v", args.Design)
			}
			if !args.Claim {
				t.Error("expected Claim to be true")
			}
			issue := types.Issue{ID: "T-6", Status: types.StatusInProgress}
			return successResp(issue), nil
		},
	}

	b := newFleetDBBackend(mock, "test")
	err := b.UpdateIssue(context.Background(), "T-6", UpdateOpts{
		Status: "in_progress", Assignee: &assignee, Design: "plan text", Claim: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFleetDBBackend_UpdateIssue_MinimalOpts(t *testing.T) {
	mock := &mockFleetDBClient{
		updateFn: func(args *rpc.UpdateArgs) (*rpc.Response, error) {
			if args.ID != "T-7" {
				t.Errorf("expected ID 'T-7', got %q", args.ID)
			}
			if args.Status == nil || *args.Status != "open" {
				t.Errorf("expected status 'open', got %v", args.Status)
			}
			if args.Assignee != nil {
				t.Errorf("expected nil assignee, got %v", args.Assignee)
			}
			if args.Design != nil {
				t.Errorf("expected nil design, got %v", args.Design)
			}
			if args.Claim {
				t.Error("expected Claim to be false")
			}
			issue := types.Issue{ID: "T-7", Status: types.StatusOpen}
			return successResp(issue), nil
		},
	}

	b := newFleetDBBackend(mock, "test")
	err := b.UpdateIssue(context.Background(), "T-7", UpdateOpts{Status: "open"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFleetDBBackend_UpdateIssue_ClearAssignee(t *testing.T) {
	empty := ""
	mock := &mockFleetDBClient{
		updateFn: func(args *rpc.UpdateArgs) (*rpc.Response, error) {
			if args.Assignee == nil {
				t.Fatal("expected non-nil assignee pointer")
			}
			if *args.Assignee != "" {
				t.Errorf("expected empty assignee, got %q", *args.Assignee)
			}
			issue := types.Issue{ID: "T-8"}
			return successResp(issue), nil
		},
	}

	b := newFleetDBBackend(mock, "test")
	err := b.UpdateIssue(context.Background(), "T-8", UpdateOpts{Assignee: &empty})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFleetDBBackend_UpdateIssue_RPCError(t *testing.T) {
	mock := &mockFleetDBClient{
		updateFn: func(_ *rpc.UpdateArgs) (*rpc.Response, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}

	b := newFleetDBBackend(mock, "test")
	err := b.UpdateIssue(context.Background(), "X", UpdateOpts{Status: "open"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestFleetDBBackend_UpdateIssue_ServerError(t *testing.T) {
	mock := &mockFleetDBClient{
		updateFn: func(_ *rpc.UpdateArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: false, Error: "conflict"}, nil
		},
	}

	b := newFleetDBBackend(mock, "test")
	err := b.UpdateIssue(context.Background(), "X", UpdateOpts{Status: "open"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "conflict") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestFleetDBBackend_UpdateExternalRef(t *testing.T) {
	mock := &mockFleetDBClient{
		updateFn: func(args *rpc.UpdateArgs) (*rpc.Response, error) {
			if args.ID != "T-9" {
				t.Errorf("expected ID 'T-9', got %q", args.ID)
			}
			if args.ExternalRef == nil {
				t.Fatal("expected non-nil ExternalRef")
			}
			if *args.ExternalRef != "GH-123" {
				t.Errorf("ExternalRef = %q, want GH-123", *args.ExternalRef)
			}
			issue := types.Issue{ID: "T-9"}
			return successResp(issue), nil
		},
	}

	b := newFleetDBBackend(mock, "test")
	err := b.UpdateExternalRef(context.Background(), "T-9", "GH-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFleetDBBackend_UpdateExternalRef_RPCError(t *testing.T) {
	mock := &mockFleetDBClient{
		updateFn: func(_ *rpc.UpdateArgs) (*rpc.Response, error) {
			return nil, fmt.Errorf("timeout")
		},
	}

	b := newFleetDBBackend(mock, "test")
	err := b.UpdateExternalRef(context.Background(), "X", "ref")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestFleetDBBackend_UpdateExternalRef_ServerError(t *testing.T) {
	mock := &mockFleetDBClient{
		updateFn: func(_ *rpc.UpdateArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: false, Error: "permission denied"}, nil
		},
	}

	b := newFleetDBBackend(mock, "test")
	err := b.UpdateExternalRef(context.Background(), "X", "ref")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestFleetDBBackend_CloseIssue(t *testing.T) {
	mock := &mockFleetDBClient{
		closeFn: func(args *rpc.CloseArgs) (*rpc.Response, error) {
			if args.ID != "T-10" {
				t.Errorf("expected ID 'T-10', got %q", args.ID)
			}
			if args.Reason != "completed" {
				t.Errorf("expected reason 'completed', got %q", args.Reason)
			}
			issue := types.Issue{ID: "T-10", Status: types.StatusClosed}
			return successResp(issue), nil
		},
	}

	b := newFleetDBBackend(mock, "test")
	err := b.CloseIssue(context.Background(), "T-10", "completed")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFleetDBBackend_CloseIssue_RPCError(t *testing.T) {
	mock := &mockFleetDBClient{
		closeFn: func(_ *rpc.CloseArgs) (*rpc.Response, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}

	b := newFleetDBBackend(mock, "test")
	err := b.CloseIssue(context.Background(), "X", "reason")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestFleetDBBackend_CloseIssue_ServerError(t *testing.T) {
	mock := &mockFleetDBClient{
		closeFn: func(_ *rpc.CloseArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: false, Error: "already closed"}, nil
		},
	}

	b := newFleetDBBackend(mock, "test")
	err := b.CloseIssue(context.Background(), "X", "reason")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "already closed") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestFleetDBBackend_IssuesToBdIssues(t *testing.T) {
	createdAt := time.Date(2026, 3, 14, 12, 0, 0, 0, time.UTC)
	issues := []*types.Issue{
		{
			ID:        "T-1",
			Title:     "First",
			Status:    types.StatusOpen,
			Priority:  1,
			IssueType: types.TypeTask,
			Labels:    []string{"backend"},
			Dependencies: []*types.Dependency{
				{
					IssueID:     "T-1",
					DependsOnID: "T-0",
					Type:        types.DepBlocks,
					CreatedAt:   createdAt,
					CreatedBy:   "admin",
				},
			},
		},
		{
			ID:        "T-2",
			Title:     "Second",
			Status:    types.StatusInProgress,
			Priority:  2,
			IssueType: types.TypeBug,
			Labels:    nil, // nil labels
		},
	}

	result := issuesToBdIssues(issues)

	if len(result) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(result))
	}

	// First issue: has labels and deps
	if result[0].ID != "T-1" {
		t.Errorf("result[0].ID = %q", result[0].ID)
	}
	if result[0].Status != "open" {
		t.Errorf("result[0].Status = %q", result[0].Status)
	}
	if len(result[0].Labels) != 1 || result[0].Labels[0] != "backend" {
		t.Errorf("result[0].Labels = %v", result[0].Labels)
	}
	if len(result[0].Dependencies) != 1 {
		t.Fatalf("result[0].Dependencies len = %d", len(result[0].Dependencies))
	}
	if result[0].Dependencies[0].Type != "blocks" {
		t.Errorf("dep type = %q", result[0].Dependencies[0].Type)
	}
	expectedTime := createdAt.Format(time.RFC3339)
	if result[0].Dependencies[0].CreatedAt != expectedTime {
		t.Errorf("dep CreatedAt = %q, want %q", result[0].Dependencies[0].CreatedAt, expectedTime)
	}

	// Second issue: nil labels -> empty slice, nil deps -> empty slice
	if result[1].ID != "T-2" {
		t.Errorf("result[1].ID = %q", result[1].ID)
	}
	if result[1].Labels == nil {
		t.Error("result[1].Labels should not be nil")
	}
	if len(result[1].Labels) != 0 {
		t.Errorf("result[1].Labels len = %d", len(result[1].Labels))
	}
	if result[1].Dependencies == nil {
		t.Error("result[1].Dependencies should not be nil")
	}
	if len(result[1].Dependencies) != 0 {
		t.Errorf("result[1].Dependencies len = %d", len(result[1].Dependencies))
	}
}

func TestFleetDBBackend_IssuesToBdIssues_Empty(t *testing.T) {
	result := issuesToBdIssues([]*types.Issue{})
	if result == nil {
		t.Error("expected non-nil slice")
	}
	if len(result) != 0 {
		t.Errorf("expected 0 issues, got %d", len(result))
	}
}

func TestFleetDBBackend_ErrorPropagation(t *testing.T) {
	// Verify all typed methods propagate RPC errors.
	mock := &mockFleetDBClient{
		readyFn:   func(_ *rpc.ReadyArgs) (*rpc.Response, error) { return nil, fmt.Errorf("fail") },
		listFn:    func(_ *rpc.ListArgs) (*rpc.Response, error) { return nil, fmt.Errorf("fail") },
		blockedFn: func(_ *rpc.BlockedArgs) (*rpc.Response, error) { return nil, fmt.Errorf("fail") },
		statsFn:   func() (*rpc.Response, error) { return nil, fmt.Errorf("fail") },
		showFn:    func(_ *rpc.ShowArgs) (*rpc.Response, error) { return nil, fmt.Errorf("fail") },
		updateFn:  func(_ *rpc.UpdateArgs) (*rpc.Response, error) { return nil, fmt.Errorf("fail") },
		closeFn:   func(_ *rpc.CloseArgs) (*rpc.Response, error) { return nil, fmt.Errorf("fail") },
	}

	b := newFleetDBBackend(mock, "test")
	ctx := context.Background()

	tests := []struct {
		name string
		fn   func() error
	}{
		{"Ready", func() error { _, e := b.Ready(ctx, ReadyOpts{}); return e }},
		{"List", func() error { _, e := b.List(ctx, ListOpts{}); return e }},
		{"Blocked", func() error { _, e := b.Blocked(ctx); return e }},
		{"Stats", func() error { _, e := b.Stats(ctx); return e }},
		{"GetIssue", func() error { _, e := b.GetIssue(ctx, "X"); return e }},
		{"GetIssueText", func() error { _, e := b.GetIssueText(ctx, "X"); return e }},
		{"UpdateIssue", func() error { return b.UpdateIssue(ctx, "X", UpdateOpts{Status: "open"}) }},
		{"UpdateExternalRef", func() error { return b.UpdateExternalRef(ctx, "X", "ref") }},
		{"CloseIssue", func() error { return b.CloseIssue(ctx, "X", "reason") }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fn()
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
