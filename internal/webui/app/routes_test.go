package app

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

	"github.com/tysonthomas9/loomcli/internal/webui"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	healthhandlers "github.com/tysonthomas9/loomcli/internal/webui/handlers/health"
	hterminal "github.com/tysonthomas9/loomcli/internal/webui/handlers/terminal"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/svcimpl"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// setupTestRoutes constructs handlers and registers routes on app.mux.
// Cleans up rate limiter goroutines when the test finishes.
func setupTestRoutes(t *testing.T, app *Server) {
	t.Helper()
	app.mux = http.NewServeMux()
	app.buildHandlers()
	app.buildModules()
	app.registerRoutes()
	t.Cleanup(func() {
		if app.handlers != nil {
			if app.handlers.ClientErrLimiter != nil {
				app.handlers.ClientErrLimiter.Stop()
			}
			if app.handlers.AuthCfgLimiter != nil {
				app.handlers.AuthCfgLimiter.Stop()
			}
		}
	})
}

// TestHandleStats_NilPool verifies that handleStats returns 503 when pool is nil.
func TestHandleStats_NilPool(t *testing.T) {
	handler := healthhandlers.HandleStats(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d", http.StatusServiceUnavailable, rr.Code)
	}

	var resp healthhandlers.StatsResponse
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

	handler := healthhandlers.HandleStats(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	// Should be either 503 (service unavailable) or 504 (timeout)
	if rr.Code != http.StatusServiceUnavailable && rr.Code != http.StatusGatewayTimeout {
		t.Errorf("expected status %d or %d, got %d", http.StatusServiceUnavailable, http.StatusGatewayTimeout, rr.Code)
	}

	var resp healthhandlers.StatsResponse
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

	handler := healthhandlers.HandleStats(pool)

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

	var resp healthhandlers.StatsResponse
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

	handler := healthhandlers.HandleStats(pool)

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

	var resp healthhandlers.StatsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Success {
		t.Error("expected Success to be false")
	}
}

// TestStatsResponse_SuccessSerialization tests successful healthhandlers.StatsResponse serialization.
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

	resp := healthhandlers.StatsResponse{
		Success: true,
		Data:    stats,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal response: %v", err)
	}

	var parsed healthhandlers.StatsResponse
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

// TestStatsResponse_ErrorSerialization tests error healthhandlers.StatsResponse serialization.
func TestStatsResponse_ErrorSerialization(t *testing.T) {
	resp := healthhandlers.StatsResponse{
		Success: false,
		Error:   "connection failed",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal response: %v", err)
	}

	var parsed healthhandlers.StatsResponse
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
	resp := healthhandlers.StatsResponse{
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
	resp := healthhandlers.StatsResponse{
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

// TestHealthStatus_HealthySerialization tests healthy healthhandlers.HealthStatus serialization.
func TestHealthStatus_HealthySerialization(t *testing.T) {
	status := healthhandlers.HealthStatus{
		Status: "ok",
		Daemon: healthhandlers.DaemonStatus{
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
		t.Fatalf("failed to marshal healthhandlers.HealthStatus: %v", err)
	}

	var parsed healthhandlers.HealthStatus
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal healthhandlers.HealthStatus: %v", err)
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

// TestHealthStatus_DegradedSerialization tests degraded healthhandlers.HealthStatus serialization.
func TestHealthStatus_DegradedSerialization(t *testing.T) {
	status := healthhandlers.HealthStatus{
		Status: "degraded",
		Daemon: healthhandlers.DaemonStatus{
			Connected: false,
			Error:     "connection refused",
		},
	}

	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("failed to marshal healthhandlers.HealthStatus: %v", err)
	}

	var parsed healthhandlers.HealthStatus
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal healthhandlers.HealthStatus: %v", err)
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
	status := healthhandlers.HealthStatus{
		Status: "degraded",
		Daemon: healthhandlers.DaemonStatus{
			Connected: false,
		},
	}

	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("failed to marshal healthhandlers.HealthStatus: %v", err)
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
	status := healthhandlers.DaemonStatus{
		Connected: true,
		Status:    "healthy",
		Uptime:    7200.5,
		Version:   "2.0.0",
	}

	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("failed to marshal DaemonStatus: %v", err)
	}

	var parsed healthhandlers.DaemonStatus
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
	status := healthhandlers.DaemonStatus{
		Connected: false,
		Error:     "daemon not running",
	}

	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("failed to marshal DaemonStatus: %v", err)
	}

	var parsed healthhandlers.DaemonStatus
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
	status := healthhandlers.DaemonStatus{
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
//
// Pool-less mode is the steady state for fleet (no daemon to connect to),
// so we expect 200 OK with daemon.connected=false rather than the historical
// 503 + "connection pool not initialized" error response.
func TestHandleAPIHealth_NilPool(t *testing.T) {
	handler := healthhandlers.HandleAPIHealth(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	var resp healthhandlers.HealthStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Status != "ok" {
		t.Errorf("expected status 'ok', got %q", resp.Status)
	}

	if resp.Daemon.Connected {
		t.Errorf("expected daemon.connected=false in pool-less mode")
	}
}

// TestSetupRoutes_FlatTerminalWSEndpointReturns404 tests that
// the flat terminal WebSocket endpoint returns 404 (removed in favor of workspace-scoped).
func TestSetupRoutes_FlatTerminalWSEndpointReturns404(t *testing.T) {
	app := &Server{}
	setupTestRoutes(t, app)

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/ws?session=test", nil)
	rr := httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)

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
	ptyMgr := terminal.NewMultiPTYManager("bash", 0)
	defer ptyMgr.Close()

	app := &Server{ptyMgr: ptyMgr}
	setupTestRoutes(t, app)

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
			app.mux.ServeHTTP(rr, req)

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
	handler := hterminal.HandleTerminalWS(nil, nil, nil, "", nil, nil, nil, time.Time{})

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

// TestSetupRoutes_StatsEndpointDeleted verifies the unscoped /api/stats route
// is no longer registered — it had no FE caller and 503'd in fleet mode.
// The workspace-scoped /api/workspaces/{ws}/stats remains the canonical path.
func TestSetupRoutes_StatsEndpointDeleted(t *testing.T) {
	app := &Server{}
	setupTestRoutes(t, app)

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		req := httptest.NewRequest(method, "/api/stats", nil)
		rr := httptest.NewRecorder()
		app.mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Errorf("%s /api/stats: expected 404, got %d", method, rr.Code)
		}
		if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("%s /api/stats: Content-Type = %q, want %q", method, ct, "application/json")
		}
	}
}

// mockStatsClient implements healthhandlers.StatsClient for testing
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
	getFunc func(ctx context.Context) (healthhandlers.StatsClient, error)
	putFunc func(client healthhandlers.StatsClient)
}

func (m *mockStatsPool) Get(ctx context.Context) (healthhandlers.StatsClient, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx)
	}
	return nil, errors.New("getFunc not implemented")
}

