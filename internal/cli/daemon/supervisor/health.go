package supervisor

import (
	"log/slog"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/lockfile"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// StopAgent sends SIGTERM then SIGKILL to a single agent and its entire process group.
// This function is safe to call concurrently with waitForAgent.
// It uses polling instead of cmd.Wait() to avoid double-wait issues.
// The process group kill ensures child processes (e.g. codex) are not orphaned.
//
// Wrapped in a daemon.supervisor.stop span. The span attaches loom.exit_code
// at end (as observed by waitForAgent and recorded on the AgentProcess) and
// loom.stop_reason from the AgentProcess.StopReason set by the caller. The
// span ends when StopAgent returns; the actual exec.Cmd.Wait happens on the
// supervise goroutine concurrently — see the long-lived monitoring goroutine
// note in the comment on superviseAgent.
func (s *Supervisor) StopAgent(ap *AgentProcess, sigtermTimeout time.Duration) {
	ap.Mu.Lock()
	proc := ap.Cmd
	pid := ap.Pid
	ap.Mu.Unlock()

	if proc == nil || proc.Process == nil || pid == 0 {
		return
	}

	_, span := startSpan(cmdstore.RootContext(),
		"daemon.supervisor.stop",
		attribute.String("loom.agent", ap.Entry.Worktree),
		attribute.String("loom.workspace", s.WorkspaceID),
	)
	defer finalizeStopSpan(span, ap)

	// Snapshot descendant pgroups BEFORE the worker dies. Once we send SIGTERM,
	// the worker may exit within microseconds and its children get reparented
	// to init — at which point we can no longer correlate them as our
	// descendants. We re-use this snapshot for the SIGKILL pass below so a
	// backend that ignored SIGTERM (or was already reparented to init) is
	// still reachable by pgid.
	descendantPGIDs := findDescendantPGIDs(pid, syscall.Getpgrp())

	slog.Info("sending signal to process group", "worktree", ap.Entry.Worktree, "signal", "SIGTERM", "pid", pid, "extra_pgroups", len(descendantPGIDs))
	if !sendSigterm(ap, proc, pid) {
		// Still signal descendants — the worker may already be gone but its
		// reparented backends won't be.
		signalDescendantPGroups(descendantPGIDs, syscall.SIGTERM, ap.Entry.Worktree)
		signalDescendantPGroups(descendantPGIDs, syscall.SIGKILL, ap.Entry.Worktree)
		return
	}

	// Children that called Setpgid (e.g. codex/claude/cursor backends) sit in
	// their own pgroup, so the kill(-pid) above misses them. Signal each
	// pgroup we discovered in the descendant snapshot.
	signalDescendantPGroups(descendantPGIDs, syscall.SIGTERM, ap.Entry.Worktree)

	if waitForProcessExit(ap, pid, sigtermTimeout) {
		slog.Info("process exited gracefully", "worktree", ap.Entry.Worktree)
		// Even on clean worker exit, hit the descendant pgroups with SIGKILL —
		// a backend that ignored SIGTERM won't be reachable any other way once
		// it's reparented to init.
		signalDescendantPGroups(descendantPGIDs, syscall.SIGKILL, ap.Entry.Worktree)
		return
	}

	// Force kill the entire process group if still running.
	ap.Mu.Lock()
	stillRunning := ap.Pid != 0
	ap.Mu.Unlock()
	if stillRunning {
		slog.Warn("sending SIGKILL to process group", "worktree", ap.Entry.Worktree, "pid", pid)
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
	signalDescendantPGroups(descendantPGIDs, syscall.SIGKILL, ap.Entry.Worktree)
}

// finalizeStopSpan records the agent's exit code and stop reason on the stop
// span and ends it. Pulled out of StopAgent so that function stays under the
// funlen lint budget; keeps the span lifecycle in one named place.
func finalizeStopSpan(span trace.Span, ap *AgentProcess) {
	ap.Mu.Lock()
	exitCode := ap.LastExitCode
	stopReason := string(ap.StopReason)
	ap.Mu.Unlock()
	span.SetAttributes(
		attribute.Int("loom.exit_code", exitCode),
		attribute.String("loom.stop_reason", stopReason),
	)
	span.End()
}

// sendSigterm signals the process group, falling back to the leader process.
// Returns false if the process appears to have already exited.
func sendSigterm(ap *AgentProcess, proc *exec.Cmd, pid int) bool {
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		slog.Warn("SIGTERM to process group failed, trying process directly", "worktree", ap.Entry.Worktree, "err", err)
		if err := proc.Process.Signal(syscall.SIGTERM); err != nil {
			slog.Warn("SIGTERM failed, process may have exited", "worktree", ap.Entry.Worktree, "err", err)
			return false
		}
	}
	return true
}

// waitForProcessExit polls until the agent process exits or the timeout
// elapses. Wait() itself is called by waitForAgent on the supervise loop,
// so we observe exit via ap.Pid being cleared or the OS reporting the
// process gone. Returns true when the process exited within the budget.
func waitForProcessExit(ap *AgentProcess, pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ap.Mu.Lock()
		currentPID := ap.Pid
		ap.Mu.Unlock()
		if currentPID == 0 {
			return true
		}
		if !lockfile.IsProcessRunning(pid) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// checkWatchdog applies the two per-run ceilings the supervisor owns.
//
// The first is the run-duration cap: a wall-clock bound on how long a single
// run may last, measured from lastStart. See run_duration.go.
//
// The second is the idle kill, which fires if no liveness signal is newer than
// outputTimeout seconds. It considers three signals and uses the freshest:
//
//   - ap.LastActivity: live PTY-output heartbeat from the agent's wrapper,
//     delivered over agent IPC (RecordAgentActivity). This is the ONLY live
//     signal on the RunTurn path, whose interactive TUI updates neither the
//     stdout log nor a hook transcript — without it a busy agent looks
//     "silent" and is wrongly killed at the timeout.
//   - session transcript mtime (updated by hooks on every turn, when installed).
//   - stdout log file mtime.
//
// Silence is only evidence of a hang when nobody asked the agent to be quiet:
// applyIdleKill suspends the kill (and only the kill) while an interactive
// prompt is outstanding. See input_wait.go.
func (s *Supervisor) checkWatchdog(ap *AgentProcess, outputTimeout int, logPath string, lastStart time.Time, worktreeName string) {
	// Age first, and on its own terms. Everything below is about what the agent
	// has said lately, and the duration cap must not inherit any of it: not the
	// activitySource early return (a run that produced no signal at all is the
	// one most in need of a ceiling), and not the input-wait suspension.
	if s.applyRunDurationKill(ap, lastStart, worktreeName) {
		return
	}

	// output_timeout <= 0 opts out of the SILENCE check only. The two ceilings
	// are gated separately because they answer different questions: an operator
	// who disables the idle kill for a backend with long quiet stretches is not
	// asking for unbounded runs, and until the cap above existed that setting
	// left the daemon with no ceiling whatsoever.
	if outputTimeout <= 0 {
		return
	}

	var lastActivity time.Time
	activitySource := "none"
	// consider records t as the activity signal when it is the newest seen.
	consider := func(t time.Time, source string) {
		if t.IsZero() {
			return
		}
		if activitySource == "none" || t.After(lastActivity) {
			lastActivity = t
			activitySource = source
		}
	}

	ap.Mu.Lock()
	heartbeat := ap.LastActivity
	txPath := ap.TranscriptPath
	ap.Mu.Unlock()

	// Tier 0: live PTY-output heartbeat (agent IPC). Survives the RunTurn path,
	// where the file-based tiers below go stale even while the agent is busy.
	consider(heartbeat, "heartbeat")

	// Tier 1: session transcript mtime (updated by hooks on every turn).
	if txPath != "" {
		if info, err := os.Stat(txPath); err == nil {
			consider(info.ModTime(), "transcript")
		}
	}

	// Tier 2: log file mtime (stdout output).
	if logPath != "" {
		if info, err := os.Stat(logPath); err == nil {
			consider(info.ModTime(), "log")
		}
	}

	// Apply timeout if we found any activity signal
	if activitySource == "none" {
		return
	}
	// Use lastStart if activity signal predates agent spawn
	if lastActivity.Before(lastStart) {
		lastActivity = lastStart
	}
	s.applyIdleKill(ap, time.Since(lastActivity), outputTimeout, activitySource, worktreeName)
}

// applyIdleKill kills the agent when it has been silent past outputTimeout,
// unless an outstanding interactive prompt explains the silence.
//
// The decision this function makes is exactly:
//
//	idle := silent > threshold && !(pending > 0)
//
// and the negated term is the whole of the change. A harness parked on a dialog
// emits no PTY output by design, so "no output" stops being evidence of a hang
// for as long as loom knows an answer is outstanding — and only for that long,
// because inputWaitHoldsOff refuses to keep suspending once the wait exceeds its
// bound. Nothing else is suspended: shutdown, drain and manual stop never
// consult the counter, so a waiting agent can always be stopped immediately.
// See input_wait.go for why the signal is a counter rather than a flag.
func (s *Supervisor) applyIdleKill(ap *AgentProcess, silent time.Duration, outputTimeout int, activitySource, worktreeName string) {
	threshold := time.Duration(outputTimeout) * time.Second
	holdOff, pending, expired := inputWaitHoldsOff(ap, s.GetInputWaitMax())
	idle := silent > threshold && !holdOff

	if !idle {
		if silent > threshold {
			slog.Info("watchdog idle kill suspended, agent is waiting on interactive input",
				"worktree", worktreeName, "silent_duration", silent.Truncate(time.Second),
				"threshold_sec", outputTimeout, "input_wait_pending", pending)
		}
		return
	}

	// input_wait_expired distinguishes "nothing was pending, this really is a
	// hang" from "a human never answered and the wait outlived its bound".
	slog.Error("killing hung process, no activity detected",
		"worktree", worktreeName, "silent_duration", silent.Truncate(time.Second),
		"threshold_sec", outputTimeout, "source", activitySource,
		"input_wait_pending", pending, "input_wait_expired", expired)
	s.setStopReasonDefault(ap, StopReasonWatchdog)
	s.StopAgent(ap, 10*time.Second)
}

// applyRunDurationKill stops the agent when its current run has outlived the
// configured wall-clock cap, and reports whether it did.
//
// The decision has the same shape as the idle branch above:
//
//	over := maxRun > 0 && time.Since(lastStart) > maxRun
//
// with one deliberate asymmetry — it does not consult the input-wait counter.
// That is not an oversight in the ordering, it IS the ordering. applyIdleKill
// suspends the silence kill for as long as a prompt is outstanding, so during a
// wait this cap is the only bound left standing over the run; suspending it too
// would rebuild, one layer up, exactly the open-ended hold-off that
// inputWaitMax exists to prevent. A question nobody has answered in four hours
// is not about to be answered, and the worker slot is not free while they think
// about it. The pending count is logged rather than obeyed, so an operator
// reading the kill line can see the wait it fired through.
//
// Ordering also matters against the shutdown path: a duration kill sets the stop
// reason via setStopReasonDefault, which never overwrites, so a manual stop or
// drain that already claimed the agent keeps its own reason.
func (s *Supervisor) applyRunDurationKill(ap *AgentProcess, lastStart time.Time, worktreeName string) bool {
	maxRun := s.maxRunDurationFor(ap)
	// A zero lastStart means the spawn time was never recorded, not that the run
	// began at the epoch — time.Since would read it as decades and kill on the
	// first tick. Absent evidence of age, leave the run alone.
	if maxRun <= 0 || lastStart.IsZero() {
		return false
	}
	ran := time.Since(lastStart)
	if ran <= maxRun {
		return false
	}

	ap.Mu.Lock()
	pending := ap.InputWaitPending
	ap.Mu.Unlock()

	slog.Error("killing agent, run exceeded its maximum duration",
		"worktree", worktreeName, "ran", ran.Truncate(time.Second),
		"max_run_duration", maxRun, "input_wait_pending", pending)
	s.setStopReasonDefault(ap, StopReasonRunDurationExceeded)
	s.StopAgent(ap, 10*time.Second)
	return true
}

// healthChecker runs periodic health checks in a goroutine.
func (s *Supervisor) healthChecker() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.Shutdown:
			return
		case <-ticker.C:
			s.checkAgentHealth()
			s.RecordTick(GoroutineHealthChecker)
		}
	}
}

