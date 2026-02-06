package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestHandleMetrics_NilHub tests that handleMetrics returns 503 when hub is nil.
func TestHandleMetrics_NilHub(t *testing.T) {
	handler := handleMetrics(nil)

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
	hub := NewSSEHub()

	handler := handleMetrics(hub)

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
func TestGetRetryQueueDepth(t *testing.T) {
	hub := NewSSEHub()

	// Initially should be 0
	if depth := hub.GetRetryQueueDepth(); depth != 0 {
		t.Errorf("expected initial retry queue depth=0, got %d", depth)
	}

	// Manually add items to retryQueue (same package, so we can access unexported fields)
	hub.retryMu.Lock()
	hub.retryQueue = append(hub.retryQueue, &MutationPayload{Type: "create", IssueID: "test-1"})
	hub.retryQueue = append(hub.retryQueue, &MutationPayload{Type: "update", IssueID: "test-2"})
	hub.retryQueue = append(hub.retryQueue, &MutationPayload{Type: "delete", IssueID: "test-3"})
	hub.retryMu.Unlock()

	// Should now be 3
	if depth := hub.GetRetryQueueDepth(); depth != 3 {
		t.Errorf("expected retry queue depth=3, got %d", depth)
	}

	// Add one more
	hub.retryMu.Lock()
	hub.retryQueue = append(hub.retryQueue, &MutationPayload{Type: "status", IssueID: "test-4"})
	hub.retryMu.Unlock()

	// Should now be 4
	if depth := hub.GetRetryQueueDepth(); depth != 4 {
		t.Errorf("expected retry queue depth=4, got %d", depth)
	}
}

// TestGetUptime tests that GetUptime returns a positive duration after creation.
func TestGetUptime(t *testing.T) {
	hub := NewSSEHub()

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
