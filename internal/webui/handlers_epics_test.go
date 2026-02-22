package webui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
)

// --- Mock infrastructure for epic status tests ---

// mockEpicStatusClient implements epicStatusClient for testing.
type mockEpicStatusClient struct {
	epicStatusFunc func(args *rpc.EpicStatusArgs) (*rpc.Response, error)
}

func (m *mockEpicStatusClient) EpicStatus(args *rpc.EpicStatusArgs) (*rpc.Response, error) {
	if m.epicStatusFunc != nil {
		return m.epicStatusFunc(args)
	}
	return nil, errors.New("epicStatusFunc not implemented")
}

// mockEpicStatusPool implements epicStatusConnectionGetter for testing.
type mockEpicStatusPool struct {
	getFunc func(ctx context.Context) (epicStatusClient, error)
	putFunc func(client epicStatusClient)
}

func (m *mockEpicStatusPool) Get(ctx context.Context) (epicStatusClient, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx)
	}
	return nil, errors.New("getFunc not implemented")
}

func (m *mockEpicStatusPool) Put(client epicStatusClient) {
	if m.putFunc != nil {
		m.putFunc(client)
	}
}

// --- Tests ---

// TestHandleGetEpicStatus_NilPool verifies that handleGetEpicStatus returns 503 when pool is nil.
func TestHandleGetEpicStatus_NilPool(t *testing.T) {
	handler := handleGetEpicStatus(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/epics/status", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d", http.StatusServiceUnavailable, rr.Code)
	}

	var resp EpicStatusResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Success {
		t.Error("expected Success to be false")
	}

	if resp.Error != "connection pool not initialized" {
		t.Errorf("expected error 'connection pool not initialized', got %q", resp.Error)
	}

	if resp.Data != nil {
		t.Error("expected Data to be nil")
	}

	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got %q", ct)
	}
}

// TestHandleGetEpicStatus_Success tests the happy path with mock epic statuses.
func TestHandleGetEpicStatus_Success(t *testing.T) {
	statuses := []*types.EpicStatus{
		{
			Epic:             &types.Issue{ID: "epic-1", Title: "Authentication"},
			TotalChildren:    5,
			ClosedChildren:   3,
			EligibleForClose: false,
		},
		{
			Epic:             &types.Issue{ID: "epic-2", Title: "Dashboard"},
			TotalChildren:    10,
			ClosedChildren:   10,
			EligibleForClose: true,
		},
	}
	statusesJSON, err := json.Marshal(statuses)
	if err != nil {
		t.Fatalf("failed to marshal statuses: %v", err)
	}

	client := &mockEpicStatusClient{
		epicStatusFunc: func(args *rpc.EpicStatusArgs) (*rpc.Response, error) {
			return &rpc.Response{
				Success: true,
				Data:    statusesJSON,
			}, nil
		},
	}

	pool := &mockEpicStatusPool{
		getFunc: func(ctx context.Context) (epicStatusClient, error) {
			return client, nil
		},
		putFunc: func(c epicStatusClient) {},
	}

	handler := handleGetEpicStatusWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/epics/status", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp EpicStatusResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if !resp.Success {
		t.Errorf("Success = false, want true (error: %s)", resp.Error)
	}

	if resp.Data == nil {
		t.Fatal("Data is nil, want non-nil")
	}

	if len(resp.Data) != 2 {
		t.Fatalf("expected 2 epic statuses, got %d", len(resp.Data))
	}

	if resp.Data[0].Epic == nil || resp.Data[0].Epic.ID != "epic-1" {
		t.Errorf("expected first epic ID 'epic-1', got %v", resp.Data[0].Epic)
	}

	if resp.Data[0].TotalChildren != 5 {
		t.Errorf("expected TotalChildren 5, got %d", resp.Data[0].TotalChildren)
	}

	if resp.Data[0].ClosedChildren != 3 {
		t.Errorf("expected ClosedChildren 3, got %d", resp.Data[0].ClosedChildren)
	}

	if resp.Data[0].EligibleForClose {
		t.Error("expected EligibleForClose false for first epic")
	}

	if resp.Data[1].Epic == nil || resp.Data[1].Epic.ID != "epic-2" {
		t.Errorf("expected second epic ID 'epic-2', got %v", resp.Data[1].Epic)
	}

	if !resp.Data[1].EligibleForClose {
		t.Error("expected EligibleForClose true for second epic")
	}

	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

// TestHandleGetEpicStatus_RPCError tests that RPC error returns 500 Internal Server Error.
func TestHandleGetEpicStatus_RPCError(t *testing.T) {
	client := &mockEpicStatusClient{
		epicStatusFunc: func(args *rpc.EpicStatusArgs) (*rpc.Response, error) {
			return nil, errors.New("connection reset by peer")
		},
	}

	pool := &mockEpicStatusPool{
		getFunc: func(ctx context.Context) (epicStatusClient, error) {
			return client, nil
		},
		putFunc: func(c epicStatusClient) {},
	}

	handler := handleGetEpicStatusWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/epics/status", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status code = %d, want %d", rr.Code, http.StatusInternalServerError)
	}

	var resp EpicStatusResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Success {
		t.Error("Success = true, want false")
	}

	if resp.Error == "" {
		t.Error("expected non-empty error message")
	}

	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

