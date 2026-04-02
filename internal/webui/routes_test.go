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
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

// setupTestRoutes constructs handlers and registers routes on the given mux.
// Cleans up rate limiter goroutines when the test finishes.
func setupTestRoutes(t *testing.T, app *Server, mux *http.ServeMux) {
	t.Helper()
	app.buildHandlers()
	app.setupRoutes(mux)
	t.Cleanup(func() {
		if app.clientErrLimiter != nil {
			app.clientErrLimiter.stop()
		}
		if app.cspLimiter != nil {
			app.cspLimiter.stop()
		}
		if app.authCfgLimiter != nil {
			app.authCfgLimiter.stop()
		}
	})
}

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

// TestSetupRoutes_FlatTerminalWSEndpointReturns404 tests that
// the flat terminal WebSocket endpoint returns 404 (removed in favor of workspace-scoped).
func TestSetupRoutes_FlatTerminalWSEndpointReturns404(t *testing.T) {
	mux := http.NewServeMux()
	setupTestRoutes(t, &Server{}, mux)

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/ws?session=test", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected flat /api/terminal/ws to return 404, got %d", rr.Code)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

// TestSetupRoutes_FlatTerminalRoutesReturn404 verifies that flat terminal routes
// return 404 even when termManager is non-nil (they have been removed in favor
// of workspace-scoped equivalents).
func TestSetupRoutes_FlatTerminalRoutesReturn404(t *testing.T) {
	termMgr, err := NewTerminalManager("bash", "", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer termMgr.Shutdown()

	mux := http.NewServeMux()
	setupTestRoutes(t, &Server{termMgr: termMgr}, mux)

	flatRoutes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/terminal/sessions"},
		{http.MethodGet, "/api/terminal/ws?session=test"},
		{http.MethodPost, "/api/terminal/restart?session=test"},
		{http.MethodPost, "/api/terminal/kill?session=test"},
		{http.MethodGet, "/api/terminal/session-status?session=test"},
		{http.MethodPost, "/api/terminal/spawn"},
		{http.MethodPost, "/api/terminal/sessions/test/seed"},
		{http.MethodPost, "/api/terminal/sessions/test/kill"},
		{http.MethodPost, "/api/terminal/sessions/close-all"},
	}

	for _, tc := range flatRoutes {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			if rr.Code != http.StatusNotFound {
				t.Errorf("flat route %s %s: expected 404, got %d", tc.method, tc.path, rr.Code)
			}
		})
	}
}

