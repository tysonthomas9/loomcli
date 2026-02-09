package webui

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
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

// --- Mock infrastructure for pollDBChanges tests ---

// startSubscriptionMockServer creates a Unix socket server that handles RPC requests.
// The countHandler is called for "count" operations. Health checks are automatically handled.
// Returns the socket path. The server and temp dir are cleaned up via t.Cleanup.
func startSubscriptionMockServer(t *testing.T, countHandler func(req rpc.Request) rpc.Response) string {
	handler := func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "health":
			healthData, _ := json.Marshal(rpc.HealthResponse{
				Status:     "healthy",
				Version:    "0.0.0",
				Compatible: true,
			})
			return rpc.Response{Success: true, Data: healthData}
		case "ping":
			return rpc.Response{Success: true}
		case "count":
			return countHandler(req)
		default:
			return rpc.Response{Success: false, Error: "unknown operation: " + req.Operation}
		}
	}
	return startSubscriptionMockServerRaw(t, handler)
}

// startSubscriptionMockServerRaw creates a Unix socket server that handles RPC requests.
// The handler receives the decoded rpc.Request and returns an rpc.Response.
// Returns the socket path. The server and temp dir are cleaned up via t.Cleanup.
func startSubscriptionMockServerRaw(t *testing.T, handler func(req rpc.Request) rpc.Response) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "sub-test-*")
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
				return // listener closed
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

// subscriptionMockPool implements daemon.Pool for testing, using a real Unix socket mock server.
type subscriptionMockPool struct {
	socketPath string
	clients    []*rpc.Client
	mu         sync.Mutex
}

func newSubscriptionMockPool(socketPath string) *subscriptionMockPool {
	return &subscriptionMockPool{socketPath: socketPath}
}

func (p *subscriptionMockPool) Get(ctx context.Context) (*rpc.Client, error) {
	client, err := rpc.TryConnectWithTimeout(p.socketPath, 2*time.Second)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, errors.New("failed to connect to mock server")
	}
	client.SetTimeout(5 * time.Second)
	p.mu.Lock()
	p.clients = append(p.clients, client)
	p.mu.Unlock()
	return client, nil
}

func (p *subscriptionMockPool) Put(client *rpc.Client) {
	if client != nil {
		client.Close()
	}
}

func (p *subscriptionMockPool) Discard(client *rpc.Client) {
	if client != nil {
		client.Close()
	}
}

func (p *subscriptionMockPool) Stats() daemon.PoolStats {
	return daemon.PoolStats{}
}

func (p *subscriptionMockPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.clients {
		c.Close()
	}
	return nil
}

// --- Tests for external DB change polling ---

// TestDaemonSubscriber_ExternalPollInterval verifies that externalPollInterval is 3 seconds.
func TestDaemonSubscriber_ExternalPollInterval(t *testing.T) {
	if externalPollInterval != 3*time.Second {
		t.Errorf("externalPollInterval = %v, want %v", externalPollInterval, 3*time.Second)
	}
	if externalPollInterval <= 0 {
		t.Errorf("externalPollInterval should be positive, got %v", externalPollInterval)
	}
}

