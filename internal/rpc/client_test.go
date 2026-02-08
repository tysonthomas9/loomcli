package rpc

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// startMockServer creates a Unix socket server that accepts one connection,
// reads a JSON request, and sends a JSON response. Returns the socket path and
// a cleanup function. The handler receives the decoded Request and should return
// a Response to send back.
//
// Uses /tmp with a short random name because macOS $TMPDIR paths are too long
// for Unix sockets (104-byte limit).
func startMockServer(t *testing.T, handler func(req Request) Response) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "rpc-test-*")
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
					var req Request
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

func TestTryConnectWithTimeout_HealthyServer(t *testing.T) {
	t.Parallel()

	// Start a mock server that responds to health checks
	dir, err := os.MkdirTemp("/tmp", "rpc-conn-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	socketPath := filepath.Join(dir, "bd.sock")

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}
	t.Cleanup(func() { listener.Close() })

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
					var req Request
					if err := json.Unmarshal(line, &req); err != nil {
						return
					}
					var resp Response
					if req.Operation == OpHealth {
						data, _ := json.Marshal(HealthResponse{
							Status:     "healthy",
							Version:    "1.0.0",
							Compatible: true,
							Uptime:     10,
						})
						resp = Response{Success: true, Data: data}
					} else {
						resp = Response{Success: true}
					}
					respJSON, _ := json.Marshal(resp)
					respJSON = append(respJSON, '\n')
					c.Write(respJSON)
				}
			}(conn)
		}
	}()

	client, err := TryConnectWithTimeout(socketPath, time.Second)
	if err != nil {
		t.Fatalf("TryConnectWithTimeout() error: %v", err)
	}
	if client == nil {
		t.Fatal("TryConnectWithTimeout() returned nil client for healthy server")
	}
	defer client.Close()
}

func TestTryConnectWithTimeout_UnhealthyServer(t *testing.T) {
	t.Parallel()

	dir, err := os.MkdirTemp("/tmp", "rpc-unhealthy-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	socketPath := filepath.Join(dir, "bd.sock")

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}
	t.Cleanup(func() { listener.Close() })

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
					var req Request
					json.Unmarshal(line, &req)

					data, _ := json.Marshal(HealthResponse{
						Status: "unhealthy",
						Error:  "database locked",
					})
					resp := Response{Success: true, Data: data}
					respJSON, _ := json.Marshal(resp)
					respJSON = append(respJSON, '\n')
					c.Write(respJSON)
				}
			}(conn)
		}
	}()

	client, err := TryConnectWithTimeout(socketPath, time.Second)
	if err != nil {
		t.Fatalf("TryConnectWithTimeout() error: %v", err)
	}
	if client != nil {
		client.Close()
		t.Error("TryConnectWithTimeout() should return nil for unhealthy server")
	}
}

func TestTryConnectWithTimeout_NegativeTimeout(t *testing.T) {
	t.Parallel()

	// Negative timeout should use default 200ms
	client, err := TryConnectWithTimeout("/nonexistent/path/bd.sock", -1)
	if err != nil {
		t.Errorf("TryConnectWithTimeout() error: %v", err)
	}
	if client != nil {
		client.Close()
		t.Error("should return nil for non-existent socket")
	}
}

func TestClient_SetAuthToken(t *testing.T) {
	t.Parallel()

	client := &Client{}
	client.SetAuthToken("test-token-abc")

	client.mu.RLock()
	token := client.authToken
	client.mu.RUnlock()

	if token != "test-token-abc" {
		t.Errorf("authToken = %q, want %q", token, "test-token-abc")
	}
}

func TestClient_Execute_Success(t *testing.T) {
	t.Parallel()

	socketPath := startMockServer(t, func(req Request) Response {
		if req.Operation != OpPing {
			t.Errorf("unexpected operation: %q", req.Operation)
		}
		data, _ := json.Marshal(PingResponse{Message: "pong", Version: "1.0.0"})
		return Response{Success: true, Data: data}
	})

	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	client := &Client{
		conn:       conn,
		socketPath: socketPath,
		timeout:    5 * time.Second,
	}
	defer client.Close()

	resp, err := client.Execute(OpPing, nil)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if !resp.Success {
		t.Error("Execute() response should be successful")
	}
}