// TestHandleGetEpicStatus_DaemonError tests that daemon error (success=false) returns 500.
func TestHandleGetEpicStatus_DaemonError(t *testing.T) {
	client := &mockEpicStatusClient{
		epicStatusFunc: func(args *rpc.EpicStatusArgs) (*rpc.Response, error) {
			return &rpc.Response{
				Success: false,
				Error:   "database connection failed",
			}, nil
		},
	}

	pool := &mockEpicStatusPool{
		getFunc: func(ctx context.Context) (epicStatusClient, error) {
			return client, nil
		},
		putFunc: func(c epicStatusClient) {},
	}

	handler := handleGetEpicStatusWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/epics/status", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status code = %d, want %d", rr.Code, http.StatusInternalServerError)
	}

	var resp EpicStatusResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Success {
		t.Error("Success = true, want false")
	}

	if resp.Error != "database connection failed" {
		t.Errorf("Error = %q, want %q", resp.Error, "database connection failed")
	}
}

// TestHandleGetEpicStatus_EligibleOnlyQueryParam tests that eligible_only=true is passed to RPC.
func TestHandleGetEpicStatus_EligibleOnlyQueryParam(t *testing.T) {
	var capturedArgs *rpc.EpicStatusArgs

	client := &mockEpicStatusClient{
		epicStatusFunc: func(args *rpc.EpicStatusArgs) (*rpc.Response, error) {
			capturedArgs = args
			statusesJSON, _ := json.Marshal([]*types.EpicStatus{})
			return &rpc.Response{
				Success: true,
				Data:    statusesJSON,
			}, nil
		},
	}

	pool := &mockEpicStatusPool{
		getFunc: func(ctx context.Context) (epicStatusClient, error) {
			return client, nil
		},
		putFunc: func(c epicStatusClient) {},
	}

	handler := handleGetEpicStatusWithPool(pool)

	// Test with eligible_only=true
	req := httptest.NewRequest(http.MethodGet, "/api/epics/status?eligible_only=true", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", rr.Code, http.StatusOK)
	}

	if capturedArgs == nil {
		t.Fatal("expected EpicStatusArgs to be captured")
	}

	if !capturedArgs.EligibleOnly {
		t.Error("expected EligibleOnly to be true")
	}

	// Test without eligible_only (should default to false)
	capturedArgs = nil
	req = httptest.NewRequest(http.MethodGet, "/api/epics/status", nil)
	rr = httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", rr.Code, http.StatusOK)
	}

	if capturedArgs == nil {
		t.Fatal("expected EpicStatusArgs to be captured")
	}

	if capturedArgs.EligibleOnly {
		t.Error("expected EligibleOnly to be false when not specified")
	}
}

// TestHandleGetEpicStatus_ConnectionTimeout tests that deadline exceeded returns 504.
func TestHandleGetEpicStatus_ConnectionTimeout(t *testing.T) {
	pool := &mockEpicStatusPool{
		getFunc: func(ctx context.Context) (epicStatusClient, error) {
			return nil, context.DeadlineExceeded
		},
		putFunc: func(c epicStatusClient) {},
	}

	handler := handleGetEpicStatusWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/epics/status", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusGatewayTimeout {
		t.Errorf("status code = %d, want %d", rr.Code, http.StatusGatewayTimeout)
	}

	var resp EpicStatusResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Success {
		t.Error("Success = true, want false")
	}

	if resp.Error == "" {
		t.Error("expected non-empty error message")
	}

	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

// TestHandleGetEpicStatus_PoolGetError tests that pool.Get error returns 503.
func TestHandleGetEpicStatus_PoolGetError(t *testing.T) {
	pool := &mockEpicStatusPool{
		getFunc: func(ctx context.Context) (epicStatusClient, error) {
			return nil, errors.New("pool exhausted")
		},
		putFunc: func(c epicStatusClient) {},
	}

	handler := handleGetEpicStatusWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/epics/status", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status code = %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}

	var resp EpicStatusResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Success {
		t.Error("Success = true, want false")
	}

	if resp.Error == "" {
		t.Error("expected non-empty error message")
	}
}

