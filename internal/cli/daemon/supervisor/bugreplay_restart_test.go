//go:build daemon_bugreplay

// Bug-replay fault tests for the crash/restart/orphan, liveness-watchdog and
// config/boot groups of the daemon bug catalogue (wave 2, restart group).
//
// Each live-bug test is named after its fix PR and asserts the behaviour the
// fix restores, so it FAILS on v5 for the catalogued reason and PASSES on the
// fix PR head. History (already fixed) rows get regression tests that pass on
// v5. Tests use only APIs that exist on v5 so the same file compiles at the
// fix heads. Helpers are prefixed "restart" so they cannot collide with the
// other bug-replay groups that share this package.
package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/discovery"
	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/backendcheck"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
)

// ---------------------------------------------------------------------------
// restart-group helpers
// ---------------------------------------------------------------------------

func restartSupervisor(cfg *config.DaemonConfig) *Supervisor {
	if cfg == nil {
		cfg = &config.DaemonConfig{}
	}
	return &Supervisor{
		ConfigSnapshot: func() *config.DaemonConfig { return cfg },
		Shutdown:       make(chan struct{}),
		FatalCh:        make(chan error, 1),
		StoppedAgents:  make(map[string]struct{}),
		EmitEvent:      func(events.Event) {},
	}
}

func restartMaxRetriesConfig(n int) *config.DaemonConfig {
	return &config.DaemonConfig{Daemon: config.DaemonSettings{
		RestartPolicy: config.RestartPolicy{MaxRetries: &n},
	}}
}

func restartHarnessError(ap *AgentProcess, class wrapper.ErrorClass, exitCode int) {
	ap.LastExitCode = exitCode
	ap.LastStart = time.Now()
	ap.LastNoWork = false
	ap.LastError = &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(class), ExitCode: exitCode, Message: "replayed failure"}
}

func restartNoWork(ap *AgentProcess) {
	ap.LastExitCode = 0
	ap.LastStart = time.Now()
	ap.LastNoWork = true
	ap.LastError = &agenterr.AgentError{Class: agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome), Message: "no claimable tasks"}
}