// TestSetupRoutes_TerminalEndpointNilManagerReturns503 tests that
// calling handleTerminalWS directly with nil manager returns 503.
// This complements the route registration test by verifying handler behavior.
func TestSetupRoutes_TerminalEndpointNilManagerReturns503(t *testing.T) {
	handler := handleTerminalWS(nil, nil, nil, "", nil, nil, nil)

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
	setupTestRoutes(t, &Server{}, mux)

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

// TestSetupRoutes_StatsEndpointPOSTFallsThrough tests that POST to stats returns 404 JSON.
// Note: Go 1.22's pattern matching means "GET /api/stats" only matches GET requests.
// A POST to /api/stats doesn't match that route, so it falls through to the
// catch-all frontend handler which rejects /api/* paths with 404 JSON.
func TestSetupRoutes_StatsEndpointPOSTFallsThrough(t *testing.T) {
	mux := http.NewServeMux()
	setupTestRoutes(t, &Server{}, mux)

	// POST to GET-only endpoint falls through to frontend handler which rejects /api/* paths
	req := httptest.NewRequest(http.MethodPost, "/api/stats", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected POST /api/stats to return 404, got %d", rr.Code)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
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
	setupTestRoutes(t, &Server{}, mux) // fleetEnabled=false

	// Request to fleet endpoint should return 404 JSON since the route is not
	// registered and the SPA catch-all rejects /api/* paths
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected /api/fleet/claim to return 404 when fleetEnabled is false, got %d", rr.Code)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

// TestSetupRoutes_FlatFleetRoutesReturn404 verifies that flat fleet routes
// return 404 even when fleet is enabled (they have been removed in favor of
// workspace-scoped equivalents).
func TestSetupRoutes_FlatFleetRoutesReturn404(t *testing.T) {
	mux := http.NewServeMux()
	setupTestRoutes(t, &Server{}, mux) // fleetEnabled=true

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected flat /api/fleet/claim to return 404, got %d", rr.Code)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
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

// TestSetupRoutes_LegacySSEEndpointReturns404 verifies that GET /api/events
// (legacy endpoint) returns 404 now that SSE is workspace-scoped.
func TestSetupRoutes_LegacySSEEndpointReturns404(t *testing.T) {
	mux := http.NewServeMux()
	setupTestRoutes(t, &Server{}, mux)

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	// Legacy SSE endpoint removed; SPA catch-all rejects /api/* with 404 JSON
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected /api/events to return 404, got %d", rr.Code)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

// TestSetupRoutes_SSEEndpointRegisteredOnWorkspaceScope verifies that
// GET /api/workspaces/{ws}/events is handled by the SSE handler when
// hub and multiPool are non-nil.
func TestSetupRoutes_SSEEndpointRegisteredOnWorkspaceScope(t *testing.T) {
	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	multiPool := daemon.NewMultiPool(WorkspaceFromContext, 1)
	// Register a fake workspace so WorkspaceMiddleware passes
	_ = multiPool.Register("test-ws", &stubPool{})

	mux := http.NewServeMux()
	wsExistsFn := func(id string) bool { return multiPool.PoolForWorkspace(id) != nil }
	setupTestRoutes(t, &Server{multiPool: multiPool, hub: hub, wsExistsFn: wsExistsFn}, mux)

	// Use a context with short timeout because the SSE handler streams forever
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/events", nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	// With non-nil hub and multiPool, the workspace-scoped SSE route IS registered.
	// The SSE handler sets Content-Type to text/event-stream.
	ct := rr.Header().Get("Content-Type")
	if ct == "text/html; charset=utf-8" {
		t.Error("expected SSE route to be registered, but request fell through to frontend handler")
	}
}

// TestSetupRoutes_WorkspaceBackendPatchEndpoint verifies that
// PATCH /api/workspaces/{ws}/config/backend is handled by handleWorkspaceBackendPatch
// (which returns workspaceResponse shape) rather than handlePatchBackendConfig.
func TestSetupRoutes_WorkspaceBackendPatchEndpoint(t *testing.T) {
	// Set up a temp config dir with a test workspace so the handler can load it
	dir := t.TempDir()
	setLoomConfigDir(t, dir)

	writeTestBackendConfig(t, dir, &loomConfigForBackend{
		Version: 1,
		Backend: "claude",
		Workspaces: map[string]loomWorkspaceForBackend{
			"test-ws": {ID: "test-ws", Path: "/tmp/test", Backend: "claude"},
		},
	})

	multiPool := daemon.NewMultiPool(WorkspaceFromContext, 1)
	_ = multiPool.Register("test-ws", &stubPool{})

	mux := http.NewServeMux()
	wsExistsFn := func(id string) bool { return multiPool.PoolForWorkspace(id) != nil }
	workspaceConfigFn := func() (*WorkspaceData, error) {
		return &WorkspaceData{Name: "test-ws", Path: "/tmp/test"}, nil
	}
	setupTestRoutes(t, &Server{multiPool: multiPool, config: ServerConfig{WorkspaceConfigFn: workspaceConfigFn}, wsExistsFn: wsExistsFn}, mux)

	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/test-ws/config/backend",
		strings.NewReader(`{"backend":"codex"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	// The route must be registered (not fall through to SPA catch-all)
	ct := rr.Header().Get("Content-Type")
	if ct == "text/html; charset=utf-8" {
		t.Fatal("expected workspace backend PATCH route to be registered, but request fell through to frontend handler")
	}

	// Response must be JSON with workspaceResponse shape (has "success" field)
	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}

	if _, ok := body["success"]; !ok {
		t.Error("response missing 'success' field — expected workspaceResponse shape")
	}

	// Verify the handler returned data with WorkspaceData shape (has "name" field)
	// which only handleWorkspaceBackendPatch provides, not handlePatchBackendConfig
	if rr.Code == http.StatusOK {
		data, ok := body["data"].(map[string]interface{})
		if !ok {
			t.Error("expected 'data' field in successful response")
		} else if _, hasName := data["name"]; !hasName {
			t.Error("expected 'name' field in data — handleWorkspaceBackendPatch returns WorkspaceData")
		}
	}
}

// --- Terminal token route conditional registration tests ---

// TestSetupRoutes_FlatTerminalTokenReturns404 verifies that GET /api/terminal/token
// returns 404 (flat route removed, only workspace-scoped route exists).
func TestSetupRoutes_FlatTerminalTokenReturns404(t *testing.T) {
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
	setupTestRoutes(t, &Server{termMgr: termMgr, termAuth: termAuth}, mux)

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/token?session=test", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected flat /api/terminal/token to return 404, got %d", rr.Code)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

// --- Health endpoint method restriction test ---

// TestSetupRoutes_HealthEndpoint_GETOnly verifies that GET /health returns 200 JSON
// and POST /health falls through to the frontend.
func TestSetupRoutes_HealthEndpoint_GETOnly(t *testing.T) {
	mux := http.NewServeMux()
	setupTestRoutes(t, &Server{}, mux)

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
	setupTestRoutes(t, &Server{pool: pool}, mux)

	tests := []struct {
		name        string
		method      string
		path        string
		expectRoute bool // true if route is registered for this method
	}{
		{"GET /api/issues", http.MethodGet, "/api/issues", false},
		{"POST /api/issues", http.MethodPost, "/api/issues", false},
		{"DELETE /api/issues", http.MethodDelete, "/api/issues", false},
		{"DELETE /api/issues/test-id", http.MethodDelete, "/api/issues/test-id", false},
		{"GET /api/issues/test-id", http.MethodGet, "/api/issues/test-id", false},
		{"PATCH /api/issues/test-id", http.MethodPatch, "/api/issues/test-id", false},
		{"PUT /api/issues/test-id", http.MethodPut, "/api/issues/test-id", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			ct := rr.Header().Get("Content-Type")

			if tt.expectRoute {
				// Route should be handled by a registered handler (returns JSON, not 404)
				if rr.Code == http.StatusNotFound && ct == "application/json" {
					t.Errorf("%s %s: expected route handler, but got 404 JSON (unregistered API path)",
						tt.method, tt.path)
				}
			} else {
				// Unregistered /api/* paths should return 404 JSON
				if rr.Code != http.StatusNotFound {
					t.Errorf("%s %s: expected 404 for unregistered API path, got %d",
						tt.method, tt.path, rr.Code)
				}
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
	setupTestRoutes(t, &Server{pool: pool}, mux)

	tests := []struct {
		name        string
		method      string
		path        string
		expectRoute bool
	}{
		{"POST /api/issues/id/dependencies", http.MethodPost, "/api/issues/test-id/dependencies", false},
		{"DELETE /api/issues/id/dependencies/depId", http.MethodDelete, "/api/issues/test-id/dependencies/dep-1", false},
		{"GET /api/issues/id/dependencies", http.MethodGet, "/api/issues/test-id/dependencies", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			ct := rr.Header().Get("Content-Type")

			if tt.expectRoute {
				if rr.Code == http.StatusNotFound && ct == "application/json" {
					t.Errorf("%s %s: expected route handler, but got 404 JSON (unregistered API path)",
						tt.method, tt.path)
				}
			} else {
				// Unregistered /api/* paths should return 404 JSON
				if rr.Code != http.StatusNotFound {
					t.Errorf("%s %s: expected 404 for unregistered API path, got %d",
						tt.method, tt.path, rr.Code)
				}
			}
		})
	}
}

// --- Fleet endpoints all routes test ---

// TestSetupRoutes_FleetEndpoints_AllFlatRoutesReturn404 verifies that all flat
// fleet endpoints return 404 (removed in favor of workspace-scoped equivalents).
func TestSetupRoutes_FleetEndpoints_AllFlatRoutesReturn404(t *testing.T) {
	mux := http.NewServeMux()
	setupTestRoutes(t, &Server{}, mux)

	flatRoutes := []string{
		"/api/fleet/register",
		"/api/fleet/claim",
		"/api/fleet/done/test-id",
		"/api/fleet/heartbeat",
	}

	for _, path := range flatRoutes {
		t.Run("POST "+path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			if rr.Code != http.StatusNotFound {
				t.Errorf("POST %s: expected 404 for removed flat route, got %d", path, rr.Code)
			}

			ct := rr.Header().Get("Content-Type")
			if ct != "application/json" {
				t.Errorf("POST %s: Content-Type = %q, want %q", path, ct, "application/json")
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
	setupTestRoutes(t, &Server{config: ServerConfig{DevMode: true, DevFrontendDir: "/nonexistent/dev/dir"}}, mux)

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
	setupTestRoutes(t, &Server{config: ServerConfig{LoomServerURL: "http://localhost:9999"}}, mux)

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
	setupTestRoutes(t, &Server{config: ServerConfig{LoomServerURL: "http://external-host.example.com:9999"}}, mux)

	req := httptest.NewRequest(http.MethodGet, "/api/loom/status", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	// With nil proxy, the route falls through to SPA catch-all which rejects /api/* with 404 JSON
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected /api/loom/status to return 404, got %d", rr.Code)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

// --- Tab metadata route conditional registration tests ---

// TestSetupRoutes_TabMetadataReturns404WhenStoreNil verifies that GET /api/terminal/tabs
// returns a 404 JSON response (not SPA HTML) when tabMetaStore is nil.
// When Redis is not configured the tab metadata routes are not registered,
// so the request falls through to the SPA catch-all which rejects /api/* with 404 JSON.
func TestSetupRoutes_TabMetadataReturns404WhenStoreNil(t *testing.T) {
	mux := http.NewServeMux()
	// All nil params — tabMetaStore (param 21) is nil, so tab metadata routes are not registered.
	setupTestRoutes(t, &Server{}, mux)

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/tabs", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	// Route is not registered; SPA catch-all rejects /api/* with 404 JSON
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected /api/terminal/tabs to return %d when tabMetaStore is nil, got %d",
			http.StatusNotFound, rr.Code)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got %q", ct)
	}
}

// TestLegacyFlatAgentRoutesRemoved verifies that the legacy flat agent routes
// (e.g. POST /api/agents/{name}/git/push) have been removed and return 404,
// while the workspace-scoped equivalents (e.g.
// POST /api/workspaces/{ws}/agents/{name}/git/push) still work.
func TestLegacyFlatAgentRoutesRemoved(t *testing.T) {
	// Set up a multiPool with a registered workspace so workspace-scoped routes
	// are functional.
	multiPool := daemon.NewMultiPool(WorkspaceFromContext, 1)
	_ = multiPool.Register("test-ws", &stubPool{})

	wsExistsFn := func(id string) bool { return multiPool.PoolForWorkspace(id) != nil }

	gitOps := &mockGitOps{}
	fileOps := &mockFileOps{}

	mux := http.NewServeMux()
	setupTestRoutes(t, &Server{multiPool: multiPool, config: ServerConfig{GitOps: gitOps, FileOps: fileOps}, wsExistsFn: wsExistsFn}, mux)

	// Legacy flat routes that should have been removed — each must return 404.
	legacyRoutes := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/git/push-all"},
		{http.MethodPost, "/api/agents/alice/git/push"},
		{http.MethodPost, "/api/agents/alice/git/pull"},
		{http.MethodPost, "/api/agents/alice/git/sync"},
		{http.MethodPost, "/api/agents/alice/git/pr"},
		{http.MethodPost, "/api/agents/alice/git/reset"},
		{http.MethodGet, "/api/agents/alice/git/status"},
		{http.MethodPatch, "/api/agents/alice/git/target"},
		{http.MethodGet, "/api/issues/ISSUE-1/git/diff-stat"},
		{http.MethodGet, "/api/agents/alice/diff/commits"},
		{http.MethodGet, "/api/agents/alice/diff/files"},
		{http.MethodGet, "/api/agents/alice/diff/file"},
		{http.MethodGet, "/api/agents/alice/files/tree"},
		{http.MethodGet, "/api/agents/alice/files"},
		{http.MethodPut, "/api/agents/alice/files"},
	}

	for _, tc := range legacyRoutes {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			if rr.Code != http.StatusNotFound {
				t.Errorf("legacy route %s %s: expected status %d, got %d",
					tc.method, tc.path, http.StatusNotFound, rr.Code)
			}

			ct := rr.Header().Get("Content-Type")
			if ct != "application/json" {
				t.Errorf("legacy route %s %s: expected Content-Type 'application/json', got %q",
					tc.method, tc.path, ct)
			}
		})
	}

	// Workspace-scoped equivalents should be handled by the wsMux routes.
	// The mock ops return "not found" for agent resolution, so handlers may
	// still return 404 with an agent-specific error. The key assertion is that
	// the response body differs from the SPA catch-all's generic {"error":"not found"}.
	// If the route were truly unregistered, the SPA catch-all would respond with
	// exactly that generic message.
	scopedRoutes := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/workspaces/test-ws/git/push-all"},
		{http.MethodPost, "/api/workspaces/test-ws/agents/alice/git/push"},
		{http.MethodPost, "/api/workspaces/test-ws/agents/alice/git/pull"},
		{http.MethodPost, "/api/workspaces/test-ws/agents/alice/git/sync"},
		{http.MethodPost, "/api/workspaces/test-ws/agents/alice/git/pr"},
		{http.MethodPost, "/api/workspaces/test-ws/agents/alice/git/reset"},
		{http.MethodGet, "/api/workspaces/test-ws/agents/alice/git/status"},
		{http.MethodPatch, "/api/workspaces/test-ws/agents/alice/git/target"},
		{http.MethodGet, "/api/workspaces/test-ws/agents/alice/diff/commits"},
		{http.MethodGet, "/api/workspaces/test-ws/agents/alice/diff/files"},
		{http.MethodGet, "/api/workspaces/test-ws/agents/alice/diff/file"},
		{http.MethodGet, "/api/workspaces/test-ws/agents/alice/files/tree"},
		{http.MethodGet, "/api/workspaces/test-ws/agents/alice/files"},
		{http.MethodPut, "/api/workspaces/test-ws/agents/alice/files"},
	}

	for _, tc := range scopedRoutes {
		t.Run("scoped "+tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			// The SPA catch-all returns exactly {"error":"not found"} for
			// unregistered /api/* paths. If the route is properly registered,
			// the handler produces a different response (even if it is still
			// a 404 with a more specific error message like "agent worktree
			// \"alice\" not found").
			var body map[string]interface{}
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("workspace-scoped route %s %s: failed to parse JSON body: %v",
					tc.method, tc.path, err)
			}

			errMsg, _ := body["error"].(string)
			if rr.Code == http.StatusNotFound && errMsg == "not found" {
				t.Errorf("workspace-scoped route %s %s fell through to SPA catch-all (got generic 404 %q); route is not registered",
					tc.method, tc.path, errMsg)
			}
		})
	}
}
