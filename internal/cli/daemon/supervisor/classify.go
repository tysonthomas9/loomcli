package supervisor

import (
	"log"
	"log/slog"
	"time"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/agent"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// classifyAgentExit reads the lock file (before recovery clears it) and classifies
// the agent's exit into an error class. Sets ap.LastError and ap.LastNoWork.
func (s *Supervisor) classifyAgentExit(ap *AgentProcess, exitCode int) {
	// Read lock info before recovery clears it (for logging and NoWork detection)
	lockInfo, _, _ := cli.CheckLock(ap.WorktreePath)
	taskID := s.taskIDForLifecycle(ap, lockInfo)
	if taskID != "" {
		title := ""
		if lockInfo != nil {
			title = lockInfo.TaskTitle
		}
		log.Printf("[daemon] Agent %s: exited with code %d (task %s: %s)",
			ap.Entry.Worktree, exitCode, taskID, title)
	} else {
		log.Printf("[daemon] Agent %s: exited with code %d", ap.Entry.Worktree, exitCode)
	}

	// Resolve backend for classification
	ap.Mu.Lock()
	backend := ap.Entry.Backend
	logPath := ap.LogFilePath
	logStart := ap.LogFileStartOffset
	stopReason := ap.StopReason
	ap.Mu.Unlock()
	if backend == "" {
		backend = s.ConfigSnapshot().Backend
	}

	if s.classifyFromHarnessMarker(ap, exitCode, backend, logPath, logStart, stopReason) {
		return
	}

	// A duration kill is classified from the stop reason rather than the exit,
	// and checked before everything else because every arm below would read it
	// wrong. See markRunDurationExceeded.
	if stopReason == StopReasonRunDurationExceeded {
		s.markRunDurationExceeded(ap, exitCode, backend)
		return
	}

	if taskID == "" && (exitCode == 0 || stopReason == StopReasonWatchdog) {
		s.markNoWork(ap, backend)
	} else if exitCode != 0 {
		ae := agenterr.ClassifyFromLogAt(logPath, logStart, exitCode, backend)
		ap.Mu.Lock()
		ap.LastError = ae
		ap.LastNoWork = false
		ap.Mu.Unlock()
		log.Printf("[daemon] Agent %s: classified error: %v", ap.Entry.Worktree, ae)
	} else if s.runLeftClaimHeld(ap, taskID) {
		s.markIncompleteRun(ap, taskID, backend)
	} else {
		ap.Mu.Lock()
		ap.LastError = nil
		ap.LastNoWork = false
		ap.Mu.Unlock()
	}
}

// classifyFromHarnessMarker applies the leaf's categorical markers, and
// reports whether one applied.
//
// A categorical harness marker outranks the stop reason, and is therefore
// read before it. The reasoning below ("a capped run has no characteristic
// output, so log classification is arbitrary") holds for INFERENCE from a
// kill's arbitrary output tail — it does not hold for one of the three
// explicit markers the leaf emits, which are statements about the account,
// not guesses about the text. Without this a billing wall that outlived the
// run cap is filed as a timeout, and a walled agent with no task claimed is
// filed as "no work": both hide the one fact an operator needs.
//
// Only the explicit markers override. A pattern match never does.
func (s *Supervisor) classifyFromHarnessMarker(ap *AgentProcess, exitCode int, backend, logPath string, logStart int64, stopReason StopReason) bool {
	ae, ok := agenterr.ClassifyMarkerFromLogAt(logPath, logStart)
	if !ok {
		return false
	}
	ae.ExitCode = exitCode
	ae.Backend = backend
	ap.Mu.Lock()
	ap.LastError = ae
	ap.LastNoWork = false
	ap.Mu.Unlock()
	slog.Info("agent exit classified from a harness marker",
		"worktree", ap.Entry.Worktree, "class", ae.Class, "stop_reason", stopReason, "message", ae.Message)
	return true
}

// markNoWork records an exit with no task attached: the agent found nothing
// claimable and went home. A watchdog stop can make an otherwise idle agent exit
// non-zero, so this is preferred over log-pattern timeout classification
// whenever there is no task context — but only for the silence watchdog. A run
// stopped for LENGTH never reaches here; see markRunDurationExceeded.
func (s *Supervisor) markNoWork(ap *AgentProcess, backend string) {
	ap.Mu.Lock()
	ap.LastError = &agenterr.AgentError{
		Class:   agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome),
		Message: "no claimable tasks",
		Backend: backend,
	}
	ap.LastNoWork = true
	ap.Mu.Unlock()
	log.Printf("[daemon] Agent %s: no work available (idle)", ap.Entry.Worktree)
}

