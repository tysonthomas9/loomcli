package supervisor

import (
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// TestStopWithBudget_ReturnsWithinBudget_WhenGoroutineHangs is the regression
// test for the unbounded s.Wg.Wait(): a superviseAgent goroutine wedged in
// cmd.Wait() used to hang daemon shutdown indefinitely. Against the old code
// this test hangs until the Go test timeout.
func TestStopWithBudget_ReturnsWithinBudget_WhenGoroutineHangs(t *testing.T) {
	s := newDrainTestSupervisor(&config.DaemonConfig{})

	stuck := make(chan struct{})
	t.Cleanup(func() { close(stuck) }) // release the leaked goroutine
	s.Wg.Add(1)
	go func() {
		defer s.Wg.Done()
		<-stuck
	}()

	start := time.Now()
	report := s.StopWithBudget(2 * time.Second)
	elapsed := time.Since(start)

	if elapsed > 5*time.Second {
		t.Errorf("StopWithBudget took %v, want it bounded by the 2s budget", elapsed)
	}
	if report.WaitCompleted {
		t.Error("WaitCompleted = true, want false (a goroutine is still running)")
	}
	if !report.TimedOut() {
		t.Error("TimedOut() = false, want true")
	}
}

func TestStopWithBudget_NoAgents_ReturnsImmediately(t *testing.T) {
	s := newDrainTestSupervisor(&config.DaemonConfig{})

	start := time.Now()
	report := s.StopWithBudget(30 * time.Second)
	elapsed := time.Since(start)

	if elapsed > time.Second {
		t.Errorf("StopWithBudget took %v with no agents, want near-instant", elapsed)
	}
	if report.TimedOut() {
		t.Errorf("TimedOut() = true, want false: %+v", report)
	}
	if len(report.DrainOutcomes) != 0 {
		t.Errorf("DrainOutcomes = %v, want empty", report.DrainOutcomes)
	}
}

func TestStopWithBudget_Idempotent(t *testing.T) {
	s := newDrainTestSupervisor(&config.DaemonConfig{})
	_ = s.StopWithBudget(5 * time.Second)
	_ = s.StopWithBudget(5 * time.Second) // must not panic on the double close
}

// TestDrainAllWithGrace_CollectsPerAgentOutcomes is the regression test for the
// missing attribution: the 2026-08-15 incident could not say which worktree
// failed to yield because the per-agent result was discarded.
func TestDrainAllWithGrace_CollectsPerAgentOutcomes(t *testing.T) {
	s := newDrainTestSupervisor(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{
			YieldTimeout:   config.IntPtr(2),
			SigtermTimeout: config.IntPtr(5),
		}},
	})

	yielder := startYieldingAgent(t, s, "worker")
	ignorer := startIgnoringAgent(t, s, "integrator")

	outcomes, completed := s.drainAllWithGrace([]*AgentProcess{yielder, ignorer}, time.Now().Add(60*time.Second))
	if !completed {
		t.Fatal("drainAllWithGrace() completed = false, want true")
	}
	if len(outcomes) != 2 {
		t.Fatalf("got %d outcomes, want 2", len(outcomes))
	}

	byWorktree := map[string]DrainOutcome{}
	for _, o := range outcomes {
		byWorktree[o.Worktree] = o
	}
	if got := byWorktree["worker"].Phase; got != DrainPhaseYielded {
		t.Errorf("worker phase = %q, want %q", got, DrainPhaseYielded)
	}
	if got := byWorktree["integrator"].Phase; got != DrainPhaseSigterm {
		t.Errorf("integrator phase = %q, want %q", got, DrainPhaseSigterm)
	}
}

func TestDrainAllWithGrace_DeadlineExpires(t *testing.T) {
	s := newDrainTestSupervisor(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{
			YieldTimeout:   config.IntPtr(30),
			SigtermTimeout: config.IntPtr(30),
		}},
	})
	ignorer := startIgnoringAgent(t, s, "integrator")

	start := time.Now()
	outcomes, completed := s.drainAllWithGrace([]*AgentProcess{ignorer}, time.Now().Add(-time.Second))
	elapsed := time.Since(start)

	if completed {
		t.Error("completed = true, want false for an expired deadline")
	}
	if elapsed > 2*time.Second {
		t.Errorf("drainAllWithGrace took %v on an expired deadline, want prompt return", elapsed)
	}
	// The straggler is still attributable even though its drain never finished.
	if len(outcomes) != 1 || outcomes[0].Worktree != "integrator" {
		t.Errorf("outcomes = %+v, want one entry for integrator", outcomes)
	}
	if names := (StopReport{DrainOutcomes: outcomes}).StragglerWorktrees(); len(names) != 1 {
		t.Errorf("StragglerWorktrees() = %v, want [integrator]", names)
	}

	// The abandoned drain goroutine keeps touching the worktree (it clears the
	// yield file on the way out). Let it finish before t.TempDir is removed.
	settleDrain(t, ignorer)
}

// settleDrain joins the drain goroutine that an expired deadline abandoned, so
// the test does not race it. The goroutine writes the yield file into the
// worktree and clears it again on the way out; t.TempDir's RemoveAll fails with
// "directory not empty" if it lands in between.
//
// Waiting for the file to APPEAR before waiting for it to disappear is what
// makes the join ordered. An expired deadline returns drainAllWithGrace through
// waitUntil's non-blocking path, which can happen before the goroutine has even
// reached RequestYield — so absence on its own proves nothing, and the earlier
// absence-only wait returned immediately and let the cleanup race the write.
func settleDrain(t *testing.T, ap *AgentProcess) {
	t.Helper()
	// Phase 1 of the drain writes the yield file. Wait for it before killing the
	// agent: a process already dead at the drain's pid check takes the
	// already-stopped path and never writes one.
	awaitYieldFile(t, ap.WorktreePath, true, "drain goroutine never requested a yield")

	ap.Mu.Lock()
	pid := ap.Pid
	ap.Mu.Unlock()
	if pid != 0 {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}

	// DrainWithGrace's deferred ClearYieldFile is its last touch of the
	// worktree, so the file's disappearance means the goroutine is done there.
	awaitYieldFile(t, ap.WorktreePath, false, "drain goroutine did not release the worktree")
}

// awaitYieldFile blocks until the worktree's yield file matches want.
func awaitYieldFile(t *testing.T, worktree string, want bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if IsYieldRequested(worktree) == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(msg)
}

// startYieldingAgent spawns a process that exits as soon as the yield file
// appears, with waitForAgent running so the pid is cleared on exit.
func startYieldingAgent(t *testing.T, s *Supervisor, worktree string) *AgentProcess {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("bash", "-c", //nolint:norawexec
		`while [ ! -f "`+filepath.Join(dir, YieldFileName)+`" ]; do sleep 0.1; done; exit 0`)
	cmd.Dir = dir
	return startTestAgent(t, s, worktree, dir, cmd)
}

// startIgnoringAgent spawns a process that never reads the yield file, forcing
// the drain down the SIGTERM path.
func startIgnoringAgent(t *testing.T, s *Supervisor, worktree string) *AgentProcess {
	t.Helper()
	return startTestAgent(t, s, worktree, t.TempDir(), exec.Command("sleep", "60")) //nolint:norawexec
}

func startTestAgent(t *testing.T, s *Supervisor, worktree, dir string, cmd *exec.Cmd) *AgentProcess {
	t.Helper()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start test process: %v", err)
	}
	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: worktree},
		Cmd:          cmd,
		Pid:          cmd.Process.Pid,
		WorktreePath: dir,
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.waitForAgent(ap)
	}()
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		wg.Wait()
	})
	return ap
}
