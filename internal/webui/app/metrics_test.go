package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
	healthhandlers "github.com/tysonthomas9/loomcli/internal/webui/handlers/health"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// TestHandleMetrics_NilHub tests that handleMetrics returns 503 when hub is nil.
func TestHandleMetrics_NilHub(t *testing.T) {
	handler := healthhandlers.HandleMetrics(nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/metrics", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d", http.StatusServiceUnavailable, rec.Code)
	}

	// Verify Content-Type header
	contentType := rec.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", contentType)
	}

	// Parse response body
	var body map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}

	// Verify success is false
	success, ok := body["success"].(bool)
	if !ok {
		t.Fatal("expected 'success' field to be a bool")
	}
	if success {
		t.Error("expected 'success' to be false")
	}

	// Verify error message
	errMsg, ok := body["error"].(string)
	if !ok {
		t.Fatal("expected 'error' field to be a string")
	}
	if errMsg != "SSE hub not initialized" {
		t.Errorf("expected error 'SSE hub not initialized', got %q", errMsg)
	}
}

// TestHandleMetrics_ValidHub tests that handleMetrics returns correct metrics for a valid hub.
func TestHandleMetrics_ValidHub(t *testing.T) {
	hub := realtime.NewHub()

	handler := healthhandlers.HandleMetrics(hub, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/metrics", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	// Verify Content-Type header
	contentType := rec.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", contentType)
	}

	// Parse response body
	var body map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}

	// Verify success is true
	success, ok := body["success"].(bool)
	if !ok {
		t.Fatal("expected 'success' field to be a bool")
	}
	if !success {
		t.Error("expected 'success' to be true")
	}

	// Verify data field exists and contains all metric fields
	data, ok := body["data"].(map[string]interface{})
	if !ok {
		t.Fatal("expected 'data' field to be an object")
	}

	// connected_clients should be 0 (no clients registered)
	connectedClients, ok := data["connected_clients"].(float64)
	if !ok {
		t.Fatal("expected 'connected_clients' field to be a number")
	}
	if int(connectedClients) != 0 {
		t.Errorf("expected connected_clients=0, got %v", connectedClients)
	}

	// dropped_mutations should be 0
	droppedMutations, ok := data["dropped_mutations"].(float64)
	if !ok {
		t.Fatal("expected 'dropped_mutations' field to be a number")
	}
	if int64(droppedMutations) != 0 {
		t.Errorf("expected dropped_mutations=0, got %v", droppedMutations)
	}

	// retry_queue_depth should be 0
	retryQueueDepth, ok := data["retry_queue_depth"].(float64)
	if !ok {
		t.Fatal("expected 'retry_queue_depth' field to be a number")
	}
	if int(retryQueueDepth) != 0 {
		t.Errorf("expected retry_queue_depth=0, got %v", retryQueueDepth)
	}

	// uptime_seconds should be > 0 (hub was just created)
	uptimeSeconds, ok := data["uptime_seconds"].(float64)
	if !ok {
		t.Fatal("expected 'uptime_seconds' field to be a number")
	}
	if uptimeSeconds <= 0 {
		t.Errorf("expected uptime_seconds > 0, got %v", uptimeSeconds)
	}
}

// TestGetRetryQueueDepth tests that GetRetryQueueDepth returns the correct count.
// Since retryQueue is unexported, we fill the broadcast channel (buffer=256) first,
// then overflow into retryQueue via Broadcast().
func TestGetRetryQueueDepth(t *testing.T) {
	hub := realtime.NewHub()
	// Do NOT call hub.Run() — we want the broadcast channel to fill up.

	// Initially should be 0
	if depth := hub.GetRetryQueueDepth(); depth != 0 {
		t.Errorf("expected initial retry queue depth=0, got %d", depth)
	}

	// Fill the broadcast channel (buffer=256)
	for i := 0; i < 256; i++ {
		hub.Broadcast(&realtime.MutationPayload{Type: "create", IssueID: fmt.Sprintf("fill-%d", i), WorkspaceID: "ws"})
	}

	// Now the broadcast channel is full. The next Broadcast() calls will
	// go to the retryQueue.
	hub.Broadcast(&realtime.MutationPayload{Type: "create", IssueID: "retry-1", WorkspaceID: "ws"})
	hub.Broadcast(&realtime.MutationPayload{Type: "update", IssueID: "retry-2", WorkspaceID: "ws"})
	hub.Broadcast(&realtime.MutationPayload{Type: "delete", IssueID: "retry-3", WorkspaceID: "ws"})

	if depth := hub.GetRetryQueueDepth(); depth != 3 {
		t.Errorf("expected retry queue depth=3, got %d", depth)
	}

	// Add one more
	hub.Broadcast(&realtime.MutationPayload{Type: "status", IssueID: "retry-4", WorkspaceID: "ws"})

	if depth := hub.GetRetryQueueDepth(); depth != 4 {
		t.Errorf("expected retry queue depth=4, got %d", depth)
	}
}

// TestGetUptime tests that GetUptime returns a positive duration after creation.
func TestGetUptime(t *testing.T) {
	hub := realtime.NewHub()

	// Sleep to ensure measurable uptime
	time.Sleep(10 * time.Millisecond)

	uptime := hub.GetUptime()
	if uptime <= 0 {
		t.Errorf("expected uptime > 0, got %v", uptime)
	}

	// Uptime should be at least 10ms since we slept that long
	if uptime < 10*time.Millisecond {
		t.Errorf("expected uptime >= 10ms, got %v", uptime)
	}
}

// TestHandleMetrics_WithClaimMetrics tests that claim metrics appear in the
// /api/metrics response when a ClaimMetrics instance is provided.
func TestHandleMetrics_WithClaimMetrics(t *testing.T) {
	hub := realtime.NewHub()

	claimMetrics := fleet.NewClaimMetrics()
	// Record 3 successes, 2 collisions, 1 timeout.
	for i := 0; i < 3; i++ {
		claimMetrics.RecordClaim(fleet.ClaimResultSuccess)
	}
	for i := 0; i < 2; i++ {
		claimMetrics.RecordClaim(fleet.ClaimResultCollision)
	}
	claimMetrics.RecordClaim(fleet.ClaimResultTimeout)

	handler := healthhandlers.HandleMetrics(hub, nil, claimMetrics)

	req := httptest.NewRequest(http.MethodGet, "/api/metrics", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	// Verify Content-Type header.
	contentType := rec.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", contentType)
	}

	// Parse response body.
	var body map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}

	// Verify success is true.
	success, ok := body["success"].(bool)
	if !ok {
		t.Fatal("expected 'success' field to be a bool")
	}
	if !success {
		t.Error("expected 'success' to be true")
	}

	// Verify data field exists.
	data, ok := body["data"].(map[string]interface{})
	if !ok {
		t.Fatal("expected 'data' field to be an object")
	}

	// Verify claim metrics fields.
	tests := []struct {
		field    string
		expected int64
	}{
		{"loom_fleet_claims_success", 3},
		{"loom_fleet_claims_collision", 2},
		{"loom_fleet_claims_timeout", 1},
		{"loom_fleet_claims_total", 6},
	}

	for _, tc := range tests {
		val, ok := data[tc.field].(float64)
		if !ok {
			t.Errorf("expected %q field to be a number", tc.field)
			continue
		}
		if int64(val) != tc.expected {
			t.Errorf("expected %s=%d, got %v", tc.field, tc.expected, val)
		}
	}
}
