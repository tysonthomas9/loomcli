package webui

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

// --- Mock RPC server infrastructure for handleListIssues / fetchUnclosedIDSetAndMap ---

// issuesMockRPCHandler is a function that handles an RPC request and returns a response.
type issuesMockRPCHandler func(req rpc.Request) rpc.Response

// startIssuesMockRPCServer creates a Unix socket mock server that handles RPC requests
// using the provided handler. Returns the socket path.
func startIssuesMockRPCServer(t *testing.T, handler issuesMockRPCHandler) string {
	t.Helper()

	dir, err := os.MkdirTemp("/tmp", "issues-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	socketPath := filepath.Join(dir, "bd.sock")

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("failed to create mock server: %v", err)
	}
	t.Cleanup(func() { listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return // listener closed
			}
			go func(c net.Conn) {
				defer c.Close()
				scanner := bufio.NewScanner(c)
				scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
				for scanner.Scan() {
					var req rpc.Request
					if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
						continue
					}
					resp := handler(req)
					data, _ := json.Marshal(resp)
					data = append(data, '\n')
					_, _ = c.Write(data)
				}
			}(conn)
		}
	}()

	return socketPath
}

// issuesCovDaemonPool implements daemon.Pool for testing with a mock RPC server.
type issuesCovDaemonPool struct {
	socketPath string
	clients    []*rpc.Client
}

func newIssuesCovDaemonPool(socketPath string) *issuesCovDaemonPool {
	return &issuesCovDaemonPool{socketPath: socketPath}
}

func (p *issuesCovDaemonPool) Get(_ context.Context) (*rpc.Client, error) {
	client, err := rpc.TryConnectWithTimeout(p.socketPath, 2*time.Second)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, context.DeadlineExceeded
	}
	p.clients = append(p.clients, client)
	return client, nil
}

func (p *issuesCovDaemonPool) Put(client *rpc.Client) {
	if client != nil {
		_ = client.Close()
	}
}

func (p *issuesCovDaemonPool) PutAfterError(client *rpc.Client) { p.Put(client) }

func (p *issuesCovDaemonPool) Discard(client *rpc.Client) {
	if client != nil {
		_ = client.Close()
	}
}

func (p *issuesCovDaemonPool) Stats() daemon.PoolStats {
	return daemon.PoolStats{}
}

func (p *issuesCovDaemonPool) Close() error {
	for _, c := range p.clients {
		_ = c.Close()
	}
	return nil
}