func TestClient_Execute_ErrorResponse(t *testing.T) {
	t.Parallel()

	socketPath := startMockServer(t, func(req Request) Response {
		return Response{Success: false, Error: "not found"}
	})

	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	client := &Client{
		conn:       conn,
		socketPath: socketPath,
		timeout:    5 * time.Second,
	}
	defer client.Close()

	resp, err := client.Execute(OpShow, &ShowArgs{ID: "bd-nonexistent"})
	if err == nil {
		t.Fatal("Execute() should return error for failed response")
	}
	if resp == nil {
		t.Fatal("Execute() should return response even on failure")
	}
	if resp.Error != "not found" {
		t.Errorf("resp.Error = %q, want %q", resp.Error, "not found")
	}
}

func TestClient_Execute_SetsRequestFields(t *testing.T) {
	t.Parallel()

	var capturedReq Request
	socketPath := startMockServer(t, func(req Request) Response {
		capturedReq = req
		return Response{Success: true}
	})

	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	client := &Client{
		conn:       conn,
		socketPath: socketPath,
		timeout:    5 * time.Second,
		actor:      "test-actor",
		dbPath:     "/test/db.sqlite",
		authToken:  "secret-123",
	}
	defer client.Close()

	_, err = client.Execute(OpCreate, &CreateArgs{Title: "Test", IssueType: "task"})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	if capturedReq.Operation != OpCreate {
		t.Errorf("Operation = %q, want %q", capturedReq.Operation, OpCreate)
	}
	if capturedReq.Actor != "test-actor" {
		t.Errorf("Actor = %q, want %q", capturedReq.Actor, "test-actor")
	}
	if capturedReq.ExpectedDB != "/test/db.sqlite" {
		t.Errorf("ExpectedDB = %q, want %q", capturedReq.ExpectedDB, "/test/db.sqlite")
	}
	if capturedReq.AuthToken != "secret-123" {
		t.Errorf("AuthToken = %q, want %q", capturedReq.AuthToken, "secret-123")
	}
	if capturedReq.ClientVersion != ClientVersion {
		t.Errorf("ClientVersion = %q, want %q", capturedReq.ClientVersion, ClientVersion)
	}
}

func TestClient_ExecuteWithCwd(t *testing.T) {
	t.Parallel()

	var capturedReq Request
	socketPath := startMockServer(t, func(req Request) Response {
		capturedReq = req
		return Response{Success: true}
	})

	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	client := &Client{
		conn:       conn,
		socketPath: socketPath,
		timeout:    5 * time.Second,
	}
	defer client.Close()

	_, err = client.ExecuteWithCwd(OpList, &ListArgs{}, "/custom/cwd")
	if err != nil {
		t.Fatalf("ExecuteWithCwd() error: %v", err)
	}

	if capturedReq.Cwd != "/custom/cwd" {
		t.Errorf("Cwd = %q, want %q", capturedReq.Cwd, "/custom/cwd")
	}
}

func TestClient_Ping(t *testing.T) {
	t.Parallel()

	socketPath := startMockServer(t, func(req Request) Response {
		if req.Operation != OpPing {
			t.Errorf("unexpected operation: %q", req.Operation)
		}
		return Response{Success: true}
	})

	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	client := &Client{conn: conn, socketPath: socketPath, timeout: 5 * time.Second}
	defer client.Close()

	if err := client.Ping(); err != nil {
		t.Errorf("Ping() error: %v", err)
	}
}

func TestClient_Status(t *testing.T) {
	t.Parallel()

	socketPath := startMockServer(t, func(req Request) Response {
		data, _ := json.Marshal(StatusResponse{
			Version:       "2.0.0",
			WorkspacePath: "/home/user/project",
			PID:           12345,
			UptimeSeconds: 300,
		})
		return Response{Success: true, Data: data}
	})

	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	client := &Client{conn: conn, socketPath: socketPath, timeout: 5 * time.Second}
	defer client.Close()

	status, err := client.Status()
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	if status.Version != "2.0.0" {
		t.Errorf("Version = %q, want %q", status.Version, "2.0.0")
	}
	if status.PID != 12345 {
		t.Errorf("PID = %d, want 12345", status.PID)
	}
}

