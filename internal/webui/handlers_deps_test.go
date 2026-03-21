package webui

// Tests here supplement the dependency handler tests in handlers_test.go,
// covering additional code paths not exercised there.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/rpc"
)

// ---------------------------------------------------------------------------
// handleAddDependencyWithPool -- uncovered paths
// ---------------------------------------------------------------------------

// TestHandleAddDep_DuplicateDependency_RPCError verifies 409 when RPC returns
// an error containing "already exists".
func TestHandleAddDep_DuplicateDependency_RPCError(t *testing.T) {
	client := &mockDependencyClient{
		addDependencyFunc: func(args *rpc.DepAddArgs) (*rpc.Response, error) {
			return nil, errors.New("dependency already exists")
		},
	}
	pool := &mockDependencyPool{
		getFunc: func(ctx context.Context) (dependencyManager, error) { return client, nil },
		putFunc: func(c dependencyManager) {},
	}
	handler := handleAddDependencyWithPool(pool)

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
	if resp.Error != "internal server error" {
		t.Errorf("expected error %q, got %q", "internal server error", resp.Error)
	}
}

// TestHandleAddDep_DuplicateDependency_RPCSuccessFalse verifies 409 when
// resp.Success=false and resp.Error contains "already exists".
func TestHandleAddDep_DuplicateDependency_RPCSuccessFalse(t *testing.T) {
	client := &mockDependencyClient{
		addDependencyFunc: func(args *rpc.DepAddArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: false, Error: "dependency already exists"}, nil
		},
	}
	pool := &mockDependencyPool{
		getFunc: func(ctx context.Context) (dependencyManager, error) { return client, nil },
		putFunc: func(c dependencyManager) {},
	}
	handler := handleAddDependencyWithPool(pool)

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

// TestHandleAddDep_CircularDependency_RPCSuccessFalse verifies 409 when
// resp.Success=false and resp.Error contains "cycle".
func TestHandleAddDep_CircularDependency_RPCSuccessFalse(t *testing.T) {
	client := &mockDependencyClient{
		addDependencyFunc: func(args *rpc.DepAddArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: false, Error: "dependency would create a cycle"}, nil
		},
	}
	pool := &mockDependencyPool{
		getFunc: func(ctx context.Context) (dependencyManager, error) { return client, nil },
		putFunc: func(c dependencyManager) {},
	}
	handler := handleAddDependencyWithPool(pool)

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

