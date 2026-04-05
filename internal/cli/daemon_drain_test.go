package cli

import (
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// getYieldTimeout tests
// ---------------------------------------------------------------------------

func TestGetYieldTimeout_Default(t *testing.T) {
	d := &Daemon{config: &DaemonConfig{}}
	got := d.getYieldTimeout()
	want := DefaultYieldTimeout * time.Second
	if got != want {
		t.Errorf("getYieldTimeout() = %v, want %v", got, want)
	}
}

func TestGetYieldTimeout_Custom(t *testing.T) {
	d := &Daemon{config: &DaemonConfig{
		Daemon: DaemonSettings{RestartPolicy: RestartPolicy{
			YieldTimeout: intPtr(120),
		}},
	}}
	got := d.getYieldTimeout()
	want := 120 * time.Second
	if got != want {
		t.Errorf("getYieldTimeout() = %v, want %v", got, want)
	}
}

func TestGetYieldTimeout_Zero(t *testing.T) {
	d := &Daemon{config: &DaemonConfig{
		Daemon: DaemonSettings{RestartPolicy: RestartPolicy{
			YieldTimeout: intPtr(0),
		}},
	}}
	got := d.getYieldTimeout()
	want := DefaultYieldTimeout * time.Second
	if got != want {
		t.Errorf("getYieldTimeout(0) = %v, want %v (default)", got, want)
	}
}

func TestGetYieldTimeout_Negative(t *testing.T) {
	d := &Daemon{config: &DaemonConfig{
		Daemon: DaemonSettings{RestartPolicy: RestartPolicy{
			YieldTimeout: intPtr(-1),
		}},
	}}
	got := d.getYieldTimeout()
	want := DefaultYieldTimeout * time.Second
	if got != want {
		t.Errorf("getYieldTimeout(-1) = %v, want %v (default)", got, want)
	}
}

// ---------------------------------------------------------------------------
// DrainWithGrace tests
// ---------------------------------------------------------------------------

func TestDrainWithGrace_PIDZero(t *testing.T) {
	d := &Daemon{config: &DaemonConfig{}}
	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "test"},
		pid:          0,
		worktreePath: t.TempDir(),
	}

	start := time.Now()
	result := d.DrainWithGrace(ap, "test", 10*time.Second)
	elapsed := time.Since(start)

	if !result {
		t.Error("DrainWithGrace() = false, want true (pid already 0)")
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("DrainWithGrace took %v, want < 100ms for pid=0", elapsed)
	}

	// Verify yield file was written even though pid was 0
	if !IsYieldRequested(ap.worktreePath) {
		t.Error("yield file should have been written before detecting pid=0")
	}
}

func TestDrainWithGrace_AgentExitsDuringYield(t *testing.T) {
	d := &Daemon{config: &DaemonConfig{}}
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
		entry:        AgentEntry{Worktree: "test"},
		cmd:          cmd,
		pid:          cmd.Process.Pid,
		worktreePath: dir,
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
		d.waitForAgent(ap)
	}()

	start := time.Now()
	result := d.DrainWithGrace(ap, "test-yield", 10*time.Second)
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
	d := &Daemon{config: &DaemonConfig{}}
	dir := t.TempDir()

	// Spawn a process that does NOT read yield files
	cmd := exec.Command("sleep", "60") //nolint:norawexec
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start sleep: %v", err)
	}

	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "test"},
		cmd:          cmd,
		pid:          cmd.Process.Pid,
		worktreePath: dir,
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
		d.waitForAgent(ap)
	}()

	// Use a short yield timeout so the test doesn't take too long
	result := d.DrainWithGrace(ap, "test-timeout", 2*time.Second)

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
	ap.mu.Lock()
	finalPID := ap.pid
	ap.mu.Unlock()
	if finalPID != 0 {
		t.Errorf("pid = %d after DrainWithGrace, want 0", finalPID)
	}
}

func TestDrainWithGrace_RequestYieldFails(t *testing.T) {
	d := &Daemon{config: &DaemonConfig{}}

	// Spawn a real process so stopAgent has something to terminate
	cmd := exec.Command("sleep", "60") //nolint:norawexec
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start sleep: %v", err)
	}

	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "test"},
		cmd:          cmd,
		pid:          cmd.Process.Pid,
		worktreePath: "/nonexistent/path", // yield file write will fail
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
		d.waitForAgent(ap)
	}()

	start := time.Now()
	result := d.DrainWithGrace(ap, "test-fail", 10*time.Second)
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
// overlayRestartPolicy YieldTimeout tests
// ---------------------------------------------------------------------------

func TestOverlayRestartPolicy_YieldTimeout(t *testing.T) {
	t.Run("src sets YieldTimeout on nil dst", func(t *testing.T) {
		dst := RestartPolicy{}
		src := RestartPolicy{YieldTimeout: intPtr(120)}
		overlayRestartPolicy(&dst, &src)

		if dst.YieldTimeout == nil {
			t.Fatal("dst.YieldTimeout is nil, want 120")
		}
		if *dst.YieldTimeout != 120 {
			t.Errorf("dst.YieldTimeout = %d, want 120", *dst.YieldTimeout)
		}
	})

	t.Run("src nil does not overwrite dst", func(t *testing.T) {
		dst := RestartPolicy{YieldTimeout: intPtr(90)}
		src := RestartPolicy{} // YieldTimeout is nil
		overlayRestartPolicy(&dst, &src)

		if dst.YieldTimeout == nil {
			t.Fatal("dst.YieldTimeout became nil, should remain 90")
		}
		if *dst.YieldTimeout != 90 {
			t.Errorf("dst.YieldTimeout = %d, want 90 (should not be overwritten)", *dst.YieldTimeout)
		}
	})

	t.Run("src overwrites existing dst", func(t *testing.T) {
		dst := RestartPolicy{YieldTimeout: intPtr(60)}
		src := RestartPolicy{YieldTimeout: intPtr(180)}
		overlayRestartPolicy(&dst, &src)

		if dst.YieldTimeout == nil {
			t.Fatal("dst.YieldTimeout is nil, want 180")
		}
		if *dst.YieldTimeout != 180 {
			t.Errorf("dst.YieldTimeout = %d, want 180", *dst.YieldTimeout)
		}
	})
}

// ---------------------------------------------------------------------------
// applyRestartPolicyDefaults YieldTimeout test
// ---------------------------------------------------------------------------

func TestApplyRestartPolicyDefaults_YieldTimeout(t *testing.T) {
	t.Run("nil gets default", func(t *testing.T) {
		rp := RestartPolicy{}
		applyRestartPolicyDefaults(&rp)

		if rp.YieldTimeout == nil {
			t.Fatal("YieldTimeout is nil after applyDefaults, want DefaultYieldTimeout")
		}
		if *rp.YieldTimeout != DefaultYieldTimeout {
			t.Errorf("YieldTimeout = %d, want %d", *rp.YieldTimeout, DefaultYieldTimeout)
		}
	})

	t.Run("already set is preserved", func(t *testing.T) {
		rp := RestartPolicy{YieldTimeout: intPtr(200)}
		applyRestartPolicyDefaults(&rp)

		if *rp.YieldTimeout != 200 {
			t.Errorf("YieldTimeout = %d, want 200 (should not be overwritten)", *rp.YieldTimeout)
		}
	})
}