// markRunDurationExceeded records a run the supervisor killed for outliving its
// wall-clock cap.
//
// It is classified here, from the stop reason, because none of the exit-shaped
// arms can get it right — and which one it lands in turns on nothing more
// meaningful than whether the harness installed a SIGTERM handler:
//
//   - Exit 0 with no task claimed reaches the NoWork arm, which reads a
//     four-hour stall as "idle, nothing to do" and hands it an UNCOUNTED retry
//     (agentpolicy.Decide on NoWorkOutcome). The cap would then fire every four
//     hours forever and change nothing. Folding this into StopReasonWatchdog
//     would be worse still: that reason widens the arm to any exit code.
//   - Exit 0 with a task claimed reaches the IncompleteRun arm. Close, but
//     wrong in the way that matters — that outcome describes a turn that ended
//     before its task did, whereas this run did not end early, it was ended
//     late, by us. And if the claim happens to have been released, it reaches
//     the clean-success arm instead, where shouldRestart zeroes every counter
//     the kill was meant to charge.
//   - A non-zero exit log-classifies the tail: whatever the agent happened to
//     print in its last hundred lines. A run capped for length has no
//     characteristic output, so the verdict is arbitrary — in practice the
//     exit-143 fallback, Transient, which is the wrong backoff bucket. This
//     is an argument about INFERENCE from prose, which is why classifyAgentExit
//     still lets an explicit harness marker outrank the stop reason before it
//     ever gets here.
//
// The class is wrapper.ErrTimeout rather than a new domain outcome. That is
// already where the silence watchdog's own kills resolve (exit 137 via
// classifyByExitCode), and its disposition is precisely what a blown time budget
// wants: a COUNTED retry on the timeout backoff, escalating to Block when the
// budget is spent, and quarantine-eligible — two runs of the same task both
// hitting the ceiling is exactly the no-progress signal task quarantine watches
// for. A fresh DomainOutcome would have to re-earn all three, and would be
// quarantine-INeligible by construction: QuarantineEligible answers false for
// every domain outcome, on the grounds that they are coordination signals rather
// than task-fault. A run that cannot finish inside four hours is task-fault.
func (s *Supervisor) markRunDurationExceeded(ap *AgentProcess, exitCode int, backend string) {
	ap.Mu.Lock()
	ap.LastError = &agenterr.AgentError{
		Class:     agenterr.OutcomeFromHarness(wrapper.ErrTimeout),
		ExitCode:  exitCode,
		Message:   "run exceeded its maximum duration and was stopped by the supervisor",
		Backend:   backend,
		Timestamp: time.Now(),
	}
	ap.LastNoWork = false
	ap.Mu.Unlock()
	log.Printf("[daemon] Agent %s: run exceeded its maximum duration — treating the run as failed",
		ap.Entry.Worktree)
}

