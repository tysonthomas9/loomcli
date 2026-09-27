package supervisor

import (
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
)

// authOutageErr is the exact shape fleet-db returned throughout the
// 2026-08-27 outage: a KindUnavailable carrying an auth rejection.
func authOutageErr() error {
	return backend.ErrUnavailable("List", "authentication failed: workspace access denied", nil)
}

func outageAgent() *AgentProcess {
	return &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"},
		RoleConfig: cfgpkg.RoleConfig{},
	}
}

// The claim preflight is where the outage entered the restart policy: a failed
// Ready query was classified Unknown, which is a COUNTED retry, so a fault
// shared by every agent eroded every agent's budget in lockstep.
func TestClaimTask_IssueBackendOutage_ClassifiesAsInfrastructure(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.ReadyErr = authOutageErr()
	s := &Supervisor{IssueBackend: mock}
	ap := outageAgent()

	if s.claimTask(ap, "") {
		t.Fatal("claimTask must fail when the ready query fails")
	}
	if ap.LastError == nil {
		t.Fatal("claimTask must record the failure")
	}
	if !ap.LastError.Class.Is(agenterr.IssueBackendOutageOutcome) {
		t.Fatalf("class = %v, want IssueBackendOutage", ap.LastError.Class)
	}
}

// The narrowness matters as much as the classification: a backend that ANSWERS
// with an error is not an outage, and must keep eroding the budget.
func TestClaimTask_NonOutageBackendError_StaysUnknown(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.ReadyErr = backend.ErrInternal("List", "malformed response", nil)
	s := &Supervisor{IssueBackend: mock}
	ap := outageAgent()

	if s.claimTask(ap, "") {
		t.Fatal("claimTask must fail when the ready query fails")
	}
	if !ap.LastError.Class.IsClass(wrapper.ErrUnknown) {
		t.Fatalf("class = %v, want Unknown", ap.LastError.Class)
	}
}

// The incident in one test. Nine agents took three block cycles to reach
// StopReasonFastFail; with the outage classified as infrastructure, no number
// of consecutive failures spends the budget or stops the supervisor.
func TestShouldRestart_IssueBackendOutage_NeverExhaustsTheBudget(t *testing.T) {
	maxRetries := 3
	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig {
			return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{
				RestartPolicy: cfgpkg.RestartPolicy{MaxRetries: &maxRetries},
			}}
		},
	}
	ap := outageAgent()
	ap.LastExitCode = 1
	ap.LastError = &agenterr.AgentError{
		Class:   agenterr.OutcomeFromDomain(agenterr.IssueBackendOutageOutcome),
		Message: "ready query failed: issue backend unavailable",
	}

	for i := 0; i < 20; i++ {
		if !s.shouldRestart(ap) {
			t.Fatalf("attempt %d: shouldRestart = false, want true (an outage is not the agent's fault)", i+1)
		}
		if ap.RestartCount != 0 {
			t.Fatalf("attempt %d: RestartCount = %d, want 0", i+1, ap.RestartCount)
		}
		if ap.BlockCount != 0 {
			t.Fatalf("attempt %d: BlockCount = %d, want 0", i+1, ap.BlockCount)
		}
		if ap.StopReason != StopReasonIssueBackendUnavailable {
			t.Fatalf("attempt %d: StopReason = %q, want %q", i+1, ap.StopReason, StopReasonIssueBackendUnavailable)
		}
	}
}

// A pre-outage failure streak must not survive the outage: the agent gets a
// whole budget back once the backend is the thing that is broken.
func TestShouldRestart_IssueBackendOutage_ResetsAPriorFailureStreak(t *testing.T) {
	maxRetries := 3
	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig {
			return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{
				RestartPolicy: cfgpkg.RestartPolicy{MaxRetries: &maxRetries},
			}}
		},
	}
	ap := outageAgent()
	ap.RestartCount = 3
	ap.LastExitCode = 1
	ap.LastError = &agenterr.AgentError{
		Class: agenterr.OutcomeFromDomain(agenterr.IssueBackendOutageOutcome),
	}

	if !s.shouldRestart(ap) {
		t.Fatal("shouldRestart = false, want true")
	}
	if ap.RestartCount != 0 {
		t.Fatalf("RestartCount = %d, want 0", ap.RestartCount)
	}
}

func TestComputeBackoff_IssueBackendOutage_IsAFixedRecheck(t *testing.T) {
	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{} },
	}
	ap := outageAgent()
	ap.RestartCount = 9 // an exponential profile would be minutes by now
	ap.LastError = &agenterr.AgentError{
		Class: agenterr.OutcomeFromDomain(agenterr.IssueBackendOutageOutcome),
	}

	if got := s.computeBackoff(ap); got != issueBackendOutageRecheckInterval {
		t.Fatalf("backoff = %v, want the fixed %v recheck", got, issueBackendOutageRecheckInterval)
	}

	s.backendRecheckInterval = 10 * time.Millisecond
	if got := s.computeBackoff(ap); got != 10*time.Millisecond {
		t.Fatalf("backoff = %v, want the configured override", got)
	}
}

// --- fleet stall / re-arm -------------------------------------------------

// stalledSupervisor builds a supervisor whose re-armed agents exit their
// supervise loop immediately (the concurrency tracker is closed), so a test can
// observe that the goroutine really was restarted without spawning anything.
func stalledSupervisor(mock *clitest.MockIssueBackend) *Supervisor {
	ct := NewConcurrencyTracker(nil)
	ct.Close()
	return &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{} },
		Shutdown:       make(chan struct{}),
		StoppedAgents:  make(map[string]struct{}),
		Concurrency:    ct,
		EmitEvent:      func(events.Event) {},
		IssueBackend:   mock,
	}
}

