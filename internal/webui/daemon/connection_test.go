package daemon

import (
	"errors"
	"testing"
	"time"
)

func TestNewDaemonConnection(t *testing.T) {
	tests := []struct {
		name       string
		socketPath string
		wantErr    error
	}{
		{
			name:       "valid socket path",
			socketPath: "/tmp/test.sock",
			wantErr:    nil,
		},
		{
			name:       "empty socket path returns error",
			socketPath: "",
			wantErr:    ErrInvalidSocketPath,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, err := NewDaemonConnection(tt.socketPath)

			if tt.wantErr != nil {
				if err != tt.wantErr {
					t.Errorf("NewDaemonConnection() error = %v, wantErr %v", err, tt.wantErr)
				}
				if conn != nil {
					t.Error("NewDaemonConnection() returned non-nil connection on error")
				}
				return
			}

			if err != nil {
				t.Errorf("NewDaemonConnection() unexpected error = %v", err)
				return
			}

			if conn == nil {
				t.Error("NewDaemonConnection() returned nil connection")
				return
			}

			// Verify default values
			if conn.socketPath != tt.socketPath {
				t.Errorf("socketPath = %v, want %v", conn.socketPath, tt.socketPath)
			}
			if conn.dialTimeout != DefaultDialTimeout {
				t.Errorf("dialTimeout = %v, want %v", conn.dialTimeout, DefaultDialTimeout)
			}
			if conn.requestTimeout != DefaultRequestTimeout {
				t.Errorf("requestTimeout = %v, want %v", conn.requestTimeout, DefaultRequestTimeout)
			}
		})
	}
}

func TestDaemonConnection_SocketPath(t *testing.T) {
	expectedPath := "/tmp/beads-test/bd.sock"
	conn, err := NewDaemonConnection(expectedPath)
	if err != nil {
		t.Fatalf("NewDaemonConnection() error = %v", err)
	}

	if conn.SocketPath() != expectedPath {
		t.Errorf("SocketPath() = %v, want %v", conn.SocketPath(), expectedPath)
	}
}

func TestDaemonConnection_IsConnected(t *testing.T) {
	conn, err := NewDaemonConnection("/tmp/test.sock")
	if err != nil {
		t.Fatalf("NewDaemonConnection() error = %v", err)
	}

	// Initially not connected
	if conn.IsConnected() {
		t.Error("IsConnected() = true, want false for new connection")
	}

	// Client should be nil
	if conn.Client() != nil {
		t.Error("Client() returned non-nil for new connection")
	}
}

func TestDaemonConnection_SetDialTimeout(t *testing.T) {
	conn, err := NewDaemonConnection("/tmp/test.sock")
	if err != nil {
		t.Fatalf("NewDaemonConnection() error = %v", err)
	}

	newTimeout := 5 * time.Second
	conn.SetDialTimeout(newTimeout)

	// Access the field directly for testing
	conn.mu.RLock()
	actualTimeout := conn.dialTimeout
	conn.mu.RUnlock()

	if actualTimeout != newTimeout {
		t.Errorf("dialTimeout = %v, want %v", actualTimeout, newTimeout)
	}
}

func TestDaemonConnection_SetRequestTimeout(t *testing.T) {
	conn, err := NewDaemonConnection("/tmp/test.sock")
	if err != nil {
		t.Fatalf("NewDaemonConnection() error = %v", err)
	}

	newTimeout := 60 * time.Second
	conn.SetRequestTimeout(newTimeout)

	// Access the field directly for testing
	conn.mu.RLock()
	actualTimeout := conn.requestTimeout
	conn.mu.RUnlock()

	if actualTimeout != newTimeout {
		t.Errorf("requestTimeout = %v, want %v", actualTimeout, newTimeout)
	}
}

func TestDaemonConnection_Disconnect_NoConnection(t *testing.T) {
	conn, err := NewDaemonConnection("/tmp/test.sock")
	if err != nil {
		t.Fatalf("NewDaemonConnection() error = %v", err)
	}

	// Disconnect should not error when no connection exists
	err = conn.Disconnect()
	if err != nil {
		t.Errorf("Disconnect() unexpected error = %v", err)
	}

	// Should still not be connected
	if conn.IsConnected() {
		t.Error("IsConnected() = true after Disconnect()")
	}
}

