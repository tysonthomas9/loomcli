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

func TestClient_DocumentConcurrencySafety(t *testing.T) {
	t.Parallel()

	// Document: Client is NOT goroutine-safe
	// Each goroutine should use its own Client instance
	// This test documents this behavior rather than testing it

	t.Log("Note: Client is not goroutine-safe. Use separate Client instances per goroutine.")
}
