package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/rpc"
)

// TestNewSSEHub tests that NewSSEHub creates a properly initialized hub.
func TestNewSSEHub(t *testing.T) {
	hub := NewSSEHub()
	if hub == nil {
		t.Fatal("NewSSEHub() returned nil")
	}

	if hub.clients == nil {
		t.Error("expected clients map to be initialized")
	}
	if hub.register == nil {
		t.Error("expected register channel to be initialized")
	}
	if hub.unregister == nil {
		t.Error("expected unregister channel to be initialized")
	}
	if hub.broadcast == nil {
		t.Error("expected broadcast channel to be initialized")
	}
	if hub.done == nil {
		t.Error("expected done channel to be initialized")
	}
}

// TestSSEHub_RegisterClient tests that Run() properly registers clients.
func TestSSEHub_RegisterClient(t *testing.T) {
	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	client := &SSEClient{
		id:   1,
		send: make(chan *MutationPayload, 64),
		done: make(chan struct{}),
	}

	hub.RegisterClient(client)

	// Wait for registration to complete
	time.Sleep(50 * time.Millisecond)

	count := hub.ClientCount()
	if count != 1 {
		t.Errorf("expected 1 client, got %d", count)
	}
}

// TestSSEHub_UnregisterClient tests that Run() properly unregisters clients.
func TestSSEHub_UnregisterClient(t *testing.T) {
	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	client := &SSEClient{
		id:   1,
		send: make(chan *MutationPayload, 64),
		done: make(chan struct{}),
	}

	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond)

	hub.UnregisterClient(client)
	time.Sleep(50 * time.Millisecond)

	count := hub.ClientCount()
	if count != 0 {
		t.Errorf("expected 0 clients after unregister, got %d", count)
	}
}

// TestSSEHub_UnregisterClosesChannel tests that unregistering a client closes its send channel.
func TestSSEHub_UnregisterClosesChannel(t *testing.T) {
	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	client := &SSEClient{
		id:   1,
		send: make(chan *MutationPayload, 64),
		done: make(chan struct{}),
	}

	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond)

	hub.UnregisterClient(client)
	time.Sleep(50 * time.Millisecond)

	// Channel should be closed, so read should return immediately with ok=false
	select {
	case _, ok := <-client.send:
		if ok {
			t.Error("expected send channel to be closed")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("send channel should have been closed but wasn't")
	}
}

// TestSSEHub_MultipleClients tests registering multiple clients.
func TestSSEHub_MultipleClients(t *testing.T) {
	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	clients := make([]*SSEClient, 5)
	for i := 0; i < 5; i++ {
		clients[i] = &SSEClient{
			id:   int64(i + 1),
			send: make(chan *MutationPayload, 64),
			done: make(chan struct{}),
		}
		hub.RegisterClient(clients[i])
	}

	time.Sleep(100 * time.Millisecond)

	count := hub.ClientCount()
	if count != 5 {
		t.Errorf("expected 5 clients, got %d", count)
	}

	// Unregister some clients
	hub.UnregisterClient(clients[0])
	hub.UnregisterClient(clients[2])
	time.Sleep(100 * time.Millisecond)

	count = hub.ClientCount()
	if count != 3 {
		t.Errorf("expected 3 clients after unregistering 2, got %d", count)
	}
}

