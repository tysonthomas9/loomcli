package webui

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
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

// TestHandleStats_NilPool verifies that handleStats returns 503 when pool is nil.
func TestHandleStats_NilPool(t *testing.T) {
	handler := handleStats(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d", http.StatusServiceUnavailable, rr.Code)
	}

	var resp StatsResponse
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

// TestHandleStats_PoolGetError verifies that handleStats returns 503 when pool.Get fails.
func TestHandleStats_PoolGetError(t *testing.T) {
	// Create a pool with an invalid socket path that will fail to connect
	pool, err := daemon.NewConnectionPool("/nonexistent/socket.sock", 1)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Close()

	// Set very short timeouts to make test fast
	pool.SetDialTimeout(10 * time.Millisecond)
	pool.SetPoolTimeout(20 * time.Millisecond)

	handler := handleStats(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	// Should be either 503 (service unavailable) or 504 (timeout)
	if rr.Code != http.StatusServiceUnavailable && rr.Code != http.StatusGatewayTimeout {
		t.Errorf("expected status %d or %d, got %d", http.StatusServiceUnavailable, http.StatusGatewayTimeout, rr.Code)
	}

	var resp StatsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Success {
		t.Error("expected Success to be false")
	}

	if resp.Error == "" {
		t.Error("expected non-empty error message")
	}
}

// TestHandleStats_PoolClosed verifies that handleStats returns 503 when pool is closed.
func TestHandleStats_PoolClosed(t *testing.T) {
	pool, err := daemon.NewConnectionPool("/tmp/test.sock", 1)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}

	// Close the pool before making request
	pool.Close()

	handler := handleStats(pool)

	// Create request with a very short timeout context
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil).WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	// Should return 503 (pool closed)
	if rr.Code != http.StatusServiceUnavailable && rr.Code != http.StatusGatewayTimeout {
		t.Errorf("expected status %d or %d, got %d", http.StatusServiceUnavailable, http.StatusGatewayTimeout, rr.Code)
	}

	var resp StatsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Success {
		t.Error("expected Success to be false")
	}
}

// TestHandleStats_ContextDeadlineExceeded verifies that handleStats returns 504 on context timeout.
func TestHandleStats_ContextDeadlineExceeded(t *testing.T) {
	// Create a pool that will block trying to get a connection
	pool, err := daemon.NewConnectionPool("/nonexistent/socket.sock", 1)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Close()

	// Set dial timeout longer than request timeout to trigger deadline exceeded
	pool.SetDialTimeout(5 * time.Second)
	pool.SetPoolTimeout(5 * time.Second)

	handler := handleStats(pool)

	// Create request with a very short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil).WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	// Should return 504 (gateway timeout) or 503 (service unavailable)
	if rr.Code != http.StatusGatewayTimeout && rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d or %d, got %d", http.StatusGatewayTimeout, http.StatusServiceUnavailable, rr.Code)
	}

	var resp StatsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Success {
		t.Error("expected Success to be false")
	}
}

// TestStatsResponse_SuccessSerialization tests successful StatsResponse serialization.
func TestStatsResponse_SuccessSerialization(t *testing.T) {
	stats := &types.Statistics{
		TotalIssues:      100,
		OpenIssues:       50,
		InProgressIssues: 20,
		ClosedIssues:     30,
		BlockedIssues:    5,
		DeferredIssues:   10,
		ReadyIssues:      15,
		TombstoneIssues:  2,
		PinnedIssues:     3,
		AverageLeadTime:  24.5,
	}

	resp := StatsResponse{
		Success: true,
		Data:    stats,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal response: %v", err)
	}

	var parsed StatsResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if !parsed.Success {
		t.Error("expected Success to be true")
	}

	if parsed.Data == nil {
		t.Fatal("expected Data to be non-nil")
	}

	if parsed.Data.TotalIssues != 100 {
		t.Errorf("expected TotalIssues 100, got %d", parsed.Data.TotalIssues)
	}

	if parsed.Data.OpenIssues != 50 {
		t.Errorf("expected OpenIssues 50, got %d", parsed.Data.OpenIssues)
	}

	if parsed.Data.AverageLeadTime != 24.5 {
		t.Errorf("expected AverageLeadTime 24.5, got %f", parsed.Data.AverageLeadTime)
	}
}

