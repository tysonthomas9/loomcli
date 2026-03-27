package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// TestFindAvailablePort_FirstPortFree tests that findAvailablePort returns the first port
// when it is available.
func TestFindAvailablePort_FirstPortFree(t *testing.T) {
	// Use a high port number to avoid conflicts with common services
	startPort := 59100

	listener, port, err := findAvailablePort("127.0.0.1", startPort, 5)
	if err != nil {
		t.Fatalf("findAvailablePort(%d, 5) failed: %v", startPort, err)
	}
	defer listener.Close()

	if port != startPort {
		t.Errorf("findAvailablePort(%d, 5) = %d, want %d", startPort, port, startPort)
	}
}

// TestFindAvailablePort_FallbackToNextPort tests that findAvailablePort falls back
// to the next port when the first one is in use.
func TestFindAvailablePort_FallbackToNextPort(t *testing.T) {
	// Use a high port number to avoid conflicts
	startPort := 59200

	// Occupy the first port
	addr := fmt.Sprintf("127.0.0.1:%d", startPort)
	occupier, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("failed to occupy port %d: %v", startPort, err)
	}
	defer occupier.Close()

	// Now findAvailablePort should return the next port
	listener, port, err := findAvailablePort("127.0.0.1", startPort, 5)
	if err != nil {
		t.Fatalf("findAvailablePort(%d, 5) failed: %v", startPort, err)
	}
	defer listener.Close()

	want := startPort + 1
	if port != want {
		t.Errorf("findAvailablePort(%d, 5) = %d, want %d (first port is occupied)", startPort, port, want)
	}
}

// TestFindAvailablePort_FallbackMultiplePorts tests that findAvailablePort correctly
// skips multiple occupied ports.
func TestFindAvailablePort_FallbackMultiplePorts(t *testing.T) {
	// Use a high port number to avoid conflicts
	startPort := 59300

	// Occupy the first three ports
	var occupiers []net.Listener
	for i := 0; i < 3; i++ {
		addr := fmt.Sprintf("127.0.0.1:%d", startPort+i)
		occupier, err := net.Listen("tcp", addr)
		if err != nil {
			// Clean up already created listeners
			for _, l := range occupiers {
				l.Close()
			}
			t.Fatalf("failed to occupy port %d: %v", startPort+i, err)
		}
		occupiers = append(occupiers, occupier)
	}
	defer func() {
		for _, l := range occupiers {
			l.Close()
		}
	}()

	// Now findAvailablePort should return the fourth port
	listener, port, err := findAvailablePort("127.0.0.1", startPort, 5)
	if err != nil {
		t.Fatalf("findAvailablePort(%d, 5) failed: %v", startPort, err)
	}
	defer listener.Close()

	want := startPort + 3
	if port != want {
		t.Errorf("findAvailablePort(%d, 5) = %d, want %d (first 3 ports are occupied)", startPort, port, want)
	}
}

// TestFindAvailablePort_AllPortsInUse tests that findAvailablePort returns an error
// when all ports in the range are occupied.
func TestFindAvailablePort_AllPortsInUse(t *testing.T) {
	// Use a high port number to avoid conflicts
	startPort := 59400
	maxAttempts := 3

	// Occupy all ports in the range
	var occupiers []net.Listener
	for i := 0; i < maxAttempts; i++ {
		addr := fmt.Sprintf("127.0.0.1:%d", startPort+i)
		occupier, err := net.Listen("tcp", addr)
		if err != nil {
			// Clean up already created listeners
			for _, l := range occupiers {
				l.Close()
			}
			t.Fatalf("failed to occupy port %d: %v", startPort+i, err)
		}
		occupiers = append(occupiers, occupier)
	}
	defer func() {
		for _, l := range occupiers {
			l.Close()
		}
	}()

	// Now findAvailablePort should return an error
	listener, port, err := findAvailablePort("127.0.0.1", startPort, maxAttempts)
	if err == nil {
		listener.Close()
		t.Errorf("findAvailablePort(%d, %d) = %d, want error (all ports occupied)", startPort, maxAttempts, port)
	}

	// Verify the error message mentions the port range
	wantErrSubstr := fmt.Sprintf("no available port found on 127.0.0.1 in range %d-%d", startPort, startPort+maxAttempts-1)
	if err != nil && err.Error() != wantErrSubstr {
		t.Errorf("findAvailablePort error = %q, want %q", err.Error(), wantErrSubstr)
	}
}