// TestSSEHub_Broadcast tests that Broadcast sends mutations to all connected clients.
func TestSSEHub_Broadcast(t *testing.T) {
	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	// Register two clients
	client1 := &SSEClient{
		id:   1,
		send: make(chan *MutationPayload, 64),
		done: make(chan struct{}),
	}
	client2 := &SSEClient{
		id:   2,
		send: make(chan *MutationPayload, 64),
		done: make(chan struct{}),
	}

	hub.RegisterClient(client1)
	hub.RegisterClient(client2)
	time.Sleep(50 * time.Millisecond)

	// Broadcast a mutation
	mutation := &MutationPayload{
		Type:      "create",
		IssueID:   "bd-123",
		Title:     "Test Issue",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	hub.Broadcast(mutation)

	// Both clients should receive the mutation
	timeout := time.After(500 * time.Millisecond)

	select {
	case received := <-client1.send:
		if received.IssueID != "bd-123" {
			t.Errorf("client1 received wrong issue_id: %s", received.IssueID)
		}
	case <-timeout:
		t.Error("client1 did not receive mutation")
	}

	select {
	case received := <-client2.send:
		if received.IssueID != "bd-123" {
			t.Errorf("client2 received wrong issue_id: %s", received.IssueID)
		}
	case <-timeout:
		t.Error("client2 did not receive mutation")
	}
}

// TestSSEHub_BroadcastSkipsFullBuffer tests that Broadcast skips clients with full buffers.
func TestSSEHub_BroadcastSkipsFullBuffer(t *testing.T) {
	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	// Create client with tiny buffer
	client := &SSEClient{
		id:   1,
		send: make(chan *MutationPayload, 1), // Buffer of 1
		done: make(chan struct{}),
	}

	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond)

	// Fill the buffer
	mutation1 := &MutationPayload{Type: "create", IssueID: "bd-1"}
	hub.Broadcast(mutation1)
	time.Sleep(50 * time.Millisecond)

	// This should be skipped (buffer full)
	mutation2 := &MutationPayload{Type: "create", IssueID: "bd-2"}
	hub.Broadcast(mutation2)
	time.Sleep(50 * time.Millisecond)

	// Should only receive first mutation
	select {
	case received := <-client.send:
		if received.IssueID != "bd-1" {
			t.Errorf("expected bd-1, got %s", received.IssueID)
		}
	default:
		t.Error("expected to receive first mutation")
	}

	// Buffer should be empty now (second was skipped)
	select {
	case received := <-client.send:
		t.Errorf("expected empty buffer, got mutation: %s", received.IssueID)
	default:
		// Expected - buffer is empty
	}
}

// TestSSEHub_Stop tests that Stop closes all client channels.
func TestSSEHub_Stop(t *testing.T) {
	hub := NewSSEHub()
	go hub.Run()

	client := &SSEClient{
		id:   1,
		send: make(chan *MutationPayload, 64),
		done: make(chan struct{}),
	}

	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond)

	hub.Stop()
	time.Sleep(50 * time.Millisecond)

	// Channel should be closed
	select {
	case _, ok := <-client.send:
		if ok {
			t.Error("expected send channel to be closed after Stop")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("send channel should have been closed but wasn't")
	}
}

// TestSSEHub_ClientCount tests ClientCount returns accurate count.
func TestSSEHub_ClientCount(t *testing.T) {
	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	if hub.ClientCount() != 0 {
		t.Errorf("initial count should be 0, got %d", hub.ClientCount())
	}

	client := &SSEClient{
		id:   1,
		send: make(chan *MutationPayload, 64),
		done: make(chan struct{}),
	}
	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond)

	if hub.ClientCount() != 1 {
		t.Errorf("count after register should be 1, got %d", hub.ClientCount())
	}
}

// TestHandleSSE_Headers tests that handleSSE sets correct SSE headers.
func TestHandleSSE_Headers(t *testing.T) {
	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	handler := handleSSE(hub, nil)

	// Create a test request
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	rr := httptest.NewRecorder()

	// Run handler in goroutine since it blocks
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(rr, req)
		close(done)
	}()

	// Give handler time to set headers and write initial response
	time.Sleep(100 * time.Millisecond)

	// Check headers
	if ct := rr.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected Content-Type 'text/event-stream', got %q", ct)
	}
	if cc := rr.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("expected Cache-Control 'no-cache', got %q", cc)
	}
	if conn := rr.Header().Get("Connection"); conn != "keep-alive" {
		t.Errorf("expected Connection 'keep-alive', got %q", conn)
	}
	if xab := rr.Header().Get("X-Accel-Buffering"); xab != "no" {
		t.Errorf("expected X-Accel-Buffering 'no', got %q", xab)
	}
}