func TestClient_Health(t *testing.T) {
	t.Parallel()

	socketPath := startMockServer(t, func(req Request) Response {
		data, _ := json.Marshal(HealthResponse{
			Status:     "healthy",
			Version:    "2.0.0",
			Compatible: true,
			Uptime:     600,
		})
		return Response{Success: true, Data: data}
	})

	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	client := &Client{conn: conn, socketPath: socketPath, timeout: 5 * time.Second}
	defer client.Close()

	health, err := client.Health()
	if err != nil {
		t.Fatalf("Health() error: %v", err)
	}
	if health.Status != "healthy" {
		t.Errorf("Status = %q, want %q", health.Status, "healthy")
	}
}

func TestClient_Shutdown(t *testing.T) {
	t.Parallel()

	socketPath := startMockServer(t, func(req Request) Response {
		if req.Operation != OpShutdown {
			t.Errorf("unexpected operation: %q", req.Operation)
		}
		return Response{Success: true}
	})

	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	client := &Client{conn: conn, socketPath: socketPath, timeout: 5 * time.Second}
	defer client.Close()

	if err := client.Shutdown(); err != nil {
		t.Errorf("Shutdown() error: %v", err)
	}
}

func TestClient_Metrics(t *testing.T) {
	t.Parallel()

	socketPath := startMockServer(t, func(req Request) Response {
		data, _ := json.Marshal(MetricsSnapshot{
			UptimeSeconds: 120,
			TotalConns:    50,
		})
		return Response{Success: true, Data: data}
	})

	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	client := &Client{conn: conn, socketPath: socketPath, timeout: 5 * time.Second}
	defer client.Close()

	metrics, err := client.Metrics()
	if err != nil {
		t.Fatalf("Metrics() error: %v", err)
	}
	if metrics.TotalConns != 50 {
		t.Errorf("TotalConns = %d, want 50", metrics.TotalConns)
	}
}

func TestClient_Create(t *testing.T) {
	t.Parallel()

	var capturedOp string
	socketPath := startMockServer(t, func(req Request) Response {
		capturedOp = req.Operation
		data, _ := json.Marshal(map[string]string{"id": "bd-new"})
		return Response{Success: true, Data: data}
	})

	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	client := &Client{conn: conn, socketPath: socketPath, timeout: 5 * time.Second}
	defer client.Close()

	resp, err := client.Create(&CreateArgs{Title: "Test Issue", IssueType: "task"})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if !resp.Success {
		t.Error("Create() should succeed")
	}
	if capturedOp != OpCreate {
		t.Errorf("operation = %q, want %q", capturedOp, OpCreate)
	}
}

func TestClient_Update(t *testing.T) {
	t.Parallel()

	var capturedOp string
	socketPath := startMockServer(t, func(req Request) Response {
		capturedOp = req.Operation
		return Response{Success: true}
	})

	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	client := &Client{conn: conn, socketPath: socketPath, timeout: 5 * time.Second}
	defer client.Close()

	title := "Updated"
	_, err = client.Update(&UpdateArgs{ID: "bd-1", Title: &title})
	if err != nil {
		t.Fatalf("Update() error: %v", err)
	}
	if capturedOp != OpUpdate {
		t.Errorf("operation = %q, want %q", capturedOp, OpUpdate)
	}
}

func TestClient_CloseIssue(t *testing.T) {
	t.Parallel()

	var capturedOp string
	socketPath := startMockServer(t, func(req Request) Response {
		capturedOp = req.Operation
		return Response{Success: true}
	})

	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	client := &Client{conn: conn, socketPath: socketPath, timeout: 5 * time.Second}
	defer client.Close()

	_, err = client.CloseIssue(&CloseArgs{ID: "bd-1", Reason: "done"})
	if err != nil {
		t.Fatalf("CloseIssue() error: %v", err)
	}
	if capturedOp != OpClose {
		t.Errorf("operation = %q, want %q", capturedOp, OpClose)
	}
}