// checkAgentHealth performs health checks on all agents.
func (s *Supervisor) checkAgentHealth() {
	outputTimeout := s.GetOutputTimeout()
	var totalAgents, healthyAgents int

	s.AgentsMu.RLock()
	snapshot := make([]*AgentProcess, len(s.Agents))
	copy(snapshot, s.Agents)
	s.AgentsMu.RUnlock()

	for _, ap := range snapshot {
		ap.Mu.Lock()
		pid := ap.Pid
		worktreePath := ap.WorktreePath
		worktreeName := ap.Entry.Worktree
		logPath := ap.LogFilePath
		lastStart := ap.LastStart
		ap.Mu.Unlock()

		totalAgents++

		if pid == 0 {
			continue // Not running
		}

		// Check if PID is alive
		if !lockfile.IsProcessRunning(pid) {
			// Process died unexpectedly - superviseAgent will detect via cmd.Wait()
			slog.Warn("agent is not running", "worktree", worktreeName, "pid", pid)
		} else {
			healthyAgents++
		}

		// Check lock file for stale state
		lockInfo, isRunning, err := cli.CheckLock(worktreePath)
		if err == nil && lockInfo != nil && !isRunning {
			slog.Warn("stale lock detected", "worktree", worktreeName)
		}

		// Watchdog: kill the agent if it has gone silent past outputTimeout, or
		// if the run itself has outlived its wall-clock cap. The outputTimeout
		// gate lives inside checkWatchdog now — the two ceilings are switched
		// off independently, so disabling the idle kill must not also disable
		// the duration cap.
		s.checkWatchdog(ap, outputTimeout, logPath, lastStart, worktreeName)
	}

	// Fleet-level check: every agent stopped while work is queued. Runs after
	// the per-agent loop so it sees this tick's state.
	s.checkFleetStall()

	// Emit health_check summary event
	if evt, err := events.NewEvent(events.HealthCheck, "", "", "", events.HealthCheckData{AgentCount: totalAgents, HealthyCount: healthyAgents}); err == nil {
		s.EmitEvent(evt)
	}
}