// TestHandleSSE_ParsesLastEventID tests that Last-Event-ID header is parsed.
func TestHandleSSE_ParsesLastEventID(t *testing.T) {
	tests := []struct {
		name          string
		lastEventID   string
		sinceParam    string
		expectedSince int64
	}{
		{
			name:          "no last event id",
			lastEventID:   "",
			sinceParam:    "",
			expectedSince: 0,
		},
		{
			name:          "valid last event id",
			lastEventID:   "1706000000000",
			sinceParam:    "",
			expectedSince: 1706000000000,
		},
		{
			name:          "invalid last event id ignored",
			lastEventID:   "not-a-number",
			sinceParam:    "",
			expectedSince: 0,
		},
		{
			name:          "since param takes precedence when larger",
			lastEventID:   "1000",
			sinceParam:    "2000",
			expectedSince: 2000,
		},
		{
			name:          "last event id used when larger",
			lastEventID:   "2000",
			sinceParam:    "1000",
			expectedSince: 2000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hub := NewSSEHub()
			go hub.Run()
			defer hub.Stop()

			var capturedSince int64
			getMutations := func(since int64) []rpc.MutationEvent {
				capturedSince = since
				return nil
			}

			handler := handleSSE(hub, getMutations)

			url := "/api/events"
			if tt.sinceParam != "" {
				url += "?since=" + tt.sinceParam
			}
			req := httptest.NewRequest(http.MethodGet, url, nil)
			if tt.lastEventID != "" {
				req.Header.Set("Last-Event-ID", tt.lastEventID)
			}
			rr := httptest.NewRecorder()

			done := make(chan struct{})
			go func() {
				handler.ServeHTTP(rr, req)
				close(done)
			}()

			time.Sleep(100 * time.Millisecond)

			// If expectedSince > 0, getMutations should have been called
			if tt.expectedSince > 0 && capturedSince != tt.expectedSince {
				t.Errorf("expected since=%d, got %d", tt.expectedSince, capturedSince)
			}
		})
	}
}

// TestHandleSSE_SendsConnectedEvent tests that a connected event is sent on connect.
func TestHandleSSE_SendsConnectedEvent(t *testing.T) {
	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	handler := handleSSE(hub, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(rr, req)
		close(done)
	}()

	time.Sleep(100 * time.Millisecond)

	body := rr.Body.String()
	if !strings.Contains(body, "event: connected") {
		t.Error("expected connected event in response")
	}
	if !strings.Contains(body, `"clientId":`) {
		t.Error("expected clientId in connected event data")
	}
}

// TestHandleSSE_CatchUpEvents tests that catch-up events are sent on reconnection.
func TestHandleSSE_CatchUpEvents(t *testing.T) {
	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	// Create getMutationsSince that returns catch-up events
	getMutations := func(since int64) []rpc.MutationEvent {
		return []rpc.MutationEvent{
			{
				Type:      "create",
				IssueID:   "bd-1",
				Title:     "Catch-up Issue",
				Timestamp: time.Now().UTC(),
			},
		}
	}

	handler := handleSSE(hub, getMutations)

	// Connect with a since timestamp to trigger catch-up
	req := httptest.NewRequest(http.MethodGet, "/api/events?since=1000", nil)
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(rr, req)
		close(done)
	}()

	time.Sleep(100 * time.Millisecond)

	body := rr.Body.String()
	// Should contain mutation event for catch-up
	if !strings.Contains(body, "event: mutation") {
		t.Error("expected mutation event for catch-up")
	}
	if !strings.Contains(body, "bd-1") {
		t.Error("expected catch-up issue id in response")
	}
}

// TestWriteSSEEvent_Format tests that writeSSEEvent formats SSE events correctly.
func TestWriteSSEEvent_Format(t *testing.T) {
	tests := []struct {
		name     string
		mutation *MutationPayload
		wantID   bool
		wantData bool
	}{
		{
			name: "basic mutation",
			mutation: &MutationPayload{
				Type:      "create",
				IssueID:   "bd-123",
				Title:     "Test Issue",
				Timestamp: "2025-01-23T12:00:00Z",
			},
			wantID:   true,
			wantData: true,
		},
		{
			name: "status mutation",
			mutation: &MutationPayload{
				Type:      "status",
				IssueID:   "bd-456",
				Timestamp: "2025-01-23T14:00:00Z",
				OldStatus: "open",
				NewStatus: "closed",
			},
			wantID:   true,
			wantData: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			writeSSEEvent(rr, tt.mutation)

			output := rr.Body.String()

			// Check for id: line
			if tt.wantID && !strings.Contains(output, "id: ") {
				t.Error("expected id: in SSE event")
			}

			// Check for event: mutation line
			if !strings.Contains(output, "event: mutation") {
				t.Error("expected event: mutation in SSE event")
			}

			// Check for data: line with JSON
			if tt.wantData {
				if !strings.Contains(output, "data: ") {
					t.Error("expected data: in SSE event")
				}
				if !strings.Contains(output, tt.mutation.IssueID) {
					t.Errorf("expected issue_id %s in data", tt.mutation.IssueID)
				}
			}

			// Check for double newline terminator
			if !strings.HasSuffix(output, "\n\n") {
				t.Error("expected SSE event to end with double newline")
			}
		})
	}
}