// TestStatsResponse_ErrorSerialization tests error StatsResponse serialization.
func TestStatsResponse_ErrorSerialization(t *testing.T) {
	resp := StatsResponse{
		Success: false,
		Error:   "connection failed",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal response: %v", err)
	}

	var parsed StatsResponse
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

// TestStatsResponse_ErrorOmitsDataField verifies that error responses omit the data field.
func TestStatsResponse_ErrorOmitsDataField(t *testing.T) {
	resp := StatsResponse{
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

// TestStatsResponse_SuccessOmitsErrorField verifies that success responses omit the error field.
func TestStatsResponse_SuccessOmitsErrorField(t *testing.T) {
	resp := StatsResponse{
		Success: true,
		Data:    &types.Statistics{TotalIssues: 10},
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

// TestHandleStats_RPCResponseParsing_ValidStatistics tests parsing valid statistics from RPC response.
func TestHandleStats_RPCResponseParsing_ValidStatistics(t *testing.T) {
	// Test that the RPC response data structure can be parsed correctly
	stats := types.Statistics{
		TotalIssues:             100,
		OpenIssues:              50,
		InProgressIssues:        20,
		ClosedIssues:            30,
		BlockedIssues:           5,
		DeferredIssues:          10,
		ReadyIssues:             15,
		TombstoneIssues:         2,
		PinnedIssues:            3,
		EpicsEligibleForClosure: 1,
		AverageLeadTime:         48.0,
	}

	data, err := json.Marshal(stats)
	if err != nil {
		t.Fatalf("failed to marshal statistics: %v", err)
	}

	// Simulate what the RPC response would contain
	rpcResp := rpc.Response{
		Success: true,
		Data:    data,
	}

	// Parse like handleStats does
	var parsedStats types.Statistics
	if err := json.Unmarshal(rpcResp.Data, &parsedStats); err != nil {
		t.Fatalf("failed to unmarshal statistics from RPC response: %v", err)
	}

	if parsedStats.TotalIssues != 100 {
		t.Errorf("expected TotalIssues 100, got %d", parsedStats.TotalIssues)
	}

	if parsedStats.EpicsEligibleForClosure != 1 {
		t.Errorf("expected EpicsEligibleForClosure 1, got %d", parsedStats.EpicsEligibleForClosure)
	}
}

// TestHandleStats_RPCResponseParsing_MalformedData tests that malformed RPC data causes error.
func TestHandleStats_RPCResponseParsing_MalformedData(t *testing.T) {
	// Test that malformed data would cause parsing error
	invalidData := json.RawMessage(`{"total_issues": "not a number"}`)

	var stats types.Statistics
	err := json.Unmarshal(invalidData, &stats)
	if err == nil {
		t.Error("expected error when parsing malformed data")
	}
}

// TestHandleStats_RPCResponseParsing_EmptyData tests parsing empty RPC response data.
func TestHandleStats_RPCResponseParsing_EmptyData(t *testing.T) {
	// Test parsing empty data
	emptyData := json.RawMessage(`{}`)

	var stats types.Statistics
	if err := json.Unmarshal(emptyData, &stats); err != nil {
		t.Fatalf("expected no error for empty object, got: %v", err)
	}

	// All fields should be zero values
	if stats.TotalIssues != 0 {
		t.Errorf("expected TotalIssues 0, got %d", stats.TotalIssues)
	}
}

// TestHandleStats_RPCFailureResponse tests RPC failure response handling.
func TestHandleStats_RPCFailureResponse(t *testing.T) {
	// Test that RPC failure response is handled correctly
	rpcResp := rpc.Response{
		Success: false,
		Error:   "database connection failed",
	}

	if rpcResp.Success {
		t.Error("expected Success to be false")
	}

	if rpcResp.Error != "database connection failed" {
		t.Errorf("expected error 'database connection failed', got %q", rpcResp.Error)
	}
}

// TestHealthStatus_HealthySerialization tests healthy HealthStatus serialization.
func TestHealthStatus_HealthySerialization(t *testing.T) {
	status := HealthStatus{
		Status: "ok",
		Daemon: DaemonStatus{
			Connected: true,
			Status:    "healthy",
			Uptime:    3600.0,
			Version:   "1.0.0",
		},
		Pool: &daemon.PoolStats{
			Size:      5,
			Created:   3,
			Active:    2,
			Available: 1,
			Closed:    false,
		},
	}

	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("failed to marshal HealthStatus: %v", err)
	}

	var parsed HealthStatus
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal HealthStatus: %v", err)
	}

	if parsed.Status != "ok" {
		t.Errorf("expected Status 'ok', got %q", parsed.Status)
	}

	if !parsed.Daemon.Connected {
		t.Error("expected Daemon.Connected to be true")
	}

	if parsed.Pool == nil {
		t.Fatal("expected Pool to be non-nil")
	}

	if parsed.Pool.Size != 5 {
		t.Errorf("expected Pool.Size 5, got %d", parsed.Pool.Size)
	}
}

// TestHealthStatus_DegradedSerialization tests degraded HealthStatus serialization.
func TestHealthStatus_DegradedSerialization(t *testing.T) {
	status := HealthStatus{
		Status: "degraded",
		Daemon: DaemonStatus{
			Connected: false,
			Error:     "connection refused",
		},
	}

	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("failed to marshal HealthStatus: %v", err)
	}

	var parsed HealthStatus
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal HealthStatus: %v", err)
	}

	if parsed.Status != "degraded" {
		t.Errorf("expected Status 'degraded', got %q", parsed.Status)
	}

	if parsed.Daemon.Connected {
		t.Error("expected Daemon.Connected to be false")
	}

	if parsed.Daemon.Error != "connection refused" {
		t.Errorf("expected Daemon.Error 'connection refused', got %q", parsed.Daemon.Error)
	}
}

// TestHealthStatus_PoolOmittedWhenNil verifies pool field is omitted when nil.
func TestHealthStatus_PoolOmittedWhenNil(t *testing.T) {
	status := HealthStatus{
		Status: "degraded",
		Daemon: DaemonStatus{
			Connected: false,
		},
	}

	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("failed to marshal HealthStatus: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to unmarshal to map: %v", err)
	}

	if _, hasPool := raw["pool"]; hasPool {
		t.Error("expected 'pool' field to be omitted when nil")
	}
}

// TestDaemonStatus_ConnectedSerialization tests connected DaemonStatus serialization.
func TestDaemonStatus_ConnectedSerialization(t *testing.T) {
	status := DaemonStatus{
		Connected: true,
		Status:    "healthy",
		Uptime:    7200.5,
		Version:   "2.0.0",
	}

	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("failed to marshal DaemonStatus: %v", err)
	}

	var parsed DaemonStatus
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal DaemonStatus: %v", err)
	}

	if !parsed.Connected {
		t.Error("expected Connected to be true")
	}

	if parsed.Status != "healthy" {
		t.Errorf("expected Status 'healthy', got %q", parsed.Status)
	}

	if parsed.Uptime != 7200.5 {
		t.Errorf("expected Uptime 7200.5, got %f", parsed.Uptime)
	}

	if parsed.Version != "2.0.0" {
		t.Errorf("expected Version '2.0.0', got %q", parsed.Version)
	}
}

// TestDaemonStatus_DisconnectedSerialization tests disconnected DaemonStatus serialization.
func TestDaemonStatus_DisconnectedSerialization(t *testing.T) {
	status := DaemonStatus{
		Connected: false,
		Error:     "daemon not running",
	}

	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("failed to marshal DaemonStatus: %v", err)
	}

	var parsed DaemonStatus
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal DaemonStatus: %v", err)
	}

	if parsed.Connected {
		t.Error("expected Connected to be false")
	}

	if parsed.Error != "daemon not running" {
		t.Errorf("expected Error 'daemon not running', got %q", parsed.Error)
	}
}

