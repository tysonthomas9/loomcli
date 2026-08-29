package issues

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// TestHandleGetIssueEvents_Success verifies successful event listing returns 200
func TestHandleGetIssueEvents_Success(t *testing.T) {
	svc := &mockIssueService{
		listEventsFunc: func(ctx context.Context, params service.EventListParams) ([]*types.Event, error) {
			if params.IssueID != "test-123" {
				t.Errorf("ListEvents called with IssueID = %q, want %q", params.IssueID, "test-123")
			}
			if params.Limit != 100 {
				t.Errorf("ListEvents called with Limit = %d, want %d", params.Limit, 100)
			}
			return []*types.Event{
				{ID: "1", IssueID: "test-123", EventType: types.EventCreated, Actor: "alice"},
				{ID: "2", IssueID: "test-123", EventType: types.EventStatusChanged, Actor: "bob"},
			}, nil
		},
	}

	handler := handleGetIssueEvents(svc)

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
	svc := &mockIssueService{}

	handler := handleGetIssueEvents(svc)

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

// TestHandleGetIssueEvents_ServiceUnavailable verifies 503 when service returns unavailable error
func TestHandleGetIssueEvents_ServiceUnavailable(t *testing.T) {
	svc := &mockIssueService{
		listEventsFunc: func(ctx context.Context, params service.EventListParams) ([]*types.Event, error) {
			return nil, service.ErrUnavailable("connection pool not initialized")
		},
	}

	handler := handleGetIssueEvents(svc)

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

// TestHandleGetIssueEvents_DaemonNotAvailable verifies 503 when service reports daemon unavailable
func TestHandleGetIssueEvents_DaemonNotAvailable(t *testing.T) {
	svc := &mockIssueService{
		listEventsFunc: func(ctx context.Context, params service.EventListParams) ([]*types.Event, error) {
			return nil, service.ErrUnavailable("daemon not available")
		},
	}

	handler := handleGetIssueEvents(svc)

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

	svc := &mockIssueService{
		listEventsFunc: func(ctx context.Context, params service.EventListParams) ([]*types.Event, error) {
			capturedLimit = params.Limit
			return []*types.Event{}, nil
		},
	}

	handler := handleGetIssueEvents(svc)

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

	svc := &mockIssueService{
		listEventsFunc: func(ctx context.Context, params service.EventListParams) ([]*types.Event, error) {
			capturedLimit = params.Limit
			return []*types.Event{}, nil
		},
	}

	handler := handleGetIssueEvents(svc)

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

// TestHandleGetIssueEvents_InternalError verifies 500 when service returns internal error
func TestHandleGetIssueEvents_InternalError(t *testing.T) {
	svc := &mockIssueService{
		listEventsFunc: func(ctx context.Context, params service.EventListParams) ([]*types.Event, error) {
			return nil, service.ErrInternal("internal server error", nil)
		},
	}

	handler := handleGetIssueEvents(svc)

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

// TestHandleGetIssueEvents_NotFound verifies 404 when service returns not found error
func TestHandleGetIssueEvents_NotFound(t *testing.T) {
	svc := &mockIssueService{
		listEventsFunc: func(ctx context.Context, params service.EventListParams) ([]*types.Event, error) {
			return nil, service.ErrNotFound("issue not found")
		},
	}

	handler := handleGetIssueEvents(svc)

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

	if resp.Error != "issue not found" {
		t.Errorf("error = %q, want %q", resp.Error, "issue not found")
	}
}

// TestHandleGetIssueEvents_ParseError verifies 500 when service returns parse error
func TestHandleGetIssueEvents_ParseError(t *testing.T) {
	svc := &mockIssueService{
		listEventsFunc: func(ctx context.Context, params service.EventListParams) ([]*types.Event, error) {
			return nil, service.ErrInternal("failed to parse events", nil)
		},
	}

	handler := handleGetIssueEvents(svc)

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

	svc := &mockIssueService{
		listEventsFunc: func(ctx context.Context, params service.EventListParams) ([]*types.Event, error) {
			capturedLimit = params.Limit
			return []*types.Event{}, nil
		},
	}

	handler := handleGetIssueEvents(svc)

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

// TestHandleGetIssueEvents_EmptyList verifies handler returns empty list correctly
func TestHandleGetIssueEvents_EmptyList(t *testing.T) {
	svc := &mockIssueService{
		listEventsFunc: func(ctx context.Context, params service.EventListParams) ([]*types.Event, error) {
			return []*types.Event{}, nil
		},
	}

	handler := handleGetIssueEvents(svc)

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
		t.Errorf("expected success=true, got false")
	}

	if len(resp.Data) != 0 {
		t.Errorf("expected 0 events, got %d", len(resp.Data))
	}
}
