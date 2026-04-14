package issues

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func newHandlersMockPool(t *testing.T, socketPath string) daemon.Pool {
	t.Helper()
	pool, err := daemon.NewConnectionPool(socketPath, 2)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	pool.SetDialTimeout(2 * time.Second)
	pool.SetPoolTimeout(2 * time.Second)
	t.Cleanup(func() { pool.Close() })
	return pool
}

// startHandlersMockServer creates a Unix socket mock server for handler tests.
func startHandlersMockServer(t *testing.T, handler func(req rpc.Request) rpc.Response) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "handler-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "bd.sock")

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("failed to create mock server: %v", err)
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				reader := bufio.NewReader(c)
				for {
					line, err := reader.ReadBytes('\n')
					if err != nil {
						return
					}
					var req rpc.Request
					if err := json.Unmarshal(line, &req); err != nil {
						return
					}
					resp := handler(req)
					respJSON, _ := json.Marshal(resp)
					respJSON = append(respJSON, '\n')
					c.Write(respJSON)
				}
			}(conn)
		}
	}()

	t.Cleanup(func() { listener.Close() })
	return socketPath
}

func defaultHealthPingHandler(req rpc.Request) (rpc.Response, bool) {
	switch req.Operation {
	case "health":
		hd, _ := json.Marshal(rpc.HealthResponse{Status: "healthy", Version: "0.0.0", Compatible: true})
		return rpc.Response{Success: true, Data: hd}, true
	case "ping":
		return rpc.Response{Success: true}, true
	}
	return rpc.Response{}, false
}

// mockBlockedClient implements blockedClient for testing.
type mockBlockedClient struct {
	blockedFunc func(args *rpc.BlockedArgs) (*rpc.Response, error)
}

func (m *mockBlockedClient) Blocked(args *rpc.BlockedArgs) (*rpc.Response, error) {
	if m.blockedFunc != nil {
		return m.blockedFunc(args)
	}
	return nil, errors.New("blockedFunc not implemented")
}

// mockBlockedPool implements blockedConnectionGetter for testing.
type mockBlockedPool struct {
	getFunc func(ctx context.Context) (blockedClient, error)
	putFunc func(client blockedClient)
}

func (m *mockBlockedPool) Get(ctx context.Context) (blockedClient, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx)
	}
	return nil, errors.New("getFunc not implemented")
}

func (m *mockBlockedPool) Put(client blockedClient) {
	if m.putFunc != nil {
		m.putFunc(client)
	}
}

// mockGraphClient implements graphClient for testing.
type mockGraphClient struct {
	getGraphDataFunc func(args *rpc.GetGraphDataArgs) (*rpc.GetGraphDataResponse, error)
}

func (m *mockGraphClient) GetGraphData(args *rpc.GetGraphDataArgs) (*rpc.GetGraphDataResponse, error) {
	if m.getGraphDataFunc != nil {
		return m.getGraphDataFunc(args)
	}
	return nil, errors.New("getGraphDataFunc not implemented")
}

// mockGraphPool implements graphConnectionGetter for testing.
type mockGraphPool struct {
	getFunc func(ctx context.Context) (graphClient, error)
	putFunc func(client graphClient)
}

func (m *mockGraphPool) Get(ctx context.Context) (graphClient, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx)
	}
	return nil, errors.New("getFunc not implemented")
}

func (m *mockGraphPool) Put(client graphClient) {
	if m.putFunc != nil {
		m.putFunc(client)
	}
}

// mockReadyClient implements readyClient for testing.
type mockReadyClient struct {
	readyFunc    func(args *rpc.ReadyArgs) (*rpc.Response, error)
	listFunc     func(args *rpc.ListArgs) (*rpc.Response, error)
	getParentIDs func(args *rpc.GetParentIDsArgs) (*rpc.Response, error)
}

func (m *mockReadyClient) Ready(args *rpc.ReadyArgs) (*rpc.Response, error) {
	if m.readyFunc != nil {
		return m.readyFunc(args)
	}
	return nil, errors.New("readyFunc not implemented")
}

func (m *mockReadyClient) List(args *rpc.ListArgs) (*rpc.Response, error) {
	if m.listFunc != nil {
		return m.listFunc(args)
	}
	return nil, errors.New("listFunc not implemented")
}

func (m *mockReadyClient) GetParentIDs(args *rpc.GetParentIDsArgs) (*rpc.Response, error) {
	if m.getParentIDs != nil {
		return m.getParentIDs(args)
	}
	return nil, errors.New("getParentIDs not implemented")
}

// mockReadyPool implements readyConnectionGetter for testing.
type mockReadyPool struct {
	getFunc     func(ctx context.Context) (readyClient, error)
	putFunc     func(client readyClient)
	discardFunc func(client readyClient)
}

func (m *mockReadyPool) Get(ctx context.Context) (readyClient, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx)
	}
	return nil, errors.New("getFunc not implemented")
}

func (m *mockReadyPool) Put(client readyClient) {
	if m.putFunc != nil {
		m.putFunc(client)
	}
}

func (m *mockReadyPool) Discard(client readyClient) {
	if m.discardFunc != nil {
		m.discardFunc(client)
	}
}

// Tests for parseListParams and handleListIssues from feature/web-ui branch