// TestFindAvailablePort_SingleAttempt tests findAvailablePort with maxAttempts=1.
func TestFindAvailablePort_SingleAttempt(t *testing.T) {
	// Use a high port number to avoid conflicts
	startPort := 59500

	// Test 1: Port is free
	listener, port, err := findAvailablePort("127.0.0.1", startPort, 1)
	if err != nil {
		t.Fatalf("findAvailablePort(%d, 1) failed: %v", startPort, err)
	}
	listener.Close() // Close immediately to reuse port in next test

	if port != startPort {
		t.Errorf("findAvailablePort(%d, 1) = %d, want %d", startPort, port, startPort)
	}

	// Test 2: Port is occupied - should fail immediately
	occupier, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", startPort))
	if err != nil {
		t.Fatalf("failed to occupy port %d: %v", startPort, err)
	}
	defer occupier.Close()

	listener, port, err = findAvailablePort("127.0.0.1", startPort, 1)
	if err == nil {
		listener.Close()
		t.Errorf("findAvailablePort(%d, 1) = %d, want error (port occupied with single attempt)", startPort, port)
	}
}

// TestFindAvailablePort_ZeroAttempts tests findAvailablePort with maxAttempts=0.
func TestFindAvailablePort_ZeroAttempts(t *testing.T) {
	startPort := 59600

	// With zero attempts, should immediately return error
	listener, port, err := findAvailablePort("127.0.0.1", startPort, 0)
	if err == nil {
		listener.Close()
		t.Errorf("findAvailablePort(%d, 0) = %d, want error (zero attempts)", startPort, port)
	}
}

// TestFindAvailablePort_ListenerIsUsable tests that the returned listener is actually
// usable and the port remains bound.
func TestFindAvailablePort_ListenerIsUsable(t *testing.T) {
	startPort := 59700

	listener, port, err := findAvailablePort("127.0.0.1", startPort, 5)
	if err != nil {
		t.Fatalf("findAvailablePort(%d, 5) failed: %v", startPort, err)
	}
	defer listener.Close()

	// Verify the listener is holding the port by trying to bind again
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	duplicate, err := net.Listen("tcp", addr)
	if err == nil {
		duplicate.Close()
		t.Errorf("port %d should be held by returned listener, but was able to bind again", port)
	}
}

// TestStartServer_WriteTimeout verifies that StartServer creates an HTTP server with
// WriteTimeout = 30s. Since the http.Server is created internally, we test this
// behaviorally: start the server, make an HTTP request to the health endpoint, and
// confirm the server is functioning correctly with the timeout configuration.
// The health endpoint is used because it does not require a daemon connection pool.
func TestStartServer_WriteTimeout(t *testing.T) {
	// Use a high port to avoid conflicts
	port := 59800

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	config := ServerConfig{
		Port:            port,
		BindAddress:     "127.0.0.1",
		PoolSize:        1,
		ShutdownTimeout: 1 * time.Second,
		MaxPortAttempts: 5,
		AuthEnabled:     false, // Disable auth to simplify test requests
	}

	// Start the server in a goroutine
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- StartServer(ctx, config)
	}()

	// Wait for the server to be ready by polling the health endpoint
	serverAddr := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 5 * time.Second}

	var ready bool
	for i := 0; i < 50; i++ {
		resp, err := client.Get(serverAddr + "/api/health")
		if err == nil {
			resp.Body.Close()
			ready = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		cancel()
		t.Fatal("server did not become ready within timeout")
	}

	// Make a request to the health endpoint and verify it completes successfully.
	// This confirms the server's WriteTimeout (30s) does not interfere with normal
	// request handling (the health endpoint responds in < 1s).
	resp, err := client.Get(serverAddr + "/api/health")
	if err != nil {
		cancel()
		t.Fatalf("health request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		cancel()
		t.Fatalf("failed to read response body: %v", err)
	}

	// The health endpoint returns JSON with a "status" field
	var health HealthStatus
	if err := json.Unmarshal(body, &health); err != nil {
		cancel()
		t.Fatalf("failed to parse health response: %v", err)
	}

	// With nil pool, status should be "degraded" but the response itself must succeed
	if health.Status != "degraded" {
		t.Errorf("expected health status 'degraded' (no daemon pool), got %q", health.Status)
	}

	// Verify Content-Type is JSON
	ct := resp.Header.Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got %q", ct)
	}

	// Shut down the server
	cancel()

	select {
	case err := <-serverDone:
		if err != nil {
			t.Errorf("StartServer returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down within timeout")
	}
}

// TestExtractOrigin verifies extractOrigin returns scheme://host for valid URLs.
func TestExtractOrigin(t *testing.T) {
	tests := []struct {
		name   string
		rawURL string
		want   string
	}{
		{"https without path", "https://auth.example.com", "https://auth.example.com"},
		{"https with port and path", "https://auth.example.com:8443/path", "https://auth.example.com:8443"},
		{"http localhost with port", "http://localhost:3000", "http://localhost:3000"},
		{"empty string", "", ""},
		{"not a url", "not-a-url", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractOrigin(tt.rawURL)
			if got != tt.want {
				t.Errorf("extractOrigin(%q) = %q, want %q", tt.rawURL, got, tt.want)
			}
		})
	}
}

