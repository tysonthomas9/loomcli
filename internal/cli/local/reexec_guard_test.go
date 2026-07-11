package local

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The local runtime re-execs this same binary as `loom serve` / `loom daemon` /
// `loom local service`. Under `go test`, os.Executable() is local.test, so an
// unguarded re-exec runs `local.test daemon`, which re-runs the whole suite and
// re-enters the spawn path — a fork bomb that crashes the host (the supervisor
// package hit the same trap). guardLoomReexec must turn that into a fast error.

func TestGuardLoomReexecRejectsTestBinary(t *testing.T) {
	bombs := []string{"local.test", "/tmp/go-build123/b001/local.test", "/x/y/supervisor.test"}
	for _, exe := range bombs {
		if err := guardLoomReexec(exe); err == nil {
			t.Errorf("guardLoomReexec(%q) = nil, want fork-bomb error", exe)
		}
	}
	real := []string{"loom", "/usr/local/bin/loom", "/x/y/loom"}
	for _, exe := range real {
		if err := guardLoomReexec(exe); err != nil {
			t.Errorf("guardLoomReexec(%q) = %v, want nil", exe, err)
		}
	}
}

// TestRunLocalDaemonOnceRefusesTestBinaryReexec exercises the exact path that
// fork-bombed: runLocalDaemonOnce with the test binary as the executable. With
// the guard it must return an error WITHOUT spawning. If the guard regresses,
// this test fork-bombs (caught by the podman pids-limit / make test-forkwatch).
func TestRunLocalDaemonOnceRefusesTestBinaryReexec(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	if !strings.HasSuffix(filepath.Base(exe), ".test") {
		t.Skipf("test executable %q is not a *.test binary; guard not exercised", exe)
	}
	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, logsDirName), 0o755); err != nil {
		t.Fatalf("mkdir logs: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = runLocalDaemonOnce(ctx, dataDir, exe, 0, "WS")
	if err == nil || !strings.Contains(err.Error(), "fork-bomb guard") {
		t.Fatalf("runLocalDaemonOnce(testBinary): err = %v, want a fork-bomb guard error", err)
	}
}
