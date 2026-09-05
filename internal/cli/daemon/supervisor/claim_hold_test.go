package supervisor

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/agentpolicy"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

func heldSupervisor(t *testing.T, hold *ClaimHold) (*Supervisor, *clitest.MockIssueBackend) {
	t.Helper()
	mock := clitest.NewMockIssueBackend()
	s := &Supervisor{IssueBackend: mock}
	if hold != nil {
		if err := s.SetClaimHold(hold); err != nil {
			t.Fatalf("SetClaimHold: %v", err)
		}
	}
	return s, mock
}

func newHold(actor, reason string, ttl time.Duration) *ClaimHold {
	h := &ClaimHold{Held: true, Actor: actor, Reason: reason, Since: time.Now()}
	if ttl > 0 {
		h.ExpiresAt = h.Since.Add(ttl)
	}
	return h
}

func TestGateClaimsHeld_BlocksPreFlightBeforeAnyBackendCall(t *testing.T) {
	s, mock := heldSupervisor(t, newHold("union-autodeploy", "deploy union tips", time.Hour))
	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"}}

	if s.preFlightSetup(ap) {
		t.Fatal("preFlightSetup returned true while claims are held")
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("backend was consulted while held: %#v", mock.Calls)
	}
	if ap.LastError == nil || !ap.LastError.Class.Is(agenterr.ClaimsHeldOutcome) {
		t.Fatalf("LastError = %#v, want ClaimsHeld", ap.LastError)
	}
	if ap.LastNoWork {
		t.Fatal("LastNoWork set by a claim hold; it must stay false")
	}
}

func TestGateClaimsHeld_LeavesEveryCounterUntouched(t *testing.T) {
	s, _ := heldSupervisor(t, newHold("oleh", "maintenance", time.Hour))
	ap := &AgentProcess{
		Entry:          cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"},
		RestartCount:   4,
		RateRetryCount: 2,
		NoWorkCount:    7,
		BlockCount:     1,
		StopReason:     "",
	}

	if s.gateClaimsHeld(ap) {
		t.Fatal("gateClaimsHeld returned true while held")
	}
	// The restart layer sees the gate outcome next; it must move nothing.
	ap.Mu.Lock()
	s.applyClaimsHeldRestart(ap)
	ap.Mu.Unlock()

	if ap.RestartCount != 4 || ap.RateRetryCount != 2 || ap.NoWorkCount != 7 || ap.BlockCount != 1 {
		t.Fatalf("counters moved: restart=%d rate=%d nowork=%d block=%d",
			ap.RestartCount, ap.RateRetryCount, ap.NoWorkCount, ap.BlockCount)
	}
	if ap.StopReason != "" {
		t.Fatalf("StopReason = %q, want empty (the agent is gated, not stopped)", ap.StopReason)
	}
}

func TestClaimHold_NeverWritesYieldFileOrTouchesProcess(t *testing.T) {
	dir := t.TempDir()
	s, _ := heldSupervisor(t, nil)
	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"}, WorktreePath: dir, Pid: os.Getpid()}

	if err := s.SetClaimHold(newHold("union-autodeploy", "deploy", time.Hour)); err != nil {
		t.Fatalf("SetClaimHold: %v", err)
	}
	if s.gateClaimsHeld(ap) {
		t.Fatal("gateClaimsHeld returned true while held")
	}

	if IsYieldRequested(dir) {
		t.Fatal("a claim hold wrote a yield file; it must gate the claim path only")
	}
	if ap.Pid != os.Getpid() {
		t.Fatalf("Pid changed to %d; a hold must never touch a running process", ap.Pid)
	}
	if !lockfile.IsProcessRunning(ap.Pid) {
		t.Fatal("the running process died; a hold must never signal an in-flight run")
	}
}

func TestClaimHold_ExpiredDoesNotGateAndClearsFile(t *testing.T) {
	s, mock := heldSupervisor(t, nil)
	cleared := 0
	s.PersistClaimHold = func(h *ClaimHold) error {
		if h == nil {
			cleared++
		}
		return nil
	}
	s.LoadClaimHold(&ClaimHold{
		Held: true, Actor: "oleh", Reason: "stale",
		Since:     time.Now().Add(-2 * time.Hour),
		ExpiresAt: time.Now().Add(-time.Minute),
	})

	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"}}
	if !s.gateClaimsHeld(ap) {
		t.Fatal("an expired hold gated the agent")
	}
	if s.ClaimHoldSnapshot() != nil {
		t.Fatal("expired hold was not cleared in memory")
	}
	if cleared == 0 {
		t.Fatal("expired hold did not clear the persisted file")
	}
	// The WARN-and-clear path must fire once, not per observation.
	for i := 0; i < 3; i++ {
		_ = s.ClaimHoldSnapshot()
	}
	if cleared != 1 {
		t.Fatalf("persist(nil) called %d times, want exactly 1", cleared)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("hold evaluation touched the backend: %#v", mock.Calls)
	}
}

func TestClaimHold_ReleaseReEnablesClaiming(t *testing.T) {
	s, _ := heldSupervisor(t, newHold("oleh", "deploy", time.Hour))
	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"}}
	if s.gateClaimsHeld(ap) {
		t.Fatal("gate did not hold")
	}
	if err := s.ReleaseClaimHold("oleh", false); err != nil {
		t.Fatalf("ReleaseClaimHold: %v", err)
	}
	if !s.gateClaimsHeld(ap) {
		t.Fatal("gate still held after release")
	}
}