// TestDaemonStatus_OptionalFieldsOmitted tests that optional fields are omitted when empty.
func TestDaemonStatus_OptionalFieldsOmitted(t *testing.T) {
	status := DaemonStatus{
		Connected: false,
	}

	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("failed to marshal DaemonStatus: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to unmarshal to map: %v", err)
	}

	// These fields should be omitted when empty
	optionalFields := []string{"status", "uptime", "version", "error"}
	for _, field := range optionalFields {
		if val, hasField := raw[field]; hasField {
			// Check if it's a zero value
			switch v := val.(type) {
			case string:
				if v != "" {
					t.Errorf("expected field %q to be omitted or empty, got %q", field, v)
				}
			case float64:
				if v != 0 {
					t.Errorf("expected field %q to be omitted or zero, got %f", field, v)
				}
			}
		}
	}

	// Connected should always be present (not omitempty)
	if _, hasConnected := raw["connected"]; !hasConnected {
		t.Error("expected 'connected' field to always be present")
	}
}

// TestHandleAPIHealth_NilPool tests API health endpoint with nil pool.
func TestHandleAPIHealth_NilPool(t *testing.T) {
	handler := handleAPIHealth(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d", http.StatusServiceUnavailable, rr.Code)
	}

	var resp HealthStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Status != "degraded" {
		t.Errorf("expected status 'degraded', got %q", resp.Status)
	}

	if resp.Daemon.Error != "connection pool not initialized" {
		t.Errorf("expected daemon error 'connection pool not initialized', got %q", resp.Daemon.Error)
	}
}

