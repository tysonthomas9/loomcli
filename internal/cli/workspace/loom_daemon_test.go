package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestEnsureLoomDaemonRunning_SerializesConcurrentCallsForSameWsDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	validLoomYaml(t, dir)

	const timeout = 300 * time.Millisecond
	var wg sync.WaitGroup
	wg.Add(2)
	errs := make([]error, 2)
	start := time.Now()
	for i := 0; i < 2; i++ {
		go func(idx int) {
			defer wg.Done()
			errs[idx] = EnsureLoomDaemonRunning(context.Background(), dir, timeout)
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)

	for i, err := range errs {
		if err == nil {
			t.Errorf("goroutine %d: expected timeout error, got nil", i)
		} else if !strings.Contains(err.Error(), "did not become ready") {
			t.Errorf("goroutine %d: expected 'did not become ready', got: %v", i, err)
		}
	}
	// Both calls run sequentially under the mutex; each waits its full timeout
	// because the test binary spawned as "loom daemon" never writes state. So
	// total elapsed should be ~2× timeout. Allow a 50ms slack floor for jitter.
	if elapsed < 2*timeout-150*time.Millisecond {
		t.Errorf("expected serialized execution (~%v), got %v — calls appear to run in parallel", 2*timeout, elapsed)
	}
	if elapsed > 3*timeout {
		t.Errorf("elapsed %v exceeds 3× timeout (%v) — scheduler stall or unrelated bug", elapsed, 3*timeout)
	}
}

func TestEnsureLoomDaemonRunning_DoesNotSerializeDifferentWsDirs(t *testing.T) {
	t.Parallel()
	dirA := t.TempDir()
	dirB := t.TempDir()
	validLoomYaml(t, dirA)
	validLoomYaml(t, dirB)

	const timeout = 300 * time.Millisecond
	var wg sync.WaitGroup
	wg.Add(2)
	start := time.Now()
	go func() {
		defer wg.Done()
		_ = EnsureLoomDaemonRunning(context.Background(), dirA, timeout)
	}()
	go func() {
		defer wg.Done()
		_ = EnsureLoomDaemonRunning(context.Background(), dirB, timeout)
	}()
	wg.Wait()
	elapsed := time.Since(start)

	// Different wsDirs must run in parallel; total elapsed should be ~1× timeout.
	if elapsed > 9*timeout/5 {
		t.Errorf("expected parallel execution for different wsDirs (~%v), got %v — calls appear to be serialized", timeout, elapsed)
	}
}

func TestEnsureLoomDaemonRunning_CleanedPathsShareLock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	validLoomYaml(t, dir)

	const timeout = 300 * time.Millisecond
	var wg sync.WaitGroup
	wg.Add(2)
	start := time.Now()
	go func() {
		defer wg.Done()
		_ = EnsureLoomDaemonRunning(context.Background(), dir, timeout)
	}()
	go func() {
		defer wg.Done()
		_ = EnsureLoomDaemonRunning(context.Background(), dir+string(filepath.Separator), timeout)
	}()
	wg.Wait()
	elapsed := time.Since(start)

	// Trailing-slash variant must hash to the same lock under filepath.Clean,
	// so the two calls serialize and elapsed ≈ 2× timeout.
	if elapsed < 2*timeout-150*time.Millisecond {
		t.Errorf("expected serialized execution under cleaned path (~%v), got %v — trailing slash not normalized", 2*timeout, elapsed)
	}
	if elapsed > 3*timeout {
		t.Errorf("elapsed %v exceeds 3× timeout (%v) — scheduler stall or unrelated bug", elapsed, 3*timeout)
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