func (m *mockStatsPool) Put(client healthhandlers.StatsClient) {
	if m.putFunc != nil {
		m.putFunc(client)
	}
}

func (m *mockStatsPool) Discard(client healthhandlers.StatsClient) {
	// no-op for tests
}

// TestHandleStats_ContentType verifies Content-Type header is application/json for all responses.
func TestHandleStats_ContentType(t *testing.T) {
	handler := healthhandlers.HandleStats(nil)

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
		getFunc: func(ctx context.Context) (healthhandlers.StatsClient, error) {
			return client, nil
		},
		putFunc: func(c healthhandlers.StatsClient) {},
	}

	handler := healthhandlers.HandleStatsWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp healthhandlers.StatsResponse
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
		getFunc: func(ctx context.Context) (healthhandlers.StatsClient, error) {
			return client, nil
		},
		putFunc: func(c healthhandlers.StatsClient) {},
	}

	handler := healthhandlers.HandleStatsWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status code = %d, want %d", rr.Code, http.StatusInternalServerError)
	}

	var resp healthhandlers.StatsResponse
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
		getFunc: func(ctx context.Context) (healthhandlers.StatsClient, error) {
			return client, nil
		},
		putFunc: func(c healthhandlers.StatsClient) {},
	}

	handler := healthhandlers.HandleStatsWithPool(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status code = %d, want %d", rr.Code, http.StatusInternalServerError)
	}

	var resp healthhandlers.StatsResponse
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
	app := &Server{}
	setupTestRoutes(t, app) // fleetEnabled=false

	// Request to fleet endpoint should return 404 JSON since the route is not
	// registered and the SPA catch-all rejects /api/* paths
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	rr := httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)

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
	app := &Server{}
	setupTestRoutes(t, app) // fleetEnabled=true

	req := httptest.NewRequest(http.MethodPost, "/api/fleet/claim", nil)
	rr := httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)

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
	handler := healthhandlers.HandleDaemonStatus(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/daemon/status", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}

	var resp healthhandlers.DaemonStatusResponse
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

	handler := healthhandlers.HandleDaemonStatus(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/daemon/status", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	// Should be 503 or 504
	if rr.Code != http.StatusServiceUnavailable && rr.Code != http.StatusGatewayTimeout {
		t.Errorf("status = %d, want %d or %d", rr.Code, http.StatusServiceUnavailable, http.StatusGatewayTimeout)
	}

	var resp healthhandlers.DaemonStatusResponse
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

	handler := healthhandlers.HandleDaemonStatus(pool)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/daemon/status", nil).WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusGatewayTimeout && rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d or %d", rr.Code, http.StatusGatewayTimeout, http.StatusServiceUnavailable)
	}
}