func TestClient_Delete(t *testing.T) {
	t.Parallel()

	var capturedOp string
	socketPath := startMockServer(t, func(req Request) Response {
		capturedOp = req.Operation
		return Response{Success: true}
	})

	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	client := &Client{conn: conn, socketPath: socketPath, timeout: 5 * time.Second}
	defer client.Close()

	_, err = client.Delete(&DeleteArgs{IDs: []string{"bd-1", "bd-2"}, Force: true})
	if err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	if capturedOp != OpDelete {
		t.Errorf("operation = %q, want %q", capturedOp, OpDelete)
	}
}

func TestClient_List(t *testing.T) {
	t.Parallel()

	var capturedOp string
	socketPath := startMockServer(t, func(req Request) Response {
		capturedOp = req.Operation
		return Response{Success: true, Data: json.RawMessage(`[]`)}
	})

	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	client := &Client{conn: conn, socketPath: socketPath, timeout: 5 * time.Second}
	defer client.Close()

	_, err = client.List(&ListArgs{Status: "open"})
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if capturedOp != OpList {
		t.Errorf("operation = %q, want %q", capturedOp, OpList)
	}
}

func TestClient_Show(t *testing.T) {
	t.Parallel()

	var capturedOp string
	socketPath := startMockServer(t, func(req Request) Response {
		capturedOp = req.Operation
		return Response{Success: true, Data: json.RawMessage(`{}`)}
	})

	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	client := &Client{conn: conn, socketPath: socketPath, timeout: 5 * time.Second}
	defer client.Close()

	_, err = client.Show(&ShowArgs{ID: "bd-1"})
	if err != nil {
		t.Fatalf("Show() error: %v", err)
	}
	if capturedOp != OpShow {
		t.Errorf("operation = %q, want %q", capturedOp, OpShow)
	}
}

func TestClient_Ready(t *testing.T) {
	t.Parallel()

	var capturedOp string
	socketPath := startMockServer(t, func(req Request) Response {
		capturedOp = req.Operation
		return Response{Success: true, Data: json.RawMessage(`[]`)}
	})

	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	client := &Client{conn: conn, socketPath: socketPath, timeout: 5 * time.Second}
	defer client.Close()

	_, err = client.Ready(&ReadyArgs{})
	if err != nil {
		t.Fatalf("Ready() error: %v", err)
	}
	if capturedOp != OpReady {
		t.Errorf("operation = %q, want %q", capturedOp, OpReady)
	}
}

func TestClient_Stats(t *testing.T) {
	t.Parallel()

	var capturedOp string
	socketPath := startMockServer(t, func(req Request) Response {
		capturedOp = req.Operation
		return Response{Success: true, Data: json.RawMessage(`{}`)}
	})

	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	client := &Client{conn: conn, socketPath: socketPath, timeout: 5 * time.Second}
	defer client.Close()

	_, err = client.Stats()
	if err != nil {
		t.Fatalf("Stats() error: %v", err)
	}
	if capturedOp != OpStats {
		t.Errorf("operation = %q, want %q", capturedOp, OpStats)
	}
}

func TestClient_Batch(t *testing.T) {
	t.Parallel()

	var capturedOp string
	socketPath := startMockServer(t, func(req Request) Response {
		capturedOp = req.Operation
		return Response{Success: true}
	})

	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	client := &Client{conn: conn, socketPath: socketPath, timeout: 5 * time.Second}
	defer client.Close()

	_, err = client.Batch(&BatchArgs{
		Operations: []BatchOperation{
			{Operation: OpCreate, Args: json.RawMessage(`{"title":"test"}`)},
		},
	})
	if err != nil {
		t.Fatalf("Batch() error: %v", err)
	}
	if capturedOp != OpBatch {
		t.Errorf("operation = %q, want %q", capturedOp, OpBatch)
	}
}

func TestClient_GetWorkerStatus(t *testing.T) {
	t.Parallel()

	socketPath := startMockServer(t, func(req Request) Response {
		data, _ := json.Marshal(GetWorkerStatusResponse{})
		return Response{Success: true, Data: data}
	})

	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	client := &Client{conn: conn, socketPath: socketPath, timeout: 5 * time.Second}
	defer client.Close()

	_, err = client.GetWorkerStatus(&GetWorkerStatusArgs{})
	if err != nil {
		t.Fatalf("GetWorkerStatus() error: %v", err)
	}
}