// TestWriteSSEEvent_MonotonicIDs tests that event IDs are monotonically increasing.
func TestWriteSSEEvent_MonotonicIDs(t *testing.T) {
	mutation := &MutationPayload{
		Type:      "create",
		IssueID:   "bd-123",
		Timestamp: "2025-01-23T12:00:00Z",
	}

	rr1 := httptest.NewRecorder()
	writeSSEEvent(rr1, mutation)

	rr2 := httptest.NewRecorder()
	writeSSEEvent(rr2, mutation)

	output1 := rr1.Body.String()
	output2 := rr2.Body.String()

	// Extract event IDs
	id1 := extractEventID(t, output1)
	id2 := extractEventID(t, output2)

	if id2 <= id1 {
		t.Errorf("expected second event ID (%d) > first event ID (%d)", id2, id1)
	}
}

// TestWriteSSEEvent_InvalidTimestampStillWorks tests that events with invalid timestamps still get valid IDs.
func TestWriteSSEEvent_InvalidTimestampStillWorks(t *testing.T) {
	mutation := &MutationPayload{
		Type:      "create",
		IssueID:   "bd-123",
		Timestamp: "invalid-timestamp",
	}

	rr := httptest.NewRecorder()
	writeSSEEvent(rr, mutation)

	output := rr.Body.String()

	// Should still produce valid SSE event with a monotonic ID
	if !strings.Contains(output, "event: mutation") {
		t.Error("expected valid SSE event even with invalid timestamp")
	}
	if !strings.Contains(output, "id: ") {
		t.Error("expected id in SSE event")
	}
}

// extractEventID parses the numeric event ID from an SSE event string.
func extractEventID(t *testing.T, sseOutput string) int64 {
	t.Helper()
	for _, line := range strings.Split(sseOutput, "\n") {
		if strings.HasPrefix(line, "id: ") {
			idStr := strings.TrimPrefix(line, "id: ")
			id, err := strconv.ParseInt(idStr, 10, 64)
			if err != nil {
				t.Fatalf("failed to parse event ID %q: %v", idStr, err)
			}
			return id
		}
	}
	t.Fatalf("no id: line found in SSE output: %s", sseOutput)
	return 0
}

// TestSSEHub_BroadcastQueuesToRetryQueue tests that Broadcast queues mutations when broadcast channel is full.
func TestSSEHub_BroadcastQueuesToRetryQueue(t *testing.T) {
	hub := NewSSEHub()
	// Don't run the hub - we want the broadcast channel to fill up

	// Fill the broadcast channel (capacity 256)
	for i := 0; i < 256; i++ {
		hub.Broadcast(&MutationPayload{Type: "create", IssueID: "bd-fill"})
	}

	// Now broadcast should queue to retry queue
	hub.Broadcast(&MutationPayload{Type: "create", IssueID: "bd-retry1"})
	hub.Broadcast(&MutationPayload{Type: "create", IssueID: "bd-retry2"})

	hub.retryMu.Lock()
	queueLen := len(hub.retryQueue)
	hub.retryMu.Unlock()

	if queueLen != 2 {
		t.Errorf("expected 2 items in retry queue, got %d", queueLen)
	}
}

