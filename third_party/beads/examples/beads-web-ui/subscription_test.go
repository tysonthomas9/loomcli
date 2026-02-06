package main

import (
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/rpc"
)

// TestNewDaemonSubscriber tests that NewDaemonSubscriber creates a properly initialized subscriber.
func TestNewDaemonSubscriber(t *testing.T) {
	hub := NewSSEHub()
	subscriber := NewDaemonSubscriber(nil, hub)

	if subscriber == nil {
		t.Fatal("NewDaemonSubscriber() returned nil")
	}

	if subscriber.hub != hub {
		t.Error("expected hub to be set")
	}
	if subscriber.done == nil {
		t.Error("expected done channel to be initialized")
	}
	if subscriber.pool != nil {
		t.Error("expected pool to be nil when passed nil")
	}
}

// TestDaemonSubscriber_StartStop tests the start and stop lifecycle.
func TestDaemonSubscriber_StartStop(t *testing.T) {
	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	subscriber := NewDaemonSubscriber(nil, hub)

	// Start should not block
	started := make(chan struct{})
	go func() {
		subscriber.Start()
		close(started)
	}()

	select {
	case <-started:
		// Good - Start() returned
	case <-time.After(100 * time.Millisecond):
		t.Error("Start() blocked unexpectedly")
	}

	// Stop should complete cleanly
	stopped := make(chan struct{})
	go func() {
		subscriber.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
		// Good - Stop() returned
	case <-time.After(3 * time.Second):
		t.Error("Stop() blocked for too long")
	}
}

// TestDaemonSubscriber_GetMutationsSince_NilPool tests GetMutationsSince with nil pool.
func TestDaemonSubscriber_GetMutationsSince_NilPool(t *testing.T) {
	hub := NewSSEHub()
	subscriber := NewDaemonSubscriber(nil, hub)

	result := subscriber.GetMutationsSince(0)

	if result != nil {
		t.Errorf("expected nil result with nil pool, got %v", result)
	}
}

