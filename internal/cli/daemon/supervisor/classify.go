package supervisor

import (
	"log"
	"time"

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
	stopReason := ap.StopReason
	ap.Mu.Unlock()
	if backend == "" {
		backend = s.ConfigSnapshot().Backend
	}

	if taskID == "" && (exitCode == 0 || stopReason == StopReasonWatchdog) {
		// No work available — no task was claimed. A watchdog stop can make an
		// otherwise idle agent exit non-zero, so prefer NoWork over log-pattern
		// timeout classification when there is no task context.
		ap.Mu.Lock()
		ap.LastError = &agenterr.AgentError{
			Class:   agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome),
			Message: "no claimable tasks",
			Backend: backend,
		}
		ap.LastNoWork = true
		ap.Mu.Unlock()
		log.Printf("[daemon] Agent %s: no work available (idle)", ap.Entry.Worktree)
	} else if exitCode != 0 {
		ae := agenterr.ClassifyFromLog(logPath, exitCode, backend)
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
