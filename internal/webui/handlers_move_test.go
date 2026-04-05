package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

// mockMoveClient implements issueMover for testing
type mockMoveClient struct {
	showFunc       func(args *rpc.ShowArgs) (*rpc.Response, error)
	createFunc     func(args *rpc.CreateArgs) (*rpc.Response, error)
	addCommentFn   func(args *rpc.CommentAddArgs) (*rpc.Response, error)
	closeIssueFunc func(args *rpc.CloseArgs) (*rpc.Response, error)
}

func (m *mockMoveClient) Show(args *rpc.ShowArgs) (*rpc.Response, error) {
	if m.showFunc != nil {
		return m.showFunc(args)
	}
	return nil, errors.New("showFunc not implemented")
}

func (m *mockMoveClient) Create(args *rpc.CreateArgs) (*rpc.Response, error) {
	if m.createFunc != nil {
		return m.createFunc(args)
	}
	return nil, errors.New("createFunc not implemented")
}

func (m *mockMoveClient) AddComment(args *rpc.CommentAddArgs) (*rpc.Response, error) {
	if m.addCommentFn != nil {
		return m.addCommentFn(args)
	}
	return nil, errors.New("addCommentFn not implemented")
}

func (m *mockMoveClient) CloseIssue(args *rpc.CloseArgs) (*rpc.Response, error) {
	if m.closeIssueFunc != nil {
		return m.closeIssueFunc(args)
	}
	return nil, errors.New("closeIssueFunc not implemented")
}

// mockMovePool implements moveConnectionGetter for testing
type mockMovePool struct {
	getFunc func(ctx context.Context) (issueMover, error)
	putFunc func(client issueMover)
}

func (m *mockMovePool) Get(ctx context.Context) (issueMover, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx)
	}
	return nil, errors.New("getFunc not implemented")
}

func (m *mockMovePool) Put(client issueMover) {
	if m.putFunc != nil {
		m.putFunc(client)
	}
}

func (m *mockMovePool) Discard(client issueMover) {}

// testWorkspaceConfigFn returns a workspace config function for testing.
func testWorkspaceConfigFn(name string, workspaces []WorkspaceSummary) func() (*WorkspaceData, error) {
	return func() (*WorkspaceData, error) {
		return &WorkspaceData{
			Name:       name,
			Workspaces: workspaces,
		}, nil
	}
}

// defaultWorkspaces returns a standard set of workspaces for tests.
func defaultWorkspaces() []WorkspaceSummary {
	return []WorkspaceSummary{
		{Name: "alpha", Path: "/ws/alpha", Active: true, RepoCount: 2},
		{Name: "beta", Path: "/ws/beta", Active: false, RepoCount: 1},
		{Name: "gamma", Path: "/ws/gamma", Active: false, RepoCount: 3},
	}
}

// makeSourceIssueJSON creates JSON for a source issue with the given fields.
func makeSourceIssueJSON(id, title, status, assignee string) json.RawMessage {
	issue := types.Issue{
		ID:        id,
		Title:     title,
		Status:    types.Status(status),
		Assignee:  assignee,
		IssueType: types.TypeTask,
		Priority:  2,
	}
	data, _ := json.Marshal(issue)
	return data
}

// TestHandleMoveIssue_Success verifies the full move flow:
// Show → Create → AddComment → CloseIssue → 200 with source_id and target_id
func TestHandleMoveIssue_Success(t *testing.T) {
	sourceData := makeSourceIssueJSON("src-001", "Fix the bug", "open", "")
	createdData := makeSourceIssueJSON("tgt-001", "Fix the bug", "open", "")

	client := &mockMoveClient{
		showFunc: func(args *rpc.ShowArgs) (*rpc.Response, error) {
			if args.ID != "src-001" {
				t.Errorf("Show called with ID=%q, want %q", args.ID, "src-001")
			}
			return &rpc.Response{Success: true, Data: sourceData}, nil
		},
		createFunc: func(args *rpc.CreateArgs) (*rpc.Response, error) {
			if args.Title != "Fix the bug" {
				t.Errorf("Create Title=%q, want %q", args.Title, "Fix the bug")
			}
			if !strings.Contains(args.Description, "(Moved from src-001)") {
				t.Errorf("Create Description should contain move reference, got %q", args.Description)
			}
			return &rpc.Response{Success: true, Data: createdData}, nil
		},
		addCommentFn: func(args *rpc.CommentAddArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true}, nil
		},
		closeIssueFunc: func(args *rpc.CloseArgs) (*rpc.Response, error) {
			if args.ID != "src-001" {
				t.Errorf("CloseIssue ID=%q, want %q", args.ID, "src-001")
			}
			if !args.Force {
				t.Error("CloseIssue should use Force=true")
			}
			return &rpc.Response{Success: true}, nil
		},
	}

	pool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) { return client, nil },
		putFunc: func(c issueMover) {},
	}

	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssueWithPool(pool, pool, wsCfg)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp MoveIssueResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !resp.Success {
		t.Errorf("expected success=true, got false: %s", resp.Error)
	}
	if resp.Data == nil {
		t.Fatal("expected Data to be non-nil")
	}
	if resp.Data.SourceID != "src-001" {
		t.Errorf("SourceID=%q, want %q", resp.Data.SourceID, "src-001")
	}
	if resp.Data.TargetID != "tgt-001" {
		t.Errorf("TargetID=%q, want %q", resp.Data.TargetID, "tgt-001")
	}
	if len(resp.Data.Warnings) != 0 {
		t.Errorf("expected no warnings, got %v", resp.Data.Warnings)
	}
}