func TestParseListParams(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		wantFunc func(t *testing.T, got interface{})
	}{
		{
			name: "empty query returns empty args",
			url:  "/api/issues",
			wantFunc: func(t *testing.T, got interface{}) {
				args := got.(testListArgs)
				if args.Status != "" {
					t.Errorf("expected empty status, got %q", args.Status)
				}
				if args.Priority != nil {
					t.Errorf("expected nil priority, got %v", *args.Priority)
				}
			},
		},
		{
			name: "status filter",
			url:  "/api/issues?status=open",
			wantFunc: func(t *testing.T, got interface{}) {
				args := got.(testListArgs)
				if args.Status != "open" {
					t.Errorf("expected status=open, got %q", args.Status)
				}
			},
		},
		{
			name: "priority filter",
			url:  "/api/issues?priority=1",
			wantFunc: func(t *testing.T, got interface{}) {
				args := got.(testListArgs)
				if args.Priority == nil || *args.Priority != 1 {
					t.Errorf("expected priority=1, got %v", args.Priority)
				}
			},
		},
		{
			name: "invalid priority is ignored",
			url:  "/api/issues?priority=invalid",
			wantFunc: func(t *testing.T, got interface{}) {
				args := got.(testListArgs)
				if args.Priority != nil {
					t.Errorf("expected nil priority for invalid value, got %v", *args.Priority)
				}
			},
		},
		{
			name: "type filter",
			url:  "/api/issues?type=task",
			wantFunc: func(t *testing.T, got interface{}) {
				args := got.(testListArgs)
				if args.IssueType != "task" {
					t.Errorf("expected type=task, got %q", args.IssueType)
				}
			},
		},
		{
			name: "assignee filter",
			url:  "/api/issues?assignee=tyson",
			wantFunc: func(t *testing.T, got interface{}) {
				args := got.(testListArgs)
				if args.Assignee != "tyson" {
					t.Errorf("expected assignee=tyson, got %q", args.Assignee)
				}
			},
		},
		{
			name: "labels filter",
			url:  "/api/issues?labels=phase-2,urgent",
			wantFunc: func(t *testing.T, got interface{}) {
				args := got.(testListArgs)
				if len(args.Labels) != 2 || args.Labels[0] != "phase-2" || args.Labels[1] != "urgent" {
					t.Errorf("expected labels=[phase-2,urgent], got %v", args.Labels)
				}
			},
		},
		{
			name: "labels with spaces are trimmed",
			url:  "/api/issues?labels=phase-2%2C%20urgent%20%2C%20important",
			wantFunc: func(t *testing.T, got interface{}) {
				args := got.(testListArgs)
				if len(args.Labels) != 3 {
					t.Errorf("expected 3 labels, got %d", len(args.Labels))
				}
				for _, l := range args.Labels {
					if l != "phase-2" && l != "urgent" && l != "important" {
						t.Errorf("unexpected label %q", l)
					}
				}
			},
		},
		{
			name: "limit filter",
			url:  "/api/issues?limit=50",
			wantFunc: func(t *testing.T, got interface{}) {
				args := got.(testListArgs)
				if args.Limit != 50 {
					t.Errorf("expected limit=50, got %d", args.Limit)
				}
			},
		},
		{
			name: "negative limit is ignored",
			url:  "/api/issues?limit=-1",
			wantFunc: func(t *testing.T, got interface{}) {
				args := got.(testListArgs)
				if args.Limit != 0 {
					t.Errorf("expected limit=0 for negative value, got %d", args.Limit)
				}
			},
		},
		{
			name: "excessive limit is capped at MaxListLimit",
			url:  "/api/issues?limit=999999999",
			wantFunc: func(t *testing.T, got interface{}) {
				args := got.(testListArgs)
				if args.Limit != MaxListLimit {
					t.Errorf("expected limit=%d for excessive value, got %d", MaxListLimit, args.Limit)
				}
			},
		},
		{
			name: "query filter",
			url:  "/api/issues?q=search+term",
			wantFunc: func(t *testing.T, got interface{}) {
				args := got.(testListArgs)
				if args.Query != "search term" {
					t.Errorf("expected q='search term', got %q", args.Query)
				}
			},
		},
		{
			name: "title_contains filter",
			url:  "/api/issues?title_contains=bug",
			wantFunc: func(t *testing.T, got interface{}) {
				args := got.(testListArgs)
				if args.TitleContains != "bug" {
					t.Errorf("expected title_contains=bug, got %q", args.TitleContains)
				}
			},
		},
		{
			name: "pinned filter true",
			url:  "/api/issues?pinned=true",
			wantFunc: func(t *testing.T, got interface{}) {
				args := got.(testListArgs)
				if args.Pinned == nil || !*args.Pinned {
					t.Errorf("expected pinned=true, got %v", args.Pinned)
				}
			},
		},
		{
			name: "pinned filter false",
			url:  "/api/issues?pinned=false",
			wantFunc: func(t *testing.T, got interface{}) {
				args := got.(testListArgs)
				if args.Pinned == nil || *args.Pinned {
					t.Errorf("expected pinned=false, got %v", args.Pinned)
				}
			},
		},
		{
			name: "empty_description filter",
			url:  "/api/issues?empty_description=true",
			wantFunc: func(t *testing.T, got interface{}) {
				args := got.(testListArgs)
				if !args.EmptyDescription {
					t.Errorf("expected empty_description=true")
				}
			},
		},
		{
			name: "no_assignee filter",
			url:  "/api/issues?no_assignee=true",
			wantFunc: func(t *testing.T, got interface{}) {
				args := got.(testListArgs)
				if !args.NoAssignee {
					t.Errorf("expected no_assignee=true")
				}
			},
		},
		{
			name: "multiple filters combined",
			url:  "/api/issues?status=open&priority=1&type=task&limit=10",
			wantFunc: func(t *testing.T, got interface{}) {
				args := got.(testListArgs)
				if args.Status != "open" {
					t.Errorf("expected status=open, got %q", args.Status)
				}
				if args.Priority == nil || *args.Priority != 1 {
					t.Errorf("expected priority=1, got %v", args.Priority)
				}
				if args.IssueType != "task" {
					t.Errorf("expected type=task, got %q", args.IssueType)
				}
				if args.Limit != 10 {
					t.Errorf("expected limit=10, got %d", args.Limit)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.url, nil)
			args, err := parseListParams(req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// Convert to testListArgs for comparison
			testArgs := testListArgs{
				Status:           args.Status,
				Priority:         args.Priority,
				IssueType:        args.IssueType,
				Assignee:         args.Assignee,
				Labels:           args.Labels,
				Limit:            args.Limit,
				Query:            args.Query,
				TitleContains:    args.TitleContains,
				EmptyDescription: args.EmptyDescription,
				NoAssignee:       args.NoAssignee,
				Pinned:           args.Pinned,
			}
			tt.wantFunc(t, testArgs)
		})
	}
}