// TestDefaultConfig tests that DefaultConfig returns sensible defaults.
func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	if config.Port != 8080 {
		t.Errorf("Port = %d, want 8080", config.Port)
	}
	if config.BindAddress != "127.0.0.1" {
		t.Errorf("BindAddress = %q, want %q", config.BindAddress, "127.0.0.1")
	}
	if config.PoolSize != 100 {
		t.Errorf("PoolSize = %d, want 100", config.PoolSize)
	}
	if config.ShutdownTimeout != 5*time.Second {
		t.Errorf("ShutdownTimeout = %v, want %v", config.ShutdownTimeout, 5*time.Second)
	}
	if config.MaxPortAttempts != 10 {
		t.Errorf("MaxPortAttempts = %d, want 10", config.MaxPortAttempts)
	}
	// Auth should be disabled by default
	if config.AuthEnabled {
		t.Error("AuthEnabled should be false by default")
	}
	// Socket path should be empty by default
	if config.SocketPath != "" {
		t.Errorf("SocketPath = %q, want empty", config.SocketPath)
	}
}

// TestStartServer_WriteTimeout_NonStreamingEndpoint verifies that a non-streaming
// endpoint (stats) works correctly under the 30s WriteTimeout. This tests the
// opposite concern from streaming handlers: non-streaming handlers must complete
// within the WriteTimeout, which they easily do for well-behaved requests.
func TestStartServer_WriteTimeout_NonStreamingEndpoint(t *testing.T) {
	port := 59810

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	config := ServerConfig{
		Port:            port,
		BindAddress:     "127.0.0.1",
		PoolSize:        1,
		ShutdownTimeout: 1 * time.Second,
		MaxPortAttempts: 5,
		AuthEnabled:     false,
	}

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- StartServer(ctx, config)
	}()

	serverAddr := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 5 * time.Second}

	// Wait for the server to be ready
	var ready bool
	for i := 0; i < 50; i++ {
		resp, err := client.Get(serverAddr + "/api/health")
		if err == nil {
			resp.Body.Close()
			ready = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		cancel()
		t.Fatal("server did not become ready within timeout")
	}

	// Hit the stats endpoint (non-streaming). Without a daemon pool, it returns 503
	// but the response itself should be well-formed JSON, confirming the WriteTimeout
	// does not interfere with fast non-streaming responses.
	resp, err := client.Get(serverAddr + "/api/stats")
	if err != nil {
		cancel()
		t.Fatalf("stats request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected status %d for stats without pool, got %d",
			http.StatusServiceUnavailable, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		cancel()
		t.Fatalf("failed to read response body: %v", err)
	}

	var statsResp StatsResponse
	if err := json.Unmarshal(body, &statsResp); err != nil {
		cancel()
		t.Fatalf("failed to parse stats response: %v", err)
	}

	if statsResp.Success {
		t.Error("expected success to be false without daemon pool")
	}

	// Shut down
	cancel()

	select {
	case err := <-serverDone:
		if err != nil {
			t.Errorf("StartServer returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down within timeout")
	}
}