// TestHandleGetEpicStatus_MalformedRPCData tests that malformed RPC data returns 500.
func TestHandleGetEpicStatus_MalformedRPCData(t *testing.T) {
	client := &mockEpicStatusClient{
		epicStatusFunc: func(args *rpc.EpicStatusArgs) (*rpc.Response, error) {
			return &rpc.Response{
				Success: true,
				Data:    []byte(`{"not": "an array"}`),
			}, nil
		},
	}

	pool := &mockEpicStatusPool{
		getFunc: func(ctx context.Context) (epicStatusClient, error) {
			return client, nil
		},
		putFunc: func(c epicStatusClient) {},
	}

	handler := handleGetEpicStatusWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/epics/status", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status code = %d, want %d", rr.Code, http.StatusInternalServerError)
	}

	var resp EpicStatusResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Success {
		t.Error("Success = true, want false")
	}

	if resp.Error == "" {
		t.Error("expected non-empty error message")
	}
}

// TestHandleGetEpicStatus_EmptyStatuses tests that an empty statuses array returns success.
func TestHandleGetEpicStatus_EmptyStatuses(t *testing.T) {
	client := &mockEpicStatusClient{
		epicStatusFunc: func(args *rpc.EpicStatusArgs) (*rpc.Response, error) {
			statusesJSON, _ := json.Marshal([]*types.EpicStatus{})
			return &rpc.Response{
				Success: true,
				Data:    statusesJSON,
			}, nil
		},
	}

	pool := &mockEpicStatusPool{
		getFunc: func(ctx context.Context) (epicStatusClient, error) {
			return client, nil
		},
		putFunc: func(c epicStatusClient) {},
	}

	handler := handleGetEpicStatusWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/epics/status", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp EpicStatusResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if !resp.Success {
		t.Errorf("Success = false, want true (error: %s)", resp.Error)
	}

	if len(resp.Data) != 0 {
		t.Errorf("expected 0 epic statuses, got %d", len(resp.Data))
	}
}

// TestEpicStatusResponse_SuccessSerialization tests successful EpicStatusResponse serialization.
func TestEpicStatusResponse_SuccessSerialization(t *testing.T) {
	resp := EpicStatusResponse{
		Success: true,
		Data: []*types.EpicStatus{
			{
				Epic:             &types.Issue{ID: "epic-1", Title: "Auth"},
				TotalChildren:    8,
				ClosedChildren:   4,
				EligibleForClose: false,
			},
		},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal response: %v", err)
	}

	var parsed EpicStatusResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if !parsed.Success {
		t.Error("expected Success to be true")
	}

	if parsed.Data == nil || len(parsed.Data) != 1 {
		t.Fatal("expected Data with 1 entry")
	}

	if parsed.Data[0].TotalChildren != 8 {
		t.Errorf("expected TotalChildren 8, got %d", parsed.Data[0].TotalChildren)
	}
}

// TestEpicStatusResponse_ErrorSerialization tests error EpicStatusResponse serialization.
func TestEpicStatusResponse_ErrorSerialization(t *testing.T) {
	resp := EpicStatusResponse{
		Success: false,
		Error:   "connection failed",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal response: %v", err)
	}

	var parsed EpicStatusResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if parsed.Success {
		t.Error("expected Success to be false")
	}

	if parsed.Error != "connection failed" {
		t.Errorf("expected Error 'connection failed', got %q", parsed.Error)
	}

	if parsed.Data != nil {
		t.Error("expected Data to be nil")
	}
}

// TestEpicStatusResponse_ErrorOmitsDataField verifies that error responses omit the data field.
func TestEpicStatusResponse_ErrorOmitsDataField(t *testing.T) {
	resp := EpicStatusResponse{
		Success: false,
		Error:   "some error",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal response: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to unmarshal to map: %v", err)
	}

	if _, hasData := raw["data"]; hasData {
		t.Error("expected 'data' field to be omitted in error response")
	}
}

// TestEpicStatusResponse_SuccessOmitsErrorField verifies that success responses omit the error field.
func TestEpicStatusResponse_SuccessOmitsErrorField(t *testing.T) {
	resp := EpicStatusResponse{
		Success: true,
		Data:    []*types.EpicStatus{},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal response: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to unmarshal to map: %v", err)
	}

	if _, hasError := raw["error"]; hasError {
		t.Error("expected 'error' field to be omitted in success response")
	}
}

// TestHandleGetEpicStatus_ClientPutCalled verifies that the pool's Put method is called after use.
func TestHandleGetEpicStatus_ClientPutCalled(t *testing.T) {
	var putCalled bool

	client := &mockEpicStatusClient{
		epicStatusFunc: func(args *rpc.EpicStatusArgs) (*rpc.Response, error) {
			statusesJSON, _ := json.Marshal([]*types.EpicStatus{})
			return &rpc.Response{
				Success: true,
				Data:    statusesJSON,
			}, nil
		},
	}

	pool := &mockEpicStatusPool{
		getFunc: func(ctx context.Context) (epicStatusClient, error) {
			return client, nil
		},
		putFunc: func(c epicStatusClient) {
			putCalled = true
			if c != client {
				t.Error("expected Put to be called with the same client from Get")
			}
		},
	}

	handler := handleGetEpicStatusWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/epics/status", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if !putCalled {
		t.Error("expected pool.Put to be called")
	}
}
