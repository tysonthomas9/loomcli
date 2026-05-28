package daemon

import (
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
}