// markIncompleteRun records an exit-0 run whose claim was never released: the
// turn ended, the task did not.
//
// Without this outcome the run is indistinguishable from a completed one — a
// daemon worker always carries a task id, so it fell through to the
// clean-success arm and every downstream consumer treated the unfinished work
// as delivered: the checkpoint cleared, the untracked WIP git-cleaned, the
// restart/block budgets zeroed and the quarantine ledger evicted. The distinct
// class is what lets those paths tell the two apart; it is a counted retry
// (agentpolicy.Decide), not a success.
func (s *Supervisor) markIncompleteRun(ap *AgentProcess, taskID, backend string) {
	ap.Mu.Lock()
	ap.LastError = &agenterr.AgentError{
		Class:     agenterr.OutcomeFromDomain(agenterr.IncompleteRunOutcome),
		Message:   "exited 0 without releasing the claim on " + taskID,
		Backend:   backend,
		Timestamp: time.Now(),
	}
	ap.LastNoWork = false
	ap.Mu.Unlock()
	log.Printf("[daemon] Agent %s: exited 0 but task %s is still claimed — treating the run as incomplete",
		ap.Entry.Worktree, taskID)
}

// runLeftClaimHeld reports whether an exit-0 run left its fleet claim in place,
// which means the agent never reached `loom complete` and the task is unfinished.
//
// Reached only for exit 0 with a task attached; every other shape is already
// classified by the time we get here. Costs one GET on the exit path, bounded by
// the same timeout as the supervisor's other claim operations, and answers false
// whenever it cannot tell — see ClaimStillHeld for why the ambiguity has to fall
// back to the established clean-success behavior.
func (s *Supervisor) runLeftClaimHeld(ap *AgentProcess, taskID string) bool {
	if s.IssueBackend == nil || taskID == "" {
		return false
	}
	ctx, cancel := s.operationContext(claimOperationTimeout)
	defer cancel()
	return agent.ClaimStillHeld(ctx, s.IssueBackend, taskID, ap.Entry.Worktree)
}

// isIncompleteRun reports whether classifyAgentExit tagged this exit as a run
// that ended without finishing its task. The single predicate every path that
// would otherwise destroy the run's state consults.
func isIncompleteRun(ap *AgentProcess) bool {
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	return ap.LastError != nil && ap.LastError.Class.Is(agenterr.IncompleteRunOutcome)
}

// markSpawnFailure records a spawn failure as a synthetic agent exit so the
// single post-spawn restart decision (shouldRestart + sleepBeforeRestart) owns
// counting and backoff — there is no longer a separate decision-maker for the
// spawn-failure path.
//
// LastExitCode = -1 (with a non-nil LastError) keeps shouldRestart's
// clean-success branch from firing on stale state from a prior clean run, which
// would otherwise reset RestartCount and retry spawn failures forever. The
// SpawnFailure class is non-fatal and counts toward max_retries, and does not
// trigger backend failover (see tryFallbackBackend).
func (s *Supervisor) markSpawnFailure(ap *AgentProcess, spawnErr error) {
	// Resolve backend before locking — GetEffectiveBackend acquires ap.Mu.
	backend := s.GetEffectiveBackend(ap)

	msg := "failed to spawn agent subprocess"
	if spawnErr != nil {
		msg = spawnErr.Error()
	}

	ap.Mu.Lock()
	ap.LastExitCode = -1
	ap.LastNoWork = false
	ap.LastError = &agenterr.AgentError{
		Class:     agenterr.OutcomeFromDomain(agenterr.SpawnFailureOutcome),
		ExitCode:  -1,
		Message:   msg,
		Backend:   backend,
		Timestamp: time.Now(),
	}
	ap.Mu.Unlock()

	log.Printf("[daemon] Agent %s: spawn failed, treating as retryable error: %v",
		ap.Entry.Worktree, spawnErr)
}