// testListArgs is a simplified version of rpc.ListArgs for testing.
type testListArgs struct {
	Status           string
	Priority         *int
	IssueType        string
	Assignee         string
	Labels           []string
	Limit            int
	Query            string
	TitleContains    string
	EmptyDescription bool
	NoAssignee       bool
	Pinned           *bool
}

func TestIssuesResponseJSON(t *testing.T) {
	// Test success response structure
	t.Run("success response", func(t *testing.T) {
		resp := IssuesResponse{
			Success: true,
			Data:    json.RawMessage(`[{"id":"test-1","title":"Test Issue"}]`),
		}
		bytes, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}

		var decoded map[string]interface{}
		if err := json.Unmarshal(bytes, &decoded); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		if decoded["success"] != true {
			t.Errorf("expected success=true")
		}
		if decoded["data"] == nil {
			t.Errorf("expected data to be present")
		}
		if _, hasError := decoded["error"]; hasError {
			// error should be omitted when empty
			if decoded["error"] != "" {
				t.Errorf("expected error to be omitted or empty")
			}
		}
	})

	// Test error response structure
	t.Run("error response", func(t *testing.T) {
		resp := IssuesResponse{
			Success: false,
			Error:   "connection failed",
			Code:    "DAEMON_UNAVAILABLE",
		}
		bytes, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}

		var decoded map[string]interface{}
		if err := json.Unmarshal(bytes, &decoded); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		if decoded["success"] != false {
			t.Errorf("expected success=false")
		}
		if decoded["error"] != "connection failed" {
			t.Errorf("expected error='connection failed', got %v", decoded["error"])
		}
		if decoded["code"] != "DAEMON_UNAVAILABLE" {
			t.Errorf("expected code='DAEMON_UNAVAILABLE', got %v", decoded["code"])
		}
	})
}

// ===========================================================================
// splitAndTrim tests (from webui/nova branch)
// ===========================================================================

func TestSplitAndTrim(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "empty string returns nil",
			input:    "",
			expected: nil,
		},
		{
			name:     "single value",
			input:    "bug",
			expected: []string{"bug"},
		},
		{
			name:     "multiple values with commas",
			input:    "bug,feature,enhancement",
			expected: []string{"bug", "feature", "enhancement"},
		},
		{
			name:     "values with whitespace",
			input:    "  bug , feature  ,  enhancement  ",
			expected: []string{"bug", "feature", "enhancement"},
		},
		{
			name:     "empty values are removed",
			input:    "bug,,feature,,,enhancement",
			expected: []string{"bug", "feature", "enhancement"},
		},
		{
			name:     "only whitespace values are removed",
			input:    "bug,  ,feature,   ,enhancement",
			expected: []string{"bug", "feature", "enhancement"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := splitAndTrim(tt.input)

			if tt.expected == nil {
				if result != nil {
					t.Errorf("splitAndTrim(%q) = %v, want nil", tt.input, result)
				}
				return
			}

			if len(result) != len(tt.expected) {
				t.Errorf("splitAndTrim(%q) len = %d, want %d", tt.input, len(result), len(tt.expected))
				return
			}

			for i, v := range result {
				if v != tt.expected[i] {
					t.Errorf("splitAndTrim(%q)[%d] = %q, want %q", tt.input, i, v, tt.expected[i])
				}
			}
		})
	}
}

// ===========================================================================
// parseReadyParams tests (from webui/nova branch)
// ===========================================================================

func TestParseReadyParams_EmptyQuery(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/ready", nil)

	args, err := parseReadyParams(req)
	if err != nil {
		t.Errorf("parseReadyParams() unexpected error: %v", err)
	}

	// Verify default values
	if args.Assignee != "" {
		t.Errorf("Assignee = %q, want empty", args.Assignee)
	}
	if args.Unassigned {
		t.Error("Unassigned = true, want false")
	}
	if args.Priority != nil {
		t.Errorf("Priority = %v, want nil", args.Priority)
	}
	if args.Type != "" {
		t.Errorf("Type = %q, want empty", args.Type)
	}
	if args.Limit != 0 {
		t.Errorf("Limit = %d, want 0", args.Limit)
	}
	if args.SortPolicy != "" {
		t.Errorf("SortPolicy = %q, want empty", args.SortPolicy)
	}
	if args.Labels != nil {
		t.Errorf("Labels = %v, want nil", args.Labels)
	}
	if args.LabelsAny != nil {
		t.Errorf("LabelsAny = %v, want nil", args.LabelsAny)
	}
	if args.ParentID != "" {
		t.Errorf("ParentID = %q, want empty", args.ParentID)
	}
	if args.MolType != "" {
		t.Errorf("MolType = %q, want empty", args.MolType)
	}
	if args.IncludeDeferred {
		t.Error("IncludeDeferred = true, want false")
	}
}

func TestParseReadyParams_Assignee(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/ready?assignee=alice", nil)

	args, err := parseReadyParams(req)
	if err != nil {
		t.Errorf("parseReadyParams() unexpected error: %v", err)
	}

	if args.Assignee != "alice" {
		t.Errorf("Assignee = %q, want %q", args.Assignee, "alice")
	}
}

func TestParseReadyParams_Priority(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		wantVal   *int
		wantErr   bool
		errSubstr string
	}{
		{
			name:    "valid priority 0",
			query:   "priority=0",
			wantVal: intPtr(0),
			wantErr: false,
		},
		{
			name:    "valid priority 2",
			query:   "priority=2",
			wantVal: intPtr(2),
			wantErr: false,
		},
		{
			name:    "valid priority 4",
			query:   "priority=4",
			wantVal: intPtr(4),
			wantErr: false,
		},
		{
			name:      "invalid priority not a number",
			query:     "priority=high",
			wantErr:   true,
			errSubstr: "invalid priority value",
		},
		{
			name:      "invalid priority negative",
			query:     "priority=-1",
			wantErr:   true,
			errSubstr: "priority must be between 0 and 4",
		},
		{
			name:      "invalid priority too high",
			query:     "priority=5",
			wantErr:   true,
			errSubstr: "priority must be between 0 and 4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/ready?"+tt.query, nil)

			args, err := parseReadyParams(req)

			if tt.wantErr {
				if err == nil {
					t.Error("parseReadyParams() expected error, got nil")
					return
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("error = %q, want to contain %q", err.Error(), tt.errSubstr)
				}
				return
			}

			if err != nil {
				t.Errorf("parseReadyParams() unexpected error: %v", err)
				return
			}

			if tt.wantVal == nil {
				if args.Priority != nil {
					t.Errorf("Priority = %v, want nil", args.Priority)
				}
			} else {
				if args.Priority == nil {
					t.Errorf("Priority = nil, want %d", *tt.wantVal)
				} else if *args.Priority != *tt.wantVal {
					t.Errorf("Priority = %d, want %d", *args.Priority, *tt.wantVal)
				}
			}
		})
	}
}

