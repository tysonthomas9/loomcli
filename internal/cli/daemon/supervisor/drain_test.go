package supervisor

import (
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
)

// newDrainTestSupervisor creates a minimal Supervisor for drain/yield/sigterm tests.
func newDrainTestSupervisor(cfg *config.DaemonConfig) *Supervisor {
	return &Supervisor{
		ConfigSnapshot: func() *config.DaemonConfig { return cfg },
		Shutdown:       make(chan struct{}),
		StoppedAgents:  make(map[string]struct{}),
		EmitEvent:      func(events.Event) {}, // no-op for tests
	}
}

// ---------------------------------------------------------------------------
// GetYieldTimeout tests
// ---------------------------------------------------------------------------

func TestGetYieldTimeout_Default(t *testing.T) {
	s := newDrainTestSupervisor(&config.DaemonConfig{})
	got := s.GetYieldTimeout()
	want := DefaultYieldTimeout * time.Second
	if got != want {
		t.Errorf("GetYieldTimeout() = %v, want %v", got, want)
	}
}

func TestGetYieldTimeout_Custom(t *testing.T) {
	s := newDrainTestSupervisor(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{
			YieldTimeout: config.IntPtr(120),
		}},
	})
	got := s.GetYieldTimeout()
	want := 120 * time.Second
	if got != want {
		t.Errorf("GetYieldTimeout() = %v, want %v", got, want)
	}
}

func TestGetYieldTimeout_Zero(t *testing.T) {
	s := newDrainTestSupervisor(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{
			YieldTimeout: config.IntPtr(0),
		}},
	})
	got := s.GetYieldTimeout()
	want := DefaultYieldTimeout * time.Second
	if got != want {
		t.Errorf("GetYieldTimeout(0) = %v, want %v (default)", got, want)
	}
}

func TestGetYieldTimeout_Negative(t *testing.T) {
	s := newDrainTestSupervisor(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{
			YieldTimeout: config.IntPtr(-1),
		}},
	})
	got := s.GetYieldTimeout()
	want := DefaultYieldTimeout * time.Second
	if got != want {
		t.Errorf("GetYieldTimeout(-1) = %v, want %v (default)", got, want)
	}
}

// ---------------------------------------------------------------------------
// DrainWithGrace tests
// ---------------------------------------------------------------------------

func TestDrainWithGrace_PIDZero(t *testing.T) {
	s := newDrainTestSupervisor(&config.DaemonConfig{})
	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "test"},
		Pid:          0,
		WorktreePath: t.TempDir(),
	}

	start := time.Now()
	result := s.DrainWithGrace(ap, "test", 10*time.Second, 5*time.Second)
	elapsed := time.Since(start)

	if !result {
		t.Error("DrainWithGrace() = false, want true (pid already 0)")
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("DrainWithGrace took %v, want < 100ms for pid=0", elapsed)
	}

	// Verify yield file was written even though pid was 0
	if !IsYieldRequested(ap.WorktreePath) {
		t.Error("yield file should have been written before detecting pid=0")
	}
}

func TestDrainWithGrace_AgentExitsDuringYield(t *testing.T) {
	s := newDrainTestSupervisor(&config.DaemonConfig{})
	dir := t.TempDir()

	// Spawn a process that watches for the yield file, then exits
	cmd := exec.Command("bash", "-c", //nolint:norawexec
		`while [ ! -f "`+filepath.Join(dir, YieldFileName)+`" ]; do sleep 0.1; done; exit 0`)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start test process: %v", err)
	}

	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "test"},
		Cmd:          cmd,
		Pid:          cmd.Process.Pid,
		WorktreePath: dir,
	}

	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})

	// Run waitForAgent in background to clear pid when process exits
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.waitForAgent(ap)
	}()

	start := time.Now()
	result := s.DrainWithGrace(ap, "test-yield", 10*time.Second, 5*time.Second)
	elapsed := time.Since(start)

	if !result {
		t.Error("DrainWithGrace() = false, want true (agent should exit from yield file)")
	}
	if elapsed > 5*time.Second {
		t.Errorf("DrainWithGrace took %v, want < 5s", elapsed)
	}

	// Wait for waitForAgent goroutine to finish
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for waitForAgent to complete")
	}
}

