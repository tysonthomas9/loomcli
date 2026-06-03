package flue

import (
	"log/slog"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

func TestFlueServerEnv(t *testing.T) {
	env := flueServerEnv(4321, "/work/tree", "openai-codex/gpt-5.5")
	has := func(kv string) bool {
		for _, e := range env {
			if e == kv {
				return true
			}
		}
		return false
	}
	if !has("PORT=4321") {
		t.Error("missing PORT=4321")
	}
	if !has("FLUE_MODE=local") {
		t.Error("missing FLUE_MODE=local")
	}
	if !has("LOOM_WORKTREE_PATH=/work/tree") {
		t.Error("missing LOOM_WORKTREE_PATH")
	}
	if !has("LOOM_FLUE_MODEL=openai-codex/gpt-5.5") {
		t.Error("missing LOOM_FLUE_MODEL")
	}

	// Empty model must not add a LOOM_FLUE_MODEL entry.
	env2 := flueServerEnv(1, "/w", "")
	for _, e := range env2 {
		if strings.HasPrefix(e, "LOOM_FLUE_MODEL=") {
			t.Errorf("LOOM_FLUE_MODEL should be omitted when model empty, got %q", e)
		}
	}
}

func TestServerStopTerminatesProcessAndIsIdempotent(t *testing.T) {
	sleepBin, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep not available on PATH")
	}
	// A real subprocess is required here: this test verifies Stop actually
	// terminates the spawned process group, which a DI fake cannot exercise.
	cmd := exec.Command(sleepBin, "60") //nolint:norawexec // intentional real process to test Stop()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid
	s := &Server{cmd: cmd, logger: slog.Default(), done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(s.done)
	}()

	start := time.Now()
	s.Stop()
	if time.Since(start) > 6*time.Second {
		t.Fatalf("Stop took too long: %v", time.Since(start))
	}

	// done must be closed (Stop waits on it).
	select {
	case <-s.done:
	default:
		t.Fatal("done channel not closed after Stop")
	}

	// Give the OS a beat, then confirm the process is gone.
	time.Sleep(50 * time.Millisecond)
	if lockfile.IsProcessRunning(pid) {
		t.Errorf("process %d still running after Stop", pid)
	}

	// Idempotent + nil-safe.
	s.Stop()
	var ns *Server
	ns.Stop()
}