// issuesCovHealthyResponse returns a successful health response for mock servers.
func issuesCovHealthyResponse() rpc.Response {
	healthData, _ := json.Marshal(rpc.HealthResponse{
		Status:     "healthy",
		Version:    "test",
		Compatible: true,
		Uptime:     1.0,
	})
	return rpc.Response{Success: true, Data: json.RawMessage(healthData)}
}

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
	issues := []*types.IssueWithCounts{
		makeCovIssue("issue-1", "First Issue", types.StatusOpen, 2),
		makeCovIssue("issue-2", "Second Issue", types.StatusInProgress, 1),
	}

	socketPath := startIssuesMockRPCServer(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "list":
			data, _ := json.Marshal(issues)
			return rpc.Response{Success: true, Data: json.RawMessage(data)}
		case "get_parent_ids":
			return rpc.Response{
				Success: true,
				Data:    json.RawMessage(`{"parents":{}}`),
			}
		default:
			return rpc.Response{Success: true, Data: json.RawMessage(`{}`)}
		}
	})

	pool := newIssuesCovDaemonPool(socketPath)
	defer pool.Close()

	handler := handleListIssues(pool)

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
	socketPath := startIssuesMockRPCServer(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "list":
			data, _ := json.Marshal([]*types.IssueWithCounts{})
			return rpc.Response{Success: true, Data: json.RawMessage(data)}
		default:
			return rpc.Response{Success: true, Data: json.RawMessage(`{}`)}
		}
	})

	pool := newIssuesCovDaemonPool(socketPath)
	defer pool.Close()

	handler := handleListIssues(pool)

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
	issues := []*types.IssueWithCounts{
		makeCovIssue("issue-1", "Open", types.StatusOpen, 2),
		makeCovIssue("issue-2", "Closed", types.StatusClosed, 1),
		makeCovIssue("issue-3", "In Progress", types.StatusInProgress, 0),
	}

	socketPath := startIssuesMockRPCServer(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "list":
			data, _ := json.Marshal(issues)
			return rpc.Response{Success: true, Data: json.RawMessage(data)}
		case "get_parent_ids":
			return rpc.Response{
				Success: true,
				Data:    json.RawMessage(`{"parents":{}}`),
			}
		default:
			return rpc.Response{Success: true, Data: json.RawMessage(`{}`)}
		}
	})

	pool := newIssuesCovDaemonPool(socketPath)
	defer pool.Close()

	handler := handleListIssues(pool)

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
	issues := []*types.IssueWithCounts{
		makeCovIssue("child-1", "Child Issue", types.StatusOpen, 2),
	}

	parentInfo := map[string]*rpc.ParentInfo{
		"child-1": {ParentID: "parent-1", ParentTitle: "Parent Epic"},
	}

	socketPath := startIssuesMockRPCServer(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "list":
			data, _ := json.Marshal(issues)
			return rpc.Response{Success: true, Data: json.RawMessage(data)}
		case "get_parent_ids":
			resp := rpc.GetParentIDsResponse{Parents: parentInfo}
			data, _ := json.Marshal(resp)
			return rpc.Response{Success: true, Data: json.RawMessage(data)}
		default:
			return rpc.Response{Success: true, Data: json.RawMessage(`{}`)}
		}
	})

	pool := newIssuesCovDaemonPool(socketPath)
	defer pool.Close()

	handler := handleListIssues(pool)

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
	issues := []*types.IssueWithCounts{
		makeCovIssue("task-1", "Blocked Task", types.StatusOpen, 2),
		makeCovIssue("task-2", "Free Task", types.StatusOpen, 3),
	}

	allIssues := []*types.IssueWithCounts{
		makeCovIssue("task-1", "Blocked Task", types.StatusOpen, 2),
		makeCovIssue("task-2", "Free Task", types.StatusOpen, 3),
		makeCovIssue("blocker-1", "Blocker Issue", types.StatusOpen, 1),
	}
	issues[0].Issue.Dependencies = []*types.Dependency{
		{
			IssueID:     "task-1",
			DependsOnID: "blocker-1",
			Type:        types.DepBlocks,
			CreatedAt:   time.Now(),
		},
	}

	callCount := 0
	socketPath := startIssuesMockRPCServer(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "list":
			callCount++
			if callCount == 1 {
				data, _ := json.Marshal(issues)
				return rpc.Response{Success: true, Data: json.RawMessage(data)}
			}
			data, _ := json.Marshal(allIssues)
			return rpc.Response{Success: true, Data: json.RawMessage(data)}
		case "get_parent_ids":
			return rpc.Response{
				Success: true,
				Data:    json.RawMessage(`{"parents":{}}`),
			}
		case "blocked":
			data, _ := json.Marshal([]*types.BlockedIssue{})
			return rpc.Response{Success: true, Data: json.RawMessage(data)}
		default:
			return rpc.Response{Success: true, Data: json.RawMessage(`{}`)}
		}
	})

	pool := newIssuesCovDaemonPool(socketPath)
	defer pool.Close()

	handler := handleListIssues(pool)

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
	socketPath := startIssuesMockRPCServer(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "list":
			data, _ := json.Marshal([]*types.IssueWithCounts{})
			return rpc.Response{Success: true, Data: json.RawMessage(data)}
		default:
			return rpc.Response{Success: true, Data: json.RawMessage(`{}`)}
		}
	})

	pool := newIssuesCovDaemonPool(socketPath)
	defer pool.Close()

	handler := handleListIssues(pool)

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

func TestHandleListIssues_DaemonErrorResponse(t *testing.T) {
	socketPath := startIssuesMockRPCServer(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "list":
			return rpc.Response{Success: false, Error: "internal daemon error"}
		default:
			return rpc.Response{Success: true, Data: json.RawMessage(`{}`)}
		}
	})

	pool := newIssuesCovDaemonPool(socketPath)
	defer pool.Close()

	handler := handleListIssues(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp IssuesResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Success {
		t.Error("expected success=false for daemon error")
	}
	// When the daemon returns Success=false, the rpc.Client wraps it as an error,
	// so handleListIssues sees it as an RPC error (not a DAEMON_ERROR).
	if resp.Code != "RPC_ERROR" {
		t.Errorf("expected code RPC_ERROR, got %q", resp.Code)
	}
}