func TestDaemonConnection_ConcurrentAccess(t *testing.T) {
	conn, err := NewDaemonConnection("/tmp/test.sock")
	if err != nil {
		t.Fatalf("NewDaemonConnection() error = %v", err)
	}

	// Test concurrent reads don't race
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			_ = conn.IsConnected()
			_ = conn.Client()
			_ = conn.SocketPath()
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestDaemonConnection_Constants(t *testing.T) {
	// Verify constants have sensible values
	if DefaultDialTimeout <= 0 {
		t.Errorf("DefaultDialTimeout = %v, want positive value", DefaultDialTimeout)
	}
	if DefaultRequestTimeout <= 0 {
		t.Errorf("DefaultRequestTimeout = %v, want positive value", DefaultRequestTimeout)
	}
	if HealthCheckInterval <= 0 {
		t.Errorf("HealthCheckInterval = %v, want positive value", HealthCheckInterval)
	}

	// Verify sensible ordering
	if DefaultDialTimeout > DefaultRequestTimeout {
		t.Error("DefaultDialTimeout should not exceed DefaultRequestTimeout")
	}
}

func TestNewDaemonConnectionAutoDiscover_EmptyPath(t *testing.T) {
	_, err := NewDaemonConnectionAutoDiscover("")
	if !errors.Is(err, ErrInvalidSocketPath) {
		t.Errorf("expected ErrInvalidSocketPath, got %v", err)
	}
}

func TestNewDaemonConnectionAutoDiscover_NoDaemon(t *testing.T) {
	// A temp dir with no daemon running should still return a connection
	// (lazy connect via ComputeSocketPath fallback)
	dir := t.TempDir()
	conn, err := NewDaemonConnectionAutoDiscover(dir)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if conn == nil {
		t.Fatal("expected non-nil connection")
	}
	if conn.socketPath == "" {
		t.Error("expected non-empty socket path")
	}
}

func TestDaemonConnection_Connect_NoDaemon(t *testing.T) {
	conn, err := NewDaemonConnection("/tmp/nonexistent-daemon-test.sock")
	if err != nil {
		t.Fatal(err)
	}
	conn.SetDialTimeout(100 * time.Millisecond)

	err = conn.Connect()
	if err == nil {
		t.Fatal("expected error connecting to nonexistent socket")
	}
	if !errors.Is(err, ErrConnectionTimeout) && !errors.Is(err, ErrDaemonNotRunning) {
		t.Errorf("expected ErrConnectionTimeout or ErrDaemonNotRunning, got %v", err)
	}
}

func TestDaemonConnection_GetClient_NoDaemon(t *testing.T) {
	conn, err := NewDaemonConnection("/tmp/nonexistent-daemon-test.sock")
	if err != nil {
		t.Fatal(err)
	}
	conn.SetDialTimeout(100 * time.Millisecond)

	_, err = conn.GetClient()
	if err == nil {
		t.Fatal("expected error from GetClient with no daemon")
	}
}

func TestDaemonConnection_Health_NoDaemon(t *testing.T) {
	conn, err := NewDaemonConnection("/tmp/nonexistent-daemon-test.sock")
	if err != nil {
		t.Fatal(err)
	}
	conn.SetDialTimeout(100 * time.Millisecond)

	_, err = conn.Health()
	if err == nil {
		t.Fatal("expected error from Health with no daemon")
	}
}

func TestDaemonConnection_Reconnect_NoDaemon(t *testing.T) {
	conn, err := NewDaemonConnection("/tmp/nonexistent-daemon-test.sock")
	if err != nil {
		t.Fatal(err)
	}
	conn.SetDialTimeout(100 * time.Millisecond)

	err = conn.Reconnect()
	if err == nil {
		t.Fatal("expected error reconnecting to nonexistent socket")
	}
	if !errors.Is(err, ErrConnectionTimeout) && !errors.Is(err, ErrDaemonNotRunning) {
		t.Errorf("expected ErrConnectionTimeout or ErrDaemonNotRunning, got %v", err)
	}
}

func TestDaemonConnection_Connect_Success(t *testing.T) {
	socketPath := startMockDaemonServer(t)
	conn, err := NewDaemonConnection(socketPath)
	if err != nil {
		t.Fatal(err)
	}

	err = conn.Connect()
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	if !conn.IsConnected() {
		t.Error("expected IsConnected() = true after Connect")
	}
	if conn.Client() == nil {
		t.Error("expected non-nil Client() after Connect")
	}

	conn.Disconnect()
}

func TestDaemonConnection_GetClient_LazyConnect(t *testing.T) {
	socketPath := startMockDaemonServer(t)
	conn, err := NewDaemonConnection(socketPath)
	if err != nil {
		t.Fatal(err)
	}

	// GetClient should auto-connect
	client, err := conn.GetClient()
	if err != nil {
		t.Fatalf("GetClient() error = %v", err)
	}
	if client == nil {
		t.Error("expected non-nil client from GetClient")
	}
	if !conn.IsConnected() {
		t.Error("expected IsConnected() = true after GetClient")
	}

	conn.Disconnect()
}

func TestDaemonConnection_GetClient_AlreadyConnected(t *testing.T) {
	socketPath := startMockDaemonServer(t)
	conn, err := NewDaemonConnection(socketPath)
	if err != nil {
		t.Fatal(err)
	}

	// Connect first
	if err := conn.Connect(); err != nil {
		t.Fatal(err)
	}
	firstClient := conn.Client()

	// GetClient should return the existing client
	client, err := conn.GetClient()
	if err != nil {
		t.Fatalf("GetClient() error = %v", err)
	}
	if client != firstClient {
		t.Error("expected GetClient to return existing client")
	}

	conn.Disconnect()
}

func TestDaemonConnection_Disconnect_WithActiveConnection(t *testing.T) {
	socketPath := startMockDaemonServer(t)
	conn, err := NewDaemonConnection(socketPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := conn.Connect(); err != nil {
		t.Fatal(err)
	}
	if !conn.IsConnected() {
		t.Fatal("expected connected after Connect")
	}

	err = conn.Disconnect()
	if err != nil {
		t.Errorf("Disconnect() error = %v", err)
	}
	if conn.IsConnected() {
		t.Error("expected IsConnected() = false after Disconnect")
	}
	if conn.Client() != nil {
		t.Error("expected nil Client() after Disconnect")
	}
}

func TestDaemonConnection_SetRequestTimeout_WithClient(t *testing.T) {
	socketPath := startMockDaemonServer(t)
	conn, err := NewDaemonConnection(socketPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := conn.Connect(); err != nil {
		t.Fatal(err)
	}

	// SetRequestTimeout with active client should not panic
	newTimeout := 10 * time.Second
	conn.SetRequestTimeout(newTimeout)

	conn.mu.RLock()
	actualTimeout := conn.requestTimeout
	conn.mu.RUnlock()
	if actualTimeout != newTimeout {
		t.Errorf("requestTimeout = %v, want %v", actualTimeout, newTimeout)
	}

	conn.Disconnect()
}

func TestDaemonConnection_Health_Success(t *testing.T) {
	socketPath := startMockDaemonServer(t)
	conn, err := NewDaemonConnection(socketPath)
	if err != nil {
		t.Fatal(err)
	}

	health, err := conn.Health()
	if err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	if health == nil {
		t.Fatal("expected non-nil health response")
	}
	if health.Status != "healthy" {
		t.Errorf("expected status 'healthy', got %q", health.Status)
	}

	conn.Disconnect()
}

func TestDaemonConnection_Reconnect_Success(t *testing.T) {
	socketPath := startMockDaemonServer(t)
	conn, err := NewDaemonConnection(socketPath)
	if err != nil {
		t.Fatal(err)
	}

	// Connect first
	if err := conn.Connect(); err != nil {
		t.Fatal(err)
	}
	firstClient := conn.Client()

	// Reconnect should close old connection and establish new one
	err = conn.Reconnect()
	if err != nil {
		t.Fatalf("Reconnect() error = %v", err)
	}
	if !conn.IsConnected() {
		t.Error("expected IsConnected() = true after Reconnect")
	}

	newClient := conn.Client()
	if newClient == nil {
		t.Error("expected non-nil Client() after Reconnect")
	}
	if newClient == firstClient {
		t.Error("expected Reconnect to establish a new client")
	}

	conn.Disconnect()
}
