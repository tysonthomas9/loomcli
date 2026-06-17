package supervisor

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/discovery"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/backendcheck"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
)

// stubCheckBackend swaps backendcheck.CheckBackend with the given fn
// for the duration of the test, restoring the original on cleanup.
// Used to drive the spawn gate deterministically without touching PATH.
func stubCheckBackend(t *testing.T, fn func(string) (discovery.Info, error)) {
	t.Helper()
	prev := backendcheck.CheckBackend
	backendcheck.CheckBackend = fn
	t.Cleanup(func() { backendcheck.CheckBackend = prev })
}

func newBackendUnavailableSupervisor() *Supervisor {
	return &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig {
			return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}, Backend: "codex"}
		},
		Shutdown:      make(chan struct{}),
		StoppedAgents: make(map[string]struct{}),
		Agents:        make([]*AgentProcess, 0),
		EmitEvent:     func(events.Event) {},
	}
}

func newBackendUnavailableAgentProcess() *AgentProcess {
	return &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "falcon", Role: "plan", Backend: "codex"},
		RoleConfig: cfgpkg.RoleConfig{Description: "test"},
	}
}

// TestSpawnAgent_BackendNotOnPATH_DoesNotCrashLoop is the LOOM-5
// reproduction. Before the fix, spawnAgent returned nil (subprocess
// started) and the actual exec("codex") failure surfaced ~30ms later,
// burning restart budget every cycle. With the gate, the failure is
// caught up-front, RestartCount stays at zero, and no backoff is
// scheduled — the supervise loop's sleepBeforeBackendRecheck handles
// the blocking instead.
func TestSpawnAgent_BackendNotOnPATH_DoesNotCrashLoop(t *testing.T) {
	stubCheckBackend(t, func(name string) (discovery.Info, error) {
		return discovery.Info{
			Name:        name,
			Binary:      name,
			Installed:   false,
			InstallHint: `"codex" not on PATH. Install codex (e.g. ` + "`npm i -g @openai/codex`" + `).`,
		}, nil
	})

	s := newBackendUnavailableSupervisor()
	ap := newBackendUnavailableAgentProcess()

	if ap.RestartCount != 0 {
		t.Fatalf("precondition: RestartCount must start at 0, got %d", ap.RestartCount)
	}

	err := s.spawnAgent(ap)

	if !errors.Is(err, ErrBackendUnavailable) {
		t.Fatalf("expected ErrBackendUnavailable, got %v", err)
	}
	if ap.StopReason != StopReasonBackendUnavailable {
		t.Errorf("StopReason = %q, want %q", ap.StopReason, StopReasonBackendUnavailable)
	}
	if ap.LastError == nil {
		t.Fatal("LastError must be populated")
	}
	if ap.LastError.Class != agenterr.OutcomeFromDomain(agenterr.BackendUnavailableOutcome) {
		t.Errorf("LastError.Class = %v, want BackendUnavailable", ap.LastError.Class)
	}
	if ap.LastError.Backend != "codex" {
		t.Errorf("LastError.Backend = %q, want %q", ap.LastError.Backend, "codex")
	}
	if ap.RestartCount != 0 {
		t.Errorf("RestartCount = %d, want 0 (gate must preserve restart budget)", ap.RestartCount)
	}
	if !ap.BackoffUntil.IsZero() {
		t.Errorf("BackoffUntil = %v, want zero (no backoff for backend_unavailable)", ap.BackoffUntil)
	}
	if ap.Cmd != nil {
		t.Errorf("Cmd = %v, want nil (gate fires before buildCommand)", ap.Cmd)
	}
}

// TestSpawnAgent_BackendNotOnPATH_RepeatedDoesNotAccumulate verifies
// that re-calling spawnAgent (as the supervise loop will after the
// sleepBeforeBackendRecheck interval) does not silently increment
// restart bookkeeping or duplicate log noise.
func TestSpawnAgent_BackendNotOnPATH_RepeatedDoesNotAccumulate(t *testing.T) {
	stubCheckBackend(t, func(name string) (discovery.Info, error) {
		return discovery.Info{
			Name: name, Binary: name, Installed: false,
			InstallHint: "missing",
		}, nil
	})

	s := newBackendUnavailableSupervisor()
	ap := newBackendUnavailableAgentProcess()

	for i := 0; i < 3; i++ {
		err := s.spawnAgent(ap)
		if !errors.Is(err, ErrBackendUnavailable) {
			t.Fatalf("iteration %d: expected ErrBackendUnavailable, got %v", i, err)
		}
	}
	if ap.RestartCount != 0 {
		t.Errorf("RestartCount = %d after 3 gate fires, want 0", ap.RestartCount)
	}
	if !ap.BackoffUntil.IsZero() {
		t.Errorf("BackoffUntil = %v after 3 gate fires, want zero", ap.BackoffUntil)
	}
}