// TestHandleMoveIssue_MissingIssueID verifies 400 when issue ID is empty in path.
func TestHandleMoveIssue_MissingIssueID(t *testing.T) {
	pool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) { return &mockMoveClient{}, nil },
		putFunc: func(c issueMover) {},
	}

	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssueWithPool(pool, pool, wsCfg)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues//move", strings.NewReader(body))
	req.SetPathValue("id", "")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp MoveIssueResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error != "missing issue ID in path" {
		t.Errorf("error=%q, want %q", resp.Error, "missing issue ID in path")
	}
}

// TestHandleMoveIssue_MissingTargetWorkspace verifies 400 when target_workspace is empty.
func TestHandleMoveIssue_MissingTargetWorkspace(t *testing.T) {
	pool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) { return &mockMoveClient{}, nil },
		putFunc: func(c issueMover) {},
	}

	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssueWithPool(pool, pool, wsCfg)

	body := `{"target_workspace":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp MoveIssueResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error != "target_workspace is required" {
		t.Errorf("error=%q, want %q", resp.Error, "target_workspace is required")
	}
}

// TestHandleMoveIssue_TargetWorkspaceNotFound verifies 400 when target workspace does not exist.
func TestHandleMoveIssue_TargetWorkspaceNotFound(t *testing.T) {
	pool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) { return &mockMoveClient{}, nil },
		putFunc: func(c issueMover) {},
	}

	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssueWithPool(pool, pool, wsCfg)

	body := `{"target_workspace":"nonexistent"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp MoveIssueResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !strings.Contains(resp.Error, "not found") {
		t.Errorf("error=%q, want it to contain %q", resp.Error, "not found")
	}
}