func TestDrainWithGrace_AgentIgnoresYield_FallsToSIGTERM(t *testing.T) {
	s := newDrainTestSupervisor(&config.DaemonConfig{})
	dir := t.TempDir()

	// Spawn a process that does NOT read yield files
	cmd := exec.Command("sleep", "60") //nolint:norawexec
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start sleep: %v", err)
	}

	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "test"},
		Cmd:          cmd,
		Pid:          cmd.Process.Pid,
		WorktreePath: dir,
	}

	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})

	// Run waitForAgent in background to clear pid
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.waitForAgent(ap)
	}()

	// Use a short yield timeout so the test doesn't take too long
	result := s.DrainWithGrace(ap, "test-timeout", 2*time.Second, 5*time.Second)

	if result {
		t.Error("DrainWithGrace() = true, want false (agent ignores yield, should fall to SIGTERM)")
	}

	// Wait for waitForAgent goroutine to finish
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for waitForAgent to complete")
	}

	// Verify process was cleaned up
	ap.Mu.Lock()
	finalPID := ap.Pid
	ap.Mu.Unlock()
	if finalPID != 0 {
		t.Errorf("pid = %d after DrainWithGrace, want 0", finalPID)
	}
}

func TestDrainWithGrace_RequestYieldFails(t *testing.T) {
	s := newDrainTestSupervisor(&config.DaemonConfig{})

	// Spawn a real process so stopAgent has something to terminate
	cmd := exec.Command("sleep", "60") //nolint:norawexec
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start sleep: %v", err)
	}

	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "test"},
		Cmd:          cmd,
		Pid:          cmd.Process.Pid,
		WorktreePath: "/nonexistent/path", // yield file write will fail
	}

	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})

	// Run waitForAgent in background to clear pid
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.waitForAgent(ap)
	}()

	start := time.Now()
	result := s.DrainWithGrace(ap, "test-fail", 10*time.Second, 5*time.Second)
	elapsed := time.Since(start)

	if result {
		t.Error("DrainWithGrace() = true, want false (yield file write failed)")
	}
	// stopAgent has a ~5s SIGTERM window before SIGKILL, but sleep responds to SIGTERM
	// immediately, so this should complete well under 6s.
	if elapsed > 6*time.Second {
		t.Errorf("DrainWithGrace took %v, want < 6s", elapsed)
	}

	// Wait for waitForAgent goroutine to finish
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for waitForAgent to complete")
	}
}

// ---------------------------------------------------------------------------
// GetSigtermTimeout tests
// ---------------------------------------------------------------------------

func TestGetSigtermTimeout_Default(t *testing.T) {
	s := newDrainTestSupervisor(&config.DaemonConfig{})
	got := s.GetSigtermTimeout()
	want := time.Duration(DefaultSigtermTimeout) * time.Second
	if got != want {
		t.Errorf("GetSigtermTimeout() = %v, want %v", got, want)
	}
}

func TestGetSigtermTimeout_Custom(t *testing.T) {
	s := newDrainTestSupervisor(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{
			SigtermTimeout: config.IntPtr(120),
		}},
	})
	got := s.GetSigtermTimeout()
	want := 120 * time.Second
	if got != want {
		t.Errorf("GetSigtermTimeout() = %v, want %v", got, want)
	}
}

func TestGetSigtermTimeout_Zero(t *testing.T) {
	s := newDrainTestSupervisor(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{
			SigtermTimeout: config.IntPtr(0),
		}},
	})
	got := s.GetSigtermTimeout()
	want := time.Duration(DefaultSigtermTimeout) * time.Second
	if got != want {
		t.Errorf("GetSigtermTimeout(0) = %v, want %v (default)", got, want)
	}
}

func TestGetSigtermTimeout_Negative(t *testing.T) {
	s := newDrainTestSupervisor(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{
			SigtermTimeout: config.IntPtr(-1),
		}},
	})
	got := s.GetSigtermTimeout()
	want := time.Duration(DefaultSigtermTimeout) * time.Second
	if got != want {
		t.Errorf("GetSigtermTimeout(-1) = %v, want %v (default)", got, want)
	}
}

func TestGetSigtermTimeout_One(t *testing.T) {
	s := newDrainTestSupervisor(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{
			SigtermTimeout: config.IntPtr(1),
		}},
	})
	got := s.GetSigtermTimeout()
	want := 1 * time.Second
	if got != want {
		t.Errorf("GetSigtermTimeout(1) = %v, want %v", got, want)
	}
}