func TestParseReadyParams_Limit(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		wantVal   int
		wantErr   bool
		errSubstr string
	}{
		{
			name:    "valid limit 0",
			query:   "limit=0",
			wantVal: 0,
			wantErr: false,
		},
		{
			name:    "valid limit 10",
			query:   "limit=10",
			wantVal: 10,
			wantErr: false,
		},
		{
			name:    "valid limit 100",
			query:   "limit=100",
			wantVal: 100,
			wantErr: false,
		},
		{
			name:      "invalid limit not a number",
			query:     "limit=ten",
			wantErr:   true,
			errSubstr: "invalid limit value",
		},
		{
			name:      "invalid limit negative",
			query:     "limit=-5",
			wantErr:   true,
			errSubstr: "limit must be non-negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/ready?"+tt.query, nil)

			args, err := parseReadyParams(req)

			if tt.wantErr {
				if err == nil {
					t.Error("parseReadyParams() expected error, got nil")
					return
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("error = %q, want to contain %q", err.Error(), tt.errSubstr)
				}
				return
			}

			if err != nil {
				t.Errorf("parseReadyParams() unexpected error: %v", err)
				return
			}

			if args.Limit != tt.wantVal {
				t.Errorf("Limit = %d, want %d", args.Limit, tt.wantVal)
			}
		})
	}
}

func TestParseReadyParams_SortPolicy(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		wantVal   string
		wantErr   bool
		errSubstr string
	}{
		{
			name:    "valid sort policy hybrid",
			query:   "sort=hybrid",
			wantVal: "hybrid",
			wantErr: false,
		},
		{
			name:    "valid sort policy priority",
			query:   "sort=priority",
			wantVal: "priority",
			wantErr: false,
		},
		{
			name:    "valid sort policy oldest",
			query:   "sort=oldest",
			wantVal: "oldest",
			wantErr: false,
		},
		{
			name:      "invalid sort policy",
			query:     "sort=newest",
			wantErr:   true,
			errSubstr: "invalid sort policy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/ready?"+tt.query, nil)

			args, err := parseReadyParams(req)

			if tt.wantErr {
				if err == nil {
					t.Error("parseReadyParams() expected error, got nil")
					return
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("error = %q, want to contain %q", err.Error(), tt.errSubstr)
				}
				return
			}

			if err != nil {
				t.Errorf("parseReadyParams() unexpected error: %v", err)
				return
			}

			if args.SortPolicy != tt.wantVal {
				t.Errorf("SortPolicy = %q, want %q", args.SortPolicy, tt.wantVal)
			}
		})
	}
}

func TestParseReadyParams_Labels(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		wantVal []string
	}{
		{
			name:    "single label",
			query:   "labels=bug",
			wantVal: []string{"bug"},
		},
		{
			name:    "multiple labels comma-separated",
			query:   "labels=bug,feature,urgent",
			wantVal: []string{"bug", "feature", "urgent"},
		},
		{
			name:    "labels with whitespace",
			query:   "labels=" + url.QueryEscape("bug , feature , urgent"),
			wantVal: []string{"bug", "feature", "urgent"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/ready?"+tt.query, nil)

			args, err := parseReadyParams(req)
			if err != nil {
				t.Errorf("parseReadyParams() unexpected error: %v", err)
				return
			}

			if len(args.Labels) != len(tt.wantVal) {
				t.Errorf("Labels len = %d, want %d", len(args.Labels), len(tt.wantVal))
				return
			}

			for i, v := range args.Labels {
				if v != tt.wantVal[i] {
					t.Errorf("Labels[%d] = %q, want %q", i, v, tt.wantVal[i])
				}
			}
		})
	}
}

func TestParseReadyParams_LabelsAny(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		wantVal []string
	}{
		{
			name:    "single label_any",
			query:   "labels_any=bug",
			wantVal: []string{"bug"},
		},
		{
			name:    "multiple labels_any comma-separated",
			query:   "labels_any=bug,feature,urgent",
			wantVal: []string{"bug", "feature", "urgent"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/ready?"+tt.query, nil)

			args, err := parseReadyParams(req)
			if err != nil {
				t.Errorf("parseReadyParams() unexpected error: %v", err)
				return
			}

			if len(args.LabelsAny) != len(tt.wantVal) {
				t.Errorf("LabelsAny len = %d, want %d", len(args.LabelsAny), len(tt.wantVal))
				return
			}

			for i, v := range args.LabelsAny {
				if v != tt.wantVal[i] {
					t.Errorf("LabelsAny[%d] = %q, want %q", i, v, tt.wantVal[i])
				}
			}
		})
	}
}