// TestSetupRoutes_TerminalEndpointNotRegisteredWithNilManager tests that
// the terminal WebSocket endpoint is NOT registered when termManager is nil.
func TestSetupRoutes_TerminalEndpointNotRegisteredWithNilManager(t *testing.T) {
	mux := http.NewServeMux()
	setupRoutes(mux, nil, nil, nil, nil, "", nil, nil, nil, "", false, nil, nil, nil, nil, false, false, "", "") // nil termManager

	// Request to terminal endpoint should fall through to frontend handler
	// (the SPA catch-all) since the route is not registered
	req := httptest.NewRequest(http.MethodGet, "/api/terminal/ws?session=test", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	// When termManager is nil, the route is not registered, so the request
	// falls through to the frontend handler "/" which returns 200 with index.html
	if rr.Code != http.StatusOK {
		t.Errorf("expected /api/terminal/ws to fall through to frontend with status %d when termManager is nil, got %d",
			http.StatusOK, rr.Code)
	}

	// Verify it's serving HTML (the SPA index.html), not JSON
	ct := rr.Header().Get("Content-Type")
	if ct != "text/html; charset=utf-8" {
		t.Logf("Content-Type: %q (may vary based on file detection)", ct)
	}
}

// TestSetupRoutes_TerminalEndpointRegisteredWithManager tests that
// the terminal WebSocket endpoint IS registered when termManager is non-nil.
func TestSetupRoutes_TerminalEndpointRegisteredWithManager(t *testing.T) {
	// Create a terminal manager - skip if tmux not available
	termMgr, err := NewTerminalManager("bash", "", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer termMgr.Shutdown()

	mux := http.NewServeMux()
	setupRoutes(mux, nil, nil, nil, termMgr, "", nil, nil, nil, "", false, nil, nil, nil, nil, false, false, "", "") // non-nil termManager

	// Request to terminal endpoint should be handled by the terminal handler,
	// not fall through to frontend. Without WebSocket upgrade headers,
	// it should return a 400 or other error, but NOT serve HTML.
	req := httptest.NewRequest(http.MethodGet, "/api/terminal/ws?session=test", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	// When termManager is non-nil, the route IS registered.
	// Without proper WebSocket upgrade headers, the handler should return an error.
	// The exact status depends on the WebSocket library - it might be 400 or 500.
	// The key is it should NOT be 200 with HTML content (which would indicate
	// the request fell through to the frontend handler).
	if rr.Code == http.StatusOK {
		ct := rr.Header().Get("Content-Type")
		// If it's HTML, then the route wasn't registered properly
		if ct == "text/html; charset=utf-8" {
			t.Error("expected terminal route to be registered, but request fell through to frontend handler")
		}
	}

	// Additional verification: the response should be JSON (our handler's error response)
	// OR a WebSocket upgrade failure, not HTML
	ct := rr.Header().Get("Content-Type")
	if ct == "text/html; charset=utf-8" {
		t.Errorf("expected non-HTML response when terminal route is registered, got Content-Type %q", ct)
	}
}

// TestSetupRoutes_TerminalEndpointNilManagerReturns503 tests that
// calling handleTerminalWS directly with nil manager returns 503.
// This complements the route registration test by verifying handler behavior.
func TestSetupRoutes_TerminalEndpointNilManagerReturns503(t *testing.T) {
	handler := handleTerminalWS(nil, "", nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/ws?session=test", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d for nil manager, got %d", http.StatusServiceUnavailable, rr.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["success"] != false {
		t.Error("expected success to be false")
	}

	if resp["error"] != "terminal manager not initialized" {
		t.Errorf("expected error 'terminal manager not initialized', got %q", resp["error"])
	}
}

// TestSetupRoutes_StatsEndpoint tests that stats endpoint is registered.
func TestSetupRoutes_StatsEndpoint(t *testing.T) {
	mux := http.NewServeMux()
	setupRoutes(mux, nil, nil, nil, nil, "", nil, nil, nil, "", false, nil, nil, nil, nil, false, false, "", "")

	// Test that stats endpoint is registered
	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	// Should return 503 with nil pool
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected /api/stats to return %d with nil pool, got %d", http.StatusServiceUnavailable, rr.Code)
	}

	// Verify JSON response
	var resp StatsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Success {
		t.Error("expected Success to be false with nil pool")
	}
}

// TestSetupRoutes_StatsEndpointPOSTFallsThrough tests that POST to stats falls through to frontend.
// Note: Go 1.22's pattern matching means "GET /api/stats" only matches GET requests.
// A POST to /api/stats doesn't match that route, so it falls through to the
// catch-all frontend handler "/" which returns index.html (200 OK).
func TestSetupRoutes_StatsEndpointPOSTFallsThrough(t *testing.T) {
	mux := http.NewServeMux()
	setupRoutes(mux, nil, nil, nil, nil, "", nil, nil, nil, "", false, nil, nil, nil, nil, false, false, "", "")

	// POST to GET-only endpoint falls through to frontend handler
	req := httptest.NewRequest(http.MethodPost, "/api/stats", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	// The frontend handler (SPA routing) catches unmatched routes and returns 200 with index.html
	if rr.Code != http.StatusOK {
		t.Errorf("expected POST /api/stats to fall through to frontend handler with status %d, got %d", http.StatusOK, rr.Code)
	}

	// Verify it's serving HTML (the SPA index.html), not JSON
	ct := rr.Header().Get("Content-Type")
	if ct != "text/html; charset=utf-8" {
		t.Logf("Content-Type: %q (may vary based on file detection)", ct)
	}
}

// mockStatsClient implements statsClient for testing
type mockStatsClient struct {
	statsFunc func() (*rpc.Response, error)
}

func (m *mockStatsClient) Stats() (*rpc.Response, error) {
	if m.statsFunc != nil {
		return m.statsFunc()
	}
	return nil, errors.New("statsFunc not implemented")
}

// mockStatsPool implements statsConnectionGetter for testing
type mockStatsPool struct {
	getFunc func(ctx context.Context) (statsClient, error)
	putFunc func(client statsClient)
}

func (m *mockStatsPool) Get(ctx context.Context) (statsClient, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx)
	}
	return nil, errors.New("getFunc not implemented")
}

func (m *mockStatsPool) Put(client statsClient) {
	if m.putFunc != nil {
		m.putFunc(client)
	}
}

// TestHandleStats_ContentType verifies Content-Type header is application/json for all responses.
func TestHandleStats_ContentType(t *testing.T) {
	handler := handleStats(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

// TestHandleStats_Success tests the success path with mock data
func TestHandleStats_Success(t *testing.T) {
	statsJSON := `{"total_issues":100,"open_issues":50,"in_progress_issues":20,"closed_issues":30,"blocked_issues":5,"deferred_issues":10,"ready_issues":15}`

	client := &mockStatsClient{
		statsFunc: func() (*rpc.Response, error) {
			return &rpc.Response{
				Success: true,
				Data:    []byte(statsJSON),
			}, nil
		},
	}

	pool := &mockStatsPool{
		getFunc: func(ctx context.Context) (statsClient, error) {
			return client, nil
		},
		putFunc: func(c statsClient) {},
	}

	handler := handleStatsWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp StatsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if !resp.Success {
		t.Error("Success = false, want true")
	}

	if resp.Data == nil {
		t.Fatal("Data is nil, want non-nil")
	}

	if resp.Data.TotalIssues != 100 {
		t.Errorf("TotalIssues = %d, want 100", resp.Data.TotalIssues)
	}

	if resp.Data.OpenIssues != 50 {
		t.Errorf("OpenIssues = %d, want 50", resp.Data.OpenIssues)
	}

	// Verify Content-Type header
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

// TestHandleStats_RPCError tests that RPC error returns 500 Internal Server Error
func TestHandleStats_RPCError(t *testing.T) {
	client := &mockStatsClient{
		statsFunc: func() (*rpc.Response, error) {
			return nil, errors.New("connection reset by peer")
		},
	}

	pool := &mockStatsPool{
		getFunc: func(ctx context.Context) (statsClient, error) {
			return client, nil
		},
		putFunc: func(c statsClient) {},
	}

	handler := handleStatsWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status code = %d, want %d", rr.Code, http.StatusInternalServerError)
	}

	var resp StatsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Success {
		t.Error("Success = true, want false")
	}

	if resp.Error == "" {
		t.Error("expected non-empty error message")
	}

	// Verify Content-Type header
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

// TestHandleStats_DaemonError tests that daemon error (success=false) returns 500
func TestHandleStats_DaemonError(t *testing.T) {
	client := &mockStatsClient{
		statsFunc: func() (*rpc.Response, error) {
			return &rpc.Response{
				Success: false,
				Error:   "database connection failed",
			}, nil
		},
	}

	pool := &mockStatsPool{
		getFunc: func(ctx context.Context) (statsClient, error) {
			return client, nil
		},
		putFunc: func(c statsClient) {},
	}

	handler := handleStatsWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status code = %d, want %d", rr.Code, http.StatusInternalServerError)
	}

	var resp StatsResponse
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

// TestSetupRoutes_FleetEndpointsNotRegisteredWhenDisabled tests that
// fleet endpoints are NOT registered when fleetEnabled is false.
func TestSetupRoutes_FleetEndpointsNotRegisteredWhenDisabled(t *testing.T) {
	mux := http.NewServeMux()
	setupRoutes(mux, nil, nil, nil, nil, "", nil, nil, nil, "", false, nil, nil, nil, nil, false, false, "", "") // fleetEnabled=false

	// Request to fleet endpoint should fall through to frontend handler
	// (the SPA catch-all) since the route is not registered
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	// When fleetEnabled is false, the route is not registered, so the request
	// falls through to the frontend handler "/" which returns 200 with index.html
	if rr.Code != http.StatusOK {
		t.Errorf("expected /api/fleet/claim to fall through to frontend with status %d when fleetEnabled is false, got %d",
			http.StatusOK, rr.Code)
	}

	// Verify it's serving HTML (the SPA index.html), not JSON
	ct := rr.Header().Get("Content-Type")
	if ct != "text/html; charset=utf-8" {
		t.Logf("Content-Type: %q (may vary based on file detection)", ct)
	}
}

// TestSetupRoutes_FleetEndpointsRegisteredWhenEnabled tests that
// fleet endpoints ARE registered when fleetEnabled is true.
func TestSetupRoutes_FleetEndpointsRegisteredWhenEnabled(t *testing.T) {
	mux := http.NewServeMux()
	setupRoutes(mux, nil, nil, nil, nil, "", nil, nil, nil, "", false, nil, nil, nil, nil, true, false, "", "") // fleetEnabled=true

	// Request to fleet endpoint should be handled by the fleet handler,
	// not fall through to frontend. With nil pool, the handler should
	// return an error response, not HTML.
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	// When fleetEnabled is true, the route IS registered.
	// With nil pool, the handler should return a non-HTML error response.
	// The key is it should NOT be 200 with HTML content (which would indicate
	// the request fell through to the frontend handler).
	if rr.Code == http.StatusOK {
		ct := rr.Header().Get("Content-Type")
		// If it's HTML, then the route wasn't registered properly
		if ct == "text/html; charset=utf-8" {
			t.Error("expected fleet route to be registered, but request fell through to frontend handler")
		}
	}

	// Additional verification: the response should NOT be HTML
	ct := rr.Header().Get("Content-Type")
	if ct == "text/html; charset=utf-8" {
		t.Errorf("expected non-HTML response when fleet route is registered, got Content-Type %q", ct)
	}
}

// --- handleDaemonStatus tests ---

// TestHandleDaemonStatus_NilPool tests handleDaemonStatus with nil pool returns 503.
func TestHandleDaemonStatus_NilPool(t *testing.T) {
	handler := handleDaemonStatus(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/daemon/status", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}

	var resp DaemonStatusResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Success {
		t.Error("expected Success to be false")
	}

	if resp.Error != "connection pool not initialized" {
		t.Errorf("error = %q, want %q", resp.Error, "connection pool not initialized")
	}

	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

// TestHandleDaemonStatus_PoolGetError tests handleDaemonStatus when pool.Get fails.
func TestHandleDaemonStatus_PoolGetError(t *testing.T) {
	pool, err := daemon.NewConnectionPool("/nonexistent/daemon-status-test.sock", 1)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Close()

	pool.SetDialTimeout(10 * time.Millisecond)
	pool.SetPoolTimeout(20 * time.Millisecond)

	handler := handleDaemonStatus(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/daemon/status", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	// Should be 503 or 504
	if rr.Code != http.StatusServiceUnavailable && rr.Code != http.StatusGatewayTimeout {
		t.Errorf("status = %d, want %d or %d", rr.Code, http.StatusServiceUnavailable, http.StatusGatewayTimeout)
	}

	var resp DaemonStatusResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Success {
		t.Error("expected Success to be false")
	}

	if resp.Error == "" {
		t.Error("expected non-empty error message")
	}
}

// TestHandleDaemonStatus_ContextTimeout tests handleDaemonStatus with very short context timeout.
func TestHandleDaemonStatus_ContextTimeout(t *testing.T) {
	pool, err := daemon.NewConnectionPool("/nonexistent/daemon-status-timeout.sock", 1)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Close()

	pool.SetDialTimeout(5 * time.Second)
	pool.SetPoolTimeout(5 * time.Second)

	handler := handleDaemonStatus(pool)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/daemon/status", nil).WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusGatewayTimeout && rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d or %d", rr.Code, http.StatusGatewayTimeout, http.StatusServiceUnavailable)
	}
}

// TestDaemonStatusResponse_Serialization tests DaemonStatusResponse JSON serialization.
func TestDaemonStatusResponse_Serialization(t *testing.T) {
	resp := DaemonStatusResponse{
		Success: true,
		Data: &rpc.StatusResponse{
			Version:       "1.0.0",
			WorkspacePath: "/path/to/workspace",
			AutoCommit:    true,
			AutoPush:      false,
			LocalMode:     false,
			SyncInterval:  "5s",
		},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var parsed DaemonStatusResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if !parsed.Success {
		t.Error("expected Success to be true")
	}

	if parsed.Data == nil {
		t.Fatal("expected Data to be non-nil")
	}

	if parsed.Data.Version != "1.0.0" {
		t.Errorf("Version = %q, want %q", parsed.Data.Version, "1.0.0")
	}

	if !parsed.Data.AutoCommit {
		t.Error("expected AutoCommit to be true")
	}

	if parsed.Data.SyncInterval != "5s" {
		t.Errorf("SyncInterval = %q, want %q", parsed.Data.SyncInterval, "5s")
	}
}

// TestDaemonStatusResponse_ErrorSerialization tests error DaemonStatusResponse serialization.
func TestDaemonStatusResponse_ErrorSerialization(t *testing.T) {
	resp := DaemonStatusResponse{
		Success: false,
		Error:   "connection failed",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var parsed DaemonStatusResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if parsed.Success {
		t.Error("expected Success to be false")
	}

	if parsed.Data != nil {
		t.Error("expected Data to be nil for error response")
	}

	if parsed.Error != "connection failed" {
		t.Errorf("Error = %q, want %q", parsed.Error, "connection failed")
	}
}

// --- Mock server infrastructure for routes tests ---

func startRoutesMockServer(t *testing.T, handler func(req rpc.Request) rpc.Response) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "routes-test-*")
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

func newRoutesMockPool(t *testing.T, socketPath string) daemon.Pool {
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

// TestHandleDaemonStatus_Success tests the full success path of handleDaemonStatus.
func TestHandleDaemonStatus_Success(t *testing.T) {
	statusResp := &rpc.StatusResponse{
		Version:       "1.2.3",
		WorkspacePath: "/tmp/workspace",
		DatabasePath:  "/tmp/workspace/.beads/db.sqlite",
		SocketPath:    "/tmp/workspace/.beads/bd.sock",
		PID:           12345,
		UptimeSeconds: 3600.5,
		AutoCommit:    true,
		AutoPush:      false,
		AutoPull:      true,
		LocalMode:     false,
		SyncInterval:  "5s",
	}
	statusData, _ := json.Marshal(statusResp)

	socketPath := startRoutesMockServer(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "health":
			hd, _ := json.Marshal(rpc.HealthResponse{Status: "healthy", Version: "1.2.3", Compatible: true})
			return rpc.Response{Success: true, Data: hd}
		case "ping":
			return rpc.Response{Success: true}
		case "status":
			return rpc.Response{Success: true, Data: statusData}
		default:
			return rpc.Response{Success: false, Error: "unknown: " + req.Operation}
		}
	})

	pool := newRoutesMockPool(t, socketPath)
	handler := handleDaemonStatus(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/daemon/status", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp DaemonStatusResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if !resp.Success {
		t.Errorf("expected Success=true, got false (error: %s)", resp.Error)
	}

	if resp.Data == nil {
		t.Fatal("expected Data to be non-nil")
	}

	if resp.Data.Version != "1.2.3" {
		t.Errorf("Version = %q, want %q", resp.Data.Version, "1.2.3")
	}
	if !resp.Data.AutoCommit {
		t.Error("expected AutoCommit=true")
	}
	if resp.Data.AutoPush {
		t.Error("expected AutoPush=false")
	}
	if !resp.Data.AutoPull {
		t.Error("expected AutoPull=true")
	}
	if resp.Data.SyncInterval != "5s" {
		t.Errorf("SyncInterval = %q, want %q", resp.Data.SyncInterval, "5s")
	}
}

// TestHandleStats_SuccessWithMockServer tests the full stats success path with mock server.
func TestHandleStats_SuccessWithMockServer(t *testing.T) {
	stats := types.Statistics{
		TotalIssues:      42,
		OpenIssues:       20,
		InProgressIssues: 10,
		ClosedIssues:     12,
		ReadyIssues:      8,
	}
	statsData, _ := json.Marshal(stats)

	socketPath := startRoutesMockServer(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "health":
			hd, _ := json.Marshal(rpc.HealthResponse{Status: "healthy", Version: "0.0.0", Compatible: true})
			return rpc.Response{Success: true, Data: hd}
		case "ping":
			return rpc.Response{Success: true}
		case "stats":
			return rpc.Response{Success: true, Data: statsData}
		default:
			return rpc.Response{Success: false, Error: "unknown: " + req.Operation}
		}
	})

	pool := newRoutesMockPool(t, socketPath)
	handler := handleStats(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp StatsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if !resp.Success {
		t.Errorf("expected Success=true, got false (error: %s)", resp.Error)
	}

	if resp.Data == nil {
		t.Fatal("expected Data to be non-nil")
	}

	if resp.Data.TotalIssues != 42 {
		t.Errorf("TotalIssues = %d, want 42", resp.Data.TotalIssues)
	}
}

// TestHandleAPIHealth_SuccessWithMockServer tests the full API health success path.
func TestHandleAPIHealth_SuccessWithMockServer(t *testing.T) {
	socketPath := startRoutesMockServer(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "health":
			hd, _ := json.Marshal(rpc.HealthResponse{Status: "healthy", Version: "1.0.0", Compatible: true, Uptime: 3600})
			return rpc.Response{Success: true, Data: hd}
		case "ping":
			return rpc.Response{Success: true}
		default:
			return rpc.Response{Success: false, Error: "unknown: " + req.Operation}
		}
	})

	pool := newRoutesMockPool(t, socketPath)
	handler := handleAPIHealth(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp HealthStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Status != "ok" {
		t.Errorf("Status = %q, want %q", resp.Status, "ok")
	}

	if !resp.Daemon.Connected {
		t.Error("expected Daemon.Connected=true")
	}

	if resp.Daemon.Version != "1.0.0" {
		t.Errorf("Daemon.Version = %q, want %q", resp.Daemon.Version, "1.0.0")
	}
}

// --- SSE route conditional registration tests ---

// TestSetupRoutes_SSEEndpointNotRegisteredWhenHubNil verifies that GET /api/events
// falls through to the frontend handler when hub is nil.
func TestSetupRoutes_SSEEndpointNotRegisteredWhenHubNil(t *testing.T) {
	mux := http.NewServeMux()
	setupRoutes(mux, nil, nil, nil, nil, "", nil, nil, nil, "", false, nil, nil, nil, nil, false, false, "", "")

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	// With nil hub, the SSE route is not registered; request falls through to frontend
	if rr.Code != http.StatusOK {
		t.Errorf("expected /api/events to fall through to frontend with status %d, got %d",
			http.StatusOK, rr.Code)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "text/html; charset=utf-8" {
		t.Logf("Content-Type: %q (may vary based on file detection)", ct)
	}
}

// TestSetupRoutes_SSEEndpointRegisteredWhenHubNonNil verifies that GET /api/events
// is handled by the SSE handler when hub is non-nil.
func TestSetupRoutes_SSEEndpointRegisteredWhenHubNonNil(t *testing.T) {
	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	mux := http.NewServeMux()
	setupRoutes(mux, nil, hub, nil, nil, "", nil, nil, nil, "", false, nil, nil, nil, nil, false, false, "", "")

	// Use a context with short timeout because the SSE handler streams forever
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	// With non-nil hub, the route IS registered.
	// The SSE handler sets Content-Type to text/event-stream, not HTML.
	ct := rr.Header().Get("Content-Type")
	if ct == "text/html; charset=utf-8" {
		t.Error("expected SSE route to be registered, but request fell through to frontend handler")
	}
}

// --- Auth token route conditional registration tests ---

// TestSetupRoutes_AuthTokenRegisteredWhenEnabled verifies that GET /api/auth/token
// is handled when authEnabled=true.
func TestSetupRoutes_AuthTokenRegisteredWhenEnabled(t *testing.T) {
	mux := http.NewServeMux()
	setupRoutes(mux, nil, nil, nil, nil, "", nil, nil, nil, "test-api-key", true, nil, nil, nil, nil, false, false, "", "")

	req := httptest.NewRequest(http.MethodGet, "/api/auth/token", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	// Auth token handler returns JSON, not HTML
	ct := rr.Header().Get("Content-Type")
	if ct == "text/html; charset=utf-8" {
		t.Error("expected auth token route to be registered, but request fell through to frontend handler")
	}

	if ct != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got %q", ct)
	}
}

// TestSetupRoutes_AuthTokenNotRegisteredWhenDisabled verifies that GET /api/auth/token
// falls through to the frontend when authEnabled=false.
func TestSetupRoutes_AuthTokenNotRegisteredWhenDisabled(t *testing.T) {
	mux := http.NewServeMux()
	setupRoutes(mux, nil, nil, nil, nil, "", nil, nil, nil, "", false, nil, nil, nil, nil, false, false, "", "")

	req := httptest.NewRequest(http.MethodGet, "/api/auth/token", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	// Route is not registered; falls through to frontend
	if rr.Code != http.StatusOK {
		t.Errorf("expected /api/auth/token to fall through to frontend with status %d, got %d",
			http.StatusOK, rr.Code)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "text/html; charset=utf-8" {
		t.Logf("Content-Type: %q (may vary based on file detection)", ct)
	}
}

// --- Terminal token route conditional registration tests ---

// TestSetupRoutes_TerminalTokenRegisteredWithAuth verifies that GET /api/terminal/token
// is handled when both termManager and termAuth are non-nil.
func TestSetupRoutes_TerminalTokenRegisteredWithAuth(t *testing.T) {
	termMgr, err := NewTerminalManager("bash", "", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	t.Cleanup(func() { termMgr.Shutdown() })

	termAuth, err := newTerminalAuth()
	if err != nil {
		t.Fatalf("failed to create terminal auth: %v", err)
	}
	t.Cleanup(func() { termAuth.Stop() })

	mux := http.NewServeMux()
	setupRoutes(mux, nil, nil, nil, termMgr, "bash", termAuth, nil, nil, "", false, nil, nil, nil, nil, false, false, "", "")

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/token?session=test", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	// Route is registered; handler returns JSON (either token or error)
	ct := rr.Header().Get("Content-Type")
	if ct == "text/html; charset=utf-8" {
		t.Error("expected terminal token route to be registered, but request fell through to frontend handler")
	}
}

// TestSetupRoutes_TerminalTokenNotRegisteredWithoutAuth verifies that GET /api/terminal/token
// falls through to the frontend when termAuth is nil (even if termManager is non-nil).
func TestSetupRoutes_TerminalTokenNotRegisteredWithoutAuth(t *testing.T) {
	termMgr, err := NewTerminalManager("bash", "", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	t.Cleanup(func() { termMgr.Shutdown() })

	mux := http.NewServeMux()
	// termAuth is nil
	setupRoutes(mux, nil, nil, nil, termMgr, "bash", nil, nil, nil, "", false, nil, nil, nil, nil, false, false, "", "")

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/token?session=test", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	// Route is not registered (termAuth is nil); falls through to frontend
	if rr.Code != http.StatusOK {
		t.Errorf("expected /api/terminal/token to fall through to frontend with status %d, got %d",
			http.StatusOK, rr.Code)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "text/html; charset=utf-8" {
		t.Logf("Content-Type: %q (may vary based on file detection)", ct)
	}
}

// --- Health endpoint method restriction test ---

// TestSetupRoutes_HealthEndpoint_GETOnly verifies that GET /health returns 200 JSON
// and POST /health falls through to the frontend.
func TestSetupRoutes_HealthEndpoint_GETOnly(t *testing.T) {
	mux := http.NewServeMux()
	setupRoutes(mux, nil, nil, nil, nil, "", nil, nil, nil, "", false, nil, nil, nil, nil, false, false, "", "")

	// GET should return JSON health response
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("GET /health: expected status %d, got %d", http.StatusOK, rr.Code)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("GET /health: expected Content-Type 'application/json', got %q", ct)
	}

	// POST should fall through to frontend handler
	req = httptest.NewRequest(http.MethodPost, "/health", nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	ct = rr.Header().Get("Content-Type")
	if ct == "application/json" {
		t.Error("POST /health: should fall through to frontend, not return JSON")
	}
}

// --- Issue endpoints method restriction tests ---

// TestSetupRoutes_IssueEndpoints_MethodRestrictions verifies HTTP method restrictions
// on issue endpoints. Uses a mock server pool to avoid nil-pool panics in handlers.
func TestSetupRoutes_IssueEndpoints_MethodRestrictions(t *testing.T) {
	socketPath := startRoutesMockServer(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "health":
			hd, _ := json.Marshal(rpc.HealthResponse{Status: "healthy", Version: "0.0.0", Compatible: true})
			return rpc.Response{Success: true, Data: hd}
		case "ping":
			return rpc.Response{Success: true}
		default:
			return rpc.Response{Success: false, Error: "not implemented: " + req.Operation}
		}
	})
	pool := newRoutesMockPool(t, socketPath)

	mux := http.NewServeMux()
	setupRoutes(mux, pool, nil, nil, nil, "", nil, nil, nil, "", false, nil, nil, nil, nil, false, false, "", "")

	tests := []struct {
		name        string
		method      string
		path        string
		expectRoute bool // true if route is registered for this method
	}{
		{"GET /api/issues", http.MethodGet, "/api/issues", true},
		{"POST /api/issues", http.MethodPost, "/api/issues", true},
		{"DELETE /api/issues", http.MethodDelete, "/api/issues", false},
		{"GET /api/issues/test-id", http.MethodGet, "/api/issues/test-id", true},
		{"PATCH /api/issues/test-id", http.MethodPatch, "/api/issues/test-id", true},
		{"PUT /api/issues/test-id", http.MethodPut, "/api/issues/test-id", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			ct := rr.Header().Get("Content-Type")
			isHTML := ct == "text/html; charset=utf-8"

			if tt.expectRoute && isHTML {
				t.Errorf("%s %s: expected route handler, but fell through to frontend (HTML)",
					tt.method, tt.path)
			}
			if !tt.expectRoute && !isHTML {
				t.Errorf("%s %s: expected to fall through to frontend (HTML), got Content-Type %q",
					tt.method, tt.path, ct)
			}
		})
	}
}

// --- Dependency endpoints method restriction tests ---

// TestSetupRoutes_DependencyEndpoints_MethodRestrictions verifies HTTP method
// restrictions on dependency endpoints. Uses a mock server pool.
func TestSetupRoutes_DependencyEndpoints_MethodRestrictions(t *testing.T) {
	socketPath := startRoutesMockServer(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "health":
			hd, _ := json.Marshal(rpc.HealthResponse{Status: "healthy", Version: "0.0.0", Compatible: true})
			return rpc.Response{Success: true, Data: hd}
		case "ping":
			return rpc.Response{Success: true}
		default:
			return rpc.Response{Success: false, Error: "not implemented: " + req.Operation}
		}
	})
	pool := newRoutesMockPool(t, socketPath)

	mux := http.NewServeMux()
	setupRoutes(mux, pool, nil, nil, nil, "", nil, nil, nil, "", false, nil, nil, nil, nil, false, false, "", "")

	tests := []struct {
		name        string
		method      string
		path        string
		expectRoute bool
	}{
		{"POST /api/issues/id/dependencies", http.MethodPost, "/api/issues/test-id/dependencies", true},
		{"DELETE /api/issues/id/dependencies/depId", http.MethodDelete, "/api/issues/test-id/dependencies/dep-1", true},
		{"GET /api/issues/id/dependencies", http.MethodGet, "/api/issues/test-id/dependencies", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			ct := rr.Header().Get("Content-Type")
			isHTML := ct == "text/html; charset=utf-8"

			if tt.expectRoute && isHTML {
				t.Errorf("%s %s: expected route handler, but fell through to frontend (HTML)",
					tt.method, tt.path)
			}
			if !tt.expectRoute && !isHTML {
				t.Errorf("%s %s: expected to fall through to frontend (HTML), got Content-Type %q",
					tt.method, tt.path, ct)
			}
		})
	}
}

// --- Fleet endpoints all routes test ---

// TestSetupRoutes_FleetEndpoints_AllRoutes verifies that all fleet endpoints are
// registered when fleetEnabled=true and that GET on POST-only routes falls through.
func TestSetupRoutes_FleetEndpoints_AllRoutes(t *testing.T) {
	mux := http.NewServeMux()
	setupRoutes(mux, nil, nil, nil, nil, "", nil, nil, nil, "", false, nil, nil, nil, nil, true, false, "", "")

	// POST routes should be handled (not fall through to frontend)
	postRoutes := []string{
		"/api/fleet/register",
		"/api/fleet/claim",
		"/api/fleet/done/test-id",
		"/api/fleet/heartbeat",
	}

	for _, path := range postRoutes {
		t.Run("POST "+path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			ct := rr.Header().Get("Content-Type")
			if ct == "text/html; charset=utf-8" {
				t.Errorf("POST %s: expected fleet handler, but fell through to frontend", path)
			}
		})
	}

	// GET on POST-only fleet routes should fall through to frontend
	for _, path := range postRoutes {
		t.Run("GET "+path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			ct := rr.Header().Get("Content-Type")
			if ct != "text/html; charset=utf-8" {
				t.Errorf("GET %s: expected to fall through to frontend, got Content-Type %q", path, ct)
			}
		})
	}
}

// --- Dev mode frontend handler test ---

// TestSetupRoutes_DevMode_FrontendHandler verifies that the catch-all handler
// uses devFrontendHandler when devMode=true and doesn't panic.
func TestSetupRoutes_DevMode_FrontendHandler(t *testing.T) {
	mux := http.NewServeMux()
	// Pass devMode=true with a non-existent dir; the handler should not panic
	setupRoutes(mux, nil, nil, nil, nil, "", nil, nil, nil, "", false, nil, nil, nil, nil, false, true, "/nonexistent/dev/dir", "")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	// This should not panic
	mux.ServeHTTP(rr, req)

	// The dev handler will try to serve from the directory; since it doesn't exist,
	// it will attempt SPA fallback. Either way, we get a response (no crash).
	if rr.Code == 0 {
		t.Error("expected a non-zero status code")
	}
}

// --- Loom proxy conditional registration tests ---

// TestSetupRoutes_LoomProxy_RegisteredWhenURLSet verifies that /api/loom/ is handled
// when a valid loomServerURL is provided.
func TestSetupRoutes_LoomProxy_RegisteredWhenURLSet(t *testing.T) {
	// Clear env to avoid interference
	t.Setenv("LOOM_SERVER_URL", "")

	mux := http.NewServeMux()
	setupRoutes(mux, nil, nil, nil, nil, "", nil, nil, nil, "", false, nil, nil, nil, nil, false, false, "", "http://localhost:9999")

	req := httptest.NewRequest(http.MethodGet, "/api/loom/status", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	// With a valid URL, the proxy is registered.
	// The proxy will fail to connect to localhost:9999 but the route IS handled
	// (not falling through to the frontend handler).
	ct := rr.Header().Get("Content-Type")
	if ct == "text/html; charset=utf-8" {
		t.Error("expected loom proxy route to be registered, but request fell through to frontend handler")
	}
}

// TestSetupRoutes_LoomProxy_NotRegisteredWhenURLInvalid verifies that /api/loom/ falls
// through when the loomServerURL is invalid and causes newLoomProxy to return nil.
func TestSetupRoutes_LoomProxy_NotRegisteredWhenURLInvalid(t *testing.T) {
	// Clear env and use an invalid scheme so newLoomProxy returns nil
	t.Setenv("LOOM_SERVER_URL", "")
	t.Setenv("LOOM_PROXY_ALLOWED_HOSTS", "")

	mux := http.NewServeMux()
	// Use a URL with a non-localhost host and no allowed hosts → proxy returns nil
	setupRoutes(mux, nil, nil, nil, nil, "", nil, nil, nil, "", false, nil, nil, nil, nil, false, false, "", "http://external-host.example.com:9999")

	req := httptest.NewRequest(http.MethodGet, "/api/loom/status", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	// With nil proxy, the route falls through to frontend handler
	ct := rr.Header().Get("Content-Type")
	if ct != "text/html; charset=utf-8" {
		t.Errorf("expected /api/loom/status to fall through to frontend, got Content-Type %q", ct)
	}
}