func TestHandleListIssues_WithRepoField(t *testing.T) {
	issue := makeCovIssue("repo-issue", "Multi-repo Issue", types.StatusOpen, 2)
	issue.Issue.SourceRepo = "backend-repo"
	issues := []*types.IssueWithCounts{issue}

	socketPath := startIssuesMockRPCServer(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "list":
			data, _ := json.Marshal(issues)
			return rpc.Response{Success: true, Data: json.RawMessage(data)}
		case "get_parent_ids":
			return rpc.Response{
				Success: true,
				Data:    json.RawMessage(`{"parents":{}}`),
			}
		default:
			return rpc.Response{Success: true, Data: json.RawMessage(`{}`)}
		}
	})

	pool := newIssuesCovDaemonPool(socketPath)
	defer pool.Close()

	handler := handleListIssues(pool)

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
	issues := []*types.IssueWithCounts{
		makeCovIssue("filtered-1", "Filtered Result", types.StatusOpen, 1),
	}

	socketPath := startIssuesMockRPCServer(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "list":
			data, _ := json.Marshal(issues)
			return rpc.Response{Success: true, Data: json.RawMessage(data)}
		case "get_parent_ids":
			return rpc.Response{
				Success: true,
				Data:    json.RawMessage(`{"parents":{}}`),
			}
		default:
			return rpc.Response{Success: true, Data: json.RawMessage(`{}`)}
		}
	})

	pool := newIssuesCovDaemonPool(socketPath)
	defer pool.Close()

	handler := handleListIssues(pool)

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
	issues := []*types.IssueWithCounts{
		makeCovIssue("issue-1", "Open", types.StatusOpen, 2),
		makeCovIssue("issue-2", "InProgress", types.StatusInProgress, 1),
	}

	socketPath := startIssuesMockRPCServer(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "list":
			data, _ := json.Marshal(issues)
			return rpc.Response{Success: true, Data: json.RawMessage(data)}
		case "get_parent_ids":
			return rpc.Response{
				Success: true,
				Data:    json.RawMessage(`{"parents":{}}`),
			}
		default:
			return rpc.Response{Success: true, Data: json.RawMessage(`{}`)}
		}
	})

	pool := newIssuesCovDaemonPool(socketPath)
	defer pool.Close()

	handler := handleListIssues(pool)

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

// --- fetchUnclosedIDSetAndMap coverage tests ---

func TestFetchUnclosedIDSetAndMap_Success(t *testing.T) {
	now := time.Now()
	closedAt := now
	allIssues := []*types.IssueWithCounts{
		{
			Issue: &types.Issue{
				ID: "open-1", Title: "Open Issue", Status: types.StatusOpen,
				Priority: 2, IssueType: types.TypeTask, CreatedAt: now, UpdatedAt: now,
			},
		},
		{
			Issue: &types.Issue{
				ID: "closed-1", Title: "Closed Issue", Status: types.StatusClosed,
				Priority: 1, IssueType: types.TypeBug, CreatedAt: now, UpdatedAt: now,
				ClosedAt: &closedAt,
			},
		},
		{
			Issue: &types.Issue{
				ID: "ip-1", Title: "In Progress", Status: types.StatusInProgress,
				Priority: 0, IssueType: types.TypeFeature, CreatedAt: now, UpdatedAt: now,
			},
		},
	}

	socketPath := startIssuesMockRPCServer(t, func(req rpc.Request) rpc.Response {
		if req.Operation == "health" {
			return issuesCovHealthyResponse()
		}
		data, _ := json.Marshal(allIssues)
		return rpc.Response{Success: true, Data: json.RawMessage(data)}
	})

	client, err := rpc.TryConnectWithTimeout(socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("failed to connect to mock server: %v", err)
	}
	if client == nil {
		t.Fatal("mock server returned nil client (health check likely failed)")
	}
	defer client.Close()

	unclosedIDs, issueMap := fetchUnclosedIDSetAndMap(client)

	if unclosedIDs == nil {
		t.Fatal("expected unclosedIDs to be non-nil")
	}
	if issueMap == nil {
		t.Fatal("expected issueMap to be non-nil")
	}

	if !unclosedIDs["open-1"] {
		t.Error("expected open-1 to be in unclosedIDs")
	}
	if !unclosedIDs["ip-1"] {
		t.Error("expected ip-1 to be in unclosedIDs")
	}
	if unclosedIDs["closed-1"] {
		t.Error("closed-1 should NOT be in unclosedIDs")
	}

	if len(issueMap) != 3 {
		t.Errorf("expected 3 issues in issueMap, got %d", len(issueMap))
	}
	if issueMap["open-1"] == nil {
		t.Error("expected open-1 in issueMap")
	}
	if issueMap["closed-1"] == nil {
		t.Error("expected closed-1 in issueMap")
	}
}

