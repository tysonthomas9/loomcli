package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage/sqlite"
)

func dialTestConn(t *testing.T, socketPath string) net.Conn {
	conn, err := dialRPC(socketPath, time.Second)
	if err != nil {
		t.Fatalf("failed to dial %s: %v", socketPath, err)
	}
	return conn
}

func TestConnectionLimits(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, ".beads", "test.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0750); err != nil {
		t.Fatal(err)
	}

	store, err := sqlite.New(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	socketPath := newTestSocketPath(t)

	// Set low connection limit for testing
	os.Setenv("BEADS_DAEMON_MAX_CONNS", "5")
	defer os.Unsetenv("BEADS_DAEMON_MAX_CONNS")

	srv := NewServer(socketPath, store, tmpDir, dbPath)
	if srv.maxConns != 5 {
		t.Fatalf("expected maxConns=5, got %d", srv.maxConns)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		if err := srv.Start(ctx); err != nil && ctx.Err() == nil {
			t.Logf("server error: %v", err)
		}
	}()

	// Wait for server to be ready
	time.Sleep(100 * time.Millisecond)
	defer srv.Stop()

	// Open maxConns connections and hold them
	var wg sync.WaitGroup
	connections := make([]net.Conn, srv.maxConns)

	for i := 0; i < srv.maxConns; i++ {
		conn := dialTestConn(t, socketPath)
		connections[i] = conn

		// Send a long-running ping to keep connection busy
		wg.Add(1)
		go func(c net.Conn, _ int) {
			defer wg.Done()
			req := Request{
				Operation: OpPing,
			}
			data, _ := json.Marshal(req)
			c.Write(append(data, '\n'))

			// Read response
			reader := bufio.NewReader(c)
			_, _ = reader.ReadBytes('\n')
		}(conn, i)
	}

	// Wait for all connections to be active
	time.Sleep(200 * time.Millisecond)

	// Verify active connection count
	activeConns := atomic.LoadInt32(&srv.activeConns)
	if activeConns != int32(srv.maxConns) {
		t.Errorf("expected %d active connections, got %d", srv.maxConns, activeConns)
	}

	// Try to open one more connection - should be rejected
	extraConn := dialTestConn(t, socketPath)
	defer extraConn.Close()

	// Send request on extra connection
	req := Request{Operation: OpPing}
	data, _ := json.Marshal(req)
	extraConn.Write(append(data, '\n'))

	// Set short read timeout to detect rejection
	extraConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	reader := bufio.NewReader(extraConn)
	_, err = reader.ReadBytes('\n')

	// Connection should be closed (EOF or timeout)
	if err == nil {
		t.Error("expected extra connection to be rejected, but got response")
	}

	// Close existing connections
	for _, conn := range connections {
		conn.Close()
	}
	wg.Wait()

	// Wait for connection cleanup
	time.Sleep(100 * time.Millisecond)

	// Now should be able to connect again
	newConn := dialTestConn(t, socketPath)
	defer newConn.Close()

	req = Request{Operation: OpPing}
	data, _ = json.Marshal(req)
	newConn.Write(append(data, '\n'))

	reader = bufio.NewReader(newConn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if !resp.Success {
		t.Error("expected successful ping after connection cleanup")
	}
}

func TestRequestTimeout(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, ".beads", "test.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0750); err != nil {
		t.Fatal(err)
	}

	store, err := sqlite.New(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	socketPath := newTestSocketPath(t)

	// Set very short timeout for testing
	os.Setenv("BEADS_DAEMON_REQUEST_TIMEOUT", "100ms")
	defer os.Unsetenv("BEADS_DAEMON_REQUEST_TIMEOUT")

	srv := NewServer(socketPath, store, tmpDir, dbPath)
	if srv.requestTimeout != 100*time.Millisecond {
		t.Fatalf("expected timeout=100ms, got %v", srv.requestTimeout)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		if err := srv.Start(ctx); err != nil && ctx.Err() == nil {
			t.Logf("server error: %v", err)
		}
	}()

	time.Sleep(100 * time.Millisecond)
	defer srv.Stop()

	conn := dialTestConn(t, socketPath)
	defer conn.Close()

	// Send partial request and wait for timeout
	conn.Write([]byte(`{"operation":"ping"`)) // Incomplete JSON

	// Wait longer than timeout
	time.Sleep(200 * time.Millisecond)

	// Attempt to read - connection should have been closed or timed out
	conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	buf := make([]byte, 1)
	if _, err := conn.Read(buf); err == nil {
		t.Error("expected connection to be closed due to timeout")
	}
}

func TestHealthResponseIncludesLimits(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	socketPath := newTestSocketPath(t)

	store, err := sqlite.New(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	os.Setenv("BEADS_DAEMON_MAX_CONNS", "50")
	defer os.Unsetenv("BEADS_DAEMON_MAX_CONNS")

	srv := NewServer(socketPath, store, tmpDir, dbPath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		if err := srv.Start(ctx); err != nil && ctx.Err() == nil {
			t.Logf("server error: %v", err)
		}
	}()

	time.Sleep(100 * time.Millisecond)
	defer srv.Stop()

	conn := dialTestConn(t, socketPath)
	defer conn.Close()

	req := Request{Operation: OpHealth}
	data, _ := json.Marshal(req)
	conn.Write(append(data, '\n'))

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if !resp.Success {
		t.Fatalf("health check failed: %s", resp.Error)
	}

	var health HealthResponse
	if err := json.Unmarshal(resp.Data, &health); err != nil {
		t.Fatalf("failed to unmarshal health response: %v", err)
	}

	// Verify limit fields are present
	if health.MaxConns != 50 {
		t.Errorf("expected MaxConns=50, got %d", health.MaxConns)
	}

	if health.ActiveConns < 0 {
		t.Errorf("expected ActiveConns>=0, got %d", health.ActiveConns)
	}

	// No need to check MemoryAllocMB < 0 since it's uint64

	t.Logf("Health: %d/%d connections, %d MB memory", health.ActiveConns, health.MaxConns, health.MemoryAllocMB)
}

func TestMaxMessageSizeDefault(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, ".beads", "test.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0750); err != nil {
		t.Fatal(err)
	}

	store, err := sqlite.New(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	socketPath := newTestSocketPath(t)
	srv := NewServer(socketPath, store, tmpDir, dbPath)

	if srv.maxMessageSize != DefaultMaxMessageSize {
		t.Fatalf("expected maxMessageSize=%d, got %d", DefaultMaxMessageSize, srv.maxMessageSize)
	}
}

func TestMaxMessageSizeEnvVar(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, ".beads", "test.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0750); err != nil {
		t.Fatal(err)
	}

	store, err := sqlite.New(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	socketPath := newTestSocketPath(t)

	// Test custom value
	os.Setenv("BEADS_DAEMON_MAX_MESSAGE_SIZE", "5242880") // 5 MB
	defer os.Unsetenv("BEADS_DAEMON_MAX_MESSAGE_SIZE")

	srv := NewServer(socketPath, store, tmpDir, dbPath)
	if srv.maxMessageSize != 5242880 {
		t.Fatalf("expected maxMessageSize=5242880, got %d", srv.maxMessageSize)
	}

	// Test minimum floor enforcement (value below 64 KB should be ignored)
	os.Setenv("BEADS_DAEMON_MAX_MESSAGE_SIZE", "100")
	srv2 := NewServer(socketPath, store, tmpDir, dbPath)
	if srv2.maxMessageSize != DefaultMaxMessageSize {
		t.Fatalf("expected maxMessageSize=%d (default, below floor), got %d", DefaultMaxMessageSize, srv2.maxMessageSize)
	}

	// Test invalid value is ignored
	os.Setenv("BEADS_DAEMON_MAX_MESSAGE_SIZE", "not-a-number")
	srv3 := NewServer(socketPath, store, tmpDir, dbPath)
	if srv3.maxMessageSize != DefaultMaxMessageSize {
		t.Fatalf("expected maxMessageSize=%d (default, invalid env), got %d", DefaultMaxMessageSize, srv3.maxMessageSize)
	}
}

func TestServerRejectsOversizedMessage(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, ".beads", "test.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0750); err != nil {
		t.Fatal(err)
	}

	store, err := sqlite.New(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	socketPath := newTestSocketPath(t)

	// Set a small message size limit for testing (64 KB - the minimum floor)
	os.Setenv("BEADS_DAEMON_MAX_MESSAGE_SIZE", "65536")
	defer os.Unsetenv("BEADS_DAEMON_MAX_MESSAGE_SIZE")

	srv := NewServer(socketPath, store, tmpDir, dbPath)
	if srv.maxMessageSize != 65536 {
		t.Fatalf("expected maxMessageSize=65536, got %d", srv.maxMessageSize)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		if err := srv.Start(ctx); err != nil && ctx.Err() == nil {
			t.Logf("server error: %v", err)
		}
	}()

	time.Sleep(100 * time.Millisecond)
	defer srv.Stop()

	conn := dialTestConn(t, socketPath)
	defer conn.Close()

	// Create a message larger than 64 KB
	largePayload := strings.Repeat("x", 128*1024)
	req := Request{
		Operation: OpPing,
		Args:      json.RawMessage(`"` + largePayload + `"`),
	}
	data, _ := json.Marshal(req)
	data = append(data, '\n')

	conn.Write(data)

	// Try to read - server should close the connection or return an error
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')

	if err == nil {
		// If we got a response, it should be an error about message too large
		var resp Response
		if jsonErr := json.Unmarshal(line, &resp); jsonErr == nil {
			if resp.Success {
				t.Error("expected oversized message to be rejected, but got success response")
			}
			if !strings.Contains(resp.Error, "too large") {
				t.Errorf("expected 'too large' error, got: %s", resp.Error)
			}
		}
	}
	// err != nil is also acceptable (connection closed)
}

func TestServerAcceptsNormalMessage(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, ".beads", "test.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0750); err != nil {
		t.Fatal(err)
	}

	store, err := sqlite.New(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	socketPath := newTestSocketPath(t)

	srv := NewServer(socketPath, store, tmpDir, dbPath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		if err := srv.Start(ctx); err != nil && ctx.Err() == nil {
			t.Logf("server error: %v", err)
		}
	}()

	time.Sleep(100 * time.Millisecond)
	defer srv.Stop()

	conn := dialTestConn(t, socketPath)
	defer conn.Close()

	// Send a normal-sized ping request
	req := Request{Operation: OpPing}
	data, _ := json.Marshal(req)
	conn.Write(append(data, '\n'))

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if !resp.Success {
		t.Errorf("expected successful ping, got error: %s", resp.Error)
	}

	// Send a second request to verify connection reuse works with limit reset
	req2 := Request{Operation: OpPing}
	data2, _ := json.Marshal(req2)
	conn.Write(append(data2, '\n'))

	line2, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("failed to read second response: %v", err)
	}

	var resp2 Response
	if err := json.Unmarshal(line2, &resp2); err != nil {
		t.Fatalf("failed to unmarshal second response: %v", err)
	}

	if !resp2.Success {
		t.Errorf("expected successful second ping, got error: %s", resp2.Error)
	}
}

func TestClientRejectsOversizedResponse(t *testing.T) {
	// Test that the client-side LimitedReader catches oversized responses.
	// We simulate this by creating a raw TCP server that sends a huge response.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		// Read the request (discard it)
		reader := bufio.NewReader(conn)
		_, _ = reader.ReadBytes('\n')

		// Send an oversized response (larger than maxClientMessageSize)
		// Write a huge line without newline to exceed the limit
		writer := bufio.NewWriter(conn)
		chunk := strings.Repeat("x", 64*1024) // 64 KB chunks
		for i := 0; i < 200; i++ {             // ~12.5 MB total (> 10 MB limit)
			writer.WriteString(chunk)
		}
		writer.WriteByte('\n')
		writer.Flush()
	}()

	// Connect as a client
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Create a minimal client and send a request
	client := &Client{
		conn:    conn,
		timeout: 5 * time.Second,
	}

	_, err = client.Execute(OpPing, nil)
	if err == nil {
		t.Fatal("expected error for oversized response")
	}

	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("expected 'too large' error, got: %v", err)
	}
}