func restartWriteLock(t *testing.T, dir, taskID string) {
	t.Helper()
	data, err := json.Marshal(&cli.LockInfo{PID: 999999, Command: "task", AgentName: "replay", TaskID: taskID, StartedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cli.ResolveLockDir(dir), cli.LockFileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// restartTaskAgent returns an agent whose worktree lock names taskID, so the
// quarantine and classification hooks attribute its exit to that task.
func restartTaskAgent(t *testing.T, worktree, taskID string) *AgentProcess {
	t.Helper()
	dir := t.TempDir()
	restartWriteLock(t, dir, taskID)
	return &AgentProcess{
		Entry:        config.AgentEntry{Worktree: worktree},
		WorktreePath: dir,
		StopCh:       make(chan struct{}),
		Done:         make(chan struct{}),
	}
}

// restartKill shapes ap as a task-holding run killed with the given stop
// reason and harness class, then feeds it to the quarantine ledger hook.
func restartKill(s *Supervisor, ap *AgentProcess, stop StopReason, class wrapper.ErrorClass, exitCode int) {
	ap.Mu.Lock()
	ap.StopReason = stop
	ap.LastExitCode = exitCode
	ap.LastError = &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(class), ExitCode: exitCode, Message: "killed"}
	ap.Mu.Unlock()
	s.recordTaskExitForQuarantine(ap, exitCode)
}

func restartLedgerCount(s *Supervisor, taskID string) int {
	q := s.qrec()
	q.mu.Lock()
	defer q.mu.Unlock()
	if rec := q.rec[taskID]; rec != nil {
		return rec.Count
	}
	return 0
}

func restartSetTick(s *Supervisor, name string, at time.Time) {
	v, ok := s.Ticks.Load(name)
	if !ok {
		return
	}
	v.(*atomic.Int64).Store(at.UnixNano())
}

// restartScanUntilFatal runs the watchdog scan enough times to cross both the
// consecutive-scan and the real-span guards, back-dating streak starts the way
// production's 10 s cadence would (same technique as liveness_test.go).
func restartScanUntilFatal(s *Supervisor) {
	for i := 0; i < livenessStaleScansBeforeFatal+1; i++ {
		s.scanTicks(time.Now())
		for name, start := range s.livenessStreakStart {
			s.livenessStreakStart[name] = start.Add(-livenessMinStaleSpan)
		}
	}
}

// ---------------------------------------------------------------------------
// Liveness watchdog
// ---------------------------------------------------------------------------

// #552 (history N1: #113, #117, #517): an agent whose supervise goroutine
// returns for good leaves its liveness tick registered. The tick freezes, and
// one threshold later the watchdog FATALs the whole daemon, killing every
// healthy agent. Root cause: critical.go:107-112 supervisedAgentBody never
// unregisters the tick.
func TestBugReplay_PR552_ExitedAgentTickDoesNotFatalDaemon(t *testing.T) {
	s := restartSupervisor(nil)
	ap := &AgentProcess{
		Entry:  config.AgentEntry{Worktree: "replay-552"},
		StopCh: make(chan struct{}),
		Done:   make(chan struct{}),
	}
	close(ap.StopCh) // agent removed from config: the supervise loop returns at once

	s.startAgentSupervisor(ap)
	select {
	case <-ap.Done:
	case <-time.After(5 * time.Second):
		t.Fatal("supervise goroutine did not exit after StopCh")
	}

	name := agentTickName(ap)
	if _, still := s.Ticks.Load(name); still {
		t.Errorf("tick %q is still registered after its supervise goroutine exited (critical.go supervisedAgentBody never unregisters it)", name)
	}

	// Replay the incident: the frozen tick ages past the agent threshold and
	// the watchdog scans. A daemon with no live agents must not go fatal.
	restartSetTick(s, name, time.Now().Add(-24*time.Hour))
	restartScanUntilFatal(s)
	select {
	case err := <-s.FatalChannel():
		t.Fatalf("watchdog FATALed the daemon on an exited agent's frozen tick: %v", err)
	default:
	}
}

// F16-F19 regression chain (R-fatal): the watchdog must still FATAL on a
// genuinely wedged cadence goroutine, and must tolerate a single stale scan.
// Passes on v5; guards against a #552 fix that disables detection outright.
func TestBugReplay_RFatalChain_WedgedGoroutineStillFatal(t *testing.T) {
	s := restartSupervisor(nil)
	s.RegisterTick(GoroutineHealthChecker)
	restartSetTick(s, GoroutineHealthChecker, time.Now().Add(-10*time.Minute))

	s.scanTicks(time.Now()) // F18: one stale scan alone is not fatal
	select {
	case err := <-s.FatalChannel():
		t.Fatalf("single stale scan went fatal (F18 streak regression): %v", err)
	default:
	}

	restartScanUntilFatal(s)
	select {
	case err := <-s.FatalChannel():
		if !strings.Contains(err.Error(), GoroutineHealthChecker) {
			t.Errorf("fatal does not name the wedged goroutine: %v", err)
		}
	default:
		t.Fatal("watchdog did not FATAL on a wedged cadence goroutine (F16 regression)")
	}
}

// F19 (#429) regression: a scan-to-scan gap that only the wall clock sees
// (darwin sleep pauses the monotonic clock) is a suspension, not a stall.
func TestBugReplay_F19_SleepGapIsSuspension(t *testing.T) {
	if _, ok := suspendedScanGap(10*time.Second, 2*time.Hour); !ok {
		t.Fatal("a 2h wall gap with a 10s monotonic gap was not treated as suspension (F19 regression)")
	}
	if _, ok := suspendedScanGap(10*time.Second, 10*time.Second); ok {
		t.Fatal("an in-step 10s gap was treated as suspension")
	}
}

// ---------------------------------------------------------------------------
// Restart budget
// ---------------------------------------------------------------------------

// #760 (history N2): a NoWork cycle refunds the restart budget, so an agent
// whose failures alternate with idle polls never reaches max_retries.
// Root cause: restart.go:272 applyNoWorkRestart sets RestartCount = 0.
func TestBugReplay_PR760_NoWorkDoesNotRefundRestartBudget(t *testing.T) {
	const maxRetries = 3
	s := restartSupervisor(restartMaxRetriesConfig(maxRetries))
	ap := &AgentProcess{Entry: config.AgentEntry{Worktree: "replay-760"}}

	for i := 1; i <= maxRetries+1; i++ {
		restartHarnessError(ap, wrapper.ErrUnknown, 1)
		s.shouldRestart(ap)
		if ap.StopReason == StopReasonMaxRetriesBlocked || ap.StopReason == StopReasonMaxRetries || ap.StopReason == StopReasonFastFail {
			return // budget reached: fixed behaviour
		}
		restartNoWork(ap)
		s.shouldRestart(ap)
		if ap.RestartCount != i {
			t.Fatalf("after failure %d and an idle poll RestartCount = %d, want %d (restart.go applyNoWorkRestart refunds the budget)", i, ap.RestartCount, i)
		}
	}
	t.Fatalf("%d failures interleaved with idle polls never exhausted max_retries=%d (StopReason=%q)", maxRetries+1, maxRetries, ap.StopReason)
}

// F13/F14 regression (R-budget): BackendUnavailable does not erode the
// budget, and one spawn failure counts once. Passes on v5.
func TestBugReplay_F14_BackendUnavailableDoesNotEraseBudget(t *testing.T) {
	s := restartSupervisor(restartMaxRetriesConfig(1))
	ap := &AgentProcess{Entry: config.AgentEntry{Worktree: "replay-f14"}}
	for i := 0; i < 5; i++ {
		ap.LastExitCode = 1
		ap.LastError = &agenterr.AgentError{Class: agenterr.OutcomeFromDomain(agenterr.BackendUnavailableOutcome)}
		if !s.shouldRestart(ap) {
			t.Fatalf("BackendUnavailable stopped the agent on cycle %d", i)
		}
	}
	if ap.RestartCount != 0 {
		t.Fatalf("BackendUnavailable charged the budget: RestartCount=%d", ap.RestartCount)
	}
}

// #134: an agent that exhausts max_retries re-enters automatic retry cycles
// (block and recheck every 60 s) instead of stopping in error.
// Root cause: restart.go:248-259 applyMaxRetriesBlock resets the counters and
// keeps restarting. The fix stops the agent and blocks its task.
func TestBugReplay_PR134_MaxRetriesStopsAgent(t *testing.T) {
	const maxRetries = 2
	s := restartSupervisor(restartMaxRetriesConfig(maxRetries))
	ap := &AgentProcess{Entry: config.AgentEntry{Worktree: "replay-134"}}
	var restart bool
	for i := 0; i <= maxRetries; i++ {
		restartHarnessError(ap, wrapper.ErrTransient, 1)
		restart = s.shouldRestart(ap)
	}
	if restart {
		t.Fatalf("agent past max_retries=%d still restarts (StopReason=%q, RestartCount=%d); want an error stop", maxRetries, ap.StopReason, ap.RestartCount)
	}
}

// #737 (1): one failover-only error (model not found) with no fallback left
// stops the supervisor for good and strands its tasks.
// Root cause: restart.go:97-98 applyFailoverExhaustedStop with no retry.
func TestBugReplay_PR737_FailoverExhaustedGetsBoundedRetry(t *testing.T) {
	s := restartSupervisor(nil)
	ap := &AgentProcess{Entry: config.AgentEntry{Worktree: "replay-737"}}
	restartHarnessError(ap, wrapper.ErrModelNotFound, 1)
	if !s.shouldRestart(ap) {
		t.Fatalf("first failover-only error stopped the supervisor for good (StopReason=%q); want a bounded retry", ap.StopReason)
	}
}

// ---------------------------------------------------------------------------
// Classification of supervisor-initiated kills
// ---------------------------------------------------------------------------

// #711: a kill the supervisor itself performed (shutdown, manual stop, config
// removal) is log-classified as an agent fault and consumes restart budget.
// Root cause: classify.go:52-58 sends every non-zero exit to ClassifyFromLog.
func TestBugReplay_PR711_SupervisorStopDoesNotChargeBudget(t *testing.T) {
	for _, stop := range []StopReason{StopReasonShutdown, StopReasonManualStop, StopReasonConfigRemoved} {
		t.Run(string(stop), func(t *testing.T) {
			s := restartSupervisor(restartMaxRetriesConfig(3))
			ap := restartTaskAgent(t, "replay-711", "loom-711")
			logPath := filepath.Join(t.TempDir(), "agent.log")
			if err := os.WriteFile(logPath, []byte("error: connection reset by peer\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			ap.LogFilePath = logPath
			ap.StopReason = stop

			s.classifyAgentExit(ap, 143)
			s.shouldRestart(ap)
			if ap.RestartCount != 0 {
				t.Fatalf("supervisor-initiated %s (exit 143) charged the restart budget: class=%v RestartCount=%d",
					stop, ap.LastError, ap.RestartCount)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Task quarantine ledger
// ---------------------------------------------------------------------------

// #520: kills caused by the daemon's own budget machinery and by the
// duration cap on an agent that was still producing output are counted as
// task stalls. Root cause: quarantine.go:136 recordTaskExitForQuarantine
// counts every eligible kill.
func TestBugReplay_PR520_InfraAndActiveDurationKillsNotQuarantined(t *testing.T) {
	cases := []struct {
		name string
		stop StopReason
	}{
		{"active_duration_cap", StopReasonRunDurationExceeded},
		{"budget_blocked", StopReasonMaxRetriesBlocked},
		{"backend_unavailable", StopReasonBackendUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LOOM_TASK_QUARANTINE_THRESHOLD", "3")
			s := restartSupervisor(nil)
			ap := restartTaskAgent(t, "replay-520", "loom-520")
			for i := 0; i < 3; i++ {
				restartKill(s, ap, tc.stop, wrapper.ErrTimeout, 143)
			}
			if got := restartLedgerCount(s, "loom-520"); got != 0 {
				t.Fatalf("%s kills counted toward task quarantine: count=%d (would quarantine at 3)", tc.stop, got)
			}
		})
	}
}

// Regression (passes on v5): a genuine watchdog kill still counts.
func TestBugReplay_Quarantine_WatchdogKillStillCounts(t *testing.T) {
	t.Setenv("LOOM_TASK_QUARANTINE_THRESHOLD", "3")
	s := restartSupervisor(nil)
	ap := restartTaskAgent(t, "replay-q", "loom-q")
	restartKill(s, ap, StopReasonWatchdog, wrapper.ErrTimeout, 137)
	restartKill(s, ap, StopReasonWatchdog, wrapper.ErrTimeout, 137)
	if got := restartLedgerCount(s, "loom-q"); got != 2 {
		t.Fatalf("two watchdog kills gave ledger count %d, want 2", got)
	}
}

// #522: a review agent that progresses through comments or label changes is
// killed and quarantined as stalled. Root cause: quarantine.go:136-184 the
// progress fingerprint hashes only Design and Notes (hashIssueField :246).
func TestBugReplay_PR522_CommentProgressResetsQuarantine(t *testing.T) {
	t.Setenv("LOOM_TASK_QUARANTINE_THRESHOLD", "3")
	var calls atomic.Int32
	mock := &clitest.MockIssueBackend{
		GetFn: func(_ context.Context, id string) (*backend.IssueDetailData, error) {
			n := int64(calls.Add(1))
			d := &backend.IssueDetailData{}
			d.ID = id
			d.Design, d.Notes = "same design", "same notes"
			for c := int64(1); c <= n; c++ { // one new comment per kill
				d.Comments = append(d.Comments, backend.CommentData{ID: c, IssueID: id, Text: "review note"})
			}
			return d, nil
		},
	}
	s := restartSupervisor(nil)
	s.IssueBackend = mock
	ap := restartTaskAgent(t, "replay-522", "loom-522")
	restartKill(s, ap, StopReasonWatchdog, wrapper.ErrTimeout, 137)
	restartKill(s, ap, StopReasonWatchdog, wrapper.ErrTimeout, 137)
	restartKill(s, ap, StopReasonWatchdog, wrapper.ErrTimeout, 137)
	if got := restartLedgerCount(s, "loom-522"); got >= 2 {
		t.Fatalf("a task that gained a comment between every kill has ledger count %d; comments are progress", got)
	}
}

// #456: the quarantine ledger lives only in memory, so a daemon restart
// erases it and the threshold is never reached across restarts.
// Root cause: supervisor.go:119 quarantine is an in-memory map.
func TestBugReplay_PR456_QuarantineLedgerSurvivesRestart(t *testing.T) {
	t.Setenv("LOOM_TASK_QUARANTINE_THRESHOLD", "3")
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".loom"), 0o755); err != nil {
		t.Fatal(err)
	}
	ap := restartTaskAgent(t, "replay-456", "loom-456")

	before := restartSupervisor(nil)
	before.ProjectDir = project
	restartKill(before, ap, StopReasonWatchdog, wrapper.ErrTimeout, 137)
	restartKill(before, ap, StopReasonWatchdog, wrapper.ErrTimeout, 137)

	after := restartSupervisor(nil) // the daemon restarted
	after.ProjectDir = project
	restartKill(after, ap, StopReasonWatchdog, wrapper.ErrTimeout, 137)
	if got := restartLedgerCount(after, "loom-456"); got != 3 {
		t.Fatalf("after a daemon restart the ledger count is %d, want 3 (two kills before the restart were lost)", got)
	}
}

// ---------------------------------------------------------------------------
// Orphans
// ---------------------------------------------------------------------------

// #727: the startup orphan sweep misses reparented processes whose cwd
// differs from the configured worktree path only in letter case (the kernel
// reports the on-disk case). Root cause: proctree.go:186 case-sensitive
// HasPrefix.
func TestBugReplay_PR727_OrphanCwdMatchesCaseInsensitively(t *testing.T) {
	base := t.TempDir()
	worktree := filepath.Join(base, "Replay727")
	if err := os.MkdirAll(filepath.Join(worktree, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	lower := filepath.Join(base, "replay727")
	if st, err := os.Stat(lower); err != nil || !st.IsDir() {
		t.Skip("filesystem is case-sensitive; #727 only applies to case-insensitive volumes")
	}
	resolvedBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	orphanCwd := filepath.Join(resolvedBase, "replay727", "sub") // kernel-reported spelling

	saved := procInspector
	t.Cleanup(func() { procInspector = saved })
	procInspector = processInspector{
		List: func() ([]procInfo, error) { return []procInfo{{PID: 424242, PPID: 1, PGID: 424242}}, nil },
		CWD:  func(int) (string, error) { return orphanCwd, nil },
	}
	if got := findWorktreeOrphans([]string{worktree}); len(got) != 1 {
		t.Fatalf("orphan with cwd %q under worktree %q (case differs only) not found: %v", orphanCwd, worktree, got)
	}
}

// F11/F12 regression (R-orphan): the sweep finds an exact-case orphan and
// never signals the daemon's own process group. Passes on v5.
func TestBugReplay_F11F12_OrphanSweepFindsExactAndSkipsOwnPgroup(t *testing.T) {
	worktree := t.TempDir()
	resolved, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		t.Fatal(err)
	}
	saved := procInspector
	t.Cleanup(func() { procInspector = saved })
	procInspector = processInspector{
		List: func() ([]procInfo, error) {
			return []procInfo{{PID: 5001, PPID: 1, PGID: 5001}, {PID: 5002, PPID: 1, PGID: 777}}, nil
		},
		CWD: func(int) (string, error) { return filepath.Join(resolved, "x"), nil },
	}
	got := findWorktreeOrphans([]string{worktree})
	if len(got) != 2 {
		t.Fatalf("exact-case orphans found = %d, want 2", len(got))
	}
	kept := signalableOrphans(got, 777)
	if len(kept) != 1 || kept[0].PGID != 5001 {
		t.Fatalf("signalableOrphans did not drop the daemon's own pgroup: %v", kept)
	}
}

// ---------------------------------------------------------------------------
// Config/boot
// ---------------------------------------------------------------------------

// #289: a worker role with prompt_file=builtin:... is joined onto the
// workspace path and reported as a missing file nobody wrote, failing daemon
// creation with a misleading cause. Root cause: role.go:39-48 has no builtin:
// check. The fix names the real cause.
func TestBugReplay_PR289_BuiltinPromptOnWorkerRoleNamesCause(t *testing.T) {
	cfg := &config.DaemonConfig{Roles: map[string]config.RoleConfig{
		"replay-reviewer": {PromptFile: "builtin:pr-review"},
	}}
	_, err := ResolveRoleConfigStatic("replay-reviewer", cfg, t.TempDir())
	if err == nil {
		t.Fatal("worker role with a builtin: prompt resolved without error")
	}
	if strings.Contains(err.Error(), "not found") || !strings.Contains(err.Error(), "built-in") {
		t.Fatalf("error blames a missing file instead of the builtin: prompt on a worker role: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Backend gate and issue-backend outages
// ---------------------------------------------------------------------------

func restartFakeBackendCheck(t *testing.T, installed func(call int) bool) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	prev := backendcheck.CheckBackend
	t.Cleanup(func() { backendcheck.CheckBackend = prev })
	backendcheck.CheckBackend = func(name string) (discovery.Info, error) {
		n := int(calls.Add(1))
		ok := installed(n)
		info := discovery.Info{Name: name, Binary: name, Installed: ok, VersionMatchesPin: true}
		if !ok {
			info.InstallHint = name + " not on PATH"
		}
		return info, nil
	}
	return &calls
}

// restartBackendGateParks runs preflight up to and including the backend
// gate and reports whether the gate parked the agent as backend-unavailable.
// preFlightSetup is used (its signature is stable across the fix heads, unlike
// gateBackendAvailable's). A tool allow-list the fake backend cannot enforce
// makes the NEXT gate (safety knobs) refuse, so preflight stops right after the
// backend gate with no recovery, claim or session side effects.
func restartBackendGateParks(s *Supervisor, ap *AgentProcess) bool {
	ap.RoleConfig.AllowedTools = []string{"Read"}
	s.preFlightSetup(ap)
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	return ap.StopReason == StopReasonBackendUnavailable
}

// #90: an agent whose backend CLI is missing still takes a role concurrency
// slot before the backend gate notices, so it can starve siblings (and, when
// the role is full, it never reaches the gate at all).
// Root cause: supervisor.go:320 Concurrency.Acquire runs before
// gateBackendAvailable at :405.
func TestBugReplay_PR90_BackendGateRunsBeforeConcurrencySlot(t *testing.T) {
	restartFakeBackendCheck(t, func(int) bool { return false })
	one := 1
	roles := map[string]config.RoleConfig{"task": {MaxConcurrency: &one}}
	s := restartSupervisor(&config.DaemonConfig{Backend: "replay-missing-cli", Roles: roles})
	s.Concurrency = NewConcurrencyTracker(roles)
	s.backendRecheckInterval = 10 * time.Millisecond
	if !s.Concurrency.Acquire("task") { // a sibling holds the only slot
		t.Fatal("could not pre-acquire the role slot")
	}
	ap := &AgentProcess{
		Entry:  config.AgentEntry{Worktree: "replay-90", Role: "task"},
		StopCh: make(chan struct{}),
		Done:   make(chan struct{}),
	}
	done := make(chan struct{})
	go func() { defer close(done); s.superviseAgent(ap) }()
	t.Cleanup(func() {
		close(s.Shutdown)
		s.Concurrency.Close()
		<-done
	})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ap.Mu.Lock()
		reason := ap.StopReason
		ap.Mu.Unlock()
		if reason == StopReasonBackendUnavailable {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("agent with a missing backend never reached the backend gate: it is parked in Concurrency.Acquire behind a sibling")
}

// #433: one momentary PATH miss (e.g. while the CLI binary is being
// replaced during an update) parks the agent as backend-unavailable.
// Root cause: backend.go:32 gateBackendAvailable trusts a single lookup.
func TestBugReplay_PR433_TransientPathMissIsDebounced(t *testing.T) {
	calls := restartFakeBackendCheck(t, func(n int) bool { return n > 1 }) // miss once, then found
	s := restartSupervisor(&config.DaemonConfig{Backend: "replay-cli"})
	ap := &AgentProcess{Entry: config.AgentEntry{Worktree: "replay-433", Role: "task"}}
	if restartBackendGateParks(s, ap) {
		t.Fatalf("a single transient PATH miss parked the agent (StopReason=%q, lookups=%d)", ap.StopReason, calls.Load())
	}
}

// Regression (passes on v5): a backend that stays missing is still parked.
func TestBugReplay_PR433_PersistentMissStillParks(t *testing.T) {
	restartFakeBackendCheck(t, func(int) bool { return false })
	s := restartSupervisor(&config.DaemonConfig{Backend: "replay-cli"})
	ap := &AgentProcess{Entry: config.AgentEntry{Worktree: "replay-433b", Role: "task"}}
	if !restartBackendGateParks(s, ap) {
		t.Fatalf("a persistently missing backend was not parked: StopReason=%q", ap.StopReason)
	}
}

// #537: a fleet-db outage makes every ready query fail; each failure is
// charged to the agent's restart budget, so the outage exhausts every agent
// and strands work after recovery.
// Root cause: claim.go:119-122 a ready-query error is ErrUnknown and counted.
func TestBugReplay_PR537_IssueBackendOutageDoesNotSpendBudget(t *testing.T) {
	const maxRetries = 2
	s := restartSupervisor(restartMaxRetriesConfig(maxRetries))
	s.IssueBackend = &clitest.MockIssueBackend{
		ReadyErr: backend.ErrUnavailable("ready", "fleet-db unreachable", context.DeadlineExceeded),
	}
	ap := &AgentProcess{Entry: config.AgentEntry{Worktree: "replay-537", Role: "task"}}
	for i := 0; i < maxRetries+3; i++ {
		if s.claimTask(ap, "") {
			t.Fatal("claim succeeded against an unavailable issue backend")
		}
		s.shouldRestart(ap)
	}
	budgetStop := ap.StopReason == StopReasonMaxRetries || ap.StopReason == StopReasonMaxRetriesBlocked || ap.StopReason == StopReasonFastFail
	if ap.RestartCount != 0 || ap.BlockCount != 0 || budgetStop {
		t.Fatalf("an issue-backend outage was charged to the agent: RestartCount=%d BlockCount=%d StopReason=%q class=%v",
			ap.RestartCount, ap.BlockCount, ap.StopReason, ap.LastError)
	}
}

// #467: an interrupted worker whose resume re-claim is permanently rejected
// (task no longer claimable) keeps the stale lock target, so every cycle
// retries the same rejected resume claim.
// Root cause: claim.go:176-190 a failed resume clears only ResumeTaskID; the
// lock's TaskID (read by resume_recovery.go:38) is kept.
func TestBugReplay_PR467_RejectedResumeTargetIsAbandoned(t *testing.T) {
	s := restartSupervisor(nil)
	s.IssueBackend = &clitest.MockIssueBackend{
		ClaimIssueErr: errors.New("claim loom-467: issue is not claimable (status closed)"),
	}
	ap := restartTaskAgent(t, "replay-467", "loom-467")
	ap.Entry.Role = "task"
	ap.ResumeTaskID = "loom-467"

	s.claimTask(ap, "")

	info, _, err := cli.CheckLock(ap.WorktreePath)
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	if info != nil && info.TaskID == "loom-467" {
		t.Fatal("lock still names the permanently rejected resume target; the next cycle will retry the same claim")
	}
}

// #427: with both the daemon log and the archive log open, the child's
// stdout/stderr is an io.MultiWriter. os/exec then copies through a pipe
// goroutine, and a lingering descendant that inherited the pipe pins
// cmd.Wait() until it exits. Root cause: spawn.go:402-403 io.MultiWriter sinks.
func TestBugReplay_PR427_ChildStdoutIsAnOSFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", tmp)
	t.Setenv("LOOM_CONFIG_DIR", "")
	cfg := &config.DaemonConfig{Daemon: config.DaemonSettings{LogDir: filepath.Join(tmp, "logs")}}
	s := restartSupervisor(cfg)
	s.ProjectDir = tmp
	s.WorkspaceID = "WS427"
	ap := &AgentProcess{Entry: config.AgentEntry{Worktree: "replay-427", Role: "task"}}
	cmd := exec.Command("true")

	s.setupAgentLogFile(ap, cmd)
	t.Cleanup(func() { ap.Mu.Lock(); closeAgentLogs(ap); ap.Mu.Unlock() })

	if ap.LogFile == nil || ap.ArchiveLogFile == nil {
		t.Skipf("precondition: both sinks must open (daemon=%v archive=%v)", ap.LogFile != nil, ap.ArchiveLogFile != nil)
	}
	if _, ok := cmd.Stdout.(*os.File); !ok {
		t.Fatalf("child stdout is %T, not *os.File: os/exec adds a copy goroutine that a lingering descendant can pin", cmd.Stdout)
	}
	if _, ok := cmd.Stderr.(*os.File); !ok {
		t.Fatalf("child stderr is %T, not *os.File", cmd.Stderr)
	}
}

// #490: an agent whose harness profile fails verification (here: a profile
// directory with no manifest) still claims a task in preflight; the refusal
// only surfaces at spawn, after the claim, and a later outcome masks it.
// Root cause: spawn.go:576 AppendProfileEnv runs at spawn, after the claim
// (supervisor.go:437).
func TestBugReplay_PR490_ProfileRefusalBeforeClaim(t *testing.T) {
	prevProbe := probeHarnessVersion
	probeHarnessVersion = func(string) string { return "2.1.234 (Claude Code)" }
	resetHarnessVersionCache()
	t.Cleanup(func() { probeHarnessVersion = prevProbe; resetHarnessVersionCache() })
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", tmp)

	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".loom", AgentProfilesDirName, "replay-490", "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	mock := &clitest.MockIssueBackend{ReadyResult: []backend.IssueData{{ID: "loom-490", Title: "work", Status: "open", IssueType: "task"}}}
	s := restartSupervisor(nil)
	s.ProjectDir = project
	s.IssueBackend = mock
	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "replay-490", Role: "task"},
		WorktreePath: t.TempDir(),
		StopCh:       make(chan struct{}),
		Done:         make(chan struct{}),
	}

	s.preFlightSetup(ap)

	for _, c := range mock.Calls {
		if c.Method == "ClaimIssue" || c.Method == "Ready" {
			t.Fatalf("preflight ran %s although the agent's profile cannot verify (no manifest); calls=%v", c.Method, mock.Calls)
		}
	}
}

// #419: daemon shutdown waits on s.Wg.Wait() with no deadline. A supervise
// goroutine wedged in cmd.Wait() (a descendant still holds the child's stdout)
// keeps Stop from ever returning, so shutdown outlives its own watchdog.
// Root cause: daemon.go:148-175 Stop waits on sup.Stop() without a deadline;
// supervisor.go:286 bare Wg.Wait(). Real time: the fixed budget is
// yield(1s)+sigterm(1s)+15s slack, so the test waits up to 30s.
func TestBugReplay_PR419_ShutdownIsBounded(t *testing.T) {
	one := 1
	cfg := &config.DaemonConfig{Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{
		YieldTimeout: &one, SigtermTimeout: &one,
	}}}
	s := restartSupervisor(cfg)
	s.Concurrency = NewConcurrencyTracker(nil)
	s.Wg.Add(1) // a supervise goroutine wedged in cmd.Wait()
	t.Cleanup(s.Wg.Done)

	returned := make(chan struct{})
	go func() { s.Stop(); close(returned) }()
	select {
	case <-returned:
	case <-time.After(30 * time.Second):
		t.Fatal("Supervisor.Stop did not return within 30s with one wedged supervise goroutine (unbounded Wg.Wait)")
	}
}

// #455: after one agent hits an account-level wall (auth, billing, usage
// limit), siblings on the same account keep claiming into it and burn their
// budgets. Root cause: restart.go:60-107 restart decisions are per agent,
// with no shared wall that the pre-spawn gate consults.
func TestBugReplay_PR455_AccountWallParksSiblings(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", tmp)
	mock := &clitest.MockIssueBackend{ReadyResult: []backend.IssueData{{ID: "loom-455", Title: "work", Status: "open", IssueType: "task"}}}
	s := restartSupervisor(nil)
	s.ProjectDir = t.TempDir()
	s.IssueBackend = mock

	walled := &AgentProcess{Entry: config.AgentEntry{Worktree: "replay-455a", Role: "task"}}
	restartHarnessError(walled, wrapper.ErrBilling, 1)
	s.shouldRestart(walled) // agent A stops on the billing wall

	sibling := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "replay-455b", Role: "task"},
		WorktreePath: t.TempDir(),
		StopCh:       make(chan struct{}),
		Done:         make(chan struct{}),
	}
	s.preFlightSetup(sibling)
	for _, c := range mock.Calls {
		if c.Method == "Ready" || c.Method == "ClaimIssue" {
			t.Fatalf("sibling ran %s into an account-level billing wall another agent just hit", c.Method)
		}
	}
}