func TestClaimHold_ForeignReleaseRefusedWithoutForce(t *testing.T) {
	s, _ := heldSupervisor(t, newHold("union-autodeploy", "deploy", time.Hour))

	err := s.ReleaseClaimHold("someone-else", false)
	if err == nil {
		t.Fatal("foreign release succeeded without --force")
	}
	if s.ClaimHoldSnapshot() == nil {
		t.Fatal("refused release still cleared the hold")
	}
	if err := s.ReleaseClaimHold("someone-else", true); err != nil {
		t.Fatalf("forced release failed: %v", err)
	}
	if s.ClaimHoldSnapshot() != nil {
		t.Fatal("forced release did not clear the hold")
	}
}

func TestClaimHold_PersistFailureStillAppliesHold(t *testing.T) {
	s, _ := heldSupervisor(t, nil)
	want := errors.New("read-only file system")
	s.PersistClaimHold = func(*ClaimHold) error { return want }

	err := s.SetClaimHold(newHold("oleh", "deploy", time.Hour))
	if !errors.Is(err, want) {
		t.Fatalf("SetClaimHold err = %v, want %v", err, want)
	}
	if h := s.ClaimHoldSnapshot(); !h.Active(time.Now()) {
		t.Fatal("hold was not applied in memory after a persist failure")
	}
}

func TestClaimHold_SnapshotIsACopy(t *testing.T) {
	s, _ := heldSupervisor(t, newHold("oleh", "deploy", time.Hour))
	snap := s.ClaimHoldSnapshot()
	snap.Actor = "mutated"
	if again := s.ClaimHoldSnapshot(); again.Actor != "oleh" {
		t.Fatalf("Actor = %q; snapshot aliased the supervisor's hold", again.Actor)
	}
}

func TestClaimHold_ActiveIsNilSafe(t *testing.T) {
	var nilHold *ClaimHold
	if nilHold.Active(time.Now()) {
		t.Fatal("nil hold reported active")
	}
	if (&ClaimHold{Held: false}).Active(time.Now()) {
		t.Fatal("released hold reported active")
	}
	indefinite := &ClaimHold{Held: true, Since: time.Now()}
	if !indefinite.Active(time.Now().Add(100 * time.Hour)) {
		t.Fatal("indefinite hold expired")
	}
}

func TestClaimHold_StatusExposesGatedFlag(t *testing.T) {
	s, _ := heldSupervisor(t, newHold("oleh", "deploy", time.Hour))
	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"}}
	s.Agents = append(s.Agents, ap)

	if s.gateClaimsHeld(ap) {
		t.Fatal("gate did not hold")
	}
	gated, running := s.claimHoldGateCounts()
	if gated != 1 {
		t.Fatalf("gated = %d, want 1", gated)
	}
	if running != 0 {
		t.Fatalf("running = %d, want 0 (no process was ever spawned)", running)
	}
	if !ap.LastError.Class.Is(agenterr.ClaimsHeldOutcome) {
		t.Fatal("ClaimsGated is derived from LastError.Class; it was not recorded")
	}
}

func TestClaimsHeld_RestartPolicyIsUncountedFixedRecheck(t *testing.T) {
	d := agentpolicy.Decide(agenterr.OutcomeFromDomain(agenterr.ClaimsHeldOutcome))
	if d.Decision != agentpolicy.RetryUncounted || d.Backoff != agentpolicy.BPClaimsHeld {
		t.Fatalf("disposition = %+v, want RetryUncounted/BPClaimsHeld", d)
	}
	if agentpolicy.QuarantineEligible(agenterr.OutcomeFromDomain(agenterr.ClaimsHeldOutcome)) {
		t.Fatal("ClaimsHeld is quarantine-eligible; a quiesce is not a task fault")
	}

	s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{})
	if got := s.claimHoldRecheckBackoff(); got != defaultClaimHoldRecheckInterval {
		t.Fatalf("default recheck = %v, want %v", got, defaultClaimHoldRecheckInterval)
	}
	s.claimHoldRecheckInterval = 10 * time.Millisecond
	if got := s.claimHoldRecheckBackoff(); got != 10*time.Millisecond {
		t.Fatalf("override recheck = %v", got)
	}

	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"},
		LastExitCode: 0,
		LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromDomain(agenterr.ClaimsHeldOutcome)},
		RestartCount: 3,
		NoWorkCount:  5,
	}
	if !s.shouldRestart(ap) {
		t.Fatal("shouldRestart = false for a claim-hold gate; the agent must keep re-checking")
	}
	if ap.RestartCount != 3 || ap.NoWorkCount != 5 || ap.StopReason != "" {
		t.Fatalf("shouldRestart moved state: restart=%d nowork=%d stop=%q",
			ap.RestartCount, ap.NoWorkCount, ap.StopReason)
	}
	if got := s.computeBackoff(ap); got != 10*time.Millisecond {
		t.Fatalf("computeBackoff = %v, want the fixed claim-hold recheck", got)
	}
}
