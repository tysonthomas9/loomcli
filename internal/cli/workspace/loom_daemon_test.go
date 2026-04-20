package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnsureLoomDaemonRunning_NoLoomYaml(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := EnsureLoomDaemonRunning(context.Background(), dir, 100*time.Millisecond); err != nil {
		t.Fatalf("expected nil (skip when no loom.yaml), got %v", err)
	}
}

func TestEnsureLoomDaemonRunning_ZeroAgents(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	yaml := "daemon:\n  pid_file: .loom/daemon.pid\nagents: []\n"
	if err := os.WriteFile(filepath.Join(dir, "loom.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureLoomDaemonRunning(context.Background(), dir, 100*time.Millisecond); err != nil {
		t.Fatalf("expected nil (skip with zero agents), got %v", err)
	}
}

func TestEnsureLoomDaemonRunning_ContextCancelled(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := EnsureLoomDaemonRunning(ctx, dir, 1*time.Second)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("error should mention 'cancelled', got: %v", err)
	}
}

// validLoomYaml writes a loom.yaml with agents configured. The daemon will
// fail to actually supervise (the test binary isn't loom), but that's fine —
// we only need LoadDaemonConfig to succeed and agents to be non-empty so the
// function reaches the spawn + poll path.
func validLoomYaml(t *testing.T, dir string) {
	t.Helper()
	yaml := "daemon:\n  pid_file: .loom/daemon.pid\nagents:\n  - worktree: fake\n    role: task\n"
	if err := os.WriteFile(filepath.Join(dir, "loom.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureLoomDaemonRunning_TimeoutFallback(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	validLoomYaml(t, dir)

	// os.Executable() returns the test binary; running it with "daemon" will
	// exit quickly without writing daemon-agents.json, so the poll deadline trips.
	err := EnsureLoomDaemonRunning(context.Background(), dir, 400*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "did not become ready") {
		t.Errorf("error should mention 'did not become ready', got: %v", err)
	}
}

func TestEnsureLoomDaemonRunning_PollLoopCancelled(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	validLoomYaml(t, dir)

	// Kick off with a long timeout, then cancel the context shortly after —
	// this exercises the `case <-ctx.Done()` branch inside the select.
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	err := EnsureLoomDaemonRunning(ctx, dir, 10*time.Second)
	if err == nil {
		t.Fatal("expected cancellation error from poll loop")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("error should mention 'cancelled', got: %v", err)
	}
}

func TestEnsureLoomDaemonRunning_ClearsStaleStateFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	validLoomYaml(t, dir)

	// Pre-create a stale daemon-agents.json (as if a previous daemon crashed).
	loomSubdir := filepath.Join(dir, ".loom")
	if err := os.MkdirAll(loomSubdir, 0700); err != nil {
		t.Fatal(err)
	}
	stalePath := filepath.Join(loomSubdir, "daemon-agents.json")
	if err := os.WriteFile(stalePath, []byte(`{"pid":99999}`), 0600); err != nil {
		t.Fatal(err)
	}

	// Short timeout — we expect timeout, but critically the stale file must be
	// removed before the poll starts. If not, the poll would see the stale
	// file immediately and falsely report ready.
	err := EnsureLoomDaemonRunning(context.Background(), dir, 300*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error (spawned test binary never writes state file)")
	}
	if !strings.Contains(err.Error(), "did not become ready") {
		t.Errorf("expected 'did not become ready', got: %v", err)
	}
}

func TestStopLoomDaemonForWorkspace_EmptyDir(t *testing.T) {
	t.Parallel()
	// Should not panic with empty dir.
	StopLoomDaemonForWorkspace("")
}

func TestStopLoomDaemonForWorkspace_NotRunning(t *testing.T) {
	t.Parallel()
	// Should not panic when no daemon PID file present.
	StopLoomDaemonForWorkspace(t.TempDir())
}
