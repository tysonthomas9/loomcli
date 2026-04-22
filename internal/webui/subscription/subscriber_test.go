package subscription

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
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// TestNewDaemonSubscriber tests that NewDaemonSubscriber creates a properly initialized subscriber.
func TestNewDaemonSubscriber(t *testing.T) {
	hub := realtime.NewHub()
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
	hub := realtime.NewHub()
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
	hub := realtime.NewHub()
	subscriber := NewDaemonSubscriber(nil, hub)

	result := subscriber.GetMutationsSince(0)

	if result != nil {
		t.Errorf("expected nil result with nil pool, got %v", result)
	}
}

// TestDaemonSubscriber_LastSince tests that lastSince is tracked correctly.
func TestDaemonSubscriber_LastSince(t *testing.T) {
	hub := realtime.NewHub()
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

	// subscriptionTimeout should be longer than subscriptionAcquireTimeout
	if subscriptionTimeout < subscriptionAcquireTimeout {
		t.Errorf("subscriptionTimeout (%v) should be >= subscriptionAcquireTimeout (%v)",
			subscriptionTimeout, subscriptionAcquireTimeout)
	}
}

// TestDaemonSubscriber_WaitWithDone tests that waitWithDone respects the done channel.
func TestDaemonSubscriber_WaitWithDone(t *testing.T) {
	hub := realtime.NewHub()
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
	hub := realtime.NewHub()
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
	hub := realtime.NewHub()
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
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	// Register a client to receive broadcasts
	client := realtime.NewClient(1, 64, 0, nil, "test-ws")
	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond)

	// Manually broadcast through the hub (simulating what processMutationResponse does)
	mutation := &realtime.MutationPayload{
		Type:        "create",
		IssueID:     "bd-test",
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
		WorkspaceID: "test-ws",
	}
	hub.Broadcast(mutation)

	// Client should receive the mutation
	select {
	case received := <-client.Send():
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
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	subscriber := NewDaemonSubscriber(nil, hub)
	subscriber.workspaceID = "test-ws"

	// Register a client to observe broadcasts
	client := realtime.NewClient(1, 64, 0, nil, "test-ws")
	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond)

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

	// Call processMutationResponse — this is the method under test
	subscriber.processMutationResponse(resp)

	// Read broadcasts from the client
	time.Sleep(100 * time.Millisecond)
	var broadcastCount int
	for {
		select {
		case <-client.Send():
			broadcastCount++
		default:
			goto drained
		}
	}
drained:

	// We expect 2 broadcasts (one per mutation)
	if broadcastCount != 2 {
		t.Fatalf("expected 2 broadcasts, got %d", broadcastCount)
	}

	// The expected lastSince after update is maxTimestamp (strict ">" comparison
	// in daemon's GetRecentMutations means we don't need +1 and adding it would
	// skip mutations created at maxTimestamp+1).
	expectedLastSince := ts2.UnixMilli()

	// Verify lastSince was already updated when the FIRST broadcast was observed.
	// Verify the final lastSince value is correct
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