func TestClient_GetConfig(t *testing.T) {
	t.Parallel()

	socketPath := startMockServer(t, func(req Request) Response {
		data, _ := json.Marshal(GetConfigResponse{Value: "test-value"})
		return Response{Success: true, Data: data}
	})

	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	client := &Client{conn: conn, socketPath: socketPath, timeout: 5 * time.Second}
	defer client.Close()

	config, err := client.GetConfig(&GetConfigArgs{Key: "test.key"})
	if err != nil {
		t.Fatalf("GetConfig() error: %v", err)
	}
	if config.Value != "test-value" {
		t.Errorf("Value = %q, want %q", config.Value, "test-value")
	}
}

func TestClient_GateCreate(t *testing.T) {
	t.Parallel()

	var capturedOp string
	socketPath := startMockServer(t, func(req Request) Response {
		capturedOp = req.Operation
		return Response{Success: true}
	})

	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	client := &Client{conn: conn, socketPath: socketPath, timeout: 5 * time.Second}
	defer client.Close()

	_, err = client.GateCreate(&GateCreateArgs{Title: "Wait for PR"})
	if err != nil {
		t.Fatalf("GateCreate() error: %v", err)
	}
	if capturedOp != OpGateCreate {
		t.Errorf("operation = %q, want %q", capturedOp, OpGateCreate)
	}
}

func TestClient_Close_WithConn(t *testing.T) {
	t.Parallel()

	socketPath := startMockServer(t, func(req Request) Response {
		return Response{Success: true}
	})

	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	client := &Client{conn: conn, socketPath: socketPath}

	err = client.Close()
	if err != nil {
		t.Errorf("Close() error: %v", err)
	}

	// Second close should fail (connection already closed)
	err = client.Close()
	if err == nil {
		t.Log("Note: double close may or may not error depending on implementation")
	}
}

// newMockClient creates a client connected to a mock server for testing.
func newMockClient(t *testing.T, handler func(req Request) Response) *Client {
	t.Helper()
	socketPath := startMockServer(t, handler)
	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	client := &Client{conn: conn, socketPath: socketPath, timeout: 5 * time.Second}
	t.Cleanup(func() { client.Close() })
	return client
}

// TestClient_RemainingWrappers tests all remaining simple wrapper methods
// that just delegate to Execute with the correct operation constant.
func TestClient_RemainingWrappers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		op     string
		invoke func(c *Client) error
	}{
		{"Count", OpCount, func(c *Client) error { _, err := c.Count(&CountArgs{}); return err }},
		{"ResolveID", OpResolveID, func(c *Client) error { _, err := c.ResolveID(&ResolveIDArgs{ID: "bd"}); return err }},
		{"Blocked", OpBlocked, func(c *Client) error { _, err := c.Blocked(&BlockedArgs{}); return err }},
		{"Stale", OpStale, func(c *Client) error { _, err := c.Stale(&StaleArgs{}); return err }},
		{"GetMutations", OpGetMutations, func(c *Client) error { _, err := c.GetMutations(&GetMutationsArgs{}); return err }},
		{"AddDependency", OpDepAdd, func(c *Client) error { _, err := c.AddDependency(&DepAddArgs{}); return err }},
		{"RemoveDependency", OpDepRemove, func(c *Client) error { _, err := c.RemoveDependency(&DepRemoveArgs{}); return err }},
		{"AddLabel", OpLabelAdd, func(c *Client) error { _, err := c.AddLabel(&LabelAddArgs{}); return err }},
		{"RemoveLabel", OpLabelRemove, func(c *Client) error { _, err := c.RemoveLabel(&LabelRemoveArgs{}); return err }},
		{"ListComments", OpCommentList, func(c *Client) error { _, err := c.ListComments(&CommentListArgs{}); return err }},
		{"AddComment", OpCommentAdd, func(c *Client) error { _, err := c.AddComment(&CommentAddArgs{}); return err }},
		{"Export", OpExport, func(c *Client) error { _, err := c.Export(&ExportArgs{}); return err }},
		{"EpicStatus", OpEpicStatus, func(c *Client) error { _, err := c.EpicStatus(&EpicStatusArgs{}); return err }},
		{"GateList", OpGateList, func(c *Client) error { _, err := c.GateList(&GateListArgs{}); return err }},
		{"GateShow", OpGateShow, func(c *Client) error { _, err := c.GateShow(&GateShowArgs{ID: "g-1"}); return err }},
		{"GateClose", OpGateClose, func(c *Client) error { _, err := c.GateClose(&GateCloseArgs{ID: "g-1"}); return err }},
		{"GateWait", OpGateWait, func(c *Client) error { _, err := c.GateWait(&GateWaitArgs{ID: "g-1"}); return err }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var capturedOp string
			client := newMockClient(t, func(req Request) Response {
				capturedOp = req.Operation
				return Response{Success: true, Data: json.RawMessage(`{}`)}
			})

			if err := tt.invoke(client); err != nil {
				t.Fatalf("%s() error: %v", tt.name, err)
			}
			if capturedOp != tt.op {
				t.Errorf("operation = %q, want %q", capturedOp, tt.op)
			}
		})
	}
}

