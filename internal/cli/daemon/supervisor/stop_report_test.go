package supervisor

import (
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// ---------------------------------------------------------------------------
// ShutdownBudget
// ---------------------------------------------------------------------------

// TestShutdownBudget_ExceedsDrainCaps is the regression test for the defect
// where a 30s watchdog guarded work whose own legitimate worst case was 60s.
func TestShutdownBudget_ExceedsDrainCaps(t *testing.T) {
	s := newDrainTestSupervisor(&config.DaemonConfig{})
	yield, sigterm := s.cappedDrainTimeouts()
	budget := s.ShutdownBudget()
	if budget <= yield+sigterm {
		t.Errorf("ShutdownBudget() = %v, want > yield+sigterm (%v)", budget, yield+sigterm)
	}
}

func TestShutdownBudget_CapsApplied(t *testing.T) {
	s := newDrainTestSupervisor(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{
			YieldTimeout:   config.IntPtr(300),
			SigtermTimeout: config.IntPtr(300),
		}},
	})
	want := shutdownDrainCap + shutdownDrainCap + drainSlack
	if got := s.ShutdownBudget(); got != want {
		t.Errorf("ShutdownBudget() = %v, want %v (caps applied)", got, want)
	}
}

func TestShutdownBudget_ZeroConfig_UsesDefaults(t *testing.T) {
	s := newDrainTestSupervisor(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{
			YieldTimeout:   config.IntPtr(0),
			SigtermTimeout: config.IntPtr(-1),
		}},
	})
	if got := s.ShutdownBudget(); got <= 0 {
		t.Errorf("ShutdownBudget() = %v, want > 0", got)
	}
}

// ---------------------------------------------------------------------------
// StopReport
// ---------------------------------------------------------------------------

func TestStopReport_StragglerWorktrees(t *testing.T) {
	cases := []struct {
		phase     DrainPhase
		straggler bool
	}{
		{DrainPhaseAlreadyStopped, false},
		{DrainPhaseYielded, false},
		{DrainPhaseSigterm, true},
		{DrainPhaseYieldWriteFail, true},
		{DrainPhaseUnfinished, true},
	}
	for _, tc := range cases {
		t.Run(string(tc.phase), func(t *testing.T) {
			r := StopReport{DrainOutcomes: []DrainOutcome{{Worktree: "w", Phase: tc.phase}}}
			got := r.StragglerWorktrees()
			if tc.straggler && (len(got) != 1 || got[0] != "w") {
				t.Errorf("StragglerWorktrees() = %v, want [w] for phase %q", got, tc.phase)
			}
			if !tc.straggler && len(got) != 0 {
				t.Errorf("StragglerWorktrees() = %v, want empty for phase %q", got, tc.phase)
			}
		})
	}
}

func TestStopReport_TimedOut(t *testing.T) {
	cases := []struct {
		drain, wait, want bool
	}{
		{true, true, false},
		{false, true, true},
		{true, false, true},
		{false, false, true},
	}
	for _, tc := range cases {
		r := StopReport{DrainCompleted: tc.drain, WaitCompleted: tc.wait}
		if got := r.TimedOut(); got != tc.want {
			t.Errorf("TimedOut() with drain=%v wait=%v = %v, want %v", tc.drain, tc.wait, got, tc.want)
		}
	}
}

func TestStopReport_LogAttrs_IncludesStragglers(t *testing.T) {
	r := StopReport{
		Budget:        75 * time.Second,
		DrainOutcomes: []DrainOutcome{{Worktree: "integrator", Phase: DrainPhaseSigterm}},
	}
	attrs := r.LogAttrs()
	if len(attrs)%2 != 0 {
		t.Fatalf("LogAttrs() has odd length %d, want key/value pairs", len(attrs))
	}
	found := false
	for i := 0; i < len(attrs); i += 2 {
		if attrs[i] == "stragglers" {
			names, ok := attrs[i+1].([]string)
			if !ok || len(names) != 1 || names[0] != "integrator" {
				t.Errorf("stragglers = %v, want [integrator]", attrs[i+1])
			}
			found = true
		}
	}
	if !found {
		t.Error("LogAttrs() has no stragglers key")
	}
}

// ---------------------------------------------------------------------------
// waitUntil
// ---------------------------------------------------------------------------

func TestWaitUntil_DeadlineInPast(t *testing.T) {
	never := make(chan struct{})
	start := time.Now()
	if waitUntil(never, time.Now().Add(-time.Second)) {
		t.Error("waitUntil() = true, want false for an expired deadline")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("waitUntil blocked for %v on an expired deadline", elapsed)
	}
}

func TestWaitUntil_DeadlineInPast_DoneAlreadyClosed(t *testing.T) {
	done := make(chan struct{})
	close(done)
	if !waitUntil(done, time.Now().Add(-time.Second)) {
		t.Error("waitUntil() = false, want true when done is already closed")
	}
}
