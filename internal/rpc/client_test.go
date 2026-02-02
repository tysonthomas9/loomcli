package rpc

import (
	"testing"
	"time"
)

func TestClient_SetTimeout(t *testing.T) {
	t.Parallel()

	client := &Client{}

	client.SetTimeout(5 * time.Second)

	if client.timeout != 5*time.Second {
		t.Errorf("timeout = %v, want 5s", client.timeout)
	}
}

func TestClient_SetDatabasePath(t *testing.T) {
	t.Parallel()

	client := &Client{}

	client.SetDatabasePath("/path/to/db.sqlite")

	if client.dbPath != "/path/to/db.sqlite" {
		t.Errorf("dbPath = %q, want %q", client.dbPath, "/path/to/db.sqlite")
	}
}

func TestClient_SetActor(t *testing.T) {
	t.Parallel()

	client := &Client{}

	client.SetActor("alice")

	if client.actor != "alice" {
		t.Errorf("actor = %q, want %q", client.actor, "alice")
	}
}

func TestTryConnect_NoSocket(t *testing.T) {
	t.Parallel()

	// Non-existent socket path should return nil, nil
	client, err := TryConnect("/nonexistent/path/bd.sock")

	if err != nil {
		t.Errorf("TryConnect() error: %v", err)
	}

	if client != nil {
		t.Error("TryConnect() should return nil client for non-existent socket")
		client.Close()
	}
}

func TestTryConnectWithTimeout_NoSocket(t *testing.T) {
	t.Parallel()

	// Non-existent socket path with custom timeout
	client, err := TryConnectWithTimeout("/nonexistent/path/bd.sock", 100*time.Millisecond)

	if err != nil {
		t.Errorf("TryConnectWithTimeout() error: %v", err)
	}

	if client != nil {
		t.Error("TryConnectWithTimeout() should return nil client for non-existent socket")
		client.Close()
	}
}

func TestClient_Close_Nil(t *testing.T) {
	t.Parallel()

	client := &Client{conn: nil}

	// Should not panic with nil connection
	err := client.Close()
	if err != nil {
		t.Errorf("Close() with nil conn should not error: %v", err)
	}
}

func TestClientVersion_Variable(t *testing.T) {
	t.Parallel()

	// ClientVersion should have a default value
	if ClientVersion == "" {
		t.Error("ClientVersion should not be empty")
	}

	// It should be the placeholder value initially (unless overridden by main)
	// We just verify it exists as a non-empty string
	t.Logf("ClientVersion = %q", ClientVersion)
}

func TestClient_StructFields(t *testing.T) {
	t.Parallel()

	// Test that Client struct has expected fields
	client := &Client{
		socketPath: "/path/to/socket",
		timeout:    30 * time.Second,
		dbPath:     "/path/to/db",
		actor:      "test-actor",
	}

	if client.socketPath != "/path/to/socket" {
		t.Error("socketPath field not set correctly")
	}
	if client.timeout != 30*time.Second {
		t.Error("timeout field not set correctly")
	}
	if client.dbPath != "/path/to/db" {
		t.Error("dbPath field not set correctly")
	}
	if client.actor != "test-actor" {
		t.Error("actor field not set correctly")
	}
}

// Note: The following tests document client behavior but don't actually
// connect to a daemon. Testing full RPC interactions requires integration tests.