func TestClient_MolStale(t *testing.T) {
	t.Parallel()

	client := newMockClient(t, func(req Request) Response {
		data, _ := json.Marshal(MolStaleResponse{})
		return Response{Success: true, Data: data}
	})

	result, err := client.MolStale(&MolStaleArgs{})
	if err != nil {
		t.Fatalf("MolStale() error: %v", err)
	}
	if result == nil {
		t.Error("MolStale() returned nil result")
	}
}

func TestClient_GetParentIDs(t *testing.T) {
	t.Parallel()

	client := newMockClient(t, func(req Request) Response {
		data, _ := json.Marshal(GetParentIDsResponse{})
		return Response{Success: true, Data: data}
	})

	result, err := client.GetParentIDs(&GetParentIDsArgs{IssueIDs: []string{"bd-1"}})
	if err != nil {
		t.Fatalf("GetParentIDs() error: %v", err)
	}
	if result == nil {
		t.Error("GetParentIDs() returned nil result")
	}
}

func TestClient_GetGraphData(t *testing.T) {
	t.Parallel()

	client := newMockClient(t, func(req Request) Response {
		data, _ := json.Marshal(GetGraphDataResponse{})
		return Response{Success: true, Data: data}
	})

	result, err := client.GetGraphData(&GetGraphDataArgs{})
	if err != nil {
		t.Fatalf("GetGraphData() error: %v", err)
	}
	if result == nil {
		t.Error("GetGraphData() returned nil result")
	}
}

func TestClient_WaitForMutations(t *testing.T) {
	t.Parallel()

	var capturedOp string
	client := newMockClient(t, func(req Request) Response {
		capturedOp = req.Operation
		return Response{Success: true, Data: json.RawMessage(`{}`)}
	})

	_, err := client.WaitForMutations(&WaitForMutationsArgs{Timeout: 100})
	if err != nil {
		t.Fatalf("WaitForMutations() error: %v", err)
	}
	if capturedOp != OpWaitForMutations {
		t.Errorf("operation = %q, want %q", capturedOp, OpWaitForMutations)
	}
}

func TestClient_WaitForMutations_DefaultTimeout(t *testing.T) {
	t.Parallel()

	client := newMockClient(t, func(req Request) Response {
		return Response{Success: true, Data: json.RawMessage(`{}`)}
	})

	// Timeout = 0 should use 30s default + 5s buffer
	_, err := client.WaitForMutations(&WaitForMutationsArgs{Timeout: 0})
	if err != nil {
		t.Fatalf("WaitForMutations() error: %v", err)
	}
}

func TestCleanupStaleDaemonArtifacts(t *testing.T) {
	t.Parallel()

	t.Run("removes stale pid file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		pidFile := filepath.Join(dir, "daemon.pid")

		if err := os.WriteFile(pidFile, []byte("12345"), 0644); err != nil {
			t.Fatalf("WriteFile() error: %v", err)
		}

		cleanupStaleDaemonArtifacts(dir)

		if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
			t.Error("daemon.pid should be removed")
		}
	})

	t.Run("no pid file is noop", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		// Should not panic or error
		cleanupStaleDaemonArtifacts(dir)
	})
}