func TestFetchUnclosedIDSetAndMap_EmptyList(t *testing.T) {
	socketPath := startIssuesMockRPCServer(t, func(req rpc.Request) rpc.Response {
		if req.Operation == "health" {
			return issuesCovHealthyResponse()
		}
		data, _ := json.Marshal([]*types.IssueWithCounts{})
		return rpc.Response{Success: true, Data: json.RawMessage(data)}
	})

	client, err := rpc.TryConnectWithTimeout(socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("failed to connect to mock server: %v", err)
	}
	if client == nil {
		t.Fatal("mock server returned nil client")
	}
	defer client.Close()

	unclosedIDs, issueMap := fetchUnclosedIDSetAndMap(client)

	if unclosedIDs == nil {
		t.Fatal("expected unclosedIDs to be non-nil (empty map)")
	}
	if issueMap == nil {
		t.Fatal("expected issueMap to be non-nil (empty map)")
	}
	if len(unclosedIDs) != 0 {
		t.Errorf("expected 0 unclosed IDs, got %d", len(unclosedIDs))
	}
	if len(issueMap) != 0 {
		t.Errorf("expected 0 issues in map, got %d", len(issueMap))
	}
}

func TestFetchUnclosedIDSetAndMap_RPCError(t *testing.T) {
	socketPath := startIssuesMockRPCServer(t, func(req rpc.Request) rpc.Response {
		if req.Operation == "health" {
			return issuesCovHealthyResponse()
		}
		return rpc.Response{Success: false, Error: "daemon error"}
	})

	client, err := rpc.TryConnectWithTimeout(socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("failed to connect to mock server: %v", err)
	}
	if client == nil {
		t.Fatal("mock server returned nil client")
	}
	defer client.Close()

	unclosedIDs, issueMap := fetchUnclosedIDSetAndMap(client)

	if unclosedIDs != nil {
		t.Errorf("expected nil unclosedIDs on RPC error, got %v", unclosedIDs)
	}
	if issueMap != nil {
		t.Errorf("expected nil issueMap on RPC error, got %v", issueMap)
	}
}

func TestFetchUnclosedIDSetAndMap_AllClosed(t *testing.T) {
	now := time.Now()
	closedAt := now
	allIssues := []*types.IssueWithCounts{
		{
			Issue: &types.Issue{
				ID: "closed-1", Title: "Closed 1", Status: types.StatusClosed,
				Priority: 1, IssueType: types.TypeTask, CreatedAt: now, UpdatedAt: now,
				ClosedAt: &closedAt,
			},
		},
		{
			Issue: &types.Issue{
				ID: "closed-2", Title: "Closed 2", Status: types.StatusClosed,
				Priority: 2, IssueType: types.TypeBug, CreatedAt: now, UpdatedAt: now,
				ClosedAt: &closedAt,
			},
		},
	}

	socketPath := startIssuesMockRPCServer(t, func(req rpc.Request) rpc.Response {
		if req.Operation == "health" {
			return issuesCovHealthyResponse()
		}
		data, _ := json.Marshal(allIssues)
		return rpc.Response{Success: true, Data: json.RawMessage(data)}
	})

	client, err := rpc.TryConnectWithTimeout(socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("failed to connect to mock server: %v", err)
	}
	if client == nil {
		t.Fatal("mock server returned nil client")
	}
	defer client.Close()

	unclosedIDs, issueMap := fetchUnclosedIDSetAndMap(client)

	if unclosedIDs == nil {
		t.Fatal("expected non-nil unclosedIDs")
	}
	if len(unclosedIDs) != 0 {
		t.Errorf("expected 0 unclosed IDs (all closed), got %d", len(unclosedIDs))
	}
	if len(issueMap) != 2 {
		t.Errorf("expected 2 issues in map (all issues regardless of status), got %d", len(issueMap))
	}
}