func TestParseReadyParams_BooleanParams(t *testing.T) {
	tests := []struct {
		name             string
		query            string
		wantUnassigned   bool
		wantIncludeDefer bool
		wantErr          bool
		errSubstr        string
	}{
		{
			name:           "unassigned true",
			query:          "unassigned=true",
			wantUnassigned: true,
		},
		{
			name:           "unassigned false",
			query:          "unassigned=false",
			wantUnassigned: false,
		},
		{
			name:           "unassigned 1 (truthy)",
			query:          "unassigned=1",
			wantUnassigned: true,
		},
		{
			name:      "unassigned invalid",
			query:     "unassigned=yes",
			wantErr:   true,
			errSubstr: "invalid unassigned value",
		},
		{
			name:             "include_deferred true",
			query:            "include_deferred=true",
			wantIncludeDefer: true,
		},
		{
			name:             "include_deferred false",
			query:            "include_deferred=false",
			wantIncludeDefer: false,
		},
		{
			name:      "include_deferred invalid",
			query:     "include_deferred=maybe",
			wantErr:   true,
			errSubstr: "invalid include_deferred value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/ready?"+tt.query, nil)

			args, err := parseReadyParams(req)

			if tt.wantErr {
				if err == nil {
					t.Error("parseReadyParams() expected error, got nil")
					return
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("error = %q, want to contain %q", err.Error(), tt.errSubstr)
				}
				return
			}

			if err != nil {
				t.Errorf("parseReadyParams() unexpected error: %v", err)
				return
			}

			if args.Unassigned != tt.wantUnassigned {
				t.Errorf("Unassigned = %v, want %v", args.Unassigned, tt.wantUnassigned)
			}
			if args.IncludeDeferred != tt.wantIncludeDefer {
				t.Errorf("IncludeDeferred = %v, want %v", args.IncludeDeferred, tt.wantIncludeDefer)
			}
		})
	}
}

func TestParseReadyParams_Type(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/ready?type=bug", nil)

	args, err := parseReadyParams(req)
	if err != nil {
		t.Errorf("parseReadyParams() unexpected error: %v", err)
	}

	if args.Type != "bug" {
		t.Errorf("Type = %q, want %q", args.Type, "bug")
	}
}

func TestParseReadyParams_ParentID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/ready?parent_id=epic-123", nil)

	args, err := parseReadyParams(req)
	if err != nil {
		t.Errorf("parseReadyParams() unexpected error: %v", err)
	}

	if args.ParentID != "epic-123" {
		t.Errorf("ParentID = %q, want %q", args.ParentID, "epic-123")
	}
}

func TestParseReadyParams_MolType(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		wantVal   string
		wantErr   bool
		errSubstr string
	}{
		{
			name:    "valid mol_type swarm",
			query:   "mol_type=swarm",
			wantVal: "swarm",
			wantErr: false,
		},
		{
			name:    "valid mol_type patrol",
			query:   "mol_type=patrol",
			wantVal: "patrol",
			wantErr: false,
		},
		{
			name:    "valid mol_type work",
			query:   "mol_type=work",
			wantVal: "work",
			wantErr: false,
		},
		{
			name:      "invalid mol_type",
			query:     "mol_type=invalid",
			wantErr:   true,
			errSubstr: "invalid mol_type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/ready?"+tt.query, nil)

			args, err := parseReadyParams(req)

			if tt.wantErr {
				if err == nil {
					t.Error("parseReadyParams() expected error, got nil")
					return
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("error = %q, want to contain %q", err.Error(), tt.errSubstr)
				}
				return
			}

			if err != nil {
				t.Errorf("parseReadyParams() unexpected error: %v", err)
				return
			}

			if args.MolType != tt.wantVal {
				t.Errorf("MolType = %q, want %q", args.MolType, tt.wantVal)
			}
		})
	}
}

func TestParseReadyParams_MultipleParams(t *testing.T) {
	query := "assignee=alice&priority=2&limit=10&sort=priority&labels=bug,urgent&unassigned=false&type=task&mol_type=work"
	req := httptest.NewRequest(http.MethodGet, "/api/ready?"+query, nil)

	args, err := parseReadyParams(req)
	if err != nil {
		t.Errorf("parseReadyParams() unexpected error: %v", err)
		return
	}

	if args.Assignee != "alice" {
		t.Errorf("Assignee = %q, want %q", args.Assignee, "alice")
	}
	if args.Priority == nil || *args.Priority != 2 {
		t.Errorf("Priority = %v, want 2", args.Priority)
	}
	if args.Limit != 10 {
		t.Errorf("Limit = %d, want 10", args.Limit)
	}
	if args.SortPolicy != "priority" {
		t.Errorf("SortPolicy = %q, want %q", args.SortPolicy, "priority")
	}
	if len(args.Labels) != 2 || args.Labels[0] != "bug" || args.Labels[1] != "urgent" {
		t.Errorf("Labels = %v, want [bug, urgent]", args.Labels)
	}
	if args.Unassigned {
		t.Error("Unassigned = true, want false")
	}
	if args.Type != "task" {
		t.Errorf("Type = %q, want %q", args.Type, "task")
	}
	if args.MolType != "work" {
		t.Errorf("MolType = %q, want %q", args.MolType, "work")
	}
}

// ===========================================================================
// handleReady tests (from webui/nova branch)
// ===========================================================================

func TestHandleReady_NilPool(t *testing.T) {
	handler := HandleReady(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/ready", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status code = %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}

	var resp ReadyResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Errorf("failed to decode response: %v", err)
		return
	}

	if resp.Success {
		t.Error("Success = true, want false")
	}
	if resp.Error != "connection pool not initialized" {
		t.Errorf("Error = %q, want %q", resp.Error, "connection pool not initialized")
	}
}

func TestHandleReady_ContentType(t *testing.T) {
	handler := HandleReady(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/ready", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	contentType := rr.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
	}
}

// ===========================================================================
// Helper functions
// ===========================================================================

func intPtr(i int) *int {
	return &i
}

// Verify that rpc.ReadyArgs fields match what we expect (compile-time check)
var _ = func() bool {
	args := &rpc.ReadyArgs{
		Assignee:        "",
		Unassigned:      false,
		Priority:        nil,
		Type:            "",
		Limit:           0,
		SortPolicy:      "",
		Labels:          nil,
		LabelsAny:       nil,
		ParentID:        "",
		MolType:         "",
		IncludeDeferred: false,
	}
	_ = args
	return true
}()

// ===========================================================================
// parseBlockedParams tests
// ===========================================================================

func TestParseBlockedParams_EmptyQuery(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/blocked", nil)

	args, err := parseBlockedParams(req)
	if err != nil {
		t.Errorf("parseBlockedParams() unexpected error: %v", err)
	}

	// Verify default values
	if args.ParentID != "" {
		t.Errorf("ParentID = %q, want empty", args.ParentID)
	}
}