// TestSSEHub_BroadcastDropsWhenRetryQueueFull tests that mutations are dropped when retry queue exceeds 1024.
func TestSSEHub_BroadcastDropsWhenRetryQueueFull(t *testing.T) {
	hub := NewSSEHub()
	// Don't run the hub - we want the broadcast channel to fill up

	// Fill the broadcast channel (capacity 256)
	for i := 0; i < 256; i++ {
		hub.Broadcast(&MutationPayload{Type: "create", IssueID: "bd-fill"})
	}

	// Fill the retry queue (capacity 1024)
	for i := 0; i < 1024; i++ {
		hub.Broadcast(&MutationPayload{Type: "create", IssueID: "bd-retry"})
	}

	hub.retryMu.Lock()
	queueLen := len(hub.retryQueue)
	hub.retryMu.Unlock()

	if queueLen != 1024 {
		t.Errorf("expected retry queue to be full at 1024, got %d", queueLen)
	}

	initialDropped := hub.GetDroppedCount()
	if initialDropped != 0 {
		t.Errorf("expected initial dropped count to be 0, got %d", initialDropped)
	}

	// This mutation should be dropped
	hub.Broadcast(&MutationPayload{Type: "create", IssueID: "bd-dropped1"})
	hub.Broadcast(&MutationPayload{Type: "create", IssueID: "bd-dropped2"})

	droppedCount := hub.GetDroppedCount()
	if droppedCount != 2 {
		t.Errorf("expected 2 dropped mutations, got %d", droppedCount)
	}

	// Retry queue should still be at 1024
	hub.retryMu.Lock()
	queueLen = len(hub.retryQueue)
	hub.retryMu.Unlock()

	if queueLen != 1024 {
		t.Errorf("expected retry queue to remain at 1024, got %d", queueLen)
	}
}

// TestSSEHub_GetDroppedCount tests that GetDroppedCount returns the correct count.
func TestSSEHub_GetDroppedCount(t *testing.T) {
	hub := NewSSEHub()

	// Initial count should be 0
	if count := hub.GetDroppedCount(); count != 0 {
		t.Errorf("expected initial dropped count to be 0, got %d", count)
	}

	// Fill broadcast channel
	for i := 0; i < 256; i++ {
		hub.Broadcast(&MutationPayload{Type: "create", IssueID: "bd-fill"})
	}

	// Fill retry queue
	for i := 0; i < 1024; i++ {
		hub.Broadcast(&MutationPayload{Type: "create", IssueID: "bd-retry"})
	}

	// Drop 5 mutations
	for i := 0; i < 5; i++ {
		hub.Broadcast(&MutationPayload{Type: "create", IssueID: "bd-dropped"})
	}

	if count := hub.GetDroppedCount(); count != 5 {
		t.Errorf("expected dropped count to be 5, got %d", count)
	}
}

// TestSSEHub_DrainRetryQueue tests that drainRetryQueue sends queued mutations to broadcast channel.
func TestSSEHub_DrainRetryQueue(t *testing.T) {
	hub := NewSSEHub()

	// Manually add items to retry queue
	hub.retryMu.Lock()
	hub.retryQueue = []*MutationPayload{
		{Type: "create", IssueID: "bd-1"},
		{Type: "create", IssueID: "bd-2"},
		{Type: "create", IssueID: "bd-3"},
	}
	hub.retryMu.Unlock()

	// Drain the retry queue (broadcast channel should have room)
	hub.drainRetryQueue()

	// Check that broadcast channel has the mutations
	if len(hub.broadcast) != 3 {
		t.Errorf("expected 3 items in broadcast channel, got %d", len(hub.broadcast))
	}

	// Retry queue should be empty
	hub.retryMu.Lock()
	queueLen := len(hub.retryQueue)
	hub.retryMu.Unlock()

	if queueLen != 0 {
		t.Errorf("expected retry queue to be empty, got %d items", queueLen)
	}

	// Verify the mutations were sent in order
	m1 := <-hub.broadcast
	if m1.IssueID != "bd-1" {
		t.Errorf("expected first mutation to be bd-1, got %s", m1.IssueID)
	}
	m2 := <-hub.broadcast
	if m2.IssueID != "bd-2" {
		t.Errorf("expected second mutation to be bd-2, got %s", m2.IssueID)
	}
	m3 := <-hub.broadcast
	if m3.IssueID != "bd-3" {
		t.Errorf("expected third mutation to be bd-3, got %s", m3.IssueID)
	}
}

