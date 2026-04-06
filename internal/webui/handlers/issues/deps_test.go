package issues

// Tests here supplement the dependency handler tests in handlers_test.go,
// covering additional code paths not exercised there.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// ---------------------------------------------------------------------------
// handleAddDependency -- service-based tests
// ---------------------------------------------------------------------------

// TestHandleAddDep_DuplicateDependency verifies 409 when service returns
// a conflict error containing "already exists".
func TestHandleAddDep_DuplicateDependency(t *testing.T) {
	svc := &mockIssueService{
		addDependencyFunc: func(ctx context.Context, params service.AddDependencyParams) error {
			return service.ErrConflict("dependency already exists")
		},
	}
	handler := handleAddDependency(svc)

	body := `{"depends_on_id":"issue-2"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/issue-1/deps", strings.NewReader(body))
	req.SetPathValue("id", "issue-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp DependencyResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error != "dependency already exists" {
		t.Errorf("expected error %q, got %q", "dependency already exists", resp.Error)
	}
}

// TestHandleAddDep_CircularDependency verifies 409 when service returns
// a conflict error indicating a cycle.
func TestHandleAddDep_CircularDependency(t *testing.T) {
	svc := &mockIssueService{
		addDependencyFunc: func(ctx context.Context, params service.AddDependencyParams) error {
			return service.ErrConflict("dependency would create a cycle")
		},
	}
	handler := handleAddDependency(svc)

	body := `{"depends_on_id":"issue-2"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/issue-1/deps", strings.NewReader(body))
	req.SetPathValue("id", "issue-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp DependencyResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error != "dependency would create a cycle" {
		t.Errorf("expected error %q, got %q", "dependency would create a cycle", resp.Error)
	}
}

// TestHandleAddDep_ServiceUnavailable verifies 503 when service returns
// unavailable error.
func TestHandleAddDep_ServiceUnavailable(t *testing.T) {
	svc := &mockIssueService{
		addDependencyFunc: func(ctx context.Context, params service.AddDependencyParams) error {
			return service.ErrUnavailable("daemon not available")
		},
	}
	handler := handleAddDependency(svc)

	body := `{"depends_on_id":"issue-2"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/issue-1/deps", strings.NewReader(body))
	req.SetPathValue("id", "issue-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp DependencyResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error != "daemon not available" {
		t.Errorf("expected error %q, got %q", "daemon not available", resp.Error)
	}
}

// TestHandleAddDep_Timeout verifies 504 when service returns timeout error.
func TestHandleAddDep_Timeout(t *testing.T) {
	svc := &mockIssueService{
		addDependencyFunc: func(ctx context.Context, params service.AddDependencyParams) error {
			return service.ErrTimeout("daemon not available")
		},
	}
	handler := handleAddDependency(svc)

	body := `{"depends_on_id":"issue-2"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/issue-1/deps", strings.NewReader(body))
	req.SetPathValue("id", "issue-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected 504, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp DependencyResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error != "daemon not available" {
		t.Errorf("expected error %q, got %q", "daemon not available", resp.Error)
	}
}

// TestHandleAddDep_InternalError verifies 500 when service returns
// a generic internal error.
func TestHandleAddDep_InternalError(t *testing.T) {
	svc := &mockIssueService{
		addDependencyFunc: func(ctx context.Context, params service.AddDependencyParams) error {
			return service.ErrInternal("something went wrong", nil)
		},
	}
	handler := handleAddDependency(svc)

	body := `{"depends_on_id":"issue-2"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/issue-1/deps", strings.NewReader(body))
	req.SetPathValue("id", "issue-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp DependencyResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error != "something went wrong" {
		t.Errorf("expected error %q, got %q", "something went wrong", resp.Error)
	}
}

// TestHandleAddDep_NotFound verifies 404 when service returns not-found error.
func TestHandleAddDep_NotFound(t *testing.T) {
	svc := &mockIssueService{
		addDependencyFunc: func(ctx context.Context, params service.AddDependencyParams) error {
			return service.ErrNotFound("issue not found")
		},
	}
	handler := handleAddDependency(svc)

	body := `{"depends_on_id":"issue-2"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/issue-1/deps", strings.NewReader(body))
	req.SetPathValue("id", "issue-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp DependencyResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error != "issue not found" {
		t.Errorf("expected error %q, got %q", "issue not found", resp.Error)
	}
}

// TestHandleAddDep_Validation verifies 400 when service returns validation error
// (e.g. missing depends_on_id or self-dependency).
func TestHandleAddDep_Validation(t *testing.T) {
	svc := &mockIssueService{
		addDependencyFunc: func(ctx context.Context, params service.AddDependencyParams) error {
			return service.ErrValidation("depends_on_id is required")
		},
	}
	handler := handleAddDependency(svc)

	body := `{"depends_on_id":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/issue-1/deps", strings.NewReader(body))
	req.SetPathValue("id", "issue-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp DependencyResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error != "depends_on_id is required" {
		t.Errorf("expected error %q, got %q", "depends_on_id is required", resp.Error)
	}
}

// TestHandleAddDep_SelfDependencyValidation verifies 400 when service rejects
// self-dependency.
func TestHandleAddDep_SelfDependencyValidation(t *testing.T) {
	svc := &mockIssueService{
		addDependencyFunc: func(ctx context.Context, params service.AddDependencyParams) error {
			return service.ErrValidation("cannot add self-dependency")
		},
	}
	handler := handleAddDependency(svc)

	body := `{"depends_on_id":"issue-1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/issue-1/deps", strings.NewReader(body))
	req.SetPathValue("id", "issue-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp DependencyResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error != "cannot add self-dependency" {
		t.Errorf("expected error %q, got %q", "cannot add self-dependency", resp.Error)
	}
}

// TestHandleAddDep_MissingIssueID verifies 400 for missing issue ID path param.
func TestHandleAddDep_MissingIssueID(t *testing.T) {
	svc := &mockIssueService{}
	handler := handleAddDependency(svc)

	body := `{"depends_on_id":"issue-2"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues//deps", strings.NewReader(body))
	req.SetPathValue("id", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp DependencyResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error != "missing issue ID" {
		t.Errorf("expected error %q, got %q", "missing issue ID", resp.Error)
	}
}

// TestHandleAddDep_InvalidBody verifies 400 for malformed JSON body.
func TestHandleAddDep_InvalidBody(t *testing.T) {
	svc := &mockIssueService{}
	handler := handleAddDependency(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/issues/issue-1/deps", strings.NewReader(`not json`))
	req.SetPathValue("id", "issue-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp DependencyResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error != "invalid request body" {
		t.Errorf("expected error %q, got %q", "invalid request body", resp.Error)
	}
}

// TestHandleAddDep_Success verifies a successful dependency creation.
func TestHandleAddDep_Success(t *testing.T) {
	var capturedParams service.AddDependencyParams

	svc := &mockIssueService{
		addDependencyFunc: func(ctx context.Context, params service.AddDependencyParams) error {
			capturedParams = params
			return nil
		},
	}
	handler := handleAddDependency(svc)

	body := `{"depends_on_id":"issue-2","dep_type":"blocks"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/issue-1/deps", strings.NewReader(body))
	req.SetPathValue("id", "issue-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if capturedParams.IssueID != "issue-1" {
		t.Errorf("expected IssueID 'issue-1', got %q", capturedParams.IssueID)
	}
	if capturedParams.DependsOnID != "issue-2" {
		t.Errorf("expected DependsOnID 'issue-2', got %q", capturedParams.DependsOnID)
	}
	if capturedParams.DepType != "blocks" {
		t.Errorf("expected DepType 'blocks', got %q", capturedParams.DepType)
	}

	var resp DependencyResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !resp.Success {
		t.Errorf("expected success=true, got false (error: %s)", resp.Error)
	}
}

// ---------------------------------------------------------------------------
// handleRemoveDependency -- service-based tests
// ---------------------------------------------------------------------------

// TestHandleRemoveDep_Timeout verifies 504 when service returns timeout error.
func TestHandleRemoveDep_Timeout(t *testing.T) {
	svc := &mockIssueService{
		removeDependencyFunc: func(ctx context.Context, params service.RemoveDependencyParams) error {
			return service.ErrTimeout("daemon not available")
		},
	}
	handler := handleRemoveDependency(svc)

	req := httptest.NewRequest(http.MethodDelete, "/api/issues/issue-1/deps/issue-2", nil)
	req.SetPathValue("id", "issue-1")
	req.SetPathValue("depId", "issue-2")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected 504, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp DependencyResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error != "daemon not available" {
		t.Errorf("expected error %q, got %q", "daemon not available", resp.Error)
	}
}

// TestHandleRemoveDep_ServiceUnavailable verifies 503 when service returns
// unavailable error.
func TestHandleRemoveDep_ServiceUnavailable(t *testing.T) {
	svc := &mockIssueService{
		removeDependencyFunc: func(ctx context.Context, params service.RemoveDependencyParams) error {
			return service.ErrUnavailable("daemon not available")
		},
	}
	handler := handleRemoveDependency(svc)

	req := httptest.NewRequest(http.MethodDelete, "/api/issues/issue-1/deps/issue-2", nil)
	req.SetPathValue("id", "issue-1")
	req.SetPathValue("depId", "issue-2")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandleRemoveDep_NotFound verifies 404 when service returns not-found error.
func TestHandleRemoveDep_NotFound(t *testing.T) {
	svc := &mockIssueService{
		removeDependencyFunc: func(ctx context.Context, params service.RemoveDependencyParams) error {
			return service.ErrNotFound("dependency not found")
		},
	}
	handler := handleRemoveDependency(svc)

	req := httptest.NewRequest(http.MethodDelete, "/api/issues/issue-1/deps/issue-99", nil)
	req.SetPathValue("id", "issue-1")
	req.SetPathValue("depId", "issue-99")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp DependencyResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error != "dependency not found" {
		t.Errorf("expected error %q, got %q", "dependency not found", resp.Error)
	}
}

// TestHandleRemoveDep_InternalError verifies 500 when service returns
// a generic internal error.
func TestHandleRemoveDep_InternalError(t *testing.T) {
	svc := &mockIssueService{
		removeDependencyFunc: func(ctx context.Context, params service.RemoveDependencyParams) error {
			return service.ErrInternal("internal failure", nil)
		},
	}
	handler := handleRemoveDependency(svc)

	req := httptest.NewRequest(http.MethodDelete, "/api/issues/issue-1/deps/issue-2", nil)
	req.SetPathValue("id", "issue-1")
	req.SetPathValue("depId", "issue-2")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp DependencyResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error != "internal failure" {
		t.Errorf("expected error %q, got %q", "internal failure", resp.Error)
	}
}

// TestHandleRemoveDep_MissingIssueID verifies 400 for missing issue ID.
func TestHandleRemoveDep_MissingIssueID(t *testing.T) {
	svc := &mockIssueService{}
	handler := handleRemoveDependency(svc)

	req := httptest.NewRequest(http.MethodDelete, "/api/issues//deps/issue-2", nil)
	req.SetPathValue("id", "")
	req.SetPathValue("depId", "issue-2")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp DependencyResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error != "missing issue ID" {
		t.Errorf("expected error %q, got %q", "missing issue ID", resp.Error)
	}
}

// TestHandleRemoveDep_MissingDepID verifies 400 for missing dependency ID.
func TestHandleRemoveDep_MissingDepID(t *testing.T) {
	svc := &mockIssueService{}
	handler := handleRemoveDependency(svc)

	req := httptest.NewRequest(http.MethodDelete, "/api/issues/issue-1/deps/", nil)
	req.SetPathValue("id", "issue-1")
	req.SetPathValue("depId", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp DependencyResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error != "missing dependency ID" {
		t.Errorf("expected error %q, got %q", "missing dependency ID", resp.Error)
	}
}

// TestHandleRemoveDep_Success verifies a successful dependency removal.
func TestHandleRemoveDep_Success(t *testing.T) {
	var capturedParams service.RemoveDependencyParams

	svc := &mockIssueService{
		removeDependencyFunc: func(ctx context.Context, params service.RemoveDependencyParams) error {
			capturedParams = params
			return nil
		},
	}
	handler := handleRemoveDependency(svc)

	req := httptest.NewRequest(http.MethodDelete, "/api/issues/issue-1/deps/issue-2", nil)
	req.SetPathValue("id", "issue-1")
	req.SetPathValue("depId", "issue-2")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if capturedParams.IssueID != "issue-1" {
		t.Errorf("expected IssueID 'issue-1', got %q", capturedParams.IssueID)
	}
	if capturedParams.DepID != "issue-2" {
		t.Errorf("expected DepID 'issue-2', got %q", capturedParams.DepID)
	}

	var resp DependencyResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !resp.Success {
		t.Errorf("expected success=true, got false (error: %s)", resp.Error)
	}
}
