package daemon

import (
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