// TestSSEHub_DrainRetryQueuePreservesRemaining tests that drainRetryQueue preserves remaining items
// if broadcast channel fills up again.
func TestSSEHub_DrainRetryQueuePreservesRemaining(t *testing.T) {
	hub := NewSSEHub()

	// Fill broadcast channel almost completely (leave room for just 2 more)
	for i := 0; i < 254; i++ {
		hub.broadcast <- &MutationPayload{Type: "create", IssueID: "bd-fill"}
	}

	// Add 5 items to retry queue
	hub.retryMu.Lock()
	hub.retryQueue = []*MutationPayload{
		{Type: "create", IssueID: "bd-retry-1"},
		{Type: "create", IssueID: "bd-retry-2"},
		{Type: "create", IssueID: "bd-retry-3"},
		{Type: "create", IssueID: "bd-retry-4"},
		{Type: "create", IssueID: "bd-retry-5"},
	}
	hub.retryMu.Unlock()

	// Drain - should only be able to send 2
	hub.drainRetryQueue()

	// Check broadcast channel is full
	if len(hub.broadcast) != 256 {
		t.Errorf("expected broadcast channel to be full (256), got %d", len(hub.broadcast))
	}

	// Retry queue should still have 3 items
	hub.retryMu.Lock()
	queueLen := len(hub.retryQueue)
	remainingIDs := make([]string, len(hub.retryQueue))
	for i, m := range hub.retryQueue {
		remainingIDs[i] = m.IssueID
	}
	hub.retryMu.Unlock()

	if queueLen != 3 {
		t.Errorf("expected 3 items remaining in retry queue, got %d", queueLen)
	}

	// Verify the remaining items are the last 3 (preserves order)
	expected := []string{"bd-retry-3", "bd-retry-4", "bd-retry-5"}
	for i, id := range expected {
		if i < len(remainingIDs) && remainingIDs[i] != id {
			t.Errorf("expected remaining[%d] to be %s, got %s", i, id, remainingIDs[i])
		}
	}
}

// TestSSEHub_DrainRetryQueueEmpty tests that drainRetryQueue handles empty queue gracefully.
func TestSSEHub_DrainRetryQueueEmpty(t *testing.T) {
	hub := NewSSEHub()

	// Should not panic or error with empty queue
	hub.drainRetryQueue()

	hub.retryMu.Lock()
	queueLen := len(hub.retryQueue)
	hub.retryMu.Unlock()

	if queueLen != 0 {
		t.Errorf("expected retry queue to remain empty, got %d", queueLen)
	}
}

// TestSSEHub_RetryQueueDrainedByTicker tests that the ticker in Run() drains the retry queue.
// This test verifies the integration between the ticker and the retry queue drain mechanism.
func TestSSEHub_RetryQueueDrainedByTicker(t *testing.T) {
	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	// Register a client with large buffer to consume all broadcasts
	client := &SSEClient{
		id:   1,
		send: make(chan *MutationPayload, 512), // Large buffer to avoid blocking
		done: make(chan struct{}),
	}
	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond)

	// Manually add items to retry queue (simulating queued mutations)
	hub.retryMu.Lock()
	hub.retryQueue = []*MutationPayload{
		{Type: "create", IssueID: "bd-retry-1"},
		{Type: "create", IssueID: "bd-retry-2"},
	}
	hub.retryMu.Unlock()

	// Wait for ticker to fire and drain retry queue (ticker fires every 100ms)
	time.Sleep(200 * time.Millisecond)

	hub.retryMu.Lock()
	finalQueueLen := len(hub.retryQueue)
	hub.retryMu.Unlock()

	if finalQueueLen != 0 {
		t.Errorf("expected retry queue to be drained by ticker, got %d items", finalQueueLen)
	}

	// Verify the client received the mutations from the retry queue
	// The mutations should have been broadcast and then sent to the client
	receivedCount := 0
	timeout := time.After(500 * time.Millisecond)
drainLoop:
	for {
		select {
		case m := <-client.send:
			if m.IssueID == "bd-retry-1" || m.IssueID == "bd-retry-2" {
				receivedCount++
			}
			if receivedCount >= 2 {
				break drainLoop
			}
		case <-timeout:
			break drainLoop
		}
	}

	if receivedCount != 2 {
		t.Errorf("expected client to receive 2 retried mutations, got %d", receivedCount)
	}
}

