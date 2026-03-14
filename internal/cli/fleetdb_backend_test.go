package cli

import (
	"encoding/json"
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
	return nil, nil
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
	return nil, nil
}

func (m *mockFleetDBClient) Update(args *rpc.UpdateArgs) (*rpc.Response, error) {
	if m.updateFn != nil {
		return m.updateFn(args)
	}
	return nil, nil
}

func (m *mockFleetDBClient) CloseIssue(args *rpc.CloseArgs) (*rpc.Response, error) {
	if m.closeFn != nil {
		return m.closeFn(args)
	}
	return nil, nil
}

// helper: marshal data into a successful Response
func successResp(data interface{}) *rpc.Response {
	raw, _ := json.Marshal(data)
	return &rpc.Response{Success: true, Data: raw}
}

func TestFleetDBBackend_RunCommand_NoArgs(t *testing.T) {
	b := newFleetDBBackend(&mockFleetDBClient{}, "test")
	_, err := b.RunCommand("/some/dir")
	if err == nil || err.Error() != "no command specified" {
		t.Fatalf("expected 'no command specified', got: %v", err)
	}
}

func TestFleetDBBackend_RunCommand_InvalidCommand(t *testing.T) {
	b := newFleetDBBackend(&mockFleetDBClient{}, "test")
	_, err := b.RunCommand("/some/dir", "foobar")
	if err == nil || err.Error() != "unknown command: foobar" {
		t.Fatalf("expected 'unknown command: foobar', got: %v", err)
	}
}

func TestFleetDBBackend_RunCommand_DirIgnored(t *testing.T) {
	// Verify dir parameter does not affect behavior — same result for different dirs.
	mock := &mockFleetDBClient{
		readyFn: func(_ *rpc.ReadyArgs) (*rpc.Response, error) {
			return successResp([]*types.Issue{}), nil
		},
	}
	b := newFleetDBBackend(mock, "test")

	out1, err := b.RunCommand("/dir/one", "ready", "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out2, err := b.RunCommand("/dir/two", "ready", "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out1 != out2 {
		t.Fatalf("expected same output for different dirs, got %q vs %q", out1, out2)
	}
}

func TestFleetDBBackend_RunCommand_Ready(t *testing.T) {
	createdAt := time.Date(2026, 3, 14, 12, 0, 0, 0, time.UTC)
	mock := &mockFleetDBClient{
		readyFn: func(args *rpc.ReadyArgs) (*rpc.Response, error) {
			if args.Limit != 50 {
				t.Errorf("expected limit 50, got %d", args.Limit)
			}
			issues := []*types.Issue{
				{
					ID:        "test-1",
					Title:     "Test Issue",
					Status:    types.StatusOpen,
					Priority:  1,
					IssueType: types.TypeTask,
					Labels:    []string{"backend"},
					Dependencies: []*types.Dependency{
						{
							IssueID:     "test-1",
							DependsOnID: "test-0",
							Type:        types.DepBlocks,
							CreatedAt:   createdAt,
							CreatedBy:   "admin",
						},
					},
				},
			}
			return successResp(issues), nil
		},
	}

	b := newFleetDBBackend(mock, "test")
	out, err := b.RunCommand("", "ready", "--json", "--limit", "50")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result []BdIssue
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(result))
	}
	issue := result[0]
	if issue.ID != "test-1" {
		t.Errorf("expected ID 'test-1', got %q", issue.ID)
	}
	if issue.IssueType != "task" {
		t.Errorf("expected issue_type 'task', got %q", issue.IssueType)
	}
	if len(issue.Labels) != 1 || issue.Labels[0] != "backend" {
		t.Errorf("expected labels [backend], got %v", issue.Labels)
	}
	if len(issue.Dependencies) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(issue.Dependencies))
	}
	dep := issue.Dependencies[0]
	if dep.Type != "blocks" {
		t.Errorf("expected dep type 'blocks', got %q", dep.Type)
	}
	// CRITICAL: verify time.Time -> RFC3339 string conversion
	expectedTime := createdAt.Format(time.RFC3339)
	if dep.CreatedAt != expectedTime {
		t.Errorf("expected CreatedAt %q, got %q", expectedTime, dep.CreatedAt)
	}
}

