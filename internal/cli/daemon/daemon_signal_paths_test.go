package daemon

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

// TestSetupSignalHandler_ShutdownOnSIGTERM verifies that the shutdown channel
// returned by setupSignalHandler closes when a termination signal arrives.
func TestSetupSignalHandler_ShutdownOnSIGTERM(t *testing.T) {
	shutdown := setupSignalHandler()

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("failed to send SIGTERM to self: %v", err)
	}

	select {
	case <-shutdown:
		// expected
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown channel did not close within 3s of SIGTERM")
	}
}

// TestSetupSignalHandler_SIGUSR1DoesNotShutdown verifies that SIGUSR1 triggers
// the goroutine-dump path without closing the shutdown channel (the daemon
// must keep running after a diagnostic dump).
func TestSetupSignalHandler_SIGUSR1DoesNotShutdown(t *testing.T) {
	shutdown := setupSignalHandler()

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGUSR1); err != nil {
		t.Fatalf("failed to send SIGUSR1 to self: %v", err)
	}

	// Give the dump goroutine a moment to run, then confirm shutdown stayed open.
	select {
	case <-shutdown:
		t.Fatal("shutdown channel closed on SIGUSR1; it should only dump goroutines")
	case <-time.After(500 * time.Millisecond):
		// expected — still running
	}

	// Clean up: trip shutdown so the SIGTERM handler goroutine exits.
	_ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
	select {
	case <-shutdown:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown channel did not close after follow-up SIGTERM")
	}
}

func TestResolveDaemonPaths_DerivesSiblingsFromPIDFile(t *testing.T) {
	projectDir := t.TempDir()
	cfg := &cfgpkg.DaemonConfig{
		Daemon: cfgpkg.DaemonSettings{
			PIDFile:   ".loom/daemon.pid",
			LogDir:    ".loom/logs",
			EventsDir: ".loom/events",
		},
	}

	paths := resolveDaemonPaths(projectDir, cfg)

	wantPID := filepath.Join(projectDir, ".loom", "daemon.pid")
	if paths.pidFile != wantPID {
		t.Errorf("pidFile = %q, want %q", paths.pidFile, wantPID)
	}
	// lockFile lives next to the pid file.
	wantLock := filepath.Join(filepath.Dir(wantPID), "daemon.lock")
	if paths.lockFile != wantLock {
		t.Errorf("lockFile = %q, want %q", paths.lockFile, wantLock)
	}
	if paths.logDir == "" || paths.eventsDir == "" || paths.stateFile == "" {
		t.Errorf("expected all paths populated, got %+v", paths)
	}
	// The env snapshot lives beside the PID file so the two live and die together.
	if got, want := filepath.Dir(paths.envFile), filepath.Dir(paths.stateFile); got != want {
		t.Errorf("envFile dir = %q, want %q (beside the state/PID file)", got, want)
	}
	if got, want := filepath.Base(paths.envFile), cfgpkg.SnapshotFileName; got != want {
		t.Errorf("envFile base = %q, want %q", got, want)
	}
}

// TestInitEnvSnapshot_WritesReadableSnapshot verifies the daemon publishes the
// configuration it resolved, and that a write failure is survivable: a daemon
// must not refuse to start because it could not write a diagnostic.
func TestInitEnvSnapshot_WritesReadableSnapshot(t *testing.T) {
	t.Setenv("LOOM_WORKSPACE", "PUPPET")

	path := filepath.Join(t.TempDir(), cfgpkg.SnapshotFileName)
	initEnvSnapshot(path)

	snap, err := cfgpkg.LoadDaemonEnvSnapshot(path)
	if err != nil {
		t.Fatalf("LoadDaemonEnvSnapshot: %v", err)
	}
	if snap.PID != os.Getpid() {
		t.Errorf("pid = %d, want %d", snap.PID, os.Getpid())
	}
	if snap.Plain("LOOM_WORKSPACE") != "PUPPET" {
		t.Errorf("expected LOOM_WORKSPACE in the snapshot, got %+v", snap.Env)
	}
}

func TestInitEnvSnapshot_UnwritablePathDoesNotExit(t *testing.T) {
	// A path whose parent is a regular file cannot be created.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}

	// The bar is simply that this returns.
	initEnvSnapshot(filepath.Join(blocker, cfgpkg.SnapshotFileName))
}