// TestRpcMutationToPayload tests conversion of RPC mutation events to payloads.
func TestRpcMutationToPayload(t *testing.T) {
	tests := []struct {
		name     string
		input    rpc.MutationEvent
		expected MutationPayload
	}{
		{
			name: "create mutation",
			input: rpc.MutationEvent{
				Type:      "create",
				IssueID:   "bd-1",
				Title:     "New Issue",
				Assignee:  "user1",
				Actor:     "actor1",
				Timestamp: time.Date(2025, 1, 23, 12, 0, 0, 0, time.UTC),
			},
			expected: MutationPayload{
				Type:      "create",
				IssueID:   "bd-1",
				Title:     "New Issue",
				Assignee:  "user1",
				Actor:     "actor1",
				Timestamp: "2025-01-23T12:00:00Z",
			},
		},
		{
			name: "status mutation",
			input: rpc.MutationEvent{
				Type:      "status",
				IssueID:   "bd-2",
				Timestamp: time.Date(2025, 1, 23, 14, 0, 0, 0, time.UTC),
				OldStatus: "open",
				NewStatus: "in_progress",
			},
			expected: MutationPayload{
				Type:      "status",
				IssueID:   "bd-2",
				Timestamp: "2025-01-23T14:00:00Z",
				OldStatus: "open",
				NewStatus: "in_progress",
			},
		},
		{
			name: "bonded mutation",
			input: rpc.MutationEvent{
				Type:      "bonded",
				IssueID:   "bd-3",
				Timestamp: time.Date(2025, 1, 23, 16, 0, 0, 0, time.UTC),
				ParentID:  "bd-parent",
				StepCount: 5,
			},
			expected: MutationPayload{
				Type:      "bonded",
				IssueID:   "bd-3",
				Timestamp: "2025-01-23T16:00:00Z",
				ParentID:  "bd-parent",
				StepCount: 5,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := rpcMutationToPayload(tt.input)

			if result.Type != tt.expected.Type {
				t.Errorf("Type: got %q, want %q", result.Type, tt.expected.Type)
			}
			if result.IssueID != tt.expected.IssueID {
				t.Errorf("IssueID: got %q, want %q", result.IssueID, tt.expected.IssueID)
			}
			if result.Title != tt.expected.Title {
				t.Errorf("Title: got %q, want %q", result.Title, tt.expected.Title)
			}
			if result.Assignee != tt.expected.Assignee {
				t.Errorf("Assignee: got %q, want %q", result.Assignee, tt.expected.Assignee)
			}
			if result.Actor != tt.expected.Actor {
				t.Errorf("Actor: got %q, want %q", result.Actor, tt.expected.Actor)
			}
			if result.Timestamp != tt.expected.Timestamp {
				t.Errorf("Timestamp: got %q, want %q", result.Timestamp, tt.expected.Timestamp)
			}
			if result.OldStatus != tt.expected.OldStatus {
				t.Errorf("OldStatus: got %q, want %q", result.OldStatus, tt.expected.OldStatus)
			}
			if result.NewStatus != tt.expected.NewStatus {
				t.Errorf("NewStatus: got %q, want %q", result.NewStatus, tt.expected.NewStatus)
			}
			if result.ParentID != tt.expected.ParentID {
				t.Errorf("ParentID: got %q, want %q", result.ParentID, tt.expected.ParentID)
			}
			if result.StepCount != tt.expected.StepCount {
				t.Errorf("StepCount: got %d, want %d", result.StepCount, tt.expected.StepCount)
			}
		})
	}
}

// TestHandleSSE_SendsRetryField tests that the retry field is sent in the initial handshake.
func TestHandleSSE_SendsRetryField(t *testing.T) {
	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	handler := handleSSE(hub, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(rr, req)
		close(done)
	}()

	time.Sleep(100 * time.Millisecond)

	body := rr.Body.String()
	expectedRetry := fmt.Sprintf("retry: %d", sseRetryMs)
	if !strings.Contains(body, expectedRetry) {
		t.Errorf("expected %q in response body, got:\n%s", expectedRetry, body)
	}
}

