package cli

import (
	"encoding/json"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
)

// newTestFleetDBServer creates a FleetDBServer with auto-start miniredis and
// in-memory storage for testing. It registers a cleanup to stop the server.
func newTestFleetDBServer(t *testing.T) *FleetDBServer {
	t.Helper()
	socketPath := filepath.Join(t.TempDir(), "test.sock")
	srv, err := NewFleetDBServer(FleetDBServerConfig{
		AutoStart:  true,
		Workspace:  "test-workspace",
		SocketPath: socketPath,
	}, slog.Default())
	if err != nil {
		t.Fatalf("failed to create FleetDBServer: %v", err)
	}
	t.Cleanup(func() { srv.Stop() })
	return srv
}

func TestFleetDBServer_AutoStartMiniredis(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "test.sock")
	srv, err := NewFleetDBServer(FleetDBServerConfig{
		AutoStart:  true,
		RedisURL:   "",
		Workspace:  "test-workspace",
		SocketPath: socketPath,
	}, slog.Default())
	if err != nil {
		t.Fatalf("failed to create FleetDBServer: %v", err)
	}

	if srv.Backend() == nil {
		t.Error("expected Backend() to return non-nil")
	}
	if srv.FleetStore() == nil {
		t.Error("expected FleetStore() to return non-nil when AutoStart=true")
	}

	// Stop should succeed without error.
	srv.Stop()
}

func TestFleetDBServer_InMemoryStorage(t *testing.T) {
	srv := newTestFleetDBServer(t)

	backend := srv.Backend()
	if backend == nil {
		t.Fatal("expected Backend() to return non-nil")
	}

	// List issues — should return empty list for fresh in-memory storage.
	out, err := backend.RunCommand("", "list", "--json")
	if err != nil {
		t.Fatalf("unexpected error from list: %v", err)
	}
	if out != "[]" {
		t.Errorf("expected '[]' for empty issue list, got %q", out)
	}
}

func TestFleetDBServer_StopIdempotent(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "test.sock")
	srv, err := NewFleetDBServer(FleetDBServerConfig{
		AutoStart:  true,
		Workspace:  "test-workspace",
		SocketPath: socketPath,
	}, slog.Default())
	if err != nil {
		t.Fatalf("failed to create FleetDBServer: %v", err)
	}

	// Call Stop multiple times — should not panic.
	srv.Stop()
	srv.Stop()
	srv.Stop()
}

func TestFleetDBServer_BackendRoundTrip(t *testing.T) {
	srv := newTestFleetDBServer(t)

	backend := srv.Backend()
	if backend == nil {
		t.Fatal("expected Backend() to return non-nil")
	}

	// Verify list returns empty initially.
	out, err := backend.RunCommand("", "list", "--json")
	if err != nil {
		t.Fatalf("unexpected error from list: %v", err)
	}
	if out != "[]" {
		t.Errorf("expected '[]', got %q", out)
	}

	// Verify stats works and returns valid JSON.
	out, err = backend.RunCommand("", "stats", "--json")
	if err != nil {
		t.Fatalf("unexpected error from stats: %v", err)
	}
	var stats BdStats
	if err := json.Unmarshal([]byte(out), &stats); err != nil {
		t.Fatalf("failed to unmarshal stats output: %v", err)
	}
	// Fresh database should have zero issues.
	if stats.Summary.TotalIssues != 0 {
		t.Errorf("expected 0 total issues, got %d", stats.Summary.TotalIssues)
	}

	// Verify ready returns empty list.
	out, err = backend.RunCommand("", "ready", "--json")
	if err != nil {
		t.Fatalf("unexpected error from ready: %v", err)
	}
	if out != "[]" {
		t.Errorf("expected '[]' from ready, got %q", out)
	}

	// Verify blocked returns empty list.
	out, err = backend.RunCommand("", "blocked", "--json")
	if err != nil {
		t.Fatalf("unexpected error from blocked: %v", err)
	}
	if out != "[]" {
		t.Errorf("expected '[]' from blocked, got %q", out)
	}

	// Verify sync (no-op) works.
	out, err = backend.RunCommand("", "sync")
	if err != nil {
		t.Fatalf("unexpected error from sync: %v", err)
	}
	if out != "" {
		t.Errorf("expected empty string from sync, got %q", out)
	}

	// Stop the server.
	srv.Stop()
}

func TestFleetDBServer_ConfigValidation(t *testing.T) {
	t.Run("empty_socket_path", func(t *testing.T) {
		_, err := NewFleetDBServer(FleetDBServerConfig{
			Workspace:  "test",
			SocketPath: "",
		}, slog.Default())
		if err == nil {
			t.Fatal("expected error for empty SocketPath")
		}
		if err.Error() != "SocketPath is required" {
			t.Errorf("expected 'SocketPath is required', got %q", err.Error())
		}
	})

	t.Run("socket_path_too_long", func(t *testing.T) {
		longPath := "/" + strings.Repeat("a", maxUnixSocketPath)
		_, err := NewFleetDBServer(FleetDBServerConfig{
			Workspace:  "test",
			SocketPath: longPath,
		}, slog.Default())
		if err == nil {
			t.Fatal("expected error for socket path too long")
		}
		if !strings.Contains(err.Error(), "socket path too long") {
			t.Errorf("expected error containing 'socket path too long', got %q", err.Error())
		}
	})

	t.Run("empty_workspace_defaults", func(t *testing.T) {
		// An empty workspace should default to "default" and not cause an error.
		// We verify this by creating a server with empty workspace — if it starts
		// successfully, the default was applied (the server would fail if workspace
		// were truly empty since it's passed to beads.NewServer).
		socketPath := filepath.Join(t.TempDir(), "test.sock")
		srv, err := NewFleetDBServer(FleetDBServerConfig{
			AutoStart:  true,
			Workspace:  "",
			SocketPath: socketPath,
		}, slog.Default())
		if err != nil {
			t.Fatalf("expected no error with empty workspace, got: %v", err)
		}
		srv.Stop()
	})
}

func TestFleetDBServer_NoRedis(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "test.sock")
	srv, err := NewFleetDBServer(FleetDBServerConfig{
		AutoStart:  false,
		RedisURL:   "",
		Workspace:  "test-workspace",
		SocketPath: socketPath,
	}, slog.Default())
	if err != nil {
		t.Fatalf("failed to create FleetDBServer without Redis: %v", err)
	}
	t.Cleanup(func() { srv.Stop() })

	// FleetStore should be nil when no Redis is configured.
	if srv.FleetStore() != nil {
		t.Error("expected FleetStore() to return nil when AutoStart=false and RedisURL is empty")
	}

	// Backend should still work for issue operations.
	backend := srv.Backend()
	if backend == nil {
		t.Fatal("expected Backend() to return non-nil even without Redis")
	}

	// List issues — should still return empty list.
	out, err := backend.RunCommand("", "list", "--json")
	if err != nil {
		t.Fatalf("unexpected error from list: %v", err)
	}
	if out != "[]" {
		t.Errorf("expected '[]', got %q", out)
	}

	// Stats should work.
	out, err = backend.RunCommand("", "stats", "--json")
	if err != nil {
		t.Fatalf("unexpected error from stats: %v", err)
	}
	var stats BdStats
	if err := json.Unmarshal([]byte(out), &stats); err != nil {
		t.Fatalf("failed to unmarshal stats: %v", err)
	}
	if stats.Summary.TotalIssues != 0 {
		t.Errorf("expected 0 total issues, got %d", stats.Summary.TotalIssues)
	}
}