// handleAgentCheckpoint saves a checkpoint on non-zero exit (before recovery clears the
// worktree) or clears the checkpoint on successful exit. For yield exits (exit 0 with
// yield file present), a yield checkpoint is saved instead of clearing. An incomplete
// exit-0 run is treated the same way: the work is unfinished, so the checkpoint is the
// only thing that carries it into the next cycle.
func (s *Supervisor) handleAgentCheckpoint(ap *AgentProcess, exitCode int) {
	if exitCode == 0 {
		// Check if this was a yield exit — save checkpoint instead of clearing
		if IsYieldRequested(ap.WorktreePath) {
			s.saveYieldCheckpoint(ap)
			return
		}
		// Exit 0 with the claim still held is a preemption in all but name: the
		// task is unfinished, so clearing here would throw away the one record
		// of what the turn achieved. Save it under the IncompleteRun class (the
		// exit code stays 0 — it was not a crash) so a cold-started next cycle
		// re-derives the WIP through injectCheckpointIfNotResuming.
		if isIncompleteRun(ap) {
			s.saveAgentCheckpoint(ap, exitCode)
			return
		}
		lockDir := cli.ResolveLockDir(ap.WorktreePath)
		if err := config.ClearCheckpoint(lockDir); err != nil {
			log.Printf("[daemon] Agent %s: failed to clear checkpoint: %v", ap.Entry.Worktree, err)
		}
		return
	}
	s.saveAgentCheckpoint(ap, exitCode)
}

// saveAgentCheckpoint captures the current worktree diff and agent state into a
// checkpoint file. Called when an agent exits non-zero before recovery clears the worktree.
func (s *Supervisor) saveAgentCheckpoint(ap *AgentProcess, exitCode int) {
	lockInfo, _, _ := cli.CheckLock(ap.WorktreePath)
	taskID := s.taskIDForLifecycle(ap, lockInfo)
	if taskID == "" {
		return
	}

	diff := captureGitDiff(ap.WorktreePath, config.MaxDiffBytes)
	errClass := ""
	ap.Mu.Lock()
	if ap.LastError != nil {
		errClass = ap.LastError.Class.String()
	}
	epicID := ap.AssignedEpicID
	ap.Mu.Unlock()

	agentName := ap.Entry.Worktree
	if lockInfo != nil && lockInfo.AgentName != "" {
		agentName = lockInfo.AgentName
	}

	cp := &config.Checkpoint{
		AgentName:  agentName,
		TaskID:     taskID,
		EpicID:     epicID,
		GitDiff:    diff,
		ExitCode:   exitCode,
		ErrorClass: errClass,
		Timestamp:  time.Now(),
	}
	lockDir := cli.ResolveLockDir(ap.WorktreePath)
	if err := config.SaveCheckpoint(lockDir, cp); err != nil {
		log.Printf("[daemon] Agent %s: failed to save checkpoint: %v", ap.Entry.Worktree, err)
	} else {
		log.Printf("[daemon] Agent %s: saved checkpoint for task %s", ap.Entry.Worktree, taskID)
	}
}

// saveYieldCheckpoint captures the worktree state when an agent is preempted
// via yield. Unlike saveAgentCheckpoint (crash path), this sets ErrorClass to
// "Yielded" and records the yield reason from the yield file.
func (s *Supervisor) saveYieldCheckpoint(ap *AgentProcess) {
	lockInfo, _, _ := cli.CheckLock(ap.WorktreePath)
	taskID := s.taskIDForLifecycle(ap, lockInfo)
	if taskID == "" {
		return
	}

	diff := captureGitDiff(ap.WorktreePath, config.MaxDiffBytes)

	yieldReason := "unknown"
	if req, err := ReadYieldFile(ap.WorktreePath); err == nil && req != nil && req.Reason != "" {
		yieldReason = req.Reason
	}

	ap.Mu.Lock()
	epicID := ap.AssignedEpicID
	ap.Mu.Unlock()

	agentName := ap.Entry.Worktree
	if lockInfo != nil && lockInfo.AgentName != "" {
		agentName = lockInfo.AgentName
	}

	cp := &config.Checkpoint{
		AgentName:   agentName,
		TaskID:      taskID,
		EpicID:      epicID,
		GitDiff:     diff,
		ExitCode:    0,
		ErrorClass:  "Yielded",
		YieldReason: yieldReason,
		Timestamp:   time.Now(),
	}
	lockDir := cli.ResolveLockDir(ap.WorktreePath)
	if err := config.SaveCheckpoint(lockDir, cp); err != nil {
		log.Printf("[daemon] Agent %s: failed to save yield checkpoint: %v", ap.Entry.Worktree, err)
	} else {
		log.Printf("[daemon] Agent %s: saved yield checkpoint for task %s (reason: %s)",
			ap.Entry.Worktree, taskID, yieldReason)
	}
}