func TestFleetDBBackend_RunCommand_List(t *testing.T) {
	mock := &mockFleetDBClient{
		listFn: func(args *rpc.ListArgs) (*rpc.Response, error) {
			if args.Status != "in_progress" {
				t.Errorf("expected status 'in_progress', got %q", args.Status)
			}
			if args.Assignee != "drift" {
				t.Errorf("expected assignee 'drift', got %q", args.Assignee)
			}
			return successResp([]*types.Issue{}), nil
		},
	}

	b := newFleetDBBackend(mock, "test")
	out, err := b.RunCommand("", "list", "--status", "in_progress", "--assignee", "drift", "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "[]" {
		t.Errorf("expected '[]', got %q", out)
	}
}

func TestFleetDBBackend_RunCommand_Show(t *testing.T) {
	mock := &mockFleetDBClient{
		showFn: func(args *rpc.ShowArgs) (*rpc.Response, error) {
			if args.ID != "test-42" {
				t.Errorf("expected ID 'test-42', got %q", args.ID)
			}
			details := types.IssueDetails{
				Issue: types.Issue{
					ID:        "test-42",
					Title:     "Show Test",
					Status:    types.StatusOpen,
					Priority:  2,
					IssueType: types.TypeBug,
					CreatedAt: time.Now(),
				},
				Labels: []string{"urgent"},
				Dependencies: []*types.IssueWithDependencyMetadata{
					{
						Issue: types.Issue{
							ID:        "test-41",
							Title:     "Dep Issue",
							CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
							CreatedBy: "user1",
						},
						DependencyType: types.DepParentChild,
					},
				},
			}
			return successResp(details), nil
		},
	}

	b := newFleetDBBackend(mock, "test")
	out, err := b.RunCommand("", "show", "test-42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result BdIssue
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}
	if result.ID != "test-42" {
		t.Errorf("expected ID 'test-42', got %q", result.ID)
	}
	if result.IssueType != "bug" {
		t.Errorf("expected issue_type 'bug', got %q", result.IssueType)
	}
	if len(result.Labels) != 1 || result.Labels[0] != "urgent" {
		t.Errorf("expected labels [urgent], got %v", result.Labels)
	}
	if len(result.Dependencies) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(result.Dependencies))
	}
	if result.Dependencies[0].Type != "parent-child" {
		t.Errorf("expected dep type 'parent-child', got %q", result.Dependencies[0].Type)
	}
}

func TestFleetDBBackend_RunCommand_Show_NoID(t *testing.T) {
	b := newFleetDBBackend(&mockFleetDBClient{}, "test")
	_, err := b.RunCommand("", "show")
	if err == nil || err.Error() != "show requires an issue ID" {
		t.Fatalf("expected 'show requires an issue ID', got: %v", err)
	}
}

func TestFleetDBBackend_RunCommand_Update(t *testing.T) {
	mock := &mockFleetDBClient{
		updateFn: func(args *rpc.UpdateArgs) (*rpc.Response, error) {
			if args.ID != "test-1" {
				t.Errorf("expected ID 'test-1', got %q", args.ID)
			}
			if args.Status == nil || *args.Status != "in_progress" {
				t.Errorf("expected status 'in_progress', got %v", args.Status)
			}
			if args.Assignee == nil || *args.Assignee != "drift" {
				t.Errorf("expected assignee 'drift', got %v", args.Assignee)
			}
			if !args.Claim {
				t.Error("expected Claim to be true")
			}
			issue := types.Issue{
				ID:        "test-1",
				Title:     "Updated",
				Status:    types.StatusInProgress,
				IssueType: types.TypeTask,
				Assignee:  "drift",
			}
			return successResp(issue), nil
		},
	}

	b := newFleetDBBackend(mock, "test")
	out, err := b.RunCommand("", "update", "test-1", "--status", "in_progress", "--assignee", "drift", "--claim")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result BdIssue
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if result.Assignee != "drift" {
		t.Errorf("expected assignee 'drift', got %q", result.Assignee)
	}
}

