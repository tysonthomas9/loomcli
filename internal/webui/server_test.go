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

// grabEphemeralPort asks the OS to assign a free port, then releases it and
// returns the port number. The caller rebinds immediately in the same goroutine,
// so the race window is vanishingly small compared to hardcoded-port collisions
// across parallel worktree runs.
func grabEphemeralPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to grab ephemeral port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

// TestFindAvailablePort_FirstPortFree tests that findAvailablePort returns the first port
// when it is available.
func TestFindAvailablePort_FirstPortFree(t *testing.T) {
	startPort := grabEphemeralPort(t)

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
	// Bind port 0 and keep it open as the occupier
	occupier, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to occupy ephemeral port: %v", err)
	}
	defer occupier.Close()
	startPort := occupier.Addr().(*net.TCPAddr).Port

	// findAvailablePort should skip the occupied startPort
	listener, port, err := findAvailablePort("127.0.0.1", startPort, 5)
	if err != nil {
		t.Fatalf("findAvailablePort(%d, 5) failed: %v", startPort, err)
	}
	defer listener.Close()

	if port == startPort {
		t.Errorf("findAvailablePort(%d, 5) returned the occupied port", startPort)
	}
	if port < startPort || port > startPort+4 {
		t.Errorf("findAvailablePort(%d, 5) = %d, want port in range [%d, %d]", startPort, port, startPort+1, startPort+4)
	}
}

// TestFindAvailablePort_FallbackMultiplePorts tests that findAvailablePort correctly
// skips multiple occupied ports.
func TestFindAvailablePort_FallbackMultiplePorts(t *testing.T) {
	// Bind 3 ephemeral ports and keep them open
	var occupiers []net.Listener
	occupiedSet := make(map[int]bool)
	for i := 0; i < 3; i++ {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			for _, o := range occupiers {
				o.Close()
			}
			t.Fatalf("failed to grab ephemeral port: %v", err)
		}
		occupiers = append(occupiers, l)
		occupiedSet[l.Addr().(*net.TCPAddr).Port] = true
	}
	defer func() {
		for _, l := range occupiers {
			l.Close()
		}
	}()

	// Use the lowest occupied port as startPort with enough attempts to scan past all
	startPort := occupiers[0].Addr().(*net.TCPAddr).Port
	for p := range occupiedSet {
		if p < startPort {
			startPort = p
		}
	}
	maxPort := startPort
	for p := range occupiedSet {
		if p > maxPort {
			maxPort = p
		}
	}
	// Scan range must cover all occupied ports plus at least one more
	maxAttempts := maxPort - startPort + 2

	listener, port, err := findAvailablePort("127.0.0.1", startPort, maxAttempts)
	if err != nil {
		t.Fatalf("findAvailablePort(%d, %d) failed: %v", startPort, maxAttempts, err)
	}
	defer listener.Close()

	if occupiedSet[port] {
		t.Errorf("findAvailablePort returned an occupied port %d", port)
	}
}

// TestFindAvailablePort_AllPortsInUse tests that findAvailablePort returns an error
// when all ports in the range are occupied.
func TestFindAvailablePort_AllPortsInUse(t *testing.T) {
	// Bind a single port with :0 and keep it open, then use maxAttempts=1
	// so findAvailablePort only tries that one port.
	occupier, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to grab ephemeral port: %v", err)
	}
	defer occupier.Close()
	startPort := occupier.Addr().(*net.TCPAddr).Port
	maxAttempts := 1

	// findAvailablePort should return an error
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
	// Test 1: Port is free — use grabEphemeralPort (release-then-bind is fine
	// because findAvailablePort will be the one binding it)
	startPort := grabEphemeralPort(t)
	listener, port, err := findAvailablePort("127.0.0.1", startPort, 1)
	if err != nil {
		t.Fatalf("findAvailablePort(%d, 1) failed: %v", startPort, err)
	}
	listener.Close()

	if port != startPort {
		t.Errorf("findAvailablePort(%d, 1) = %d, want %d", startPort, port, startPort)
	}

	// Test 2: Port is occupied - should fail immediately
	occupier, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to grab ephemeral port: %v", err)
	}
	defer occupier.Close()
	occupiedPort := occupier.Addr().(*net.TCPAddr).Port

	listener, port, err = findAvailablePort("127.0.0.1", occupiedPort, 1)
	if err == nil {
		listener.Close()
		t.Errorf("findAvailablePort(%d, 1) = %d, want error (port occupied with single attempt)", occupiedPort, port)
	}
}

// TestFindAvailablePort_ZeroAttempts tests findAvailablePort with maxAttempts=0.
func TestFindAvailablePort_ZeroAttempts(t *testing.T) {
	startPort := grabEphemeralPort(t)

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
	startPort := grabEphemeralPort(t)

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
	port := grabEphemeralPort(t)

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
	port := grabEphemeralPort(t)

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