func (s *Supervisor) taskIDForLifecycle(ap *AgentProcess, lockInfo *cli.LockInfo) string {
	if lockInfo != nil && lockInfo.TaskID != "" {
		return lockInfo.TaskID
	}
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	return ap.AssignedTaskID
}

// ---------------------------------------------------------------------------
// Kill classification and the per-exit snapshot the ledger consumes
// ---------------------------------------------------------------------------

// killEvent is one observed kill of a task-holding agent, captured at exit
// time (after classifyAgentExit, before finalize/recovery clear the session
// and lock state it reads).
type killEvent struct {
	At              time.Time `json:"at"`
	Agent           string    `json:"agent"`             // ap.Entry.Worktree
	StopReason      string    `json:"stop_reason"`       // e.g. "watchdog"; empty for a bare crash / ownership kill
	ErrClass        string    `json:"err_class"`         // classified outcome (Unknown | Timeout | Transient | ContextOverflow)
	ExitCode        int       `json:"exit_code"`         //
	FleetSessionID  string    `json:"fleet_session_id"`  // ap.AgentSessionID — captured before finalize clears it
	ClaudeSessionID string    `json:"claude_session_id"` // lock ClaudeSessionID (best-effort; empty if absent)
	RunID           string    `json:"run_id"`            // lock RunID (best-effort)

	// RunSilent mirrors ap.RunSilentAtStop: for a run_duration_exceeded kill,
	// whether the run was ALSO silent past its output timeout. Meaningless (and
	// false) for every other stop reason.
	RunSilent bool `json:"run_silent,omitempty"`
	// NotCounted is the quarantineCountable reason string when this kill was
	// recorded in the timeline but never charged to the task's counter; empty
	// for a kill that counted.
	NotCounted string `json:"not_counted,omitempty"`
}

// reason renders a compact kill descriptor for status output, e.g.
// "watchdog/Timeout" or "crash/Unknown".
func (ev killEvent) reason() string {
	kind := ev.StopReason
	if kind == "" {
		kind = "crash"
	}
	if ev.ErrClass == "" {
		return kind
	}
	return kind + "/" + ev.ErrClass
}

// stopReasonQuarantineEligible reports whether a kill carrying this stop
// reason counts as evidence that the TASK is stalling.
//
// Lifecycle stops do not: a drain (config change, shutdown, an operator's
// stop) is a decision the daemon made about the AGENT, and the run it
// interrupted carries no evidence in either direction. The gate is therefore a
// skip, not an evict — an accumulated count survives a drain and the next
// genuine kill continues from where it left off.
//
// The behavioral consequence is worth stating plainly: a task that only ever
// dies during drains is never quarantined. That is correct — such a task is
// being killed by the daemon, not by its own stall.
//
// Everything else stays eligible, notably watchdog (the signal the whole
// mechanism was built for), run_duration_exceeded, fatal_error, fast_fail and
// a bare crash (empty reason). backend_unavailable and rate_limited are
// already filtered out by the outcome class.
func stopReasonQuarantineEligible(r StopReason) bool {
	switch r {
	case StopReasonConfigRemoved, StopReasonShutdown, StopReasonManualStop,
		StopReasonYielded, StopReasonEphemeralDone:
		return false
	default:
		return true
	}
}

// lifecycleUncountedReason names the lifecycle stop for the ledger's
// NotCounted field. Kept in step with stopReasonQuarantineEligible: every
// reason that predicate rejects needs a name here.
func lifecycleUncountedReason(r StopReason) string {
	switch r {
	case StopReasonShutdown:
		// The daemon SIGTERMed its own agents mid-run; the task was a bystander.
		return "daemon_shutdown"
	case StopReasonManualStop:
		return "manual_stop"
	case StopReasonConfigRemoved:
		return "config_removed"
	case StopReasonYielded:
		return "yielded"
	case StopReasonEphemeralDone:
		return "ephemeral_done"
	default:
		return "lifecycle_stop"
	}
}