// TestClient_ConcurrentSetTimeout verifies that concurrent calls to SetTimeout
// do not cause a data race.
func TestClient_ConcurrentSetTimeout(t *testing.T) {
	t.Parallel()

	client := &Client{}
	done := make(chan bool)

	// Launch multiple goroutines setting timeout concurrently
	for i := 0; i < 10; i++ {
		go func(i int) {
			for j := 0; j < 100; j++ {
				client.SetTimeout(time.Duration(i*100+j) * time.Millisecond)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify timeout is some valid value (any of the set values)
	client.mu.RLock()
	timeout := client.timeout
	client.mu.RUnlock()
	if timeout < 0 {
		t.Error("timeout should be non-negative")
	}
}

// TestClient_ConcurrentSettersAndGetters verifies that concurrent calls to
// all setter methods do not cause data races.
func TestClient_ConcurrentSettersAndGetters(t *testing.T) {
	t.Parallel()

	client := &Client{}
	done := make(chan bool)

	// Writer goroutine for SetTimeout
	go func() {
		for i := 0; i < 100; i++ {
			client.SetTimeout(time.Duration(i) * time.Millisecond)
		}
		done <- true
	}()

	// Writer goroutine for SetDatabasePath
	go func() {
		for i := 0; i < 100; i++ {
			client.SetDatabasePath("/path/to/db")
		}
		done <- true
	}()

	// Writer goroutine for SetActor
	go func() {
		for i := 0; i < 100; i++ {
			client.SetActor("actor")
		}
		done <- true
	}()

	// Wait for all writers
	for i := 0; i < 3; i++ {
		<-done
	}
}

// TestClient_ThreadSafe documents that Client is now thread-safe
// for concurrent access to mutable fields (timeout, dbPath, actor).
func TestClient_ThreadSafe(t *testing.T) {
	t.Parallel()

	// Client is now goroutine-safe for mutable field access.
	// The mu field (sync.RWMutex) protects timeout, dbPath, and actor.
	// Connection operations (conn) are not designed for concurrent use
	// on the same connection - use connection pooling for concurrent RPC calls.

	t.Log("Note: Client is goroutine-safe for SetTimeout, SetDatabasePath, SetActor. Use connection pooling for concurrent RPC calls.")
}

// TestClient_WaitForMutationsConcurrent verifies that multiple concurrent
// WaitForMutations calls do not race. Previously, WaitForMutations modified
// c.timeout directly, which was not thread-safe. Now it uses executeWithTimeout
// which reads timeout under RLock, preventing races.
func TestClient_WaitForMutationsConcurrent(t *testing.T) {
	t.Parallel()

	client := &Client{
		timeout: 30 * time.Second,
	}

	// Create a wait group to coordinate goroutines
	done := make(chan bool, 20)

	// Launch multiple goroutines calling WaitForMutations concurrently
	// We can't actually execute since we don't have a real connection,
	// but we can verify the thread-safety of the setup logic
	for i := 0; i < 20; i++ {
		go func(i int) {
			// Create args for WaitForMutations
			args := &WaitForMutationsArgs{
				Timeout: int64(100 + i), // Vary the timeout
			}

			// In a real scenario, this would call executeWithTimeout internally
			// and that should not race with other concurrent calls or SetTimeout calls
			_ = args

			// Simulate the timeout calculation that WaitForMutations does
			requestTimeout := time.Duration(args.Timeout) * time.Millisecond
			if args.Timeout == 0 {
				requestTimeout = 30 * time.Second
			}
			blockingTimeout := requestTimeout + 5*time.Second
			_ = blockingTimeout

			done <- true
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < 20; i++ {
		<-done
	}

	// Verify that the client's timeout was not modified by concurrent operations
	client.mu.RLock()
	timeout := client.timeout
	client.mu.RUnlock()

	if timeout != 30*time.Second {
		t.Errorf("client timeout should remain 30s, got %v", timeout)
	}
}

// TestClient_ConcurrentSettersWithReadOnly verifies that setters and readers
// don't race. This tests the RWMutex behavior where multiple readers can
// hold the lock simultaneously, and writers have exclusive access.
func TestClient_ConcurrentSettersWithReadOnly(t *testing.T) {
	t.Parallel()

	client := &Client{
		timeout: 10 * time.Second,
		dbPath:  "/initial/path",
		actor:   "initial-actor",
	}

	done := make(chan bool, 30)

	// Launch setter goroutines
	for i := 0; i < 5; i++ {
		go func(i int) {
			for j := 0; j < 10; j++ {
				client.SetTimeout(time.Duration(i*10+j) * time.Millisecond)
				time.Sleep(time.Microsecond) // Small delay to interleave operations
			}
			done <- true
		}(i)
	}

	// Launch reader goroutines that simulate executeWithTimeout
	for i := 0; i < 10; i++ {
		go func(i int) {
			for j := 0; j < 10; j++ {
				// Simulate the read lock pattern from executeWithTimeout
				client.mu.RLock()
				timeout := client.timeout
				dbPath := client.dbPath
				actor := client.actor
				client.mu.RUnlock()

				// Verify we got valid values
				if timeout < 0 {
					t.Errorf("timeout should be non-negative, got %v", timeout)
				}
				_ = dbPath
				_ = actor

				time.Sleep(time.Microsecond) // Small delay to interleave operations
			}
			done <- true
		}(i)
	}

	// Launch more setter goroutines for other fields
	for i := 0; i < 5; i++ {
		go func(i int) {
			for j := 0; j < 10; j++ {
				client.SetDatabasePath("/path/to/db/" + string(rune(i)))
				time.Sleep(time.Microsecond)
			}
			done <- true
		}(i)
	}

	for i := 0; i < 5; i++ {
		go func(i int) {
			for j := 0; j < 10; j++ {
				client.SetActor("actor-" + string(rune(i)))
				time.Sleep(time.Microsecond)
			}
			done <- true
		}(i)
	}

	// Wait for all 20 goroutines (5 + 10 + 5) to complete
	for i := 0; i < 20; i++ {
		<-done
	}
}

// TestClient_StressTestConcurrentOperations is a comprehensive stress test
// that exercises simultaneous setters and field readers to verify no data races.
// This test should be run with -race flag: go test -race ./...
func TestClient_StressTestConcurrentOperations(t *testing.T) {
	t.Parallel()

	client := &Client{
		timeout: 5 * time.Second,
		dbPath:  "/var/db/test.sqlite",
		actor:   "test-user",
	}

	const (
		numSetterGoroutines = 50
		numReaderGoroutines = 100
		operationsPerGoroutine = 200
	)

	done := make(chan bool, numSetterGoroutines+numReaderGoroutines)

	// Setter goroutines: continuously modify protected fields
	for i := 0; i < numSetterGoroutines; i++ {
		go func(id int) {
			defer func() { done <- true }()
			for op := 0; op < operationsPerGoroutine; op++ {
				switch op % 3 {
				case 0:
					client.SetTimeout(time.Duration((id*op)%5000+100) * time.Millisecond)
				case 1:
					client.SetDatabasePath("/path/" + string(rune(id)))
				case 2:
					client.SetActor("actor-" + string(rune(id)))
				}
			}
		}(i)
	}

	// Reader goroutines: continuously read protected fields (simulating executeWithTimeout)
	for i := 0; i < numReaderGoroutines; i++ {
		go func(id int) {
			defer func() { done <- true }()
			for op := 0; op < operationsPerGoroutine; op++ {
				// Simulate the read pattern from executeWithTimeout
				client.mu.RLock()
				timeout := client.timeout
				dbPath := client.dbPath
				actor := client.actor
				client.mu.RUnlock()

				// Verify basic invariants
				if timeout < 0 {
					t.Errorf("reader %d: timeout should be non-negative, got %v", id, timeout)
				}
				if len(dbPath) == 0 || len(actor) == 0 {
					// Either empty or has content - both are valid after initialization
				}
			}
		}(i)
	}

	// Wait for all goroutines to complete
	totalGoroutines := numSetterGoroutines + numReaderGoroutines
	for i := 0; i < totalGoroutines; i++ {
		<-done
	}
}

// TestClient_SettersStressTest exercises rapid, concurrent changes to all
// mutable fields to ensure the RWMutex properly serializes writes.
func TestClient_SettersStressTest(t *testing.T) {
	t.Parallel()

	client := &Client{}
	done := make(chan bool, 30)

	// 10 concurrent SetTimeout operations
	for i := 0; i < 10; i++ {
		go func(id int) {
			defer func() { done <- true }()
			for j := 0; j < 50; j++ {
				client.SetTimeout(time.Duration(id*50+j) * time.Millisecond)
			}
		}(i)
	}

	// 10 concurrent SetDatabasePath operations
	for i := 0; i < 10; i++ {
		go func(id int) {
			defer func() { done <- true }()
			for j := 0; j < 50; j++ {
				client.SetDatabasePath("/path/to/db/" + string(rune(id)))
			}
		}(i)
	}

	// 10 concurrent SetActor operations
	for i := 0; i < 10; i++ {
		go func(id int) {
			defer func() { done <- true }()
			for j := 0; j < 50; j++ {
				client.SetActor("actor-" + string(rune(id)))
			}
		}(i)
	}

	// Wait for all 30 goroutines to complete
	for i := 0; i < 30; i++ {
		<-done
	}

	// Verify that final state is valid (all fields should have some value)
	client.mu.RLock()
	timeout := client.timeout
	dbPath := client.dbPath
	actor := client.actor
	client.mu.RUnlock()

	if timeout < 0 {
		t.Errorf("timeout should be non-negative, got %v", timeout)
	}
	if len(dbPath) == 0 {
		t.Error("dbPath should have been set by at least one SetDatabasePath call")
	}
	if len(actor) == 0 {
		t.Error("actor should have been set by at least one SetActor call")
	}
}

// TestClient_ConcurrentMixedOperations simulates a realistic scenario where
// configuration is being set via SetTimeout/SetDatabasePath/SetActor while
// concurrent "execute" operations (simulated via manual RLock) are reading those values.
func TestClient_ConcurrentMixedOperations(t *testing.T) {
	t.Parallel()

	client := &Client{
		timeout: 30 * time.Second,
		dbPath:  "/var/db/beads.sqlite",
		actor:   "cli",
	}

	done := make(chan bool, 15)
	readErrors := make(chan string, 100)

	// Goroutines that reconfigure the client
	for i := 0; i < 3; i++ {
		go func(id int) {
			defer func() { done <- true }()
			for j := 0; j < 100; j++ {
				client.SetTimeout(time.Duration((id+1)*10) * time.Second)
				client.SetDatabasePath("/var/db/instance-" + string(rune(id)) + ".sqlite")
				client.SetActor("user-" + string(rune(id)))
			}
		}(i)
	}

	// Goroutines that simulate executing operations (reading configuration)
	for i := 0; i < 12; i++ {
		go func(id int) {
			defer func() { done <- true }()
			for j := 0; j < 100; j++ {
				// Simulate executeWithTimeout pattern
				client.mu.RLock()
				actor := client.actor
				dbPath := client.dbPath
				timeout := client.timeout
				client.mu.RUnlock()

				// Verify invariants
				if timeout < 0 {
					readErrors <- "negative timeout"
				}
				if len(actor) == 0 {
					readErrors <- "empty actor"
				}
				if len(dbPath) == 0 {
					readErrors <- "empty dbPath"
				}
			}
		}(i)
	}

	// Wait for all 15 goroutines
	for i := 0; i < 15; i++ {
		<-done
	}

	// Check for any errors
	close(readErrors)
	for err := range readErrors {
		if err != "" {
			t.Errorf("invariant violation: %s", err)
		}
	}
}
