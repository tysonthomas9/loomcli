package supervisor

import (
	"log"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/cli"
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
			Class:   agenterr.NoWork,
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
	} else {
		ap.Mu.Lock()
		ap.LastError = nil
		ap.LastNoWork = false
		ap.Mu.Unlock()
	}
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
		Class:     agenterr.SpawnFailure,
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
// yield file present), a yield checkpoint is saved instead of clearing.
func (s *Supervisor) handleAgentCheckpoint(ap *AgentProcess, exitCode int) {
	if exitCode == 0 {
		// Check if this was a yield exit — save checkpoint instead of clearing
		if IsYieldRequested(ap.WorktreePath) {
			s.saveYieldCheckpoint(ap)
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