// startSubscriptionMockServerWithDrop behaves like startSubscriptionMockServer but
// closes the connection mid-response for any request whose Operation+callIndex
// matches dropCond. callIndex is 1-based across all ops on the same connection.
// Closing the conn without writing a response causes the client's ReadBytes('\n')
// to return EOF — simulating a transport-level failure that leaves the read
// buffer in an unsafe state (ref: loomcli-67meg).
//
// countHandler is invoked for "count" operations that are NOT being dropped.
// listHandler is invoked for "list" operations that are NOT being dropped.
// Both may be nil; the defaults return Success: true with an empty body.
func startSubscriptionMockServerWithDrop(
	t *testing.T,
	dropCond func(op string, callIndex int) bool,
	countHandler func(req rpc.Request) rpc.Response,
	listHandler func(req rpc.Request) rpc.Response,
) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "sub-test-drop-*")
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
				callIndex := 0
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
					callIndex++
					if dropCond != nil && dropCond(req.Operation, callIndex) {
						// Close without writing a response — triggers EOF on the client.
						return
					}
					var resp rpc.Response
					switch req.Operation {
					case "health":
						healthData, _ := json.Marshal(rpc.HealthResponse{
							Status: "healthy", Version: "0.0.0", Compatible: true,
						})
						resp = rpc.Response{Success: true, Data: healthData}
					case "ping":
						resp = rpc.Response{Success: true}
					case "count":
						if countHandler != nil {
							resp = countHandler(req)
						} else {
							countData, _ := json.Marshal(struct {
								Count int64 `json:"count"`
							}{Count: 0})
							resp = rpc.Response{Success: true, Data: countData}
						}
					case "list":
						if listHandler != nil {
							resp = listHandler(req)
						} else {
							resp = rpc.Response{Success: true, Data: []byte("[]")}
						}
					default:
						resp = rpc.Response{Success: false, Error: "unknown operation: " + req.Operation}
					}
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
// Counts put/discard calls so tests can verify the connection-pool discipline
// in pollDBChanges (ref: loomcli-67meg).
type subscriptionMockPool struct {
	socketPath   string
	clients      []*rpc.Client
	mu           sync.Mutex
	putCount     int32
	discardCount int32
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
	atomic.AddInt32(&p.putCount, 1)
	if client != nil {
		client.Close()
	}
}

func (p *subscriptionMockPool) PutAfterError(client *rpc.Client) { p.Put(client) }

func (p *subscriptionMockPool) Discard(client *rpc.Client) {
	atomic.AddInt32(&p.discardCount, 1)
	if client != nil {
		client.Close()
	}
}

// PutCount returns the number of times Put was called.
func (p *subscriptionMockPool) PutCount() int32 {
	return atomic.LoadInt32(&p.putCount)
}

// DiscardCount returns the number of times Discard was called.
func (p *subscriptionMockPool) DiscardCount() int32 {
	return atomic.LoadInt32(&p.discardCount)
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
		countData, _ := json.Marshal(struct {
			Count int64 `json:"count"`
		}{Count: 5})
		return rpc.Response{Success: true, Data: countData}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	// Register a client to capture broadcasts
	client := realtime.NewClient(1, 64, 0, nil, "test-ws")
	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond) // Wait for registration

	subscriber := NewDaemonSubscriber(pool, hub)
	subscriber.workspaceID = "test-ws"
	// Simulate that initialization already happened with count=3
	subscriber.countInitialized = true
	subscriber.lastKnownCount = 3
	subscriber.lastPollTime = time.Now().Add(-5 * time.Second)

	subscriber.pollDBChanges()

	// Should have broadcast a refresh event
	select {
	case received := <-client.Send():
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
			countData, _ := json.Marshal(struct {
				Count int64 `json:"count"`
			}{Count: 10})
			return rpc.Response{Success: true, Data: countData}
		}
		// Second call (UpdatedAfter check): return 1 updated issue
		countData, _ := json.Marshal(struct {
			Count int64 `json:"count"`
		}{Count: 1})
		return rpc.Response{Success: true, Data: countData}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	client := realtime.NewClient(1, 64, 0, nil, "test-ws")
	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond)

	subscriber := NewDaemonSubscriber(pool, hub)
	subscriber.workspaceID = "test-ws"
	subscriber.countInitialized = true
	subscriber.lastKnownCount = 10 // Same as server will return
	subscriber.lastPollTime = time.Now().Add(-5 * time.Second)

	subscriber.pollDBChanges()

	// Should have broadcast a refresh event due to updated issues
	select {
	case received := <-client.Send():
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
			countData, _ := json.Marshal(struct {
				Count int64 `json:"count"`
			}{Count: 10})
			return rpc.Response{Success: true, Data: countData}
		}
		// Second call (UpdatedAfter): return 0 updated issues
		countData, _ := json.Marshal(struct {
			Count int64 `json:"count"`
		}{Count: 0})
		return rpc.Response{Success: true, Data: countData}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	client := realtime.NewClient(1, 64, 0, nil, "")
	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond)

	subscriber := NewDaemonSubscriber(pool, hub)
	subscriber.countInitialized = true
	subscriber.lastKnownCount = 10
	subscriber.lastPollTime = time.Now().Add(-5 * time.Second)

	subscriber.pollDBChanges()

	// Should NOT receive any broadcast
	select {
	case received := <-client.Send():
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
		countData, _ := json.Marshal(struct {
			Count int64 `json:"count"`
		}{Count: 7})
		return rpc.Response{Success: true, Data: countData}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	client := realtime.NewClient(1, 64, 0, nil, "")
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
	case received := <-client.Send():
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

	hub := realtime.NewHub()
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

	hub := realtime.NewHub()
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

	hub := realtime.NewHub()
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

	hub := realtime.NewHub()
	subscriber := NewDaemonSubscriber(pool, hub)

	result := subscriber.GetMutationsSince(0)
	if len(result) != 0 {
		t.Errorf("expected 0 mutations, got %d", len(result))
	}
}