// fleetStallRearmCooldown bounds how often one agent may be re-armed. The
// sweep fires whenever nothing is supervising and ready work is queued, so an
// agent that dies again immediately would otherwise be re-armed on every
// health tick; this is what keeps recovery from becoming a spawn loop. Long
// enough to make a failing re-arm cheap, short enough that a real recovery is
// picked up in minutes rather than by hand.
const fleetStallRearmCooldown = 5 * time.Minute

// rearmableStopReason reports whether a stopped agent may be re-armed by the
// stall sweep.
//
// Only the two policy-terminal reasons qualify. Everything else is either a
// decision a human made (manual_stop, config_removed), a shutdown in progress,
// an ephemeral agent that finished the one task it existed for, or
// fatal_error — auth/billing, which needs a human and would fail identically
// on every re-arm. Restarting any of those would be overriding an intent, not
// recovering from an outage.
func rearmableStopReason(r StopReason) bool {
	switch r {
	case StopReasonFastFail, StopReasonMaxRetries:
		return true
	default:
		return false
	}
}

// checkFleetStall detects — and reports loudly — the end state of the
// 2026-08-27 incident: every agent terminally stopped by a shared
// infrastructure fault, ready work queued, and nothing left to notice. Nine
// agents fast-failed inside two minutes of an issue-backend auth outage; the
// backend recovered three minutes later and the fleet sat at zero agents with
// five ready P1/P2 tasks until an operator restarted all nine by hand.
//
// A zero-agent fleet is only a stall when there is work to do — an idle fleet
// with an empty queue is just idle — so the ready query is both halves of the
// test: it proves work is waiting AND that the issue backend is answering
// again. A failed probe means the outage has not cleared, so the sweep says so
// and re-arms nothing.
//
// Called from checkAgentHealth on the 30s health tick.
func (s *Supervisor) checkFleetStall() {
	select {
	case <-s.Shutdown:
		return
	default:
	}

	supervising, stalled := s.partitionStalledAgents()
	if supervising > 0 || len(stalled) == 0 || s.IssueBackend == nil {
		return
	}

	names := make([]string, 0, len(stalled))
	for _, ap := range stalled {
		names = append(names, ap.Entry.Worktree)
	}

	issues, err := s.readyIssues(backendReadyProbeOpts())
	if err != nil {
		slog.Error("fleet has no supervised agents and the issue backend is still unavailable",
			"stopped_agents", names, "err", err)
		return
	}
	if len(issues) == 0 {
		return // idle with an empty queue: not a stall
	}

	// Loud on every tick the condition holds, and independent of whether
	// anything is re-armed below: the cooldown must never buy silence, because
	// silence is what made the incident a manual recovery.
	slog.Error("fleet stalled: no agent is supervising while ready work is queued",
		"stopped_agents", names, "ready_tasks", len(issues))

	for _, ap := range stalled {
		ap.Mu.Lock()
		lastRearm := ap.LastRearm
		ap.Mu.Unlock()
		if !lastRearm.IsZero() && time.Since(lastRearm) < fleetStallRearmCooldown {
			continue // re-armed too recently; let the cooldown run
		}
		s.rearmAgent(ap)
	}
}