func TestParseBlockedParams_ParentID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/blocked?parent_id=epic-123", nil)

	args, err := parseBlockedParams(req)
	if err != nil {
		t.Errorf("parseBlockedParams() unexpected error: %v", err)
	}

	if args.ParentID != "epic-123" {
		t.Errorf("ParentID = %q, want %q", args.ParentID, "epic-123")
	}
}

// ===========================================================================
// handleBlocked tests
// ===========================================================================

func TestHandleBlocked_NilPool(t *testing.T) {
	handler := handleBlocked(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/blocked", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status code = %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}

	var resp BlockedResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Errorf("failed to decode response: %v", err)
		return
	}

	if resp.Success {
		t.Error("Success = true, want false")
	}
	if resp.Error != "connection pool not initialized" {
		t.Errorf("Error = %q, want %q", resp.Error, "connection pool not initialized")
	}
}

func TestHandleBlocked_ContentType(t *testing.T) {
	handler := handleBlocked(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/blocked", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	contentType := rr.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
	}
}

// Verify that rpc.BlockedArgs fields match what we expect (compile-time check)
var _ = func() bool {
	args := &rpc.BlockedArgs{
		ParentID: "",
	}
	_ = args
	return true
}()

// ===========================================================================
// handleGetIssue tests
// ===========================================================================