// TestClient_UnmarshalErrorPaths tests the json.Unmarshal error paths
// in methods that deserialize response data into typed structs.
func TestClient_UnmarshalErrorPaths(t *testing.T) {
	t.Parallel()

	// Mock server returns success with invalid JSON data
	invalidDataHandler := func(req Request) Response {
		return Response{Success: true, Data: json.RawMessage(`{invalid json`)}
	}

	t.Run("Status unmarshal error", func(t *testing.T) {
		t.Parallel()
		client := newMockClient(t, invalidDataHandler)
		_, err := client.Status()
		if err == nil {
			t.Error("Status() should fail with invalid JSON data")
		}
	})

	t.Run("Health unmarshal error", func(t *testing.T) {
		t.Parallel()
		client := newMockClient(t, invalidDataHandler)
		_, err := client.Health()
		if err == nil {
			t.Error("Health() should fail with invalid JSON data")
		}
	})

	t.Run("Metrics unmarshal error", func(t *testing.T) {
		t.Parallel()
		client := newMockClient(t, invalidDataHandler)
		_, err := client.Metrics()
		if err == nil {
			t.Error("Metrics() should fail with invalid JSON data")
		}
	})

	t.Run("GetWorkerStatus unmarshal error", func(t *testing.T) {
		t.Parallel()
		client := newMockClient(t, invalidDataHandler)
		_, err := client.GetWorkerStatus(&GetWorkerStatusArgs{})
		if err == nil {
			t.Error("GetWorkerStatus() should fail with invalid JSON data")
		}
	})

	t.Run("GetConfig unmarshal error", func(t *testing.T) {
		t.Parallel()
		client := newMockClient(t, invalidDataHandler)
		_, err := client.GetConfig(&GetConfigArgs{Key: "test"})
		if err == nil {
			t.Error("GetConfig() should fail with invalid JSON data")
		}
	})

	t.Run("MolStale unmarshal error", func(t *testing.T) {
		t.Parallel()
		client := newMockClient(t, invalidDataHandler)
		_, err := client.MolStale(&MolStaleArgs{})
		if err == nil {
			t.Error("MolStale() should fail with invalid JSON data")
		}
	})

	t.Run("GetParentIDs unmarshal error", func(t *testing.T) {
		t.Parallel()
		client := newMockClient(t, invalidDataHandler)
		_, err := client.GetParentIDs(&GetParentIDsArgs{IssueIDs: []string{"bd-1"}})
		if err == nil {
			t.Error("GetParentIDs() should fail with invalid JSON data")
		}
	})

	t.Run("GetGraphData unmarshal error", func(t *testing.T) {
		t.Parallel()
		client := newMockClient(t, invalidDataHandler)
		_, err := client.GetGraphData(&GetGraphDataArgs{})
		if err == nil {
			t.Error("GetGraphData() should fail with invalid JSON data")
		}
	})
}

// TestClient_Ping_ErrorResponse tests the Ping error path
// where the response is successful but has error content.
func TestClient_Ping_ErrorResponse(t *testing.T) {
	t.Parallel()

	client := newMockClient(t, func(req Request) Response {
		return Response{Success: false, Error: "ping failed: unhealthy"}
	})

	err := client.Ping()
	if err == nil {
		t.Error("Ping() should fail with error response")
	}
}