// TestHandleAddDep_PoolTimeout verifies 504 when pool.Get returns
// context.DeadlineExceeded.
func TestHandleAddDep_PoolTimeout(t *testing.T) {
	pool := &mockDependencyPool{
		getFunc: func(ctx context.Context) (dependencyManager, error) {
			return nil, context.DeadlineExceeded
		},
	}
	handler := handleAddDependencyWithPool(pool)

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

// TestHandleAddDep_PoolGetNonTimeoutError verifies 503 when pool.Get fails
// with a non-deadline error.
func TestHandleAddDep_PoolGetNonTimeoutError(t *testing.T) {
	pool := &mockDependencyPool{
		getFunc: func(ctx context.Context) (dependencyManager, error) {
			return nil, errors.New("pool exhausted")
		},
	}
	handler := handleAddDependencyWithPool(pool)

	body := `{"depends_on_id":"issue-2"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/issue-1/deps", strings.NewReader(body))
	req.SetPathValue("id", "issue-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandleAddDep_RPCSuccessFalseGeneric verifies 500 when resp.Success=false
// with a generic error message (not "not found", "cycle", or "already exists").
func TestHandleAddDep_RPCSuccessFalseGeneric(t *testing.T) {
	client := &mockDependencyClient{
		addDependencyFunc: func(args *rpc.DepAddArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: false, Error: "something went wrong"}, nil
		},
	}
	pool := &mockDependencyPool{
		getFunc: func(ctx context.Context) (dependencyManager, error) { return client, nil },
		putFunc: func(c dependencyManager) {},
	}
	handler := handleAddDependencyWithPool(pool)

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

// TestHandleAddDep_RPCSuccessFalseNotFound verifies 404 when resp.Success=false
// and resp.Error contains "not found".
func TestHandleAddDep_RPCSuccessFalseNotFound(t *testing.T) {
	client := &mockDependencyClient{
		addDependencyFunc: func(args *rpc.DepAddArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: false, Error: "issue not found"}, nil
		},
	}
	pool := &mockDependencyPool{
		getFunc: func(ctx context.Context) (dependencyManager, error) { return client, nil },
		putFunc: func(c dependencyManager) {},
	}
	handler := handleAddDependencyWithPool(pool)

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

// TestHandleAddDep_RPCErrorNotFound verifies 404 when RPC returns an error
// containing "not found".
func TestHandleAddDep_RPCErrorNotFound(t *testing.T) {
	client := &mockDependencyClient{
		addDependencyFunc: func(args *rpc.DepAddArgs) (*rpc.Response, error) {
			return nil, errors.New("issue not found")
		},
	}
	pool := &mockDependencyPool{
		getFunc: func(ctx context.Context) (dependencyManager, error) { return client, nil },
		putFunc: func(c dependencyManager) {},
	}
	handler := handleAddDependencyWithPool(pool)

	body := `{"depends_on_id":"issue-2"}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/issue-1/deps", strings.NewReader(body))
	req.SetPathValue("id", "issue-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandleAddDep_RPCErrorGeneric verifies 500 when RPC returns a generic error
// (not "not found", "cycle", or "already exists").
func TestHandleAddDep_RPCErrorGeneric(t *testing.T) {
	client := &mockDependencyClient{
		addDependencyFunc: func(args *rpc.DepAddArgs) (*rpc.Response, error) {
			return nil, errors.New("connection refused")
		},
	}
	pool := &mockDependencyPool{
		getFunc: func(ctx context.Context) (dependencyManager, error) { return client, nil },
		putFunc: func(c dependencyManager) {},
	}
	handler := handleAddDependencyWithPool(pool)

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
	if resp.Error != "internal server error" {
		t.Errorf("expected error %q, got %q", "internal server error", resp.Error)
	}
}

// ---------------------------------------------------------------------------
// handleRemoveDependencyWithPool -- uncovered paths
// ---------------------------------------------------------------------------

// TestHandleRemoveDep_PoolTimeout verifies 504 when pool.Get returns
// context.DeadlineExceeded.
func TestHandleRemoveDep_PoolTimeout(t *testing.T) {
	pool := &mockDependencyPool{
		getFunc: func(ctx context.Context) (dependencyManager, error) {
			return nil, context.DeadlineExceeded
		},
	}
	handler := handleRemoveDependencyWithPool(pool)

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

// TestHandleRemoveDep_PoolGetNonTimeoutError verifies 503 when pool.Get fails
// with a non-deadline error.
func TestHandleRemoveDep_PoolGetNonTimeoutError(t *testing.T) {
	pool := &mockDependencyPool{
		getFunc: func(ctx context.Context) (dependencyManager, error) {
			return nil, errors.New("pool exhausted")
		},
	}
	handler := handleRemoveDependencyWithPool(pool)

	req := httptest.NewRequest(http.MethodDelete, "/api/issues/issue-1/deps/issue-2", nil)
	req.SetPathValue("id", "issue-1")
	req.SetPathValue("depId", "issue-2")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandleRemoveDep_NonExistentNotFoundRPCSuccessFalse verifies 404 when
// resp.Success=false and resp.Error contains "not found".
func TestHandleRemoveDep_NonExistentNotFoundRPCSuccessFalse(t *testing.T) {
	client := &mockDependencyClient{
		removeDependencyFunc: func(args *rpc.DepRemoveArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: false, Error: "dependency not found"}, nil
		},
	}
	pool := &mockDependencyPool{
		getFunc: func(ctx context.Context) (dependencyManager, error) { return client, nil },
		putFunc: func(c dependencyManager) {},
	}
	handler := handleRemoveDependencyWithPool(pool)

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

// TestHandleRemoveDep_RPCSuccessFalseGeneric verifies 500 when resp.Success=false
// with a generic error (no "not found").
func TestHandleRemoveDep_RPCSuccessFalseGeneric(t *testing.T) {
	client := &mockDependencyClient{
		removeDependencyFunc: func(args *rpc.DepRemoveArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: false, Error: "internal failure"}, nil
		},
	}
	pool := &mockDependencyPool{
		getFunc: func(ctx context.Context) (dependencyManager, error) { return client, nil },
		putFunc: func(c dependencyManager) {},
	}
	handler := handleRemoveDependencyWithPool(pool)

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

// TestHandleRemoveDep_RPCErrorGeneric verifies 500 when RPC returns a generic
// error (no "not found").
func TestHandleRemoveDep_RPCErrorGeneric(t *testing.T) {
	client := &mockDependencyClient{
		removeDependencyFunc: func(args *rpc.DepRemoveArgs) (*rpc.Response, error) {
			return nil, errors.New("connection reset")
		},
	}
	pool := &mockDependencyPool{
		getFunc: func(ctx context.Context) (dependencyManager, error) { return client, nil },
		putFunc: func(c dependencyManager) {},
	}
	handler := handleRemoveDependencyWithPool(pool)

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
	if resp.Error != "internal server error" {
		t.Errorf("expected error %q, got %q", "internal server error", resp.Error)
	}
}