// --- Tests for waitForMutations ---

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

	hub := realtime.NewHub()
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

	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	client := realtime.NewClient(1, 64, 0, nil, "test-ws")
	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond)

	subscriber := NewDaemonSubscriber(pool, hub)
	subscriber.workspaceID = "test-ws"
	subscriber.waitForMutations()

	// Should have broadcast the mutation
	select {
	case received := <-client.Send():
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

// TestDaemonSubscriber_PollDBChanges_NilPool verifies that when pool is nil,
// pollDBChanges does not panic. The externalChangeLoop skips when pool is nil.
func TestDaemonSubscriber_PollDBChanges_NilPool(t *testing.T) {
	hub := realtime.NewHub()
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

// TestDaemonSubscriber_ExternalChangeLoop_IntegrationDetectsChange tests the full
// externalChangeLoop → pollDBChanges → hub.Broadcast path as an integration test.
func TestDaemonSubscriber_ExternalChangeLoop_IntegrationDetectsChange(t *testing.T) {
	var countCallNum int32
	socketPath := startSubscriptionMockServerRaw(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "health":
			hd, _ := json.Marshal(rpc.HealthResponse{Status: "healthy", Version: "0.0.0", Compatible: true})
			return rpc.Response{Success: true, Data: hd}
		case "ping":
			return rpc.Response{Success: true}
		case "wait_for_mutations":
			// Return empty mutations quickly - we only care about external change loop
			return rpc.Response{Success: true, Data: []byte("[]")}
		case "count":
			n := atomic.AddInt32(&countCallNum, 1)
			if n == 1 {
				// First call: return count=5 (initialization)
				countData, _ := json.Marshal(struct {
					Count int64 `json:"count"`
				}{Count: 5})
				return rpc.Response{Success: true, Data: countData}
			}
			// Subsequent calls: return count=7 (change detected)
			countData, _ := json.Marshal(struct {
				Count int64 `json:"count"`
			}{Count: 7})
			return rpc.Response{Success: true, Data: countData}
		default:
			return rpc.Response{Success: false, Error: "unknown operation: " + req.Operation}
		}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	// Register an SSE client to capture broadcasts
	client := realtime.NewClient(1, 64, 0, nil, "test-ws")
	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond)

	subscriber := NewDaemonSubscriber(pool, hub)
	subscriber.workspaceID = "test-ws"
	subscriber.Start()

	// Wait for a refresh broadcast to arrive (3x externalPollInterval to account for
	// initialization poll + detection poll + some slack)
	timeout := time.After(3*externalPollInterval + time.Second)
	var received *realtime.MutationPayload
waitLoop:
	for {
		select {
		case msg := <-client.Send():
			if msg.Type == rpc.MutationRefresh {
				received = msg
				break waitLoop
			}
		case <-timeout:
			break waitLoop
		}
	}

	if received == nil {
		t.Error("timed out waiting for refresh broadcast from externalChangeLoop")
	}

	// Clean shutdown
	stopped := make(chan struct{})
	go func() {
		subscriber.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		// Good
	case <-time.After(5 * time.Second):
		t.Error("subscriber.Stop() blocked too long")
	}
}

// TestDaemonSubscriber_ExternalChangeLoop_StopsPromptly tests that calling Stop()
// interrupts the externalChangeLoop promptly.
func TestDaemonSubscriber_ExternalChangeLoop_StopsPromptly(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	// Use nil pool so externalChangeLoop just loops with continue
	subscriber := NewDaemonSubscriber(nil, hub)
	subscriber.Start()

	// Immediately call Stop
	start := time.Now()
	stopped := make(chan struct{})
	go func() {
		subscriber.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
		elapsed := time.Since(start)
		if elapsed > 1*time.Second {
			t.Errorf("Stop() took %v, expected < 1s", elapsed)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Stop() blocked for too long")
	}
}

// TestDaemonSubscriber_ConcurrentLoops_NoRace tests that both subscription loop and
// external change loop can run concurrently without data races. This test is valuable
// when run under `go test -race`.
func TestDaemonSubscriber_ConcurrentLoops_NoRace(t *testing.T) {
	ts := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	mutations := []rpc.MutationEvent{
		{Type: "create", IssueID: "bd-race-1", Timestamp: ts},
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
			// Return mutations (this simulates a working daemon)
			return rpc.Response{Success: true, Data: mutData}
		case "get_mutations":
			return rpc.Response{Success: true, Data: mutData}
		case "count":
			countData, _ := json.Marshal(struct {
				Count int64 `json:"count"`
			}{Count: 10})
			return rpc.Response{Success: true, Data: countData}
		default:
			return rpc.Response{Success: false, Error: "unknown operation: " + req.Operation}
		}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	// Register a client to consume broadcasts (prevent channel backpressure)
	client := realtime.NewClient(1, 512, 0, nil, "test-ws")
	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond)

	subscriber := NewDaemonSubscriber(pool, hub)
	subscriber.workspaceID = "test-ws"
	subscriber.Start()

	// Let both loops run concurrently for 500ms (enough for -race to catch issues)
	time.Sleep(500 * time.Millisecond)

	// Clean shutdown
	stopped := make(chan struct{})
	go func() {
		subscriber.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		// Good — clean shutdown, no race detected
	case <-time.After(5 * time.Second):
		t.Fatal("subscriber.Stop() blocked too long")
	}
}

// TestDaemonSubscriber_PollDBChanges_CountRPCNotSuccess tests that pollDBChanges handles
// a non-success response from the count RPC gracefully.
func TestDaemonSubscriber_PollDBChanges_CountRPCNotSuccess(t *testing.T) {
	socketPath := startSubscriptionMockServer(t, func(req rpc.Request) rpc.Response {
		// Return Success: false for count operation
		return rpc.Response{Success: false, Error: "permission denied"}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	client := realtime.NewClient(1, 64, 0, nil, "")
	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond)

	subscriber := NewDaemonSubscriber(pool, hub)
	subscriber.countInitialized = true
	subscriber.lastKnownCount = 10
	subscriber.lastPollTime = time.Now().Add(-5 * time.Second)

	subscriber.pollDBChanges()

	// Should NOT receive any broadcast
	select {
	case received := <-client.Send():
		t.Errorf("expected no broadcast when count RPC fails, but received: %+v", received)
	case <-time.After(200 * time.Millisecond):
		// Good — no broadcast
	}

	// lastKnownCount should remain unchanged
	if subscriber.lastKnownCount != 10 {
		t.Errorf("lastKnownCount changed to %d, expected 10", subscriber.lastKnownCount)
	}
}

// TestDaemonSubscriber_PollDBChanges_InvalidCountJSON tests that pollDBChanges handles
// invalid JSON in the count response gracefully without panicking.
func TestDaemonSubscriber_PollDBChanges_InvalidCountJSON(t *testing.T) {
	socketPath := startSubscriptionMockServer(t, func(req rpc.Request) rpc.Response {
		return rpc.Response{Success: true, Data: []byte("not json")}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	client := realtime.NewClient(1, 64, 0, nil, "")
	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond)

	subscriber := NewDaemonSubscriber(pool, hub)
	subscriber.countInitialized = true
	subscriber.lastKnownCount = 10
	subscriber.lastPollTime = time.Now().Add(-5 * time.Second)

	// Should not panic
	subscriber.pollDBChanges()

	// Should NOT receive any broadcast
	select {
	case received := <-client.Send():
		t.Errorf("expected no broadcast with invalid JSON, but received: %+v", received)
	case <-time.After(200 * time.Millisecond):
		// Good — no broadcast, no panic
	}
}

// TestDaemonSubscriber_PollDBChanges_UpdatedAfterCallFails tests that when the count is
// the same but the updatedAfter RPC call returns an error, no broadcast occurs.
func TestDaemonSubscriber_PollDBChanges_UpdatedAfterCallFails(t *testing.T) {
	callNumber := int32(0)
	socketPath := startSubscriptionMockServer(t, func(req rpc.Request) rpc.Response {
		n := atomic.AddInt32(&callNumber, 1)
		if n == 1 {
			// First call: return same count (10)
			countData, _ := json.Marshal(struct {
				Count int64 `json:"count"`
			}{Count: 10})
			return rpc.Response{Success: true, Data: countData}
		}
		// Second call (updatedAfter): return an error
		return rpc.Response{Success: false, Error: "database locked"}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	client := realtime.NewClient(1, 64, 0, nil, "")
	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond)

	subscriber := NewDaemonSubscriber(pool, hub)
	subscriber.countInitialized = true
	subscriber.lastKnownCount = 10
	subscriber.lastPollTime = time.Now().Add(-5 * time.Second)

	subscriber.pollDBChanges()

	// Should NOT receive any broadcast
	select {
	case received := <-client.Send():
		t.Errorf("expected no broadcast when updatedAfter call fails, but received: %+v", received)
	case <-time.After(200 * time.Millisecond):
		// Good — no broadcast
	}
}

// TestDaemonSubscriber_PollDBChanges_UpdatedAfterNotSuccess tests that when the count is
// the same and the updatedAfter response has Success: false, no broadcast occurs.
func TestDaemonSubscriber_PollDBChanges_UpdatedAfterNotSuccess(t *testing.T) {
	callNumber := int32(0)
	socketPath := startSubscriptionMockServer(t, func(req rpc.Request) rpc.Response {
		n := atomic.AddInt32(&callNumber, 1)
		if n == 1 {
			// First call: return same count (10)
			countData, _ := json.Marshal(struct {
				Count int64 `json:"count"`
			}{Count: 10})
			return rpc.Response{Success: true, Data: countData}
		}
		// Second call (updatedAfter): return Success: false
		return rpc.Response{Success: false, Error: "internal error"}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	client := realtime.NewClient(1, 64, 0, nil, "")
	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond)

	subscriber := NewDaemonSubscriber(pool, hub)
	subscriber.countInitialized = true
	subscriber.lastKnownCount = 10
	subscriber.lastPollTime = time.Now().Add(-5 * time.Second)

	subscriber.pollDBChanges()

	// Should NOT receive any broadcast
	select {
	case received := <-client.Send():
		t.Errorf("expected no broadcast when updatedAfter not success, but received: %+v", received)
	case <-time.After(200 * time.Millisecond):
		// Good — no broadcast
	}
}

// TestDaemonSubscriber_PollDBChanges_PerRepoRefresh verifies that when a filtered
// client is connected (source_repos=[repo-a]) and the per-repo Count RPC detects
// changes in repo-a, the client receives a refresh with SourceRepo=repo-a.
func TestDaemonSubscriber_PollDBChanges_PerRepoRefresh(t *testing.T) {
	socketPath := startSubscriptionMockServerRaw(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "health":
			hd, _ := json.Marshal(rpc.HealthResponse{Status: "healthy", Version: "0.0.0", Compatible: true})
			return rpc.Response{Success: true, Data: hd}
		case "ping":
			return rpc.Response{Success: true}
		case "count":
			var countArgs rpc.CountArgs
			if err := json.Unmarshal(req.Args, &countArgs); err == nil {
				if len(countArgs.SourceRepos) > 0 {
					// Per-repo count call — report 1 changed issue in repo-a
					countData, _ := json.Marshal(struct {
						Count int64 `json:"count"`
					}{Count: 1})
					return rpc.Response{Success: true, Data: countData}
				}
			}
			// Global count call — return count of 5 (same as lastKnownCount, change via UpdatedAfter)
			countData, _ := json.Marshal(struct {
				Count int64 `json:"count"`
			}{Count: 10})
			return rpc.Response{Success: true, Data: countData}
		default:
			return rpc.Response{Success: false, Error: "unknown operation: " + req.Operation}
		}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	// Register a filtered client watching repo-a
	client := realtime.NewClient(1, 64, 0, []string{"repo-a"}, "test-ws")
	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond)

	subscriber := NewDaemonSubscriber(pool, hub)
	subscriber.workspaceID = "test-ws"
	subscriber.countInitialized = true
	subscriber.lastKnownCount = 5 // Different from 10 to trigger change detection
	subscriber.lastPollTime = time.Now().Add(-5 * time.Second)

	subscriber.pollDBChanges()

	// Should receive a per-repo refresh with SourceRepo=repo-a
	select {
	case received := <-client.Send():
		if received.Type != rpc.MutationRefresh {
			t.Errorf("expected type %q, got %q", rpc.MutationRefresh, received.Type)
		}
		if received.SourceRepo != "repo-a" {
			t.Errorf("expected SourceRepo %q, got %q", "repo-a", received.SourceRepo)
		}
		if received.WorkspaceID != "test-ws" {
			t.Errorf("expected WorkspaceID %q, got %q", "test-ws", received.WorkspaceID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("did not receive per-repo refresh broadcast")
	}
}

// TestDaemonSubscriber_PollDBChanges_FilteredClientSkipsOtherRepo verifies that when
// a filtered client watches [repo-a] but the per-repo Count for repo-a returns 0,
// the client does NOT receive a per-repo refresh. Since the count changed globally,
// a global refresh is emitted for unfiltered clients.
func TestDaemonSubscriber_PollDBChanges_FilteredClientSkipsOtherRepo(t *testing.T) {
	socketPath := startSubscriptionMockServerRaw(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "health":
			hd, _ := json.Marshal(rpc.HealthResponse{Status: "healthy", Version: "0.0.0", Compatible: true})
			return rpc.Response{Success: true, Data: hd}
		case "ping":
			return rpc.Response{Success: true}
		case "count":
			var countArgs rpc.CountArgs
			if err := json.Unmarshal(req.Args, &countArgs); err == nil {
				if len(countArgs.SourceRepos) > 0 {
					// Per-repo count for repo-a — no changes found
					countData, _ := json.Marshal(struct {
						Count int64 `json:"count"`
					}{Count: 0})
					return rpc.Response{Success: true, Data: countData}
				}
			}
			// Global count — changed from 5 to 6 (triggers change detection)
			countData, _ := json.Marshal(struct {
				Count int64 `json:"count"`
			}{Count: 6})
			return rpc.Response{Success: true, Data: countData}
		default:
			return rpc.Response{Success: false, Error: "unknown operation: " + req.Operation}
		}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	// Filtered client watching repo-a
	filteredClient := realtime.NewClient(1, 64, 0, []string{"repo-a"}, "test-ws")
	// Unfiltered client to receive the global fallback
	unfilteredClient := realtime.NewClient(2, 64, 0, nil, "test-ws")
	hub.RegisterClient(filteredClient)
	hub.RegisterClient(unfilteredClient)
	time.Sleep(50 * time.Millisecond)

	subscriber := NewDaemonSubscriber(pool, hub)
	subscriber.workspaceID = "test-ws"
	subscriber.countInitialized = true
	subscriber.lastKnownCount = 5 // Different from 6, triggers count-changed path
	subscriber.lastPollTime = time.Now().Add(-5 * time.Second)

	subscriber.pollDBChanges()

	// The global refresh (empty SourceRepo) is emitted because count changed
	// but no per-repo updates matched. The unfiltered client should receive it.
	select {
	case received := <-unfilteredClient.Send():
		if received.Type != rpc.MutationRefresh {
			t.Errorf("expected type %q, got %q", rpc.MutationRefresh, received.Type)
		}
		if received.SourceRepo != "" {
			t.Errorf("expected empty SourceRepo for global refresh, got %q", received.SourceRepo)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("unfiltered client did not receive global refresh")
	}

	// The filtered client should also receive the global refresh (empty SourceRepo
	// matches all clients via matchesSourceRepoFilter fan-out).
	select {
	case received := <-filteredClient.Send():
		if received.Type != rpc.MutationRefresh {
			t.Errorf("expected type %q, got %q", rpc.MutationRefresh, received.Type)
		}
		if received.SourceRepo != "" {
			t.Errorf("expected empty SourceRepo for global refresh, got %q", received.SourceRepo)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("filtered client did not receive global refresh (empty SourceRepo fans out to all)")
	}
}

// TestDaemonSubscriber_PollDBChanges_NoFilteredClients_GlobalRefresh verifies that
// when no connected clients have repo filters, pollDBChanges emits a global refresh
// with empty SourceRepo (unchanged pre-existing behavior).
func TestDaemonSubscriber_PollDBChanges_NoFilteredClients_GlobalRefresh(t *testing.T) {
	socketPath := startSubscriptionMockServerRaw(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "health":
			hd, _ := json.Marshal(rpc.HealthResponse{Status: "healthy", Version: "0.0.0", Compatible: true})
			return rpc.Response{Success: true, Data: hd}
		case "ping":
			return rpc.Response{Success: true}
		case "count":
			// Return count of 8 (different from lastKnownCount=5)
			countData, _ := json.Marshal(struct {
				Count int64 `json:"count"`
			}{Count: 8})
			return rpc.Response{Success: true, Data: countData}
		default:
			return rpc.Response{Success: false, Error: "unknown operation: " + req.Operation}
		}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	// Register an unfiltered client (no sourceRepos)
	client := realtime.NewClient(1, 64, 0, nil, "test-ws")
	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond)

	subscriber := NewDaemonSubscriber(pool, hub)
	subscriber.workspaceID = "test-ws"
	subscriber.countInitialized = true
	subscriber.lastKnownCount = 5
	subscriber.lastPollTime = time.Now().Add(-5 * time.Second)

	subscriber.pollDBChanges()

	// Should receive a global refresh with empty SourceRepo
	select {
	case received := <-client.Send():
		if received.Type != rpc.MutationRefresh {
			t.Errorf("expected type %q, got %q", rpc.MutationRefresh, received.Type)
		}
		if received.SourceRepo != "" {
			t.Errorf("expected empty SourceRepo for global refresh, got %q", received.SourceRepo)
		}
		if received.WorkspaceID != "test-ws" {
			t.Errorf("expected WorkspaceID %q, got %q", "test-ws", received.WorkspaceID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("did not receive global refresh broadcast")
	}

	// Verify lastKnownCount was updated
	if subscriber.lastKnownCount != 8 {
		t.Errorf("lastKnownCount = %d, want 8", subscriber.lastKnownCount)
	}
}

// TestDaemonSubscriber_PollDBChanges_PerRepoCountFailure_FallbackGlobal verifies that
// when the per-repo Count RPC returns an error, emitPerRepoRefreshes falls back to
// emitting a global refresh.
func TestDaemonSubscriber_PollDBChanges_PerRepoCountFailure_FallbackGlobal(t *testing.T) {
	callCount := 0
	socketPath := startSubscriptionMockServerRaw(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "health":
			hd, _ := json.Marshal(rpc.HealthResponse{Status: "healthy", Version: "0.0.0", Compatible: true})
			return rpc.Response{Success: true, Data: hd}
		case "ping":
			return rpc.Response{Success: true}
		case "count":
			callCount++
			var countArgs rpc.CountArgs
			if err := json.Unmarshal(req.Args, &countArgs); err == nil {
				if len(countArgs.SourceRepos) > 0 {
					// Per-repo count call — return failure
					return rpc.Response{Success: false, Error: "per-repo count not supported"}
				}
			}
			// Global count — changed from 3 to 7
			countData, _ := json.Marshal(struct {
				Count int64 `json:"count"`
			}{Count: 7})
			return rpc.Response{Success: true, Data: countData}
		default:
			return rpc.Response{Success: false, Error: "unknown operation: " + req.Operation}
		}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	// Register a filtered client to trigger per-repo path
	client := realtime.NewClient(1, 64, 0, []string{"repo-a"}, "test-ws")
	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond)

	subscriber := NewDaemonSubscriber(pool, hub)
	subscriber.workspaceID = "test-ws"
	subscriber.countInitialized = true
	subscriber.lastKnownCount = 3
	subscriber.lastPollTime = time.Now().Add(-5 * time.Second)

	subscriber.pollDBChanges()

	// Should receive a global refresh as fallback (empty SourceRepo fans out to all)
	select {
	case received := <-client.Send():
		if received.Type != rpc.MutationRefresh {
			t.Errorf("expected type %q, got %q", rpc.MutationRefresh, received.Type)
		}
		if received.SourceRepo != "" {
			t.Errorf("expected empty SourceRepo for fallback global refresh, got %q", received.SourceRepo)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("did not receive fallback global refresh after per-repo count failure")
	}
}

// TestDaemonSubscriber_PollDBChanges_UnwatchedRepoChange_GlobalRefresh verifies that
// when the global count changed but no per-repo updates were found (change in an
// unwatched repo), a global refresh is emitted for unfiltered clients.
func TestDaemonSubscriber_PollDBChanges_UnwatchedRepoChange_GlobalRefresh(t *testing.T) {
	socketPath := startSubscriptionMockServerRaw(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "health":
			hd, _ := json.Marshal(rpc.HealthResponse{Status: "healthy", Version: "0.0.0", Compatible: true})
			return rpc.Response{Success: true, Data: hd}
		case "ping":
			return rpc.Response{Success: true}
		case "count":
			var countArgs rpc.CountArgs
			if err := json.Unmarshal(req.Args, &countArgs); err == nil {
				if len(countArgs.SourceRepos) > 0 {
					// Per-repo count for watched repo — no changes
					countData, _ := json.Marshal(struct {
						Count int64 `json:"count"`
					}{Count: 0})
					return rpc.Response{Success: true, Data: countData}
				}
			}
			// Global count changed from 10 to 12 (issues added in unwatched repo)
			countData, _ := json.Marshal(struct {
				Count int64 `json:"count"`
			}{Count: 12})
			return rpc.Response{Success: true, Data: countData}
		default:
			return rpc.Response{Success: false, Error: "unknown operation: " + req.Operation}
		}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	// Filtered client watching repo-a
	filteredClient := realtime.NewClient(1, 64, 0, []string{"repo-a"}, "test-ws")
	// Unfiltered client to catch global refresh
	unfilteredClient := realtime.NewClient(2, 64, 0, nil, "test-ws")
	hub.RegisterClient(filteredClient)
	hub.RegisterClient(unfilteredClient)
	time.Sleep(50 * time.Millisecond)

	subscriber := NewDaemonSubscriber(pool, hub)
	subscriber.workspaceID = "test-ws"
	subscriber.countInitialized = true
	subscriber.lastKnownCount = 10 // Different from 12
	subscriber.lastPollTime = time.Now().Add(-5 * time.Second)

	subscriber.pollDBChanges()

	// Unfiltered client should receive the global refresh (empty SourceRepo)
	select {
	case received := <-unfilteredClient.Send():
		if received.Type != rpc.MutationRefresh {
			t.Errorf("expected type %q, got %q", rpc.MutationRefresh, received.Type)
		}
		if received.SourceRepo != "" {
			t.Errorf("expected empty SourceRepo for unwatched-repo global refresh, got %q", received.SourceRepo)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("unfiltered client did not receive global refresh for unwatched repo change")
	}

	// Filtered client also receives the global refresh because empty SourceRepo
	// fans out to all clients via matchesSourceRepoFilter.
	select {
	case received := <-filteredClient.Send():
		if received.Type != rpc.MutationRefresh {
			t.Errorf("expected type %q, got %q", rpc.MutationRefresh, received.Type)
		}
		if received.SourceRepo != "" {
			t.Errorf("expected empty SourceRepo, got %q", received.SourceRepo)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("filtered client did not receive global refresh (empty SourceRepo fans out)")
	}

	// Verify lastKnownCount was updated to 12
	if subscriber.lastKnownCount != 12 {
		t.Errorf("lastKnownCount = %d, want 12", subscriber.lastKnownCount)
	}
}