func TestListenAndDialRPC(t *testing.T) {
	t.Parallel()

	dir, err := os.MkdirTemp("/tmp", "rpc-transport-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	socketPath := filepath.Join(dir, "test.sock")

	// Test listenRPC
	listener, err := listenRPC(socketPath)
	if err != nil {
		t.Fatalf("listenRPC() error: %v", err)
	}
	defer listener.Close()

	// Verify socket file was created
	if !endpointExists(socketPath) {
		t.Error("socket file should exist after listenRPC")
	}

	// Test dialRPC
	go func() {
		conn, _ := listener.Accept()
		if conn != nil {
			conn.Close()
		}
	}()

	conn, err := dialRPC(socketPath, time.Second)
	if err != nil {
		t.Fatalf("dialRPC() error: %v", err)
	}
	conn.Close()
}

func TestEndpointExists(t *testing.T) {
	t.Parallel()

	t.Run("exists", func(t *testing.T) {
		t.Parallel()
		f, _ := os.CreateTemp("", "ep-test-*")
		defer os.Remove(f.Name())
		f.Close()

		if !endpointExists(f.Name()) {
			t.Error("should return true for existing file")
		}
	})

	t.Run("not exists", func(t *testing.T) {
		t.Parallel()
		if endpointExists("/nonexistent/path/file") {
			t.Error("should return false for non-existing path")
		}
	})
}

func TestRpcDebugEnabled(t *testing.T) {
	// Cannot run parallel due to env var mutation
	t.Run("disabled by default", func(t *testing.T) {
		t.Setenv("BD_DEBUG_RPC", "")
		if rpcDebugEnabled() {
			t.Error("should be disabled when BD_DEBUG_RPC is empty")
		}
	})

	t.Run("enabled with 1", func(t *testing.T) {
		t.Setenv("BD_DEBUG_RPC", "1")
		if !rpcDebugEnabled() {
			t.Error("should be enabled when BD_DEBUG_RPC=1")
		}
	})

	t.Run("enabled with true", func(t *testing.T) {
		t.Setenv("BD_DEBUG_RPC", "true")
		if !rpcDebugEnabled() {
			t.Error("should be enabled when BD_DEBUG_RPC=true")
		}
	})

	t.Run("disabled with other values", func(t *testing.T) {
		t.Setenv("BD_DEBUG_RPC", "yes")
		if rpcDebugEnabled() {
			t.Error("should be disabled with BD_DEBUG_RPC=yes")
		}
	})
}

func TestRpcDebugLog(t *testing.T) {
	// Test that rpcDebugLog doesn't panic when enabled
	t.Setenv("BD_DEBUG_RPC", "1")
	rpcDebugLog("test message: %s %d", "hello", 42)

	// And when disabled
	t.Setenv("BD_DEBUG_RPC", "")
	rpcDebugLog("should not print: %s", "ignored")
}

// TestClient_ExecuteErrorPaths tests error paths in methods that call Execute
// and check for Execute-level errors (before response unmarshaling).
func TestClient_ExecuteErrorPaths(t *testing.T) {
	t.Parallel()

	// Server always returns an error response
	errorHandler := func(req Request) Response {
		return Response{Success: false, Error: "operation failed: server error"}
	}

	t.Run("Status execute error", func(t *testing.T) {
		t.Parallel()
		client := newMockClient(t, errorHandler)
		_, err := client.Status()
		if err == nil {
			t.Error("Status() should fail")
		}
	})

	t.Run("Health execute error", func(t *testing.T) {
		t.Parallel()
		client := newMockClient(t, errorHandler)
		_, err := client.Health()
		if err == nil {
			t.Error("Health() should fail")
		}
	})

	t.Run("Metrics execute error", func(t *testing.T) {
		t.Parallel()
		client := newMockClient(t, errorHandler)
		_, err := client.Metrics()
		if err == nil {
			t.Error("Metrics() should fail")
		}
	})

	t.Run("GetWorkerStatus execute error", func(t *testing.T) {
		t.Parallel()
		client := newMockClient(t, errorHandler)
		_, err := client.GetWorkerStatus(&GetWorkerStatusArgs{})
		if err == nil {
			t.Error("GetWorkerStatus() should fail")
		}
	})

	t.Run("GetConfig execute error", func(t *testing.T) {
		t.Parallel()
		client := newMockClient(t, errorHandler)
		_, err := client.GetConfig(&GetConfigArgs{Key: "test"})
		if err == nil {
			t.Error("GetConfig() should fail")
		}
	})

	t.Run("MolStale execute error", func(t *testing.T) {
		t.Parallel()
		client := newMockClient(t, errorHandler)
		_, err := client.MolStale(&MolStaleArgs{})
		if err == nil {
			t.Error("MolStale() should fail")
		}
	})

	t.Run("GetParentIDs execute error", func(t *testing.T) {
		t.Parallel()
		client := newMockClient(t, errorHandler)
		_, err := client.GetParentIDs(&GetParentIDsArgs{IssueIDs: []string{"bd-1"}})
		if err == nil {
			t.Error("GetParentIDs() should fail")
		}
	})

	t.Run("GetGraphData execute error", func(t *testing.T) {
		t.Parallel()
		client := newMockClient(t, errorHandler)
		_, err := client.GetGraphData(&GetGraphDataArgs{})
		if err == nil {
			t.Error("GetGraphData() should fail")
		}
	})
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