// partitionStalledAgents splits the supervised agents into those whose
// supervise goroutine is still alive (any state — running, backing off,
// blocked) and those that stopped for a reason the sweep may re-arm.
func (s *Supervisor) partitionStalledAgents() (supervising int, stalled []*AgentProcess) {
	s.AgentsMu.RLock()
	snapshot := make([]*AgentProcess, len(s.Agents))
	copy(snapshot, s.Agents)
	s.AgentsMu.RUnlock()

	for _, ap := range snapshot {
		if ap.Done == nil || ap.StopCh == nil {
			continue // fleet mode: no supervise goroutine was ever launched
		}
		select {
		case <-ap.Done:
		default:
			supervising++
			continue
		}
		// The goroutine has exited. A closed StopCh means a drain asked it to,
		// so leave it alone even if the reason looks re-armable.
		select {
		case <-ap.StopCh:
			continue
		default:
		}
		ap.Mu.Lock()
		reason := ap.StopReason
		ephemeral := ap.Entry.Mode == domain.AgentModeEphemeral
		ap.Mu.Unlock()
		if ephemeral || !rearmableStopReason(reason) {
			continue
		}
		stalled = append(stalled, ap)
	}
	return supervising, stalled
}

// backendReadyProbeOpts is the unfiltered "is there any ready work at all"
// query. Deliberately not scoped to a repo or a role: the question is whether
// the fleet as a whole has anything to do, and any answer at all also proves
// the backend is reachable again.
func backendReadyProbeOpts() backend.ReadyOpts {
	return backend.ReadyOpts{Limit: 1}
}

