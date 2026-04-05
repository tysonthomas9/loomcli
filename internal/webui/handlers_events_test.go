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

// mockEventClient implements eventLister for testing
type mockEventClient struct {
	listEventsFunc func(args *rpc.EventListArgs) (*rpc.Response, error)
}

func (m *mockEventClient) ListEvents(args *rpc.EventListArgs) (*rpc.Response, error) {
	if m.listEventsFunc != nil {
		return m.listEventsFunc(args)
	}
	return nil, errors.New("listEventsFunc not implemented")
}

// mockEventPool implements eventConnectionGetter for testing
type mockEventPool struct {
	getFunc func(ctx context.Context) (eventLister, error)
	putFunc func(client eventLister)
}

func (m *mockEventPool) Get(ctx context.Context) (eventLister, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx)
	}
	return nil, errors.New("getFunc not implemented")
}

func (m *mockEventPool) Put(client eventLister) {
	if m.putFunc != nil {
		m.putFunc(client)
	}
}

func (m *mockEventPool) Discard(client eventLister) {}

// TestHandleGetIssueEvents_Success verifies successful event listing returns 200
func TestHandleGetIssueEvents_Success(t *testing.T) {
	eventsData, _ := json.Marshal([]*types.Event{
		{ID: 1, IssueID: "test-123", EventType: types.EventCreated, Actor: "alice"},
		{ID: 2, IssueID: "test-123", EventType: types.EventStatusChanged, Actor: "bob"},
	})

	client := &mockEventClient{
		listEventsFunc: func(args *rpc.EventListArgs) (*rpc.Response, error) {
			if args.ID != "test-123" {
				t.Errorf("ListEvents called with ID = %q, want %q", args.ID, "test-123")
			}
			if args.Limit != 100 {
				t.Errorf("ListEvents called with Limit = %d, want %d", args.Limit, 100)
			}
			return &rpc.Response{
				Success: true,
				Data:    eventsData,
			}, nil
		},
	}

	pool := &mockEventPool{
		getFunc: func(ctx context.Context) (eventLister, error) {
			return client, nil
		},
		putFunc: func(c eventLister) {},
	}

	handler := handleGetIssueEventsWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/test-123/events", nil)
	req.SetPathValue("id", "test-123")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp EventListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !resp.Success {
		t.Errorf("expected success=true, got false (error: %s)", resp.Error)
	}

	if len(resp.Data) != 2 {
		t.Errorf("expected 2 events, got %d", len(resp.Data))
	}
}

// TestHandleGetIssueEvents_MissingID verifies 400 for missing issue ID
func TestHandleGetIssueEvents_MissingID(t *testing.T) {
	handler := handleGetIssueEventsWithPool(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/issues//events", nil)
	req.SetPathValue("id", "")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp EventListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Error != "missing issue ID" {
		t.Errorf("error = %q, want %q", resp.Error, "missing issue ID")
	}
}

// TestHandleGetIssueEvents_NilPool verifies 503 when pool is not initialized
func TestHandleGetIssueEvents_NilPool(t *testing.T) {
	handler := handleGetIssueEventsWithPool(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/test-123/events", nil)
	req.SetPathValue("id", "test-123")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}

	var resp EventListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Error != "connection pool not initialized" {
		t.Errorf("error = %q, want %q", resp.Error, "connection pool not initialized")
	}
}

// TestHandleGetIssueEvents_PoolError verifies 503 when pool connection fails
func TestHandleGetIssueEvents_PoolError(t *testing.T) {
	pool := &mockEventPool{
		getFunc: func(ctx context.Context) (eventLister, error) {
			return nil, errors.New("pool closed")
		},
	}

	handler := handleGetIssueEventsWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/test-123/events", nil)
	req.SetPathValue("id", "test-123")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}

	var resp EventListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Error != "daemon not available" {
		t.Errorf("error = %q, want %q", resp.Error, "daemon not available")
	}
}

// TestHandleGetIssueEvents_LimitParameter verifies custom limit query parameter
func TestHandleGetIssueEvents_LimitParameter(t *testing.T) {
	var capturedLimit int
	eventsData, _ := json.Marshal([]*types.Event{})

	client := &mockEventClient{
		listEventsFunc: func(args *rpc.EventListArgs) (*rpc.Response, error) {
			capturedLimit = args.Limit
			return &rpc.Response{Success: true, Data: eventsData}, nil
		},
	}

	pool := &mockEventPool{
		getFunc: func(ctx context.Context) (eventLister, error) {
			return client, nil
		},
		putFunc: func(c eventLister) {},
	}

	handler := handleGetIssueEventsWithPool(pool)

	// Test with custom limit
	req := httptest.NewRequest(http.MethodGet, "/api/issues/test-123/events?limit=25", nil)
	req.SetPathValue("id", "test-123")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", w.Code, http.StatusOK, w.Body.String())
	}

	if capturedLimit != 25 {
		t.Errorf("expected limit 25, got %d", capturedLimit)
	}
}