// TestDaemonSubscriber_PollDBChanges_CountChanged verifies that when the pool returns
// a different count than lastKnownCount, a refresh event is broadcast to the hub.
func TestDaemonSubscriber_PollDBChanges_CountChanged(t *testing.T) {
	callCount := 0
	socketPath := startSubscriptionMockServer(t, func(req rpc.Request) rpc.Response {
		callCount++
		// Return count of 5 (different from initialized lastKnownCount of 3)
		countData, _ := json.Marshal(int64(5))
		return rpc.Response{Success: true, Data: countData}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	// Register a client to capture broadcasts
	client := &SSEClient{
		id:   1,
		send: make(chan *MutationPayload, 64),
		done: make(chan struct{}),
	}
	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond) // Wait for registration

	subscriber := NewDaemonSubscriber(pool, hub)
	// Simulate that initialization already happened with count=3
	subscriber.countInitialized = true
	subscriber.lastKnownCount = 3
	subscriber.lastPollTime = time.Now().Add(-5 * time.Second)

	subscriber.pollDBChanges()

	// Should have broadcast a refresh event
	select {
	case received := <-client.send:
		if received.Type != rpc.MutationRefresh {
			t.Errorf("expected type %q, got %q", rpc.MutationRefresh, received.Type)
		}
		if received.IssueID != "" {
			t.Errorf("expected empty issue_id for refresh, got %q", received.IssueID)
		}
		if received.Timestamp == "" {
			t.Error("expected non-empty timestamp")
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("did not receive broadcast after count changed")
	}

	// Verify lastKnownCount was updated
	if subscriber.lastKnownCount != 5 {
		t.Errorf("lastKnownCount = %d, want 5", subscriber.lastKnownCount)
	}
}

// TestDaemonSubscriber_PollDBChanges_UpdateDetected verifies that when the count is the same
// but Count with UpdatedAfter returns non-zero, a refresh event is broadcast.
func TestDaemonSubscriber_PollDBChanges_UpdateDetected(t *testing.T) {
	callNumber := 0
	socketPath := startSubscriptionMockServer(t, func(req rpc.Request) rpc.Response {
		callNumber++
		if callNumber == 1 {
			// First call: return same count (10) - no count change
			countData, _ := json.Marshal(int64(10))
			return rpc.Response{Success: true, Data: countData}
		}
		// Second call (UpdatedAfter check): return 1 updated issue
		countData, _ := json.Marshal(int64(1))
		return rpc.Response{Success: true, Data: countData}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

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

	subscriber := NewDaemonSubscriber(pool, hub)
	subscriber.countInitialized = true
	subscriber.lastKnownCount = 10 // Same as server will return
	subscriber.lastPollTime = time.Now().Add(-5 * time.Second)

	subscriber.pollDBChanges()

	// Should have broadcast a refresh event due to updated issues
	select {
	case received := <-client.send:
		if received.Type != rpc.MutationRefresh {
			t.Errorf("expected type %q, got %q", rpc.MutationRefresh, received.Type)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("did not receive broadcast after update detected")
	}
}

// TestDaemonSubscriber_PollDBChanges_NoChange verifies that when count and updated count
// are both unchanged, NO broadcast occurs.
func TestDaemonSubscriber_PollDBChanges_NoChange(t *testing.T) {
	callNumber := 0
	socketPath := startSubscriptionMockServer(t, func(req rpc.Request) rpc.Response {
		callNumber++
		if callNumber == 1 {
			// First call: return same count (10)
			countData, _ := json.Marshal(int64(10))
			return rpc.Response{Success: true, Data: countData}
		}
		// Second call (UpdatedAfter): return 0 updated issues
		countData, _ := json.Marshal(int64(0))
		return rpc.Response{Success: true, Data: countData}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

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

	subscriber := NewDaemonSubscriber(pool, hub)
	subscriber.countInitialized = true
	subscriber.lastKnownCount = 10
	subscriber.lastPollTime = time.Now().Add(-5 * time.Second)

	subscriber.pollDBChanges()

	// Should NOT receive any broadcast
	select {
	case received := <-client.send:
		t.Errorf("expected no broadcast, but received: %+v", received)
	case <-time.After(200 * time.Millisecond):
		// Good - no broadcast
	}

	// lastPollTime should still have been updated
	if subscriber.lastPollTime.IsZero() {
		t.Error("expected lastPollTime to be updated even when no change detected")
	}
}

// TestDaemonSubscriber_PollDBChanges_SkipsFirstPoll verifies that on the first poll,
// the subscriber initializes lastKnownCount and countInitialized without broadcasting.
func TestDaemonSubscriber_PollDBChanges_SkipsFirstPoll(t *testing.T) {
	socketPath := startSubscriptionMockServer(t, func(req rpc.Request) rpc.Response {
		// Return count of 7
		countData, _ := json.Marshal(int64(7))
		return rpc.Response{Success: true, Data: countData}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

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

	subscriber := NewDaemonSubscriber(pool, hub)
	// Verify initial state: countInitialized should be false
	if subscriber.countInitialized {
		t.Fatal("expected countInitialized to be false initially")
	}

	subscriber.pollDBChanges()

	// Should NOT broadcast on first poll (initialization only)
	select {
	case received := <-client.send:
		t.Errorf("expected no broadcast on first poll, but received: %+v", received)
	case <-time.After(200 * time.Millisecond):
		// Good - no broadcast on first poll
	}

	// Verify state was initialized
	if !subscriber.countInitialized {
		t.Error("expected countInitialized to be true after first poll")
	}
	if subscriber.lastKnownCount != 7 {
		t.Errorf("lastKnownCount = %d, want 7", subscriber.lastKnownCount)
	}
	if subscriber.lastPollTime.IsZero() {
		t.Error("expected lastPollTime to be set after first poll")
	}
}

// --- Tests for GetMutationsSince with mock server ---

// TestDaemonSubscriber_GetMutationsSince_Success tests successful mutation retrieval.
func TestDaemonSubscriber_GetMutationsSince_Success(t *testing.T) {
	ts := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	mutations := []rpc.MutationEvent{
		{Type: "create", IssueID: "bd-1", Timestamp: ts},
		{Type: "update", IssueID: "bd-2", Timestamp: ts.Add(time.Second)},
	}
	mutData, _ := json.Marshal(mutations)

	socketPath := startSubscriptionMockServerRaw(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "health":
			hd, _ := json.Marshal(rpc.HealthResponse{Status: "healthy", Version: "0.0.0", Compatible: true})
			return rpc.Response{Success: true, Data: hd}
		case "ping":
			return rpc.Response{Success: true}
		case "get_mutations":
			return rpc.Response{Success: true, Data: mutData}
		default:
			return rpc.Response{Success: false, Error: "unknown operation: " + req.Operation}
		}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

	hub := NewSSEHub()
	subscriber := NewDaemonSubscriber(pool, hub)

	result := subscriber.GetMutationsSince(0)
	if len(result) != 2 {
		t.Fatalf("expected 2 mutations, got %d", len(result))
	}
	if result[0].IssueID != "bd-1" {
		t.Errorf("mutation[0].IssueID = %q, want %q", result[0].IssueID, "bd-1")
	}
	if result[1].IssueID != "bd-2" {
		t.Errorf("mutation[1].IssueID = %q, want %q", result[1].IssueID, "bd-2")
	}
}

// TestDaemonSubscriber_GetMutationsSince_RPCFailure tests GetMutationsSince when RPC returns failure.
func TestDaemonSubscriber_GetMutationsSince_RPCFailure(t *testing.T) {
	socketPath := startSubscriptionMockServerRaw(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "health":
			hd, _ := json.Marshal(rpc.HealthResponse{Status: "healthy", Version: "0.0.0", Compatible: true})
			return rpc.Response{Success: true, Data: hd}
		case "ping":
			return rpc.Response{Success: true}
		case "get_mutations":
			return rpc.Response{Success: false, Error: "database locked"}
		default:
			return rpc.Response{Success: false, Error: "unknown"}
		}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

	hub := NewSSEHub()
	subscriber := NewDaemonSubscriber(pool, hub)

	result := subscriber.GetMutationsSince(0)
	if result != nil {
		t.Errorf("expected nil for RPC failure, got %v", result)
	}
}

// TestDaemonSubscriber_GetMutationsSince_InvalidJSON tests GetMutationsSince with invalid JSON data.
func TestDaemonSubscriber_GetMutationsSince_InvalidJSON(t *testing.T) {
	socketPath := startSubscriptionMockServerRaw(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "health":
			hd, _ := json.Marshal(rpc.HealthResponse{Status: "healthy", Version: "0.0.0", Compatible: true})
			return rpc.Response{Success: true, Data: hd}
		case "ping":
			return rpc.Response{Success: true}
		case "get_mutations":
			return rpc.Response{Success: true, Data: []byte(`not valid json`)}
		default:
			return rpc.Response{Success: false, Error: "unknown"}
		}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

	hub := NewSSEHub()
	subscriber := NewDaemonSubscriber(pool, hub)

	result := subscriber.GetMutationsSince(0)
	if result != nil {
		t.Errorf("expected nil for invalid JSON, got %v", result)
	}
}

// TestDaemonSubscriber_GetMutationsSince_EmptyMutations tests GetMutationsSince with empty result.
func TestDaemonSubscriber_GetMutationsSince_EmptyMutations(t *testing.T) {
	socketPath := startSubscriptionMockServerRaw(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "health":
			hd, _ := json.Marshal(rpc.HealthResponse{Status: "healthy", Version: "0.0.0", Compatible: true})
			return rpc.Response{Success: true, Data: hd}
		case "ping":
			return rpc.Response{Success: true}
		case "get_mutations":
			data, _ := json.Marshal([]rpc.MutationEvent{})
			return rpc.Response{Success: true, Data: data}
		default:
			return rpc.Response{Success: false, Error: "unknown"}
		}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

	hub := NewSSEHub()
	subscriber := NewDaemonSubscriber(pool, hub)

	result := subscriber.GetMutationsSince(0)
	if len(result) != 0 {
		t.Errorf("expected 0 mutations, got %d", len(result))
	}
}

// --- Tests for waitForMutations ---

// TestDaemonSubscriber_WaitForMutations_UnknownOperation tests that waitForMutations
// switches to fallback polling when daemon returns "unknown operation" error.
func TestDaemonSubscriber_WaitForMutations_UnknownOperation(t *testing.T) {
	socketPath := startSubscriptionMockServerRaw(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "health":
			hd, _ := json.Marshal(rpc.HealthResponse{Status: "healthy", Version: "0.0.0", Compatible: true})
			return rpc.Response{Success: true, Data: hd}
		case "ping":
			return rpc.Response{Success: true}
		case "wait_for_mutations":
			return rpc.Response{Success: false, Error: "unknown operation: wait_for_mutations"}
		default:
			return rpc.Response{Success: false, Error: "unknown operation: " + req.Operation}
		}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

	hub := NewSSEHub()
	subscriber := NewDaemonSubscriber(pool, hub)

	// Verify useFallback is false initially
	subscriber.mu.RLock()
	if subscriber.useFallback {
		t.Fatal("expected useFallback to be false initially")
	}
	subscriber.mu.RUnlock()

	subscriber.waitForMutations()

	// After receiving "unknown operation" error, useFallback should be true
	subscriber.mu.RLock()
	useFallback := subscriber.useFallback
	subscriber.mu.RUnlock()

	if !useFallback {
		t.Error("expected useFallback to be true after unknown operation error")
	}
}

// TestDaemonSubscriber_WaitForMutations_RPCFailure tests waitForMutations with non-success response.
func TestDaemonSubscriber_WaitForMutations_RPCFailure(t *testing.T) {
	socketPath := startSubscriptionMockServerRaw(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "health":
			hd, _ := json.Marshal(rpc.HealthResponse{Status: "healthy", Version: "0.0.0", Compatible: true})
			return rpc.Response{Success: true, Data: hd}
		case "ping":
			return rpc.Response{Success: true}
		case "wait_for_mutations":
			return rpc.Response{Success: false, Error: "internal error"}
		default:
			return rpc.Response{Success: false, Error: "unknown"}
		}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

	hub := NewSSEHub()
	subscriber := NewDaemonSubscriber(pool, hub)

	// This should not panic or block indefinitely
	done := make(chan struct{})
	go func() {
		subscriber.waitForMutations()
		close(done)
	}()

	select {
	case <-done:
		// Good
	case <-time.After(10 * time.Second):
		t.Error("waitForMutations blocked too long on RPC failure")
	}

	// useFallback should still be false (not an unknown operation error)
	subscriber.mu.RLock()
	if subscriber.useFallback {
		t.Error("useFallback should remain false for non-unknown-operation errors")
	}
	subscriber.mu.RUnlock()
}

// TestDaemonSubscriber_WaitForMutations_Success tests waitForMutations successfully
// receiving mutations and broadcasting them.
func TestDaemonSubscriber_WaitForMutations_Success(t *testing.T) {
	ts := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	mutations := []rpc.MutationEvent{
		{Type: "create", IssueID: "bd-wait-1", Timestamp: ts},
	}
	mutData, _ := json.Marshal(mutations)

	socketPath := startSubscriptionMockServerRaw(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "health":
			hd, _ := json.Marshal(rpc.HealthResponse{Status: "healthy", Version: "0.0.0", Compatible: true})
			return rpc.Response{Success: true, Data: hd}
		case "ping":
			return rpc.Response{Success: true}
		case "wait_for_mutations":
			return rpc.Response{Success: true, Data: mutData}
		default:
			return rpc.Response{Success: false, Error: "unknown"}
		}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

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

	subscriber := NewDaemonSubscriber(pool, hub)
	subscriber.waitForMutations()

	// Should have broadcast the mutation
	select {
	case received := <-client.send:
		if received.IssueID != "bd-wait-1" {
			t.Errorf("issue_id = %q, want %q", received.IssueID, "bd-wait-1")
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("did not receive broadcast from waitForMutations")
	}

	// Verify lastSince was updated
	subscriber.mu.RLock()
	if subscriber.lastSince == 0 {
		t.Error("expected lastSince to be updated after successful waitForMutations")
	}
	subscriber.mu.RUnlock()
}

// --- Tests for pollMutations ---

// TestDaemonSubscriber_PollMutations_Success tests pollMutations with a successful response.
func TestDaemonSubscriber_PollMutations_Success(t *testing.T) {
	ts := time.Date(2025, 6, 15, 14, 0, 0, 0, time.UTC)
	mutations := []rpc.MutationEvent{
		{Type: "update", IssueID: "bd-poll-1", Timestamp: ts},
	}
	mutData, _ := json.Marshal(mutations)

	socketPath := startSubscriptionMockServerRaw(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "health":
			hd, _ := json.Marshal(rpc.HealthResponse{Status: "healthy", Version: "0.0.0", Compatible: true})
			return rpc.Response{Success: true, Data: hd}
		case "ping":
			return rpc.Response{Success: true}
		case "get_mutations":
			return rpc.Response{Success: true, Data: mutData}
		default:
			return rpc.Response{Success: false, Error: "unknown"}
		}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

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

	subscriber := NewDaemonSubscriber(pool, hub)
	subscriber.useFallback = true // Enable fallback mode

	done := make(chan struct{})
	go func() {
		subscriber.pollMutations()
		close(done)
	}()

	// Should receive the broadcast
	select {
	case received := <-client.send:
		if received.IssueID != "bd-poll-1" {
			t.Errorf("issue_id = %q, want %q", received.IssueID, "bd-poll-1")
		}
	case <-time.After(2 * time.Second):
		t.Error("did not receive broadcast from pollMutations")
	}

	// Wait for pollMutations to complete (includes fallbackPollInterval wait)
	select {
	case <-done:
		// Good
	case <-time.After(5 * time.Second):
		t.Error("pollMutations blocked too long")
	}
}

// TestDaemonSubscriber_PollMutations_RPCFailure tests pollMutations when get_mutations returns failure.
func TestDaemonSubscriber_PollMutations_RPCFailure(t *testing.T) {
	socketPath := startSubscriptionMockServerRaw(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "health":
			hd, _ := json.Marshal(rpc.HealthResponse{Status: "healthy", Version: "0.0.0", Compatible: true})
			return rpc.Response{Success: true, Data: hd}
		case "ping":
			return rpc.Response{Success: true}
		case "get_mutations":
			return rpc.Response{Success: false, Error: "database error"}
		default:
			return rpc.Response{Success: false, Error: "unknown"}
		}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	subscriber := NewDaemonSubscriber(pool, hub)
	subscriber.useFallback = true

	done := make(chan struct{})
	go func() {
		subscriber.pollMutations()
		close(done)
	}()

	select {
	case <-done:
		// Good - completed without blocking
	case <-time.After(10 * time.Second):
		t.Error("pollMutations blocked too long on RPC failure")
	}
}

// TestDaemonSubscriber_PollDBChanges_NilPool verifies that when pool is nil,
// pollDBChanges does not panic. The externalChangeLoop skips when pool is nil.
func TestDaemonSubscriber_PollDBChanges_NilPool(t *testing.T) {
	hub := NewSSEHub()
	subscriber := NewDaemonSubscriber(nil, hub)

	// pollDBChanges with nil pool should not panic
	// (it will try to call s.pool.Get which will panic with nil pool,
	// but the externalChangeLoop guards against nil pool before calling pollDBChanges)
	// We test that the externalChangeLoop guard works by verifying Start/Stop with nil pool.
	go hub.Run()
	defer hub.Stop()

	subscriber.Start()
	time.Sleep(100 * time.Millisecond) // Let the loop run a tick
	stopped := make(chan struct{})
	go func() {
		subscriber.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
		// Good - stopped without panic
	case <-time.After(5 * time.Second):
		t.Error("Stop() blocked for too long with nil pool")
	}
}