// rearmAgent restarts the supervise goroutine of an agent that stopped
// terminally, giving it a fresh restart budget.
//
// This is the recovery path fast-fail never had: StopReasonFastFail ends the
// supervise goroutine for good, so before this the only way back was `loom data
// agent start <agent>`, once per agent. The counters reset because the agent is
// being given a genuinely new start — carrying the outage's failure streak into
// the re-armed run would spend the budget it was just handed. LastError is
// cleared for the same reason; the stop reason it produced is logged here so
// the history is not lost.
func (s *Supervisor) rearmAgent(ap *AgentProcess) {
	ap.Mu.Lock()
	previous := ap.StopReason
	lastErr := ""
	if ap.LastError != nil {
		lastErr = ap.LastError.Class.String()
	}
	ap.StopReason = ""
	ap.RestartCount = 0
	ap.RateRetryCount = 0
	ap.NoWorkCount = 0
	ap.BlockCount = 0
	ap.LastError = nil
	ap.CurrentBackendIdx = 0
	ap.LastRearm = time.Now()
	ap.Done = make(chan struct{})
	ap.Mu.Unlock()

	name := agentTickName(ap)
	s.RegisterTick(name)
	s.AgentsMu.Lock()
	s.Wg.Add(1)
	s.AgentsMu.Unlock()
	go s.supervisedAgentBody(name, ap)

	slog.Warn("re-armed stopped agent after fleet stall cleared",
		"worktree", ap.Entry.Worktree, "previous_stop_reason", previous, "last_error_class", lastErr)
}