// TestHandleMoveIssue_SameWorkspaceRejected verifies 400 when target matches current workspace.
func TestHandleMoveIssue_SameWorkspaceRejected(t *testing.T) {
	pool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) { return &mockMoveClient{}, nil },
		putFunc: func(c issueMover) {},
	}

	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssueWithPool(pool, pool, wsCfg)

	body := `{"target_workspace":"alpha"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp MoveIssueResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error != "cannot move issue to the same workspace" {
		t.Errorf("error=%q, want %q", resp.Error, "cannot move issue to the same workspace")
	}
}

// TestHandleMoveIssue_SourceIssueNotFound verifies 404 when the source issue does not exist.
func TestHandleMoveIssue_SourceIssueNotFound(t *testing.T) {
	client := &mockMoveClient{
		showFunc: func(args *rpc.ShowArgs) (*rpc.Response, error) {
			return nil, errors.New("issue not found: src-999")
		},
	}

	pool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) { return client, nil },
		putFunc: func(c issueMover) {},
	}

	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssueWithPool(pool, pool, wsCfg)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-999/move", strings.NewReader(body))
	req.SetPathValue("id", "src-999")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp MoveIssueResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !strings.Contains(resp.Error, "not found") {
		t.Errorf("error=%q, want it to contain %q", resp.Error, "not found")
	}
}

// TestHandleMoveIssue_SourceIssueNotFound_ResponseError verifies 404 when
// Show returns Success=false with "not found" in the error.
func TestHandleMoveIssue_SourceIssueNotFound_ResponseError(t *testing.T) {
	client := &mockMoveClient{
		showFunc: func(args *rpc.ShowArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: false, Error: "issue not found: src-999"}, nil
		},
	}

	pool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) { return client, nil },
		putFunc: func(c issueMover) {},
	}

	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssueWithPool(pool, pool, wsCfg)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-999/move", strings.NewReader(body))
	req.SetPathValue("id", "src-999")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandleMoveIssue_SourceIssueClosed verifies 400 when the source issue is already closed.
func TestHandleMoveIssue_SourceIssueClosed(t *testing.T) {
	sourceData := makeSourceIssueJSON("src-001", "Closed bug", "closed", "")

	client := &mockMoveClient{
		showFunc: func(args *rpc.ShowArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true, Data: sourceData}, nil
		},
	}

	pool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) { return client, nil },
		putFunc: func(c issueMover) {},
	}

	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssueWithPool(pool, pool, wsCfg)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp MoveIssueResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error != "cannot move a closed issue" {
		t.Errorf("error=%q, want %q", resp.Error, "cannot move a closed issue")
	}
}

// TestHandleMoveIssue_PoolNotInitialized verifies 503 when pool is nil.
func TestHandleMoveIssue_PoolNotInitialized(t *testing.T) {
	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssueWithPool(nil, nil, wsCfg)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp MoveIssueResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error != "connection pool not initialized" {
		t.Errorf("error=%q, want %q", resp.Error, "connection pool not initialized")
	}
}

// TestHandleMoveIssue_PartialFailure_CloseFails verifies that when create succeeds
// but close fails, the handler returns success with warnings.
func TestHandleMoveIssue_PartialFailure_CloseFails(t *testing.T) {
	sourceData := makeSourceIssueJSON("src-001", "Fix the bug", "open", "")
	createdData := makeSourceIssueJSON("tgt-001", "Fix the bug", "open", "")

	client := &mockMoveClient{
		showFunc: func(args *rpc.ShowArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true, Data: sourceData}, nil
		},
		createFunc: func(args *rpc.CreateArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true, Data: createdData}, nil
		},
		addCommentFn: func(args *rpc.CommentAddArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true}, nil
		},
		closeIssueFunc: func(args *rpc.CloseArgs) (*rpc.Response, error) {
			return nil, errors.New("daemon timeout")
		},
	}

	pool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) { return client, nil },
		putFunc: func(c issueMover) {},
	}

	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssueWithPool(pool, pool, wsCfg)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (partial success), got %d: %s", rec.Code, rec.Body.String())
	}

	var resp MoveIssueResponse
	json.NewDecoder(rec.Body).Decode(&resp)

	if !resp.Success {
		t.Error("expected success=true for partial failure")
	}
	if resp.Data == nil {
		t.Fatal("expected Data to be non-nil")
	}
	if resp.Data.SourceID != "src-001" {
		t.Errorf("SourceID=%q, want %q", resp.Data.SourceID, "src-001")
	}
	if resp.Data.TargetID != "tgt-001" {
		t.Errorf("TargetID=%q, want %q", resp.Data.TargetID, "tgt-001")
	}

	// Should have a warning about the close failure
	foundCloseWarning := false
	for _, w := range resp.Data.Warnings {
		if strings.Contains(w, "could not be closed") {
			foundCloseWarning = true
			break
		}
	}
	if !foundCloseWarning {
		t.Errorf("expected warning about close failure, got warnings: %v", resp.Data.Warnings)
	}
}

// TestHandleMoveIssue_PartialFailure_CloseResponseFails verifies that when
// CloseIssue returns Success=false, the handler still returns success with warnings.
func TestHandleMoveIssue_PartialFailure_CloseResponseFails(t *testing.T) {
	sourceData := makeSourceIssueJSON("src-001", "Fix the bug", "open", "")
	createdData := makeSourceIssueJSON("tgt-001", "Fix the bug", "open", "")

	client := &mockMoveClient{
		showFunc: func(args *rpc.ShowArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true, Data: sourceData}, nil
		},
		createFunc: func(args *rpc.CreateArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true, Data: createdData}, nil
		},
		addCommentFn: func(args *rpc.CommentAddArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true}, nil
		},
		closeIssueFunc: func(args *rpc.CloseArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: false, Error: "issue has open blockers"}, nil
		},
	}

	pool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) { return client, nil },
		putFunc: func(c issueMover) {},
	}

	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssueWithPool(pool, pool, wsCfg)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp MoveIssueResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !resp.Success {
		t.Error("expected success=true")
	}

	foundCloseWarning := false
	for _, w := range resp.Data.Warnings {
		if strings.Contains(w, "could not be closed") {
			foundCloseWarning = true
			break
		}
	}
	if !foundCloseWarning {
		t.Errorf("expected close warning, got warnings: %v", resp.Data.Warnings)
	}
}

// TestHandleMoveIssue_PoolTimeout verifies 504 when pool.Get returns DeadlineExceeded.
func TestHandleMoveIssue_PoolTimeout(t *testing.T) {
	pool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) {
			return nil, context.DeadlineExceeded
		},
	}

	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssueWithPool(pool, pool, wsCfg)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected 504, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp MoveIssueResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error != "daemon not available" {
		t.Errorf("error=%q, want %q", resp.Error, "daemon not available")
	}
}

// TestHandleMoveIssue_WorkspaceConfigNotAvailable verifies 400 when
// workspaceConfigFn is nil.
func TestHandleMoveIssue_WorkspaceConfigNotAvailable(t *testing.T) {
	pool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) { return &mockMoveClient{}, nil },
		putFunc: func(c issueMover) {},
	}

	handler := handleMoveIssueWithPool(pool, pool, nil)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp MoveIssueResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error != "workspace configuration not available" {
		t.Errorf("error=%q, want %q", resp.Error, "workspace configuration not available")
	}
}

// TestHandleMoveIssue_CreateFails verifies 500 when creating issue in target workspace fails.
func TestHandleMoveIssue_CreateFails(t *testing.T) {
	sourceData := makeSourceIssueJSON("src-001", "Fix the bug", "open", "")

	client := &mockMoveClient{
		showFunc: func(args *rpc.ShowArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true, Data: sourceData}, nil
		},
		createFunc: func(args *rpc.CreateArgs) (*rpc.Response, error) {
			return nil, errors.New("database write failed")
		},
	}

	pool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) { return client, nil },
		putFunc: func(c issueMover) {},
	}

	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssueWithPool(pool, pool, wsCfg)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp MoveIssueResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !strings.Contains(resp.Error, "failed to create issue") {
		t.Errorf("error=%q, want it to contain %q", resp.Error, "failed to create issue")
	}
}

// TestHandleMoveIssue_CreateResponseFails verifies 500 when Create returns
// Success=false.
func TestHandleMoveIssue_CreateResponseFails(t *testing.T) {
	sourceData := makeSourceIssueJSON("src-001", "Fix the bug", "open", "")

	client := &mockMoveClient{
		showFunc: func(args *rpc.ShowArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true, Data: sourceData}, nil
		},
		createFunc: func(args *rpc.CreateArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: false, Error: "validation failed"}, nil
		},
	}

	pool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) { return client, nil },
		putFunc: func(c issueMover) {},
	}

	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssueWithPool(pool, pool, wsCfg)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp MoveIssueResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !strings.Contains(resp.Error, "validation failed") {
		t.Errorf("error=%q, want it to contain %q", resp.Error, "validation failed")
	}
}

// TestHandleMoveIssue_AssigneeWarning verifies that an issue with an active
// assignee produces a warning in the response.
func TestHandleMoveIssue_AssigneeWarning(t *testing.T) {
	sourceData := makeSourceIssueJSON("src-001", "Active task", "in_progress", "agent-42")
	createdData := makeSourceIssueJSON("tgt-001", "Active task", "open", "")

	client := &mockMoveClient{
		showFunc: func(args *rpc.ShowArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true, Data: sourceData}, nil
		},
		createFunc: func(args *rpc.CreateArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true, Data: createdData}, nil
		},
		addCommentFn: func(args *rpc.CommentAddArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true}, nil
		},
		closeIssueFunc: func(args *rpc.CloseArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true}, nil
		},
	}

	pool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) { return client, nil },
		putFunc: func(c issueMover) {},
	}

	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssueWithPool(pool, pool, wsCfg)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp MoveIssueResponse
	json.NewDecoder(rec.Body).Decode(&resp)

	if !resp.Success {
		t.Error("expected success=true")
	}

	foundAgentWarning := false
	for _, w := range resp.Data.Warnings {
		if strings.Contains(w, "agent-42") && strings.Contains(w, "will not stop") {
			foundAgentWarning = true
			break
		}
	}
	if !foundAgentWarning {
		t.Errorf("expected agent warning, got warnings: %v", resp.Data.Warnings)
	}
}

// TestHandleMoveIssue_InvalidRequestBody verifies 400 for invalid JSON.
func TestHandleMoveIssue_InvalidRequestBody(t *testing.T) {
	pool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) { return &mockMoveClient{}, nil },
		putFunc: func(c issueMover) {},
	}

	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssueWithPool(pool, pool, wsCfg)

	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-001/move", strings.NewReader(`{not json`))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp MoveIssueResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error != "invalid request body" {
		t.Errorf("error=%q, want %q", resp.Error, "invalid request body")
	}
}

// TestHandleMoveIssue_ClientReturnedToPool verifies the client is always
// returned to the pool, even on success.
func TestHandleMoveIssue_ClientReturnedToPool(t *testing.T) {
	putCalled := false
	sourceData := makeSourceIssueJSON("src-001", "Fix the bug", "open", "")
	createdData := makeSourceIssueJSON("tgt-001", "Fix the bug", "open", "")

	client := &mockMoveClient{
		showFunc: func(args *rpc.ShowArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true, Data: sourceData}, nil
		},
		createFunc: func(args *rpc.CreateArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true, Data: createdData}, nil
		},
		addCommentFn: func(args *rpc.CommentAddArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true}, nil
		},
		closeIssueFunc: func(args *rpc.CloseArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true}, nil
		},
	}

	pool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) { return client, nil },
		putFunc: func(c issueMover) { putCalled = true },
	}

	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssueWithPool(pool, pool, wsCfg)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !putCalled {
		t.Error("Put() was not called - client not returned to pool")
	}
}

// TestHandleMoveIssue_PoolConnectionError verifies 503 when pool.Get fails
// with a non-timeout error.
func TestHandleMoveIssue_PoolConnectionError(t *testing.T) {
	pool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) {
			return nil, errors.New("pool closed")
		},
	}

	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssueWithPool(pool, pool, wsCfg)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandleMoveIssue_AddCommentFails_StillSucceeds verifies that when
// AddComment fails, the move still succeeds with a warning.
func TestHandleMoveIssue_AddCommentFails_StillSucceeds(t *testing.T) {
	sourceData := makeSourceIssueJSON("src-001", "Fix the bug", "open", "")
	createdData := makeSourceIssueJSON("tgt-001", "Fix the bug", "open", "")

	client := &mockMoveClient{
		showFunc: func(args *rpc.ShowArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true, Data: sourceData}, nil
		},
		createFunc: func(args *rpc.CreateArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true, Data: createdData}, nil
		},
		addCommentFn: func(args *rpc.CommentAddArgs) (*rpc.Response, error) {
			return nil, errors.New("comment write failed")
		},
		closeIssueFunc: func(args *rpc.CloseArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true}, nil
		},
	}

	pool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) { return client, nil },
		putFunc: func(c issueMover) {},
	}

	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssueWithPool(pool, pool, wsCfg)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp MoveIssueResponse
	json.NewDecoder(rec.Body).Decode(&resp)

	if !resp.Success {
		t.Error("expected success=true")
	}

	foundCommentWarning := false
	for _, w := range resp.Data.Warnings {
		if strings.Contains(w, "comment") {
			foundCommentWarning = true
			break
		}
	}
	if !foundCommentWarning {
		t.Errorf("expected comment warning, got warnings: %v", resp.Data.Warnings)
	}
}

// TestHandleMoveIssue_WorkspaceConfigError verifies 500 when workspaceConfigFn
// returns an error.
func TestHandleMoveIssue_WorkspaceConfigError(t *testing.T) {
	pool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) { return &mockMoveClient{}, nil },
		putFunc: func(c issueMover) {},
	}

	errCfg := func() (*WorkspaceData, error) {
		return nil, errors.New("config file not found")
	}
	handler := handleMoveIssueWithPool(pool, pool, errCfg)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp MoveIssueResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !strings.Contains(resp.Error, "failed to load workspace config") {
		t.Errorf("error=%q, want it to contain %q", resp.Error, "failed to load workspace config")
	}
}

// TestHandleMoveIssue_ShowGenericError verifies 500 for non-"not found" RPC errors.
func TestHandleMoveIssue_ShowGenericError(t *testing.T) {
	client := &mockMoveClient{
		showFunc: func(args *rpc.ShowArgs) (*rpc.Response, error) {
			return nil, errors.New("database connection lost")
		},
	}

	pool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) { return client, nil },
		putFunc: func(c issueMover) {},
	}

	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssueWithPool(pool, pool, wsCfg)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandleMoveIssue_CreateResponseUnmarshalError verifies 500 when Create
// returns success but Data contains invalid JSON.
func TestHandleMoveIssue_CreateResponseUnmarshalError(t *testing.T) {
	sourceData := makeSourceIssueJSON("src-001", "Fix the bug", "open", "")

	client := &mockMoveClient{
		showFunc: func(args *rpc.ShowArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true, Data: sourceData}, nil
		},
		createFunc: func(args *rpc.CreateArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true, Data: json.RawMessage(`not json`)}, nil
		},
	}

	pool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) { return client, nil },
		putFunc: func(c issueMover) {},
	}

	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssueWithPool(pool, pool, wsCfg)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp MoveIssueResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !strings.Contains(resp.Error, "failed to parse response") {
		t.Errorf("error=%q, want it to contain %q", resp.Error, "failed to parse response")
	}
}

// TestHandleMoveIssue_ShowResponseUnmarshalError verifies 500 when Show
// returns success but Data contains invalid JSON.
func TestHandleMoveIssue_ShowResponseUnmarshalError(t *testing.T) {
	client := &mockMoveClient{
		showFunc: func(args *rpc.ShowArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true, Data: json.RawMessage(`not valid json`)}, nil
		},
	}

	pool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) { return client, nil },
		putFunc: func(c issueMover) {},
	}

	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssueWithPool(pool, pool, wsCfg)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp MoveIssueResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !strings.Contains(resp.Error, "failed to parse source issue") {
		t.Errorf("error=%q, want it to contain %q", resp.Error, "failed to parse source issue")
	}
}

// TestHandleMoveIssue_ResponseHasJSONContentType verifies all responses
// have Content-Type: application/json.
func TestHandleMoveIssue_ResponseHasJSONContentType(t *testing.T) {
	// Test the pool-nil case
	handler := handleMoveIssueWithPool(nil, nil, testWorkspaceConfigFn("alpha", defaultWorkspaces()))

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type=%q, want %q", ct, "application/json")
	}
}

// --- Cross-workspace move tests ---

// TestMoveIssue_CrossWorkspace_Success verifies that Create goes through the
// target pool while Show/AddComment/CloseIssue go through the source pool.
func TestMoveIssue_CrossWorkspace_Success(t *testing.T) {
	sourceData := makeSourceIssueJSON("src-001", "Fix the bug", "open", "")
	createdData := makeSourceIssueJSON("tgt-001", "Fix the bug", "open", "")

	var sourceCreateCalled, targetCreateCalled bool

	sourceClient := &mockMoveClient{
		showFunc: func(args *rpc.ShowArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true, Data: sourceData}, nil
		},
		createFunc: func(args *rpc.CreateArgs) (*rpc.Response, error) {
			sourceCreateCalled = true
			return &rpc.Response{Success: true, Data: createdData}, nil
		},
		addCommentFn: func(args *rpc.CommentAddArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true}, nil
		},
		closeIssueFunc: func(args *rpc.CloseArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true}, nil
		},
	}

	targetClient := &mockMoveClient{
		createFunc: func(args *rpc.CreateArgs) (*rpc.Response, error) {
			targetCreateCalled = true
			if args.Title != "Fix the bug" {
				t.Errorf("target Create Title=%q, want %q", args.Title, "Fix the bug")
			}
			return &rpc.Response{Success: true, Data: createdData}, nil
		},
	}

	sourcePool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) { return sourceClient, nil },
		putFunc: func(c issueMover) {},
	}
	targetPool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) { return targetClient, nil },
		putFunc: func(c issueMover) {},
	}

	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssueWithPool(sourcePool, targetPool, wsCfg)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws-alpha/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if sourceCreateCalled {
		t.Error("Create was called on the source client — should use target")
	}
	if !targetCreateCalled {
		t.Error("Create was NOT called on the target client")
	}

	var resp MoveIssueResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !resp.Success {
		t.Errorf("expected success=true, got false: %s", resp.Error)
	}
	if resp.Data.SourceID != "src-001" {
		t.Errorf("SourceID=%q, want %q", resp.Data.SourceID, "src-001")
	}
	if resp.Data.TargetID != "tgt-001" {
		t.Errorf("TargetID=%q, want %q", resp.Data.TargetID, "tgt-001")
	}
}

// TestMoveIssue_CrossWorkspace_TargetPoolNil verifies 400 when targetPool is nil.
func TestMoveIssue_CrossWorkspace_TargetPoolNil(t *testing.T) {
	sourcePool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) { return &mockMoveClient{}, nil },
		putFunc: func(c issueMover) {},
	}

	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssueWithPool(sourcePool, nil, wsCfg)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws-alpha/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp MoveIssueResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error != "cross-workspace move requires multi-workspace mode" {
		t.Errorf("error=%q, want %q", resp.Error, "cross-workspace move requires multi-workspace mode")
	}
}

// TestMoveIssue_CrossWorkspace_TargetNotRegistered verifies 400 when the
// target pool returns ErrWorkspaceNotRegistered.
func TestMoveIssue_CrossWorkspace_TargetNotRegistered(t *testing.T) {
	sourceData := makeSourceIssueJSON("src-001", "Fix the bug", "open", "")

	sourcePool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) {
			return &mockMoveClient{
				showFunc: func(args *rpc.ShowArgs) (*rpc.Response, error) {
					return &rpc.Response{Success: true, Data: sourceData}, nil
				},
			}, nil
		},
		putFunc: func(c issueMover) {},
	}
	targetPool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) {
			return nil, fmt.Errorf("%w: %q", daemon.ErrWorkspaceNotRegistered, "beta-uuid")
		},
		putFunc: func(c issueMover) {},
	}

	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssueWithPool(sourcePool, targetPool, wsCfg)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws-alpha/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp MoveIssueResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !strings.Contains(resp.Error, "not registered") {
		t.Errorf("error=%q, want it to contain %q", resp.Error, "not registered")
	}
}

// TestMoveIssue_CrossWorkspace_TargetConnectionError verifies 502 when the
// target pool returns a connection error.
func TestMoveIssue_CrossWorkspace_TargetConnectionError(t *testing.T) {
	sourceData := makeSourceIssueJSON("src-001", "Fix the bug", "open", "")

	sourcePool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) {
			return &mockMoveClient{
				showFunc: func(args *rpc.ShowArgs) (*rpc.Response, error) {
					return &rpc.Response{Success: true, Data: sourceData}, nil
				},
			}, nil
		},
		putFunc: func(c issueMover) {},
	}
	targetPool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) {
			return nil, errors.New("connection refused")
		},
		putFunc: func(c issueMover) {},
	}

	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssueWithPool(sourcePool, targetPool, wsCfg)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws-alpha/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp MoveIssueResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error != "target workspace daemon not available" {
		t.Errorf("error=%q, want %q", resp.Error, "target workspace daemon not available")
	}
}

// TestMoveIssue_CrossWorkspace_CreateFailsOnTarget verifies that when Create
// fails on the target, the source issue is NOT closed.
func TestMoveIssue_CrossWorkspace_CreateFailsOnTarget(t *testing.T) {
	sourceData := makeSourceIssueJSON("src-001", "Fix the bug", "open", "")
	var sourceClosed bool

	sourcePool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) {
			return &mockMoveClient{
				showFunc: func(args *rpc.ShowArgs) (*rpc.Response, error) {
					return &rpc.Response{Success: true, Data: sourceData}, nil
				},
				closeIssueFunc: func(args *rpc.CloseArgs) (*rpc.Response, error) {
					sourceClosed = true
					return &rpc.Response{Success: true}, nil
				},
			}, nil
		},
		putFunc: func(c issueMover) {},
	}
	targetPool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) {
			return &mockMoveClient{
				createFunc: func(args *rpc.CreateArgs) (*rpc.Response, error) {
					return nil, errors.New("target daemon crashed")
				},
			}, nil
		},
		putFunc: func(c issueMover) {},
	}

	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssueWithPool(sourcePool, targetPool, wsCfg)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws-alpha/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}

	if sourceClosed {
		t.Error("source issue should NOT be closed when target Create fails")
	}
}

// TestMoveIssue_CrossWorkspace_TargetClientReturned verifies the target client
// is always returned to the target pool via Put.
func TestMoveIssue_CrossWorkspace_TargetClientReturned(t *testing.T) {
	sourceData := makeSourceIssueJSON("src-001", "Fix the bug", "open", "")
	createdData := makeSourceIssueJSON("tgt-001", "Fix the bug", "open", "")
	var targetPutCalled bool

	sourcePool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) {
			return &mockMoveClient{
				showFunc: func(args *rpc.ShowArgs) (*rpc.Response, error) {
					return &rpc.Response{Success: true, Data: sourceData}, nil
				},
				addCommentFn: func(args *rpc.CommentAddArgs) (*rpc.Response, error) {
					return &rpc.Response{Success: true}, nil
				},
				closeIssueFunc: func(args *rpc.CloseArgs) (*rpc.Response, error) {
					return &rpc.Response{Success: true}, nil
				},
			}, nil
		},
		putFunc: func(c issueMover) {},
	}
	targetPool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) {
			return &mockMoveClient{
				createFunc: func(args *rpc.CreateArgs) (*rpc.Response, error) {
					return &rpc.Response{Success: true, Data: createdData}, nil
				},
			}, nil
		},
		putFunc: func(c issueMover) { targetPutCalled = true },
	}

	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssueWithPool(sourcePool, targetPool, wsCfg)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws-alpha/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !targetPutCalled {
		t.Error("target pool Put() was not called — target client leaked")
	}
}

// TestMoveIssue_CrossWorkspace_ClientsReturnedToCorrectPools verifies that
// each client is returned to its issuing pool — sourceClient to sourcePool,
// targetClient to targetPool. This catches pool ownership regressions where
// a client could be returned to the wrong pool.
func TestMoveIssue_CrossWorkspace_ClientsReturnedToCorrectPools(t *testing.T) {
	sourceData := makeSourceIssueJSON("src-001", "Fix the bug", "open", "")
	createdData := makeSourceIssueJSON("tgt-001", "Fix the bug", "open", "")

	sourceClient := &mockMoveClient{
		showFunc: func(args *rpc.ShowArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true, Data: sourceData}, nil
		},
		addCommentFn: func(args *rpc.CommentAddArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true}, nil
		},
		closeIssueFunc: func(args *rpc.CloseArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true}, nil
		},
	}
	targetClient := &mockMoveClient{
		createFunc: func(args *rpc.CreateArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true, Data: createdData}, nil
		},
	}

	var sourcePutClient, targetPutClient issueMover
	sourcePool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) { return sourceClient, nil },
		putFunc: func(c issueMover) { sourcePutClient = c },
	}
	targetPool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) { return targetClient, nil },
		putFunc: func(c issueMover) { targetPutClient = c },
	}

	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssueWithPool(sourcePool, targetPool, wsCfg)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws-alpha/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if sourcePutClient != sourceClient {
		t.Error("sourcePool.Put was not called with sourceClient — client returned to wrong pool")
	}
	if targetPutClient != targetClient {
		t.Error("targetPool.Put was not called with targetClient — client returned to wrong pool")
	}
	if sourcePutClient == targetClient {
		t.Error("sourcePool received targetClient — ownership swapped")
	}
	if targetPutClient == sourceClient {
		t.Error("targetPool received sourceClient — ownership swapped")
	}
}

// TestMoveIssue_CrossWorkspace_TargetGetError_SourceReturned verifies that
// when targetPool.Get fails, the sourceClient is still returned to the
// sourcePool via Put (no leak).
func TestMoveIssue_CrossWorkspace_TargetGetError_SourceReturned(t *testing.T) {
	sourceData := makeSourceIssueJSON("src-001", "Fix the bug", "open", "")

	sourceClient := &mockMoveClient{
		showFunc: func(args *rpc.ShowArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true, Data: sourceData}, nil
		},
	}

	var sourcePutCalled bool
	sourcePool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) { return sourceClient, nil },
		putFunc: func(c issueMover) {
			sourcePutCalled = true
			if c != sourceClient {
				t.Error("sourcePool.Put received wrong client")
			}
		},
	}
	targetPool := &mockMovePool{
		getFunc: func(ctx context.Context) (issueMover, error) {
			return nil, errors.New("target daemon unreachable")
		},
		putFunc: func(c issueMover) {
			t.Error("targetPool.Put should not be called when Get fails")
		},
	}

	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssueWithPool(sourcePool, targetPool, wsCfg)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws-alpha/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", rec.Code, rec.Body.String())
	}
	if !sourcePutCalled {
		t.Error("sourcePool.Put was not called — source client leaked on target Get failure")
	}
}

// --- resolveWorkspaceID tests ---

func TestResolveWorkspaceID_WithID(t *testing.T) {
	wsData := &WorkspaceData{
		Workspaces: []WorkspaceSummary{
			{ID: "uuid-beta", Name: "beta"},
		},
	}
	got := resolveWorkspaceID(wsData, "beta")
	if got != "uuid-beta" {
		t.Errorf("resolveWorkspaceID()=%q, want %q", got, "uuid-beta")
	}
}

func TestResolveWorkspaceID_WithoutID(t *testing.T) {
	wsData := &WorkspaceData{
		Workspaces: []WorkspaceSummary{
			{Name: "beta"},
		},
	}
	got := resolveWorkspaceID(wsData, "beta")
	if got != "beta" {
		t.Errorf("resolveWorkspaceID()=%q, want %q", got, "beta")
	}
}

func TestResolveWorkspaceID_NotFound(t *testing.T) {
	wsData := &WorkspaceData{
		Workspaces: []WorkspaceSummary{
			{Name: "alpha"},
		},
	}
	got := resolveWorkspaceID(wsData, "nonexistent")
	if got != "nonexistent" {
		t.Errorf("resolveWorkspaceID()=%q, want %q", got, "nonexistent")
	}
}

// TestValidateMoveRequest_ReturnsWsData verifies that validateMoveRequest
// returns workspace data on success.
func TestValidateMoveRequest_ReturnsWsData(t *testing.T) {
	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	issueID, target, wsData, ok := validateMoveRequest(rec, req, wsCfg)
	if !ok {
		t.Fatalf("expected ok=true, got false: %s", rec.Body.String())
	}
	if issueID != "src-001" {
		t.Errorf("issueID=%q, want %q", issueID, "src-001")
	}
	if target != "beta" {
		t.Errorf("target=%q, want %q", target, "beta")
	}
	if wsData == nil {
		t.Fatal("expected wsData to be non-nil")
	}
	if wsData.Name != "alpha" {
		t.Errorf("wsData.Name=%q, want %q", wsData.Name, "alpha")
	}
}