// TestDaemonStatusResponse_Serialization tests healthhandlers.DaemonStatusResponse JSON serialization.
func TestDaemonStatusResponse_Serialization(t *testing.T) {
	resp := healthhandlers.DaemonStatusResponse{
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

	var parsed healthhandlers.DaemonStatusResponse
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

// TestDaemonStatusResponse_ErrorSerialization tests error healthhandlers.DaemonStatusResponse serialization.
func TestDaemonStatusResponse_ErrorSerialization(t *testing.T) {
	resp := healthhandlers.DaemonStatusResponse{
		Success: false,
		Error:   "connection failed",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var parsed healthhandlers.DaemonStatusResponse
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
	socketPath := filepath.Join(dir, "loom.sock")

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
		DatabasePath:  "/tmp/workspace/.loom/db.sqlite",
		SocketPath:    "/tmp/workspace/.loom/loom.sock",
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
	handler := healthhandlers.HandleDaemonStatus(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/daemon/status", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp healthhandlers.DaemonStatusResponse
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
	handler := healthhandlers.HandleStats(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp healthhandlers.StatsResponse
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
	handler := healthhandlers.HandleAPIHealth(pool)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp healthhandlers.HealthStatus
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

// TestSetupRoutes_RemovedSSEEndpointReturns404 verifies that GET /api/events
// The flat endpoint returns 404 now that SSE is workspace-scoped.
func TestSetupRoutes_RemovedSSEEndpointReturns404(t *testing.T) {
	app := &Server{}
	setupTestRoutes(t, app)

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	rr := httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)

	// Removed SSE endpoint; SPA catch-all rejects /api/* with 404 JSON.
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
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	multiPool := daemon.NewMultiPool(middleware.WorkspaceFromContext, 1)
	// Register a fake workspace so WorkspaceMiddleware passes
	_ = multiPool.Register("test-ws", &stubPool{})

	wsExistsFn := func(id string) bool { return multiPool.PoolForWorkspace(id) != nil }
	app := &Server{multiPool: multiPool, hub: hub, wsExistsFn: wsExistsFn}
	app.sessSvc = svcimpl.NewSessionService(nil, nil)
	setupTestRoutes(t, app)

	// Use a context with short timeout because the SSE handler streams forever
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/events", nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)

	// With non-nil hub and multiPool, the workspace-scoped SSE route IS registered.
	// The SSE handler sets Content-Type to text/event-stream.
	ct := rr.Header().Get("Content-Type")
	if ct == "text/html; charset=utf-8" {
		t.Error("expected SSE route to be registered, but request fell through to frontend handler")
	}
}

func TestSetupRoutes_WorkspaceMonitorStatusInjectsWorkspace(t *testing.T) {
	multiPool := daemon.NewMultiPool(middleware.WorkspaceFromContext, 1)
	_ = multiPool.Register("test-ws", &stubPool{})

	wsExistsFn := func(id string) bool { return multiPool.PoolForWorkspace(id) != nil }
	app := &Server{
		multiPool:  multiPool,
		wsExistsFn: wsExistsFn,
		config: webui.ServerConfig{
			MonitorHandlers: webui.MonitorHandlers{
				Status: func(w http.ResponseWriter, r *http.Request) {
					if got := middleware.WorkspaceFromContext(r.Context()); got != "test-ws" {
						t.Errorf("workspace context = %q, want test-ws", got)
					}
					if got := r.URL.Query().Get("workspace"); got != "test-ws" {
						t.Errorf("workspace query = %q, want test-ws", got)
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"ok":true}`))
				},
			},
		},
	}
	app.sessSvc = svcimpl.NewSessionService(nil, nil)
	setupTestRoutes(t, app)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/monitor/status", nil)
	rr := httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

func TestSetupRoutes_WorkspaceBackendGetEndpoint(t *testing.T) {
	multiPool := daemon.NewMultiPool(middleware.WorkspaceFromContext, 1)
	_ = multiPool.Register("test-ws", &stubPool{})

	wsExistsFn := func(id string) bool { return multiPool.PoolForWorkspace(id) != nil }
	wsSvc := &mockWorkspaceService{
		getWorkspaceBackendFn: func(_ context.Context, wsID string) (*service.BackendConfigData, error) {
			if wsID != "test-ws" {
				t.Errorf("workspace id = %q, want test-ws", wsID)
			}
			return &service.BackendConfigData{
				Backend:   "codex",
				Source:    "workspace",
				Available: []string{"claude", "codex"},
			}, nil
		},
	}
	app := &Server{multiPool: multiPool, config: webui.ServerConfig{}, wsExistsFn: wsExistsFn, workspaceSvc: wsSvc}
	app.sessSvc = svcimpl.NewSessionService(nil, nil)
	setupTestRoutes(t, app)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/config/backend", nil)
	rr := httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Content-Type") == "text/html; charset=utf-8" {
		t.Fatal("expected workspace backend GET route to be registered, but request fell through to frontend handler")
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if success, _ := body["success"].(bool); !success {
		t.Fatalf("response success = false, want true; body=%v", body)
	}
	data, ok := body["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("response missing data object: %v", body)
	}
	if backend, _ := data["backend"].(string); backend != "codex" {
		t.Errorf("data.backend = %q, want codex", backend)
	}
}

// TestSetupRoutes_WorkspaceBackendPatchEndpoint verifies that
// PATCH /api/workspaces/{ws}/config/backend is handled by handleWorkspaceBackendPatch
// (which returns workspaceResponse shape) rather than handlePatchBackendConfig.
func TestSetupRoutes_WorkspaceBackendPatchEndpoint(t *testing.T) {
	multiPool := daemon.NewMultiPool(middleware.WorkspaceFromContext, 1)
	_ = multiPool.Register("test-ws", &stubPool{})

	wsExistsFn := func(id string) bool { return multiPool.PoolForWorkspace(id) != nil }
	wsSvc := &mockWorkspaceService{
		patchWorkspaceBackendFn: func(_ context.Context, _ string, _ string) (*ops.WorkspaceData, error) {
			return &ops.WorkspaceData{Name: "test-ws", Path: "/tmp/test"}, nil
		},
	}
	app := &Server{multiPool: multiPool, config: webui.ServerConfig{}, wsExistsFn: wsExistsFn, workspaceSvc: wsSvc}
	app.sessSvc = svcimpl.NewSessionService(nil, nil)
	setupTestRoutes(t, app)

	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/test-ws/config/backend",
		strings.NewReader(`{"backend":"codex"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)

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

// TestSetupRoutes_WorkspaceRenamePatchEndpoint verifies that
// PATCH /api/workspaces/{ws}/name is registered on the outer mux and that the
// request body reaches the handler. The latter is a regression guard: these
// PATCH routes are deliberately registered on the outer mux (not via the
// nested wsMux subtree) because Go 1.22+ http.ServeMux has a bug where
// r.Body.Read() hangs for PATCH requests routed through a nested mux via
// wildcard subtree pattern. If someone moves these routes back into wsMux,
// body decoding would break and this test would catch it.
func TestSetupRoutes_WorkspaceRenamePatchEndpoint(t *testing.T) {
	multiPool := daemon.NewMultiPool(middleware.WorkspaceFromContext, 1)
	_ = multiPool.Register("test-ws", &stubPool{})

	wsExistsFn := func(id string) bool { return multiPool.PoolForWorkspace(id) != nil }

	var capturedNewName string
	wsSvc := &mockWorkspaceService{
		renameWorkspaceFn: func(_ context.Context, _ string, newName string) (*ops.WorkspaceData, error) {
			capturedNewName = newName
			return &ops.WorkspaceData{Name: newName, Path: "/tmp/test"}, nil
		},
	}
	app := &Server{multiPool: multiPool, config: webui.ServerConfig{}, wsExistsFn: wsExistsFn, workspaceSvc: wsSvc}
	app.sessSvc = svcimpl.NewSessionService(nil, nil)
	setupTestRoutes(t, app)

	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/test-ws/name",
		strings.NewReader(`{"new_name":"renamed-ws"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)

	// The route must be registered (not fall through to SPA catch-all)
	ct := rr.Header().Get("Content-Type")
	if ct == "text/html; charset=utf-8" {
		t.Fatal("expected workspace rename PATCH route to be registered, but request fell through to frontend handler")
	}

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", rr.Code, rr.Body.String())
	}

	// The body must have been decoded and passed to the service — if it was
	// lost (e.g., nested mux body-read bug), capturedNewName would be empty.
	if capturedNewName != "renamed-ws" {
		t.Errorf("handler did not receive new_name from body; got %q, want %q", capturedNewName, "renamed-ws")
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if success, _ := body["success"].(bool); !success {
		t.Errorf("response success = false, want true; body=%v", body)
	}
	if data, ok := body["data"].(map[string]interface{}); !ok {
		t.Error("expected 'data' field in successful response")
	} else if name, _ := data["name"].(string); name != "renamed-ws" {
		t.Errorf("data.name = %q, want %q", name, "renamed-ws")
	}
}

// TestSetupRoutes_WorkspaceBackendPatchReadsBody is a regression guard that
// verifies the PATCH /config/backend route not only registers but actually
// receives the request body at the handler. Complements
// TestSetupRoutes_WorkspaceBackendPatchEndpoint which only asserts the shape.
func TestSetupRoutes_WorkspaceBackendPatchReadsBody(t *testing.T) {
	multiPool := daemon.NewMultiPool(middleware.WorkspaceFromContext, 1)
	_ = multiPool.Register("test-ws", &stubPool{})

	wsExistsFn := func(id string) bool { return multiPool.PoolForWorkspace(id) != nil }

	var capturedBackend string
	wsSvc := &mockWorkspaceService{
		patchWorkspaceBackendFn: func(_ context.Context, _ string, backend string) (*ops.WorkspaceData, error) {
			capturedBackend = backend
			return &ops.WorkspaceData{Name: "test-ws", Path: "/tmp/test"}, nil
		},
	}
	app := &Server{multiPool: multiPool, config: webui.ServerConfig{}, wsExistsFn: wsExistsFn, workspaceSvc: wsSvc}
	app.sessSvc = svcimpl.NewSessionService(nil, nil)
	setupTestRoutes(t, app)

	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/test-ws/config/backend",
		strings.NewReader(`{"backend":"codex"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", rr.Code, rr.Body.String())
	}

	if capturedBackend != "codex" {
		t.Errorf("handler did not receive backend from body; got %q, want %q", capturedBackend, "codex")
	}
}

// --- Terminal token route conditional registration tests ---

// TestSetupRoutes_FlatTerminalTokenReturns404 verifies that GET /api/terminal/token
// returns 404 (flat route removed, only workspace-scoped route exists).
func TestSetupRoutes_FlatTerminalTokenReturns404(t *testing.T) {
	ptyMgr := terminal.NewMultiPTYManager("bash", 0)
	t.Cleanup(func() { ptyMgr.Close() })

	termAuth, err := realtime.NewTerminalAuth()
	if err != nil {
		t.Fatalf("failed to create terminal auth: %v", err)
	}
	t.Cleanup(func() { termAuth.Stop() })

	app := &Server{ptyMgr: ptyMgr, termAuth: termAuth}
	setupTestRoutes(t, app)

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/token?session=test", nil)
	rr := httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)

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
	app := &Server{}
	setupTestRoutes(t, app)

	// GET should return JSON health response
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)

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
	app.mux.ServeHTTP(rr, req)

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

	app := &Server{pool: pool}
	setupTestRoutes(t, app)

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
			app.mux.ServeHTTP(rr, req)

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

	app := &Server{pool: pool}
	setupTestRoutes(t, app)

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
			app.mux.ServeHTTP(rr, req)

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
	app := &Server{}
	setupTestRoutes(t, app)

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
			app.mux.ServeHTTP(rr, req)

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

// --- Unregistered /api/ catch-all test ---

// TestUnregisteredAPIPathReturnsJSONNotFound verifies that any unmatched path
// under /api/ returns a JSON 404 with {"error":"not found"}, replacing the
// former SPA catch-all's API guard now that the frontend is served externally.
func TestUnregisteredAPIPathReturnsJSONNotFound(t *testing.T) {
	app := &Server{}
	setupTestRoutes(t, app)

	paths := []string{"/api/nonexistent", "/api/some/deep/path", "/api/auth/token"}
	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, p, nil)
			rr := httptest.NewRecorder()
			app.mux.ServeHTTP(rr, req)

			if rr.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", rr.Code)
			}
			if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
			var body map[string]string
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("response is not JSON: %v", err)
			}
			if body["error"] != "not found" {
				t.Errorf("error = %q, want %q", body["error"], "not found")
			}
		})
	}
}

// --- Tab metadata route conditional registration tests ---

// TestSetupRoutes_TabMetadataReturns404WhenStoreNil verifies that GET /api/terminal/tabs
// returns a 404 JSON response when tabMetaStore is nil. When Redis is not
// configured the tab metadata routes are not registered, so the request falls
// through to the /api/ JSON-404 catch-all.
func TestSetupRoutes_TabMetadataReturns404WhenStoreNil(t *testing.T) {
	// All nil params — tabMetaStore (param 21) is nil, so tab metadata routes are not registered.
	app := &Server{}
	setupTestRoutes(t, app)

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/tabs", nil)
	rr := httptest.NewRecorder()
	app.mux.ServeHTTP(rr, req)

	// Route is not registered; /api/ catch-all rejects with 404 JSON
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected /api/terminal/tabs to return %d when tabMetaStore is nil, got %d",
			http.StatusNotFound, rr.Code)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got %q", ct)
	}
}

// TestFlatAgentRoutesRemoved verifies that the flat agent routes
// (e.g. POST /api/agents/{name}/git/push) have been removed and return 404,
// while the workspace-scoped equivalents (e.g.
// POST /api/workspaces/{ws}/agents/{name}/git/push) still work.
func TestFlatAgentRoutesRemoved(t *testing.T) {
	// Set up a multiPool with a registered workspace so workspace-scoped routes
	// are functional.
	multiPool := daemon.NewMultiPool(middleware.WorkspaceFromContext, 1)
	_ = multiPool.Register("test-ws", &stubPool{})

	wsExistsFn := func(id string) bool { return multiPool.PoolForWorkspace(id) != nil }

	gitOps := &mockGitOps{}
	fileOps := &mockFileOps{}

	app := &Server{multiPool: multiPool, config: webui.ServerConfig{GitOps: gitOps, FileOps: fileOps}, wsExistsFn: wsExistsFn, agentSvc: svcimpl.NewAgentService(gitOps, nil, nil, nil)}
	app.diffSvc = svcimpl.NewDiffService(gitOps, nil)
	app.fileSvc = svcimpl.NewFileService(fileOps)
	app.sessSvc = svcimpl.NewSessionService(nil, nil)
	setupTestRoutes(t, app)

	// Removed flat routes; each must return 404.
	noConfigRoutes := []struct {
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

	for _, tc := range noConfigRoutes {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rr := httptest.NewRecorder()
			app.mux.ServeHTTP(rr, req)

			if rr.Code != http.StatusNotFound {
				t.Errorf("removed route %s %s: expected status %d, got %d",
					tc.method, tc.path, http.StatusNotFound, rr.Code)
			}

			ct := rr.Header().Get("Content-Type")
			if ct != "application/json" {
				t.Errorf("removed route %s %s: expected Content-Type 'application/json', got %q",
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
			app.mux.ServeHTTP(rr, req)

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