func TestFleetDBBackend_RunCommand_Update_NoID(t *testing.T) {
	b := newFleetDBBackend(&mockFleetDBClient{}, "test")
	_, err := b.RunCommand("", "update")
	if err == nil || err.Error() != "update requires an issue ID" {
		t.Fatalf("expected 'update requires an issue ID', got: %v", err)
	}
}

func TestFleetDBBackend_RunCommand_Close(t *testing.T) {
	mock := &mockFleetDBClient{
		closeFn: func(args *rpc.CloseArgs) (*rpc.Response, error) {
			if args.ID != "test-1" {
				t.Errorf("expected ID 'test-1', got %q", args.ID)
			}
			if args.Reason != "done" {
				t.Errorf("expected reason 'done', got %q", args.Reason)
			}
			issue := types.Issue{
				ID:     "test-1",
				Title:  "Closed",
				Status: types.StatusClosed,
			}
			return successResp(issue), nil
		},
	}

	b := newFleetDBBackend(mock, "test")
	_, err := b.RunCommand("", "close", "test-1", "--reason", "done")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFleetDBBackend_RunCommand_Close_NoID(t *testing.T) {
	b := newFleetDBBackend(&mockFleetDBClient{}, "test")
	_, err := b.RunCommand("", "close")
	if err == nil || err.Error() != "close requires an issue ID" {
		t.Fatalf("expected 'close requires an issue ID', got: %v", err)
	}
}

func TestFleetDBBackend_RunCommand_Stats(t *testing.T) {
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
	out, err := b.RunCommand("", "stats", "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result BdStats
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if result.Summary.TotalIssues != 100 {
		t.Errorf("expected total 100, got %d", result.Summary.TotalIssues)
	}
	if result.Summary.OpenIssues != 40 {
		t.Errorf("expected open 40, got %d", result.Summary.OpenIssues)
	}
	if result.Summary.BlockedIssues != 5 {
		t.Errorf("expected blocked 5, got %d", result.Summary.BlockedIssues)
	}
	if result.Summary.PinnedIssues != 5 {
		t.Errorf("expected pinned 5, got %d", result.Summary.PinnedIssues)
	}
}

func TestFleetDBBackend_RunCommand_Sync(t *testing.T) {
	// Sync is a noop — verify no RPC call is made and empty string returned.
	mock := &mockFleetDBClient{}
	b := newFleetDBBackend(mock, "test")
	out, err := b.RunCommand("", "sync")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "" {
		t.Errorf("expected empty string, got %q", out)
	}
}

func TestFleetDBBackend_RPCError(t *testing.T) {
	mock := &mockFleetDBClient{
		readyFn: func(_ *rpc.ReadyArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: false, Error: "database unavailable"}, nil
		},
	}

	b := newFleetDBBackend(mock, "test")
	_, err := b.RunCommand("", "ready", "--json")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "ready: database unavailable" {
		t.Errorf("expected error 'ready: database unavailable', got: %v", err)
	}
}

func TestFleetDBBackend_EmptyResults(t *testing.T) {
	// Verify empty list returns "[]" not "null".
	mock := &mockFleetDBClient{
		readyFn: func(_ *rpc.ReadyArgs) (*rpc.Response, error) {
			return successResp([]*types.Issue{}), nil
		},
	}

	b := newFleetDBBackend(mock, "test")
	out, err := b.RunCommand("", "ready", "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "[]" {
		t.Errorf("expected '[]', got %q", out)
	}
}

func TestFleetDBBackend_NilDependencies(t *testing.T) {
	// Nil deps on Issue -> empty JSON array.
	mock := &mockFleetDBClient{
		readyFn: func(_ *rpc.ReadyArgs) (*rpc.Response, error) {
			issues := []*types.Issue{
				{
					ID:           "test-1",
					Title:        "No Deps",
					Status:       types.StatusOpen,
					IssueType:    types.TypeTask,
					Dependencies: nil,
				},
			}
			return successResp(issues), nil
		},
	}

	b := newFleetDBBackend(mock, "test")
	out, err := b.RunCommand("", "ready", "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result []BdIssue
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if result[0].Dependencies == nil {
		t.Error("expected non-nil dependencies slice")
	}
	if len(result[0].Dependencies) != 0 {
		t.Errorf("expected 0 dependencies, got %d", len(result[0].Dependencies))
	}
}

func TestFleetDBBackend_NilLabels(t *testing.T) {
	// Nil labels on Issue -> empty JSON array.
	mock := &mockFleetDBClient{
		readyFn: func(_ *rpc.ReadyArgs) (*rpc.Response, error) {
			issues := []*types.Issue{
				{
					ID:        "test-1",
					Title:     "No Labels",
					Status:    types.StatusOpen,
					IssueType: types.TypeTask,
					Labels:    nil,
				},
			}
			return successResp(issues), nil
		},
	}

	b := newFleetDBBackend(mock, "test")
	out, err := b.RunCommand("", "ready", "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result []BdIssue
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if result[0].Labels == nil {
		t.Error("expected non-nil labels slice")
	}
	if len(result[0].Labels) != 0 {
		t.Errorf("expected 0 labels, got %d", len(result[0].Labels))
	}
}

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

func TestFleetDBBackend_ParseArgs(t *testing.T) {
	tests := []struct {
		name               string
		args               []string
		expectedFlags      map[string]string
		expectedPositional []string
	}{
		{
			name:               "flag=value",
			args:               []string{"--limit=50"},
			expectedFlags:      map[string]string{"limit": "50"},
			expectedPositional: nil,
		},
		{
			name:               "flag value",
			args:               []string{"--status", "open"},
			expectedFlags:      map[string]string{"status": "open"},
			expectedPositional: nil,
		},
		{
			name:               "bare flag",
			args:               []string{"--json"},
			expectedFlags:      map[string]string{"json": "true"},
			expectedPositional: nil,
		},
		{
			name:               "positional args",
			args:               []string{"test-1"},
			expectedFlags:      map[string]string{},
			expectedPositional: []string{"test-1"},
		},
		{
			name:               "mixed args",
			args:               []string{"test-1", "--status", "open", "--json"},
			expectedFlags:      map[string]string{"status": "open", "json": "true"},
			expectedPositional: []string{"test-1"},
		},
		{
			name:               "empty value with equals",
			args:               []string{"--assignee="},
			expectedFlags:      map[string]string{"assignee": ""},
			expectedPositional: nil,
		},
		{
			name:               "no args",
			args:               []string{},
			expectedFlags:      map[string]string{},
			expectedPositional: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flags, positional := parseArgs(tt.args)
			for k, v := range tt.expectedFlags {
				if got, ok := flags[k]; !ok || got != v {
					t.Errorf("expected flag %q=%q, got %q (exists=%v)", k, v, got, ok)
				}
			}
			if len(flags) != len(tt.expectedFlags) {
				t.Errorf("expected %d flags, got %d: %v", len(tt.expectedFlags), len(flags), flags)
			}
			if len(positional) != len(tt.expectedPositional) {
				t.Errorf("expected %d positional, got %d: %v", len(tt.expectedPositional), len(positional), positional)
			}
			for i, v := range tt.expectedPositional {
				if i < len(positional) && positional[i] != v {
					t.Errorf("positional[%d]: expected %q, got %q", i, v, positional[i])
				}
			}
		})
	}
}

// jsonContainsField checks if a JSON string contains a specific field name
func jsonContainsField(jsonStr, field string) bool {
	return strings.Contains(jsonStr, `"`+field+`"`)
}