func TestHandleGetIssue_EmptyID(t *testing.T) {
	svc := &mockIssueService{}
	handler := handleGetIssue(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/issues/", nil)
	req.SetPathValue("id", "")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	var response map[string]string
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if response["error"] != "missing issue ID" {
		t.Errorf("error = %q, want %q", response["error"], "missing issue ID")
	}
}

func TestHandleGetIssue_Success(t *testing.T) {
	issueJSON, _ := json.Marshal(map[string]interface{}{"id": "test-123", "title": "Test Issue"})
	svc := &mockIssueService{
		getIssueFunc: func(ctx context.Context, issueID string) (json.RawMessage, error) {
			if issueID != "test-123" {
				t.Errorf("GetIssue() called with ID = %q, want %q", issueID, "test-123")
			}
			return issueJSON, nil
		},
	}
	handler := handleGetIssue(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/issues/test-123", nil)
	req.SetPathValue("id", "test-123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var envelope IssuesResponse
	if err := json.NewDecoder(w.Body).Decode(&envelope); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !envelope.Success {
		t.Error("expected success = true")
	}
}

func TestHandleGetIssue_NotFound(t *testing.T) {
	svc := &mockIssueService{
		getIssueFunc: func(ctx context.Context, issueID string) (json.RawMessage, error) {
			return nil, service.ErrNotFound("issue not found: nonexistent-id")
		},
	}
	handler := handleGetIssue(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/issues/nonexistent-id", nil)
	req.SetPathValue("id", "nonexistent-id")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleGetIssue_InternalError(t *testing.T) {
	svc := &mockIssueService{
		getIssueFunc: func(ctx context.Context, issueID string) (json.RawMessage, error) {
			return nil, service.ErrInternal("internal server error", nil)
		},
	}
	handler := handleGetIssue(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/issues/test-123", nil)
	req.SetPathValue("id", "test-123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestHandleGetIssue_DaemonUnavailable(t *testing.T) {
	svc := &mockIssueService{
		getIssueFunc: func(ctx context.Context, issueID string) (json.RawMessage, error) {
			return nil, service.ErrUnavailable("daemon not available")
		},
	}
	handler := handleGetIssue(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/issues/abc123", nil)
	req.SetPathValue("id", "abc123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleGetIssue_Timeout(t *testing.T) {
	svc := &mockIssueService{
		getIssueFunc: func(ctx context.Context, issueID string) (json.RawMessage, error) {
			return nil, service.ErrTimeout("connection timed out")
		},
	}
	handler := handleGetIssue(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/issues/abc123", nil)
	req.SetPathValue("id", "abc123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusGatewayTimeout {
		t.Errorf("status = %d, want %d", w.Code, http.StatusGatewayTimeout)
	}
}

// ===========================================================================
// handleListIssues tests
// ===========================================================================

func TestHandleListIssues_Success(t *testing.T) {
	svc := &mockIssueService{
		listIssuesFunc: func(ctx context.Context, params service.ListIssuesParams) (*service.ListIssuesResult, error) {
			return &service.ListIssuesResult{Issues: []service.IssueWithParent{
				{IssueWithCounts: &types.IssueWithCounts{Issue: &types.Issue{ID: "issue-1", Title: "Test 1", Status: types.StatusOpen}}},
				{IssueWithCounts: &types.IssueWithCounts{Issue: &types.Issue{ID: "issue-2", Title: "Test 2", Status: types.StatusOpen}}},
			}}, nil
		},
	}
	handler := handleListIssues(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	var resp IssuesResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected Success=true, got false (error: %s)", resp.Error)
	}
}

func TestHandleListIssues_SvcUnavailable(t *testing.T) {
	svc := &mockIssueService{
		listIssuesFunc: func(ctx context.Context, params service.ListIssuesParams) (*service.ListIssuesResult, error) {
			return nil, service.ErrUnavailable("daemon not available")
		},
	}
	handler := handleListIssues(svc)
	req := httptest.NewRequest("GET", "/api/issues", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleListIssues_KanbanMode(t *testing.T) {
	svc := &mockIssueService{
		listIssuesFunc: func(ctx context.Context, params service.ListIssuesParams) (*service.ListIssuesResult, error) {
			if !params.IncludeBlocked {
				t.Error("expected IncludeBlocked=true")
			}
			return &service.ListIssuesResult{KanbanIssues: []service.KanbanIssue{
				{IssueWithCounts: &types.IssueWithCounts{Issue: &types.Issue{ID: "issue-1"}}, IsBlocked: true, BlockedByCount: 2},
				{IssueWithCounts: &types.IssueWithCounts{Issue: &types.Issue{ID: "issue-2"}}, IsBlocked: false},
			}}, nil
		},
	}
	handler := handleListIssues(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/issues?include_blocked=true", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

// ===========================================================================
// handleCreateIssue tests
// ===========================================================================

func TestHandleCreateIssue_MalformedJSON(t *testing.T) {
	svc := &mockIssueService{}
	handler := handleCreateIssue(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/issues", strings.NewReader(`{"title": "Test", "issue_type": bug}`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	var resp IssuesResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Code != "INVALID_JSON" {
		t.Errorf("code = %q, want %q", resp.Code, "INVALID_JSON")
	}
}

func TestHandleCreateIssue_ValidationError(t *testing.T) {
	svc := &mockIssueService{
		createIssueFunc: func(ctx context.Context, params service.CreateIssueParams) (json.RawMessage, error) {
			return nil, service.ErrValidation("title is required")
		},
	}
	handler := handleCreateIssue(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/issues", strings.NewReader(`{"issue_type": "bug", "priority": 1}`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleCreateIssue_Success(t *testing.T) {
	issueJSON, _ := json.Marshal(map[string]interface{}{"id": "new-123", "title": "Test Issue"})
	svc := &mockIssueService{
		createIssueFunc: func(ctx context.Context, params service.CreateIssueParams) (json.RawMessage, error) {
			if params.Title != "Test Issue" {
				t.Errorf("Title = %q, want %q", params.Title, "Test Issue")
			}
			return issueJSON, nil
		},
	}
	handler := handleCreateIssue(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/issues", strings.NewReader(`{"title":"Test Issue","issue_type":"bug","priority":1}`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", w.Code, http.StatusCreated)
	}
	var resp IssuesResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if !resp.Success {
		t.Error("expected success=true")
	}
}

func TestHandleCreateIssue_ConflictError(t *testing.T) {
	svc := &mockIssueService{
		createIssueFunc: func(ctx context.Context, params service.CreateIssueParams) (json.RawMessage, error) {
			return nil, service.ErrConflict("duplicate issue ID")
		},
	}
	handler := handleCreateIssue(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/issues", strings.NewReader(`{"title":"Test","issue_type":"bug","priority":1}`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", w.Code, http.StatusConflict)
	}
}

// ===========================================================================
// handlePatchIssue tests
// ===========================================================================

func TestHandlePatchIssue_EmptyID(t *testing.T) {
	svc := &mockIssueService{}
	handler := handlePatchIssue(svc)
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/", strings.NewReader(`{}`))
	req.SetPathValue("id", "")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandlePatchIssue_InvalidJSON(t *testing.T) {
	svc := &mockIssueService{}
	handler := handlePatchIssue(svc)
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/test-123", strings.NewReader(`{invalid`))
	req.SetPathValue("id", "test-123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandlePatchIssue_Success(t *testing.T) {
	var captured service.PatchIssueParams
	svc := &mockIssueService{
		patchIssueFunc: func(ctx context.Context, params service.PatchIssueParams) error {
			captured = params
			return nil
		},
	}
	handler := handlePatchIssue(svc)
	title := "Updated"
	reqBody, _ := json.Marshal(PatchIssueRequest{Title: &title})
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/test-123", strings.NewReader(string(reqBody)))
	req.SetPathValue("id", "test-123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if captured.IssueID != "test-123" {
		t.Errorf("IssueID = %q, want %q", captured.IssueID, "test-123")
	}
	if captured.Title == nil || *captured.Title != "Updated" {
		t.Errorf("Title = %v, want %q", captured.Title, "Updated")
	}
}

func TestHandlePatchIssue_NotFound(t *testing.T) {
	svc := &mockIssueService{
		patchIssueFunc: func(ctx context.Context, params service.PatchIssueParams) error {
			return service.ErrNotFound("issue not found")
		},
	}
	handler := handlePatchIssue(svc)
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/x", strings.NewReader(`{"title":"New"}`))
	req.SetPathValue("id", "x")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandlePatchIssue_Conflict(t *testing.T) {
	svc := &mockIssueService{
		patchIssueFunc: func(ctx context.Context, params service.PatchIssueParams) error {
			return service.ErrConflict("cannot update template issue")
		},
	}
	handler := handlePatchIssue(svc)
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/x", strings.NewReader(`{"title":"New"}`))
	req.SetPathValue("id", "x")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", w.Code, http.StatusConflict)
	}
}

func TestHandlePatchIssue_RequestBodyTooLarge(t *testing.T) {
	svc := &mockIssueService{}
	handler := handlePatchIssue(svc)
	largeBody := strings.NewReader(`{"title":"` + strings.Repeat("a", 1<<20+1) + `"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/test-123", largeBody)
	req.SetPathValue("id", "test-123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
}

// ===========================================================================
// handleCloseIssue tests
// ===========================================================================

func TestHandleCloseIssue_EmptyID(t *testing.T) {
	svc := &mockIssueService{}
	handler := handleCloseIssue(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/issues//close", nil)
	req.SetPathValue("id", "")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleCloseIssue_Success(t *testing.T) {
	svc := &mockIssueService{
		closeIssueFunc: func(ctx context.Context, params service.CloseIssueParams) (json.RawMessage, error) {
			return json.RawMessage(`{"id":"test-123","status":"closed"}`), nil
		},
	}
	handler := handleCloseIssue(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/issues/test-123/close", nil)
	req.SetPathValue("id", "test-123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var resp CloseResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if !resp.Success {
		t.Error("expected success=true")
	}
}

func TestHandleCloseIssue_NotFound(t *testing.T) {
	svc := &mockIssueService{
		closeIssueFunc: func(ctx context.Context, params service.CloseIssueParams) (json.RawMessage, error) {
			return nil, service.ErrNotFound("issue not found")
		},
	}
	handler := handleCloseIssue(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/issues/test-123/close", nil)
	req.SetPathValue("id", "test-123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleCloseIssue_HasBlockers(t *testing.T) {
	svc := &mockIssueService{
		closeIssueFunc: func(ctx context.Context, params service.CloseIssueParams) (json.RawMessage, error) {
			return nil, service.ErrConflict("issue has open blockers")
		},
	}
	handler := handleCloseIssue(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/issues/test-123/close", nil)
	req.SetPathValue("id", "test-123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", w.Code, http.StatusConflict)
	}
}

func TestHandleCloseIssue_WithReason(t *testing.T) {
	var captured service.CloseIssueParams
	svc := &mockIssueService{
		closeIssueFunc: func(ctx context.Context, params service.CloseIssueParams) (json.RawMessage, error) {
			captured = params
			return json.RawMessage(`{}`), nil
		},
	}
	handler := handleCloseIssue(svc)
	body := `{"reason":"done","session":"s1","suggest_next":true,"force":false}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/test-123/close", strings.NewReader(body))
	req.SetPathValue("id", "test-123")
	req.Header.Set("Content-Length", strconv.Itoa(len(body)))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if captured.Reason != "done" {
		t.Errorf("Reason = %q, want %q", captured.Reason, "done")
	}
	if !captured.SuggestNext {
		t.Error("SuggestNext = false, want true")
	}
}

// ===========================================================================
// handleAddComment tests
// ===========================================================================

func TestHandleAddComment_Success(t *testing.T) {
	svc := &mockIssueService{
		addCommentFunc: func(ctx context.Context, params service.AddCommentParams) (*types.Comment, error) {
			return &types.Comment{ID: 1, IssueID: params.IssueID, Author: params.Author, Text: params.Text}, nil
		},
	}
	handler := handleAddComment(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/issues/test-123/comments", strings.NewReader(`{"text":"Test comment"}`))
	req.SetPathValue("id", "test-123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", w.Code, http.StatusCreated)
	}
}

func TestHandleAddComment_MissingIssueID(t *testing.T) {
	svc := &mockIssueService{}
	handler := handleAddComment(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/issues//comments", strings.NewReader(`{"text":"Test"}`))
	req.SetPathValue("id", "")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleAddComment_EmptyText(t *testing.T) {
	svc := &mockIssueService{
		addCommentFunc: func(ctx context.Context, params service.AddCommentParams) (*types.Comment, error) {
			return nil, service.ErrValidation("comment text is required")
		},
	}
	handler := handleAddComment(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/issues/test-123/comments", strings.NewReader(`{"text":""}`))
	req.SetPathValue("id", "test-123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleAddComment_IssueNotFound(t *testing.T) {
	svc := &mockIssueService{
		addCommentFunc: func(ctx context.Context, params service.AddCommentParams) (*types.Comment, error) {
			return nil, service.ErrNotFound("issue not found")
		},
	}
	handler := handleAddComment(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/issues/x/comments", strings.NewReader(`{"text":"Test"}`))
	req.SetPathValue("id", "x")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleAddComment_InvalidJSON(t *testing.T) {
	svc := &mockIssueService{}
	handler := handleAddComment(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/issues/test-123/comments", strings.NewReader(`{invalid`))
	req.SetPathValue("id", "test-123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// ===========================================================================
// handleAddDependency / handleRemoveDependency tests
// ===========================================================================

func TestHandleAddDependency_Success(t *testing.T) {
	svc := &mockIssueService{
		addDependencyFunc: func(ctx context.Context, params service.AddDependencyParams) error {
			if params.IssueID != "issue-1" || params.DependsOnID != "issue-2" {
				t.Errorf("unexpected params: %+v", params)
			}
			return nil
		},
	}
	handler := handleAddDependency(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/issues/issue-1/dependencies", strings.NewReader(`{"depends_on_id":"issue-2"}`))
	req.SetPathValue("id", "issue-1")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleAddDependency_MissingIssueID(t *testing.T) {
	svc := &mockIssueService{}
	handler := handleAddDependency(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/issues//dependencies", strings.NewReader(`{}`))
	req.SetPathValue("id", "")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleAddDependency_ValidationError(t *testing.T) {
	svc := &mockIssueService{
		addDependencyFunc: func(ctx context.Context, params service.AddDependencyParams) error {
			return service.ErrValidation("depends_on_id is required")
		},
	}
	handler := handleAddDependency(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/issues/issue-1/dependencies", strings.NewReader(`{}`))
	req.SetPathValue("id", "issue-1")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleAddDependency_CycleError(t *testing.T) {
	svc := &mockIssueService{
		addDependencyFunc: func(ctx context.Context, params service.AddDependencyParams) error {
			return service.ErrConflict("would create a dependency cycle")
		},
	}
	handler := handleAddDependency(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/issues/issue-1/dependencies", strings.NewReader(`{"depends_on_id":"issue-2"}`))
	req.SetPathValue("id", "issue-1")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", w.Code, http.StatusConflict)
	}
}

func TestHandleAddDependency_RequestBodyTooLarge(t *testing.T) {
	svc := &mockIssueService{}
	handler := handleAddDependency(svc)
	largeBody := strings.NewReader(`{"depends_on_id":"` + strings.Repeat("a", 1<<20+1) + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/issues/issue-1/dependencies", largeBody)
	req.SetPathValue("id", "issue-1")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestHandleRemoveDependency_Success(t *testing.T) {
	svc := &mockIssueService{
		removeDependencyFunc: func(ctx context.Context, params service.RemoveDependencyParams) error {
			return nil
		},
	}
	handler := handleRemoveDependency(svc)
	req := httptest.NewRequest(http.MethodDelete, "/api/issues/issue-1/dependencies/issue-2", nil)
	req.SetPathValue("id", "issue-1")
	req.SetPathValue("depId", "issue-2")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleRemoveDependency_MissingIDs(t *testing.T) {
	svc := &mockIssueService{}
	handler := handleRemoveDependency(svc)
	t.Run("missing issue ID", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/issues//dependencies/x", nil)
		req.SetPathValue("id", "")
		req.SetPathValue("depId", "x")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})
	t.Run("missing dep ID", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/issues/x/dependencies/", nil)
		req.SetPathValue("id", "x")
		req.SetPathValue("depId", "")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})
}

func TestHandleRemoveDependency_NotFound(t *testing.T) {
	svc := &mockIssueService{
		removeDependencyFunc: func(ctx context.Context, params service.RemoveDependencyParams) error {
			return service.ErrNotFound("dependency not found")
		},
	}
	handler := handleRemoveDependency(svc)
	req := httptest.NewRequest(http.MethodDelete, "/api/issues/issue-1/dependencies/x", nil)
	req.SetPathValue("id", "issue-1")
	req.SetPathValue("depId", "x")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}