// TestIsUnknownOperationError tests detection of unknown operation errors.
func TestIsUnknownOperationError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "unknown operation error",
			err:      errors.New("unknown operation: wait_for_mutations"),
			expected: true,
		},
		{
			name:     "unsupported error",
			err:      errors.New("operation unsupported by server"),
			expected: true,
		},
		{
			name:     "contains unknown operation",
			err:      errors.New("RPC error: unknown operation requested"),
			expected: true,
		},
		{
			name:     "connection error",
			err:      errors.New("connection refused"),
			expected: false,
		},
		{
			name:     "timeout error",
			err:      errors.New("context deadline exceeded"),
			expected: false,
		},
		{
			name:     "empty error",
			err:      errors.New(""),
			expected: false,
		},
		{
			name:     "generic error",
			err:      errors.New("something went wrong"),
			expected: false,
		},
		{
			name:     "case sensitive - Unknown Operation",
			err:      errors.New("Unknown Operation"),
			expected: false, // Case-sensitive check
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isUnknownOperationError(tt.err)
			if result != tt.expected {
				t.Errorf("isUnknownOperationError(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

// TestDaemonSubscriber_UseFallback tests that useFallback flag can be set and read.
func TestDaemonSubscriber_UseFallback(t *testing.T) {
	hub := NewSSEHub()
	subscriber := NewDaemonSubscriber(nil, hub)

	// Initially should be false
	subscriber.mu.RLock()
	if subscriber.useFallback {
		t.Error("expected useFallback to be false initially")
	}
	subscriber.mu.RUnlock()

	// Set to true
	subscriber.mu.Lock()
	subscriber.useFallback = true
	subscriber.mu.Unlock()

	subscriber.mu.RLock()
	if !subscriber.useFallback {
		t.Error("expected useFallback to be true after setting")
	}
	subscriber.mu.RUnlock()
}

// TestDaemonSubscriber_LastSince tests that lastSince is tracked correctly.
func TestDaemonSubscriber_LastSince(t *testing.T) {
	hub := NewSSEHub()
	subscriber := NewDaemonSubscriber(nil, hub)

	// Initially should be 0
	subscriber.mu.RLock()
	if subscriber.lastSince != 0 {
		t.Errorf("expected lastSince to be 0 initially, got %d", subscriber.lastSince)
	}
	subscriber.mu.RUnlock()

	// Update lastSince
	subscriber.mu.Lock()
	subscriber.lastSince = 1706000000000
	subscriber.mu.Unlock()

	subscriber.mu.RLock()
	if subscriber.lastSince != 1706000000000 {
		t.Errorf("expected lastSince to be 1706000000000, got %d", subscriber.lastSince)
	}
	subscriber.mu.RUnlock()
}

// TestSubscriptionConstants tests that subscription timeout constants are reasonable.
func TestSubscriptionConstants(t *testing.T) {
	if subscriptionTimeout <= 0 {
		t.Errorf("subscriptionTimeout should be positive, got %v", subscriptionTimeout)
	}

	if subscriptionRetryDelay <= 0 {
		t.Errorf("subscriptionRetryDelay should be positive, got %v", subscriptionRetryDelay)
	}

	if subscriptionAcquireTimeout <= 0 {
		t.Errorf("subscriptionAcquireTimeout should be positive, got %v", subscriptionAcquireTimeout)
	}

	if fallbackPollInterval <= 0 {
		t.Errorf("fallbackPollInterval should be positive, got %v", fallbackPollInterval)
	}

	// subscriptionTimeout should be longer than subscriptionAcquireTimeout
	if subscriptionTimeout < subscriptionAcquireTimeout {
		t.Errorf("subscriptionTimeout (%v) should be >= subscriptionAcquireTimeout (%v)",
			subscriptionTimeout, subscriptionAcquireTimeout)
	}
}

// TestDaemonSubscriber_WaitWithDone tests that waitWithDone respects the done channel.
func TestDaemonSubscriber_WaitWithDone(t *testing.T) {
	hub := NewSSEHub()
	subscriber := NewDaemonSubscriber(nil, hub)

	// Test that waitWithDone returns early when done is closed
	done := make(chan struct{})
	go func() {
		time.Sleep(10 * time.Millisecond)
		close(subscriber.done)
		close(done)
	}()

	start := time.Now()
	subscriber.waitWithDone(5 * time.Second) // Long duration
	elapsed := time.Since(start)

	// Should have returned much faster than 5 seconds
	if elapsed > 1*time.Second {
		t.Errorf("waitWithDone took too long: %v", elapsed)
	}

	<-done
}

// TestDaemonSubscriber_WaitWithDone_NormalTimeout tests waitWithDone normal timeout behavior.
func TestDaemonSubscriber_WaitWithDone_NormalTimeout(t *testing.T) {
	hub := NewSSEHub()
	subscriber := NewDaemonSubscriber(nil, hub)

	start := time.Now()
	subscriber.waitWithDone(50 * time.Millisecond)
	elapsed := time.Since(start)

	// Should have waited approximately 50ms
	if elapsed < 40*time.Millisecond {
		t.Errorf("waitWithDone returned too early: %v", elapsed)
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("waitWithDone took too long: %v", elapsed)
	}
}

// TestDaemonSubscriber_SubscriptionLoop_NilPool tests that subscription loop handles nil pool.
func TestDaemonSubscriber_SubscriptionLoop_NilPool(t *testing.T) {
	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	subscriber := NewDaemonSubscriber(nil, hub)
	subscriber.Start()

	// Let it run a bit with nil pool (should just retry)
	time.Sleep(100 * time.Millisecond)

	// Stop should work cleanly
	stopped := make(chan struct{})
	go func() {
		subscriber.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
		// Good
	case <-time.After(5 * time.Second):
		t.Error("Stop() blocked for too long")
	}
}

// TestDaemonSubscriber_BroadcastsToHub tests that subscriber broadcasts to the hub.
func TestDaemonSubscriber_BroadcastsToHub(t *testing.T) {
	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	// Register a client to receive broadcasts
	client := &SSEClient{
		id:   1,
		send: make(chan *MutationPayload, 64),
		done: make(chan struct{}),
	}
	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond)

	// Manually broadcast through the hub (simulating what processMutationResponse does)
	mutation := &MutationPayload{
		Type:      "create",
		IssueID:   "bd-test",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	hub.Broadcast(mutation)

	// Client should receive the mutation
	select {
	case received := <-client.send:
		if received.IssueID != "bd-test" {
			t.Errorf("expected issue_id 'bd-test', got %q", received.IssueID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("client did not receive broadcast")
	}
}

// TestDaemonSubscriber_LastSinceUpdatedBeforeBroadcast verifies that lastSince
// is updated BEFORE any broadcast happens. This prevents a race condition where
// a concurrent goroutine could read a stale lastSince and request duplicate mutations.
func TestDaemonSubscriber_LastSinceUpdatedBeforeBroadcast(t *testing.T) {
	hub := NewSSEHub()
	// Do NOT call hub.Run() — we want broadcasts to land in the buffered channel
	// so we can observe ordering.

	subscriber := NewDaemonSubscriber(nil, hub)

	// Create mutations with known timestamps
	ts1 := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	ts2 := time.Date(2025, 6, 15, 12, 0, 1, 0, time.UTC)
	mutations := []rpc.MutationEvent{
		{Type: "create", IssueID: "bd-1", Timestamp: ts1},
		{Type: "update", IssueID: "bd-2", Timestamp: ts2},
	}

	mutationData, err := json.Marshal(mutations)
	if err != nil {
		t.Fatalf("failed to marshal mutations: %v", err)
	}

	resp := &rpc.Response{
		Success: true,
		Data:    mutationData,
	}

	// Record lastSince values observed at each broadcast.
	// We intercept by draining the hub's broadcast channel in a goroutine
	// that snapshots lastSince each time a message arrives.
	var lastSinceAtBroadcast []int64
	var broadcastCount int32
	done := make(chan struct{})

	go func() {
		for {
			select {
			case <-hub.broadcast:
				// Snapshot lastSince at the moment we observe a broadcast
				subscriber.mu.RLock()
				ls := subscriber.lastSince
				subscriber.mu.RUnlock()
				lastSinceAtBroadcast = append(lastSinceAtBroadcast, ls)
				atomic.AddInt32(&broadcastCount, 1)
			case <-done:
				return
			}
		}
	}()

	// Call processMutationResponse — this is the method under test
	subscriber.processMutationResponse(resp)

	// Give the goroutine time to drain all broadcasts
	time.Sleep(50 * time.Millisecond)
	close(done)

	// We expect 2 broadcasts (one per mutation)
	count := int(atomic.LoadInt32(&broadcastCount))
	if count != 2 {
		t.Fatalf("expected 2 broadcasts, got %d", count)
	}

	// The expected lastSince after update is maxTimestamp + 1
	expectedLastSince := ts2.UnixMilli() + 1

	// Verify lastSince was already updated when the FIRST broadcast was observed.
	// Before the fix, lastSince would have been 0 (stale) at broadcast time.
	for i, ls := range lastSinceAtBroadcast {
		if ls != expectedLastSince {
			t.Errorf("broadcast %d: lastSince was %d at broadcast time, expected %d (updated before broadcast)",
				i, ls, expectedLastSince)
		}
	}

	// Also verify the final lastSince value is correct
	subscriber.mu.RLock()
	finalLastSince := subscriber.lastSince
	subscriber.mu.RUnlock()

	if finalLastSince != expectedLastSince {
		t.Errorf("final lastSince = %d, want %d", finalLastSince, expectedLastSince)
	}
}