// TestSpawnAgent_BackendRecoveryClearsState verifies that once the
// binary becomes available, the next gate run clears the blocked state
// (StopReason and LastError) so UIs reflect the recovery before the
// spawn proceeds.
func TestSpawnAgent_BackendRecoveryClearsState(t *testing.T) {
	installed := false
	stubCheckBackend(t, func(name string) (discovery.Info, error) {
		if installed {
			return discovery.Info{
				Name: name, Binary: name, Installed: true, Path: "/fake/bin/" + name,
				VersionMatchesPin: true,
			}, nil
		}
		return discovery.Info{
			Name: name, Binary: name, Installed: false, InstallHint: "missing",
		}, nil
	})

	s := newBackendUnavailableSupervisor()
	ap := newBackendUnavailableAgentProcess()

	// First call: gate fires, state set.
	if err := s.spawnAgent(ap); !errors.Is(err, ErrBackendUnavailable) {
		t.Fatalf("first spawn: expected ErrBackendUnavailable, got %v", err)
	}
	if ap.StopReason != StopReasonBackendUnavailable {
		t.Fatalf("first spawn: StopReason = %q, want backend_unavailable", ap.StopReason)
	}

	// Second call: binary is "installed"; gate should clear state.
	// (Spawn itself will fail later because the buildCommand step has
	// no real `loom` binary in the test env. We only assert the gate's
	// recovery branch ran.)
	installed = true
	_ = s.spawnAgent(ap)

	if ap.StopReason != "" {
		t.Errorf("StopReason after recovery = %q, want empty", ap.StopReason)
	}
	if ap.LastError != nil && ap.LastError.Class == agenterr.OutcomeFromDomain(agenterr.BackendUnavailableOutcome) {
		t.Errorf("LastError still carries BackendUnavailable after recovery: %+v", ap.LastError)
	}
}

// TestSpawnAndWait_BackendUnavailable_BlocksWithoutSpawning verifies the
// spawnAndWait gate path under #84's void signature: when the backend is
// missing, no subprocess is started, concurrency is released, and the
// BackendUnavailable state the gate set (StopReason + LastError) is left intact
// for the single restart decision to block on. RestartCount stays at 0 — the
// budget-preserving block itself is shouldRestart + sleepBeforeRestart, asserted
// by TestShouldRestart_BackendUnavailable_BlocksWithoutEroding below.
func TestSpawnAndWait_BackendUnavailable_BlocksWithoutSpawning(t *testing.T) {
	stubCheckBackend(t, func(name string) (discovery.Info, error) {
		return discovery.Info{
			Name:        name,
			Binary:      name,
			Installed:   false,
			InstallHint: `"codex" not on PATH`,
		}, nil
	})

	s := newBackendUnavailableSupervisor()
	ap := newBackendUnavailableAgentProcess()
	ap.StopCh = make(chan struct{})

	s.spawnAndWait(ap)

	if ap.RestartCount != 0 {
		t.Errorf("RestartCount = %d, want 0 (gate must preserve restart budget)", ap.RestartCount)
	}
	if ap.StopReason != StopReasonBackendUnavailable {
		t.Errorf("StopReason = %q, want %q", ap.StopReason, StopReasonBackendUnavailable)
	}
	if ap.LastError == nil || ap.LastError.Class != agenterr.OutcomeFromDomain(agenterr.BackendUnavailableOutcome) {
		t.Errorf("LastError = %+v, want BackendUnavailable", ap.LastError)
	}
	if ap.Cmd != nil {
		t.Errorf("Cmd = %v, want nil (gate fires before buildCommand; no subprocess)", ap.Cmd)
	}
}

func TestPreFlightSetup_BackendUnavailableDoesNotClaimTask(t *testing.T) {
	stubCheckBackend(t, func(name string) (discovery.Info, error) {
		return discovery.Info{Name: name, Binary: name, Installed: false, InstallHint: "missing"}, nil
	})

	mock := clitest.NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{
		{ID: "task-1", IssueType: "task", Status: "open", Title: "Ready", Design: "design"},
	}
	s := newBackendUnavailableSupervisor()
	s.IssueBackend = mock
	ap := newBackendUnavailableAgentProcess()
	ap.Entry.Role = "task"
	ap.RoleConfig = cfgpkg.RoleConfig{TaskFilter: "has_design"}
	ap.WorktreePath = t.TempDir()

	if s.preFlightSetup(ap) {
		t.Fatal("preFlightSetup returned true, want backend-unavailable block")
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("issue backend was called before backend gate: %#v", mock.Calls)
	}
	if ap.AssignedTaskID != "" {
		t.Fatalf("AssignedTaskID = %q, want empty", ap.AssignedTaskID)
	}
	if ap.LastError == nil || ap.LastError.Class != agenterr.OutcomeFromDomain(agenterr.BackendUnavailableOutcome) {
		t.Fatalf("LastError = %+v, want BackendUnavailable", ap.LastError)
	}
}