// TestHandleGetIssueEvents_LimitCap verifies limit is capped at 500
func TestHandleGetIssueEvents_LimitCap(t *testing.T) {
	var capturedLimit int
	eventsData, _ := json.Marshal([]*types.Event{})

	client := &mockEventClient{
		listEventsFunc: func(args *rpc.EventListArgs) (*rpc.Response, error) {
			capturedLimit = args.Limit
			return &rpc.Response{Success: true, Data: eventsData}, nil
		},
	}

	pool := &mockEventPool{
		getFunc: func(ctx context.Context) (eventLister, error) {
			return client, nil
		},
		putFunc: func(c eventLister) {},
	}

	handler := handleGetIssueEventsWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/test-123/events?limit=1000", nil)
	req.SetPathValue("id", "test-123")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", w.Code, http.StatusOK, w.Body.String())
	}

	if capturedLimit != 500 {
		t.Errorf("expected limit capped to 500, got %d", capturedLimit)
	}
}

// TestHandleGetIssueEvents_RPCError verifies 500 when RPC call fails
func TestHandleGetIssueEvents_RPCError(t *testing.T) {
	client := &mockEventClient{
		listEventsFunc: func(args *rpc.EventListArgs) (*rpc.Response, error) {
			return nil, errors.New("rpc connection failed")
		},
	}

	pool := &mockEventPool{
		getFunc: func(ctx context.Context) (eventLister, error) {
			return client, nil
		},
		putFunc: func(c eventLister) {},
	}

	handler := handleGetIssueEventsWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/test-123/events", nil)
	req.SetPathValue("id", "test-123")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}

	var resp EventListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Error != "internal server error" {
		t.Errorf("error = %q, want %q", resp.Error, "internal server error")
	}
}

// TestHandleGetIssueEvents_RPCError_NotFound verifies 404 when RPC error contains "not found"
func TestHandleGetIssueEvents_RPCError_NotFound(t *testing.T) {
	client := &mockEventClient{
		listEventsFunc: func(args *rpc.EventListArgs) (*rpc.Response, error) {
			return nil, errors.New("issue not found")
		},
	}

	pool := &mockEventPool{
		getFunc: func(ctx context.Context) (eventLister, error) {
			return client, nil
		},
		putFunc: func(c eventLister) {},
	}

	handler := handleGetIssueEventsWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/nonexistent/events", nil)
	req.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}

	var resp EventListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Error != "internal server error" {
		t.Errorf("error = %q, want %q", resp.Error, "internal server error")
	}
}

// TestHandleGetIssueEvents_RPCResponseFailure verifies 500 when resp.Success=false
func TestHandleGetIssueEvents_RPCResponseFailure(t *testing.T) {
	client := &mockEventClient{
		listEventsFunc: func(args *rpc.EventListArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: false, Error: "something broke"}, nil
		},
	}

	pool := &mockEventPool{
		getFunc: func(ctx context.Context) (eventLister, error) {
			return client, nil
		},
		putFunc: func(c eventLister) {},
	}

	handler := handleGetIssueEventsWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/test-123/events", nil)
	req.SetPathValue("id", "test-123")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}

	var resp EventListResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Error != "something broke" {
		t.Errorf("expected 'something broke', got: %s", resp.Error)
	}
}

// TestHandleGetIssueEvents_RPCResponseFailure_NotFound verifies 404 when
// resp.Success=false and error contains "not found"
func TestHandleGetIssueEvents_RPCResponseFailure_NotFound(t *testing.T) {
	client := &mockEventClient{
		listEventsFunc: func(args *rpc.EventListArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: false, Error: "issue not found"}, nil
		},
	}

	pool := &mockEventPool{
		getFunc: func(ctx context.Context) (eventLister, error) {
			return client, nil
		},
		putFunc: func(c eventLister) {},
	}

	handler := handleGetIssueEventsWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/nonexistent/events", nil)
	req.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}

	var resp EventListResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Error != "issue not found" {
		t.Errorf("expected 'issue not found', got: %s", resp.Error)
	}
}

// TestHandleGetIssueEvents_UnmarshalError verifies 500 when resp.Data is invalid JSON
func TestHandleGetIssueEvents_UnmarshalError(t *testing.T) {
	client := &mockEventClient{
		listEventsFunc: func(args *rpc.EventListArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true, Data: json.RawMessage(`not json`)}, nil
		},
	}

	pool := &mockEventPool{
		getFunc: func(ctx context.Context) (eventLister, error) {
			return client, nil
		},
		putFunc: func(c eventLister) {},
	}

	handler := handleGetIssueEventsWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/test-123/events", nil)
	req.SetPathValue("id", "test-123")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}

	var resp EventListResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Error != "failed to parse events" {
		t.Errorf("expected 'failed to parse events', got: %s", resp.Error)
	}
}

// TestHandleGetIssueEvents_InvalidLimitIgnored verifies invalid limit is ignored (uses default)
func TestHandleGetIssueEvents_InvalidLimitIgnored(t *testing.T) {
	var capturedLimit int
	eventsData, _ := json.Marshal([]*types.Event{})

	client := &mockEventClient{
		listEventsFunc: func(args *rpc.EventListArgs) (*rpc.Response, error) {
			capturedLimit = args.Limit
			return &rpc.Response{Success: true, Data: eventsData}, nil
		},
	}

	pool := &mockEventPool{
		getFunc: func(ctx context.Context) (eventLister, error) {
			return client, nil
		},
		putFunc: func(c eventLister) {},
	}

	handler := handleGetIssueEventsWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/test-123/events?limit=abc", nil)
	req.SetPathValue("id", "test-123")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", w.Code, http.StatusOK, w.Body.String())
	}

	if capturedLimit != 100 {
		t.Errorf("expected default limit 100, got %d", capturedLimit)
	}
}