// stoppedAgent is an agent whose supervise goroutine has already exited with
// the given terminal stop reason — the state all nine agents were left in.
func stoppedAgent(name string, reason StopReason) *AgentProcess {
	ap := &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: name, Role: "task"},
		RoleConfig: cfgpkg.RoleConfig{},
		StopCh:     make(chan struct{}),
		Done:       make(chan struct{}),
		StopReason: reason,
	}
	close(ap.Done)
	return ap
}

func waitForDone(t *testing.T, ap *AgentProcess) {
	t.Helper()
	ap.Mu.Lock()
	done := ap.Done
	ap.Mu.Unlock()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("re-armed supervise goroutine did not run")
	}
}

func TestCheckFleetStall_RearmsFastFailedAgentsOnceWorkIsVisible(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{{ID: "task-1", IssueType: "task", Status: "open"}}
	s := stalledSupervisor(mock)
	ap := stoppedAgent("falcon", StopReasonFastFail)
	ap.RestartCount = 7
	ap.BlockCount = 3
	s.Agents = []*AgentProcess{ap}

	s.checkFleetStall()

	waitForDone(t, ap)
	s.Wg.Wait()
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.RestartCount != 0 || ap.BlockCount != 0 {
		t.Fatalf("re-armed agent kept its counters: RestartCount=%d BlockCount=%d", ap.RestartCount, ap.BlockCount)
	}
	if ap.StopReason != StopReasonShutdown {
		t.Fatalf("StopReason = %q, want the re-armed loop's own exit reason", ap.StopReason)
	}
}

func TestCheckFleetStall_DoesNotRearmWhileTheBackendIsStillDown(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.ReadyErr = authOutageErr()
	s := stalledSupervisor(mock)
	ap := stoppedAgent("falcon", StopReasonFastFail)
	s.Agents = []*AgentProcess{ap}

	s.checkFleetStall()
	s.Wg.Wait()

	if ap.StopReason != StopReasonFastFail {
		t.Fatalf("StopReason = %q, want the agent left untouched until the outage clears", ap.StopReason)
	}
}

func TestCheckFleetStall_LeavesAnIdleFleetAlone(t *testing.T) {
	mock := clitest.NewMockIssueBackend() // reachable, empty queue
	s := stalledSupervisor(mock)
	ap := stoppedAgent("falcon", StopReasonFastFail)
	s.Agents = []*AgentProcess{ap}

	s.checkFleetStall()
	s.Wg.Wait()

	if ap.StopReason != StopReasonFastFail {
		t.Fatalf("StopReason = %q, want no re-arm with an empty ready queue", ap.StopReason)
	}
}

// A stall is a FLEET condition: as long as one agent is still supervising,
// the others' terminal stops are the policy working, not an outage.
func TestCheckFleetStall_DoesNothingWhileAnAgentIsStillSupervising(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{{ID: "task-1", IssueType: "task", Status: "open"}}
	s := stalledSupervisor(mock)
	stopped := stoppedAgent("falcon", StopReasonFastFail)
	live := &AgentProcess{
		Entry:  cfgpkg.AgentEntry{Worktree: "hawk", Role: "task"},
		StopCh: make(chan struct{}),
		Done:   make(chan struct{}), // still open: goroutine alive
	}
	s.Agents = []*AgentProcess{stopped, live}

	s.checkFleetStall()
	s.Wg.Wait()

	if stopped.StopReason != StopReasonFastFail {
		t.Fatalf("StopReason = %q, want no re-arm while the fleet has a live agent", stopped.StopReason)
	}
}

// Operator intent and human-actionable faults are never overridden.
func TestCheckFleetStall_NeverRearmsOperatorOrFatalStops(t *testing.T) {
	for _, reason := range []StopReason{
		StopReasonManualStop, StopReasonConfigRemoved, StopReasonShutdown,
		StopReasonFatalError, StopReasonEphemeralDone, StopReasonWatchdog,
	} {
		t.Run(string(reason), func(t *testing.T) {
			mock := clitest.NewMockIssueBackend()
			mock.ReadyResult = []backend.IssueData{{ID: "task-1", IssueType: "task", Status: "open"}}
			s := stalledSupervisor(mock)
			ap := stoppedAgent("falcon", reason)
			s.Agents = []*AgentProcess{ap}

			s.checkFleetStall()
			s.Wg.Wait()

			if ap.StopReason != reason {
				t.Fatalf("StopReason = %q, want %q left untouched", ap.StopReason, reason)
			}
		})
	}
}

func TestCheckFleetStall_RearmIsRateLimited(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{{ID: "task-1", IssueType: "task", Status: "open"}}
	s := stalledSupervisor(mock)
	ap := stoppedAgent("falcon", StopReasonFastFail)
	s.Agents = []*AgentProcess{ap}

	s.checkFleetStall()
	waitForDone(t, ap)
	s.Wg.Wait()

	// The re-armed loop exited immediately (closed concurrency tracker), so the
	// fleet is stalled again — but the cooldown must hold the next re-arm off.
	ap.Mu.Lock()
	ap.StopReason = StopReasonFastFail
	first := ap.Done
	ap.Mu.Unlock()

	s.checkFleetStall()
	s.Wg.Wait()

	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.Done != first {
		t.Fatal("second re-arm fired inside the cooldown window")
	}
}