type backendUnavailableActorIssueBackend struct {
	*clitest.MockIssueBackend
	releases atomic.Int64
}

func (b *backendUnavailableActorIssueBackend) ClaimIssueAsActor(ctx context.Context, id string, ttl time.Duration, _ string) error {
	return b.ClaimIssue(ctx, id, ttl)
}

func (b *backendUnavailableActorIssueBackend) ReleaseIssueAsActor(ctx context.Context, id, actor string) error {
	b.releases.Add(1)
	return b.ReleaseIssueLock(ctx, id, actor)
}

func TestSpawnAndWait_BackendUnavailableAfterClaimCleansWorkerState(t *testing.T) {
	stubCheckBackend(t, func(name string) (discovery.Info, error) {
		return discovery.Info{Name: name, Binary: name, Installed: false, InstallHint: "missing"}, nil
	})

	runtimeDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	cli.ResetWorkspaceRuntimeDirCache()
	t.Cleanup(cli.ResetWorkspaceRuntimeDirCache)

	ib := &backendUnavailableActorIssueBackend{MockIssueBackend: clitest.NewMockIssueBackend()}
	ib.ReadyResult = []backend.IssueData{
		{ID: "task-1", IssueType: "task", Status: "open", Title: "Ready", Design: "design"},
	}
	s, workers := newWorkerWiringSupervisor()
	s.IssueBackend = ib
	s.Concurrency = NewConcurrencyTracker(nil)

	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "falcon", Role: "task", Backend: "codex"},
		RoleConfig:   cfgpkg.RoleConfig{TaskFilter: "has_design"},
		WorktreePath: t.TempDir(),
		StopCh:       make(chan struct{}),
	}

	if !s.claimTask(ap, "") {
		t.Fatal("claimTask returned false")
	}
	s.createAgentSession(ap, "")
	if ap.AssignedTaskID != "task-1" {
		t.Fatalf("AssignedTaskID = %q, want task-1", ap.AssignedTaskID)
	}
	if ap.AgentSessionID == "" {
		t.Fatal("AgentSessionID is empty; test setup failed")
	}

	s.spawnAndWait(ap)

	if got := ib.releases.Load(); got != 1 {
		t.Fatalf("task claim releases = %d, want 1", got)
	}
	if got := workers.deregisters.Load(); got != 1 {
		t.Fatalf("worker deregisters = %d, want 1", got)
	}
	if ap.AgentSessionID != "" || ap.AgentLeaseID != "" || ap.AgentLeaseToken != "" {
		t.Fatalf("session state not cleared: session=%q lease=%q token=%q", ap.AgentSessionID, ap.AgentLeaseID, ap.AgentLeaseToken)
	}
}

// TestShouldRestart_BackendUnavailable_BlocksWithoutEroding pins the new
// contract (replacing the old WouldErodeBudget guard): the single restart
// decision now treats BackendUnavailable as a budget-preserving block — it keeps
// retrying, never increments RestartCount, and never trips the max-retries stop
// reason, so a missing backend recovers indefinitely once the binary returns.
func TestShouldRestart_BackendUnavailable_BlocksWithoutEroding(t *testing.T) {
	s := newBackendUnavailableSupervisor()
	ap := newBackendUnavailableAgentProcess()

	// State the gate leaves behind (see gateBackendAvailable): a
	// BackendUnavailable LastError. Start at the retry limit — a generic error
	// here would already fail the agent.
	ap.LastError = &agenterr.AgentError{Class: agenterr.OutcomeFromDomain(agenterr.BackendUnavailableOutcome), Backend: "codex"}
	ap.RestartCount = s.getMaxRetries()

	for i := 0; i < 5; i++ {
		if !s.shouldRestart(ap) {
			t.Fatalf("iteration %d: shouldRestart = false, want true (BackendUnavailable blocks, never fatal)", i)
		}
		if ap.RestartCount != s.getMaxRetries() {
			t.Fatalf("iteration %d: RestartCount = %d, want %d (budget must not erode)", i, ap.RestartCount, s.getMaxRetries())
		}
		if ap.StopReason == StopReasonMaxRetries {
			t.Fatalf("iteration %d: StopReason set to max-retries (budget eroded)", i)
		}
	}
	if ap.StopReason != StopReasonBackendUnavailable {
		t.Errorf("StopReason = %q, want %q", ap.StopReason, StopReasonBackendUnavailable)
	}
}