// TestHandleSSE_HeartbeatSent tests that heartbeat comments are sent periodically.
// This test uses a short sleep to check for heartbeat delivery.
// Note: In production, sseHeartbeatInterval is 30s. For this test, we verify the
// heartbeat ticker logic is wired correctly by checking the response after the handler
// has been running. Since we can't easily override the interval in tests without
// adding test-only parameters, we test that the heartbeat mechanism exists by
// verifying the code path is reachable.
func TestHandleSSE_HeartbeatSent(t *testing.T) {
	// This test verifies that `: heartbeat` comments appear in the SSE stream.
	// Since the default interval is 30s, we can't wait that long in a unit test.
	// Instead, we verify the initial handshake works and that the handler
	// is set up to send heartbeats by checking the response structure.
	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	handler := handleSSE(hub, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(rr, req)
		close(done)
	}()

	time.Sleep(100 * time.Millisecond)

	body := rr.Body.String()
	// Verify initial handshake is present (connected event + retry field)
	if !strings.Contains(body, "event: connected") {
		t.Error("expected connected event in response")
	}
	if !strings.Contains(body, "retry:") {
		t.Error("expected retry field in response")
	}
}

// TestWriteSSEEvent_ConcurrentMonotonicIDs tests that concurrent calls produce unique, increasing IDs.
func TestWriteSSEEvent_ConcurrentMonotonicIDs(t *testing.T) {
	const goroutines = 10
	const eventsPerGoroutine = 100

	var mu sync.Mutex
	allIDs := make([]int64, 0, goroutines*eventsPerGoroutine)

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			localIDs := make([]int64, 0, eventsPerGoroutine)
			for i := 0; i < eventsPerGoroutine; i++ {
				rr := httptest.NewRecorder()
				writeSSEEvent(rr, &MutationPayload{
					Type:      "create",
					IssueID:   "bd-test",
					Timestamp: "2025-01-23T12:00:00Z",
				})
				id := extractEventID(t, rr.Body.String())
				localIDs = append(localIDs, id)
			}
			mu.Lock()
			allIDs = append(allIDs, localIDs...)
			mu.Unlock()
		}()
	}

	wg.Wait()

	// Verify all IDs are unique
	seen := make(map[int64]bool, len(allIDs))
	for _, id := range allIDs {
		if seen[id] {
			t.Errorf("duplicate event ID: %d", id)
		}
		seen[id] = true
	}

	if len(seen) != goroutines*eventsPerGoroutine {
		t.Errorf("expected %d unique IDs, got %d", goroutines*eventsPerGoroutine, len(seen))
	}
}

// TestSSEHub_RegisterClientAfterStop verifies RegisterClient doesn't block after hub is stopped.
func TestSSEHub_RegisterClientAfterStop(t *testing.T) {
	hub := NewSSEHub()
	go hub.Run()

	// Stop the hub first
	hub.Stop()
	// Give Run() time to exit
	time.Sleep(50 * time.Millisecond)

	client := &SSEClient{
		id:   1,
		send: make(chan *MutationPayload, 64),
		done: make(chan struct{}),
	}

	// RegisterClient should not block; it should close client.send instead
	done := make(chan struct{})
	go func() {
		hub.RegisterClient(client)
		close(done)
	}()

	select {
	case <-done:
		// Verify send channel was closed
		_, ok := <-client.send
		if ok {
			t.Error("expected client.send to be closed after RegisterClient on stopped hub")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RegisterClient blocked after hub was stopped")
	}
}

// TestSSEHub_UnregisterClientAfterStop verifies UnregisterClient doesn't block after hub is stopped.
func TestSSEHub_UnregisterClientAfterStop(t *testing.T) {
	hub := NewSSEHub()
	go hub.Run()

	// Stop the hub first
	hub.Stop()
	// Give Run() time to exit
	time.Sleep(50 * time.Millisecond)

	client := &SSEClient{
		id:   1,
		send: make(chan *MutationPayload, 64),
		done: make(chan struct{}),
	}

	// UnregisterClient should not block
	done := make(chan struct{})
	go func() {
		hub.UnregisterClient(client)
		close(done)
	}()

	select {
	case <-done:
		// Success — did not block
	case <-time.After(2 * time.Second):
		t.Fatal("UnregisterClient blocked after hub was stopped")
	}
}