// quarantineCountable reports whether this kill says anything about the TASK.
// Kills that are verdicts about the daemon, the agent, or the account are
// recorded in the timeline for diagnosis but never advance the counter.
//
// The outcome-class seat (agentpolicy.QuarantineEligible) cannot make this
// call: it sees an agenterr.Outcome, which knows nothing of stop reasons or
// daemon lifecycle. A daemon that SIGTERMs its own agents mid-run produces a
// perfectly eligible outcome class, and during the 2026-08-26/27 incident three
// such kills were enough to quarantine a task that had never stalled.
//
// StopReasonWatchdog stays COUNTABLE and deliberately so — the watchdog fires
// because the agent went silent past its output timeout, which is the
// definition of a stall and the breaker's best signal. So does a bare crash
// (empty StopReason), the breaker's base case. Blinding the counter to either
// would leave nothing to count.
func (s *Supervisor) quarantineCountable(ev killEvent) (bool, string) {
	r := StopReason(ev.StopReason)
	// Lifecycle stops keep stopReasonQuarantineEligible as their single
	// authority: it also rejects yielded and ephemeral_done, which the switch
	// below never listed and which must not be charged to a task either.
	if !stopReasonQuarantineEligible(r) {
		return false, lifecycleUncountedReason(r)
	}
	switch r {
	case StopReasonBackendUnavailable:
		// The agent's backend CLI is missing from PATH. Nothing to do with the task.
		return false, "backend_unavailable"
	case StopReasonMaxRetries, StopReasonMaxRetriesBlocked, StopReasonFastFail:
		// Agent-level budgets already escalate agent-side (block, fast-fail).
		// Charging the task too double-counts one failure against two breakers.
		return false, "agent_budget"
	case StopReasonRunDurationExceeded:
		// See applyRunDurationKill: the cap fires regardless of activity, so on
		// its own it is not a no-progress signal. A run that was still talking
		// when the ceiling hit it was working, however slowly; only a run that
		// was ALSO silent is the wedge markRunDurationExceeded argues about.
		if !ev.RunSilent {
			return false, "duration_kill_while_active"
		}
	}
	// Collateral of a daemon restart lands in a burst right after boot and says
	// nothing about any task. Zero BootedAt disables the grace — see the
	// constant.
	if !s.BootedAt.IsZero() && ev.At.Sub(s.BootedAt) < quarantineBootGrace {
		return false, "boot_grace"
	}
	return true, ""
}

// taskExitSnapshot is the per-exit state the ledger consumes, read under
// ap.Mu in one critical section.
type taskExitSnapshot struct {
	clean     bool
	outcome   agenterr.Outcome
	beforeRef string
	event     killEvent
}

func snapshotTaskExit(ap *AgentProcess, lockInfo *cli.LockInfo, exitCode int) taskExitSnapshot {
	ap.Mu.Lock()
	lastErr := ap.LastError
	stopReason := ap.StopReason
	beforeRef := ap.BeforeRef
	fleetSessionID := ap.AgentSessionID
	runSilent := ap.RunSilentAtStop
	ap.Mu.Unlock()

	snap := taskExitSnapshot{
		clean:     exitCode == 0 && lastErr == nil,
		beforeRef: beforeRef,
	}
	errClass := ""
	if lastErr != nil {
		snap.outcome = lastErr.Class
		errClass = lastErr.Class.String()
	}
	snap.event = killEvent{
		At:             time.Now(),
		Agent:          ap.Entry.Worktree,
		StopReason:     string(stopReason),
		ErrClass:       errClass,
		ExitCode:       exitCode,
		FleetSessionID: fleetSessionID,
		RunSilent:      runSilent,
	}
	if lockInfo != nil {
		snap.event.ClaudeSessionID = lockInfo.ClaudeSessionID
		snap.event.RunID = lockInfo.RunID
	}
	return snap
}
