package webui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

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

// TestHandleMoveIssue_Success verifies the full move flow via service.MoveIssue.
func TestHandleMoveIssue_Success(t *testing.T) {
	svc := &mockIssueService{
		moveIssueFunc: func(ctx context.Context, params service.MoveIssueParams) (*service.MoveIssueResult, error) {
			if params.IssueID != "src-001" {
				t.Errorf("MoveIssue IssueID=%q, want %q", params.IssueID, "src-001")
			}
			if params.TargetWorkspace != "beta" {
				t.Errorf("MoveIssue TargetWorkspace=%q, want %q", params.TargetWorkspace, "beta")
			}
			return &service.MoveIssueResult{
				SourceID: "src-001",
				TargetID: "tgt-001",
			}, nil
		},
	}
	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssue(svc, wsCfg)

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
	svc := &mockIssueService{}
	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssue(svc, wsCfg)

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
	svc := &mockIssueService{}
	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssue(svc, wsCfg)

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

// TestHandleMoveIssue_TargetWorkspaceNotFound verifies error when target workspace does not exist.
func TestHandleMoveIssue_TargetWorkspaceNotFound(t *testing.T) {
	svc := &mockIssueService{
		moveIssueFunc: func(ctx context.Context, params service.MoveIssueParams) (*service.MoveIssueResult, error) {
			// The validator will reject the workspace before MoveIssue logic runs
			_, err := params.Validator.ValidateTarget(params.TargetWorkspace)
			if err != nil {
				return nil, err
			}
			return nil, nil
		},
	}
	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssue(svc, wsCfg)

	body := `{"target_workspace":"nonexistent"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	errMsg, _ := resp["error"].(string)
	if !strings.Contains(errMsg, "not found") {
		t.Errorf("error=%q, want it to contain %q", errMsg, "not found")
	}
}

// TestHandleMoveIssue_SameWorkspaceRejected verifies error when target matches current workspace.
func TestHandleMoveIssue_SameWorkspaceRejected(t *testing.T) {
	svc := &mockIssueService{
		moveIssueFunc: func(ctx context.Context, params service.MoveIssueParams) (*service.MoveIssueResult, error) {
			_, err := params.Validator.ValidateTarget(params.TargetWorkspace)
			if err != nil {
				return nil, err
			}
			return nil, nil
		},
	}
	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssue(svc, wsCfg)

	body := `{"target_workspace":"alpha"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	errMsg, _ := resp["error"].(string)
	if errMsg != "cannot move issue to the same workspace" {
		t.Errorf("error=%q, want %q", errMsg, "cannot move issue to the same workspace")
	}
}

// TestHandleMoveIssue_SourceIssueNotFound verifies 404 when the source issue does not exist.
func TestHandleMoveIssue_SourceIssueNotFound(t *testing.T) {
	svc := &mockIssueService{
		moveIssueFunc: func(ctx context.Context, params service.MoveIssueParams) (*service.MoveIssueResult, error) {
			return nil, service.ErrNotFound("issue not found: src-999")
		},
	}
	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssue(svc, wsCfg)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-999/move", strings.NewReader(body))
	req.SetPathValue("id", "src-999")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandleMoveIssue_SourceIssueClosed verifies error when the source issue is already closed.
func TestHandleMoveIssue_SourceIssueClosed(t *testing.T) {
	svc := &mockIssueService{
		moveIssueFunc: func(ctx context.Context, params service.MoveIssueParams) (*service.MoveIssueResult, error) {
			return nil, service.ErrValidation("cannot move a closed issue")
		},
	}
	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssue(svc, wsCfg)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandleMoveIssue_PartialFailure_CloseFails verifies that when create succeeds
// but close fails, the handler returns success with warnings.
func TestHandleMoveIssue_PartialFailure_CloseFails(t *testing.T) {
	svc := &mockIssueService{
		moveIssueFunc: func(ctx context.Context, params service.MoveIssueParams) (*service.MoveIssueResult, error) {
			return &service.MoveIssueResult{
				SourceID: "src-001",
				TargetID: "tgt-001",
				Warnings: []string{"source issue src-001 could not be closed: daemon timeout"},
			}, nil
		},
	}
	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssue(svc, wsCfg)

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

// TestHandleMoveIssue_ServiceTimeout verifies 504 when service returns timeout error.
func TestHandleMoveIssue_ServiceTimeout(t *testing.T) {
	svc := &mockIssueService{
		moveIssueFunc: func(ctx context.Context, params service.MoveIssueParams) (*service.MoveIssueResult, error) {
			return nil, service.ErrTimeout("daemon not available")
		},
	}
	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssue(svc, wsCfg)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected 504, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandleMoveIssue_WorkspaceConfigNotAvailable verifies error when
// workspaceConfigFn is nil.
func TestHandleMoveIssue_WorkspaceConfigNotAvailable(t *testing.T) {
	svc := &mockIssueService{
		moveIssueFunc: func(ctx context.Context, params service.MoveIssueParams) (*service.MoveIssueResult, error) {
			_, err := params.Validator.ValidateTarget(params.TargetWorkspace)
			if err != nil {
				return nil, err
			}
			return nil, nil
		},
	}
	handler := handleMoveIssue(svc, nil)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandleMoveIssue_CreateFails verifies 500 when creating issue in target workspace fails.
func TestHandleMoveIssue_CreateFails(t *testing.T) {
	svc := &mockIssueService{
		moveIssueFunc: func(ctx context.Context, params service.MoveIssueParams) (*service.MoveIssueResult, error) {
			return nil, service.ErrInternal("failed to create issue in target workspace", nil)
		},
	}
	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssue(svc, wsCfg)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandleMoveIssue_AssigneeWarning verifies that an issue with an active
// assignee produces a warning in the response.
func TestHandleMoveIssue_AssigneeWarning(t *testing.T) {
	svc := &mockIssueService{
		moveIssueFunc: func(ctx context.Context, params service.MoveIssueParams) (*service.MoveIssueResult, error) {
			return &service.MoveIssueResult{
				SourceID: "src-001",
				TargetID: "tgt-001",
				Warnings: []string{"agent agent-42 will not stop running on the source issue"},
			}, nil
		},
	}
	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssue(svc, wsCfg)

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
	svc := &mockIssueService{}
	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssue(svc, wsCfg)

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

// TestHandleMoveIssue_ServiceUnavailable verifies 503 when service returns unavailable error.
func TestHandleMoveIssue_ServiceUnavailable(t *testing.T) {
	svc := &mockIssueService{
		moveIssueFunc: func(ctx context.Context, params service.MoveIssueParams) (*service.MoveIssueResult, error) {
			return nil, service.ErrUnavailable("daemon not available")
		},
	}
	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssue(svc, wsCfg)

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
	svc := &mockIssueService{
		moveIssueFunc: func(ctx context.Context, params service.MoveIssueParams) (*service.MoveIssueResult, error) {
			return &service.MoveIssueResult{
				SourceID: "src-001",
				TargetID: "tgt-001",
				Warnings: []string{"failed to add comment on source issue"},
			}, nil
		},
	}
	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssue(svc, wsCfg)

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
	svc := &mockIssueService{
		moveIssueFunc: func(ctx context.Context, params service.MoveIssueParams) (*service.MoveIssueResult, error) {
			_, err := params.Validator.ValidateTarget(params.TargetWorkspace)
			if err != nil {
				return nil, err
			}
			return nil, nil
		},
	}
	errCfg := func() (*WorkspaceData, error) {
		return nil, errors.New("config file not found")
	}
	handler := handleMoveIssue(svc, errCfg)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandleMoveIssue_ShowGenericError verifies 500 for internal service errors.
func TestHandleMoveIssue_ShowGenericError(t *testing.T) {
	svc := &mockIssueService{
		moveIssueFunc: func(ctx context.Context, params service.MoveIssueParams) (*service.MoveIssueResult, error) {
			return nil, service.ErrInternal("database connection lost", nil)
		},
	}
	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssue(svc, wsCfg)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandleMoveIssue_ResponseHasJSONContentType verifies all responses
// have Content-Type: application/json.
func TestHandleMoveIssue_ResponseHasJSONContentType(t *testing.T) {
	svc := &mockIssueService{
		moveIssueFunc: func(ctx context.Context, params service.MoveIssueParams) (*service.MoveIssueResult, error) {
			return nil, service.ErrUnavailable("not available")
		},
	}
	handler := handleMoveIssue(svc, testWorkspaceConfigFn("alpha", defaultWorkspaces()))

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

// TestHandleMoveIssue_ValidatorPassedToService verifies the workspace validator
// is correctly wired from the handler to the service.
func TestHandleMoveIssue_ValidatorPassedToService(t *testing.T) {
	var receivedValidator service.WorkspaceValidator
	svc := &mockIssueService{
		moveIssueFunc: func(ctx context.Context, params service.MoveIssueParams) (*service.MoveIssueResult, error) {
			receivedValidator = params.Validator
			return &service.MoveIssueResult{
				SourceID: "src-001",
				TargetID: "tgt-001",
			}, nil
		},
	}
	wsCfg := testWorkspaceConfigFn("alpha", defaultWorkspaces())
	handler := handleMoveIssue(svc, wsCfg)

	body := `{"target_workspace":"beta"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/src-001/move", strings.NewReader(body))
	req.SetPathValue("id", "src-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if receivedValidator == nil {
		t.Fatal("expected Validator to be passed to service")
	}
	if receivedValidator.CurrentWorkspace() != "alpha" {
		t.Errorf("CurrentWorkspace()=%q, want %q", receivedValidator.CurrentWorkspace(), "alpha")
	}
}
