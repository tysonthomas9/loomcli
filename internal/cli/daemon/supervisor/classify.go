package supervisor

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/agent"
	"github.com/tysonthomas9/loomcli/internal/cli/automode"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit/agentcapture"
	"github.com/tysonthomas9/loomcli/internal/loomgit/driverfreeze"
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
//     exit-143 fallback, Transient, which is the wrong backoff bucket.
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

// handleAgentCheckpoint captures on every exit, including clean and yielded exits.
// Capture runs before session finalization and ownership release.
func (s *Supervisor) handleAgentCheckpoint(ap *AgentProcess, exitCode int) {
	lockInfo, _, _ := cli.CheckLock(ap.WorktreePath)
	taskID := s.taskIDForLifecycle(ap, lockInfo)
	taskTitle := ""
	if lockInfo != nil {
		taskTitle = lockInfo.TaskTitle
	}
	errClass := ""
	ap.Mu.Lock()
	if ap.LastError != nil {
		errClass = ap.LastError.Class.String()
	}
	epicID := ap.AssignedEpicID
	yieldReason := ap.YieldReason
	ap.Mu.Unlock()
	if yieldReason == "" {
		if req, err := ReadYieldFile(ap.WorktreePath); err == nil && req != nil {
			yieldReason = req.Reason
		}
	}
	agentName := ap.Entry.Worktree
	if lockInfo != nil && lockInfo.AgentName != "" {
		agentName = lockInfo.AgentName
	}
	lockDir := cli.ResolveLockDir(ap.WorktreePath)
	result, retained, pendingFreeze := s.captureAndFreezeExit(ap, agentName, taskID, taskTitle, epicID, yieldReason, exitCode, lockDir)
	s.finishAgentCheckpoint(ap, result, pendingFreeze, retained, agentName, taskID, epicID, errClass, yieldReason, exitCode, lockDir)
}

func (s *Supervisor) finishAgentCheckpoint(ap *AgentProcess, result agentcapture.Result, pendingFreeze *config.Checkpoint, retained bool, agentName, taskID, epicID, errClass, yieldReason string, exitCode int, lockDir string) {
	if exitCode == 0 && yieldReason == "" && !isIncompleteRun(ap) && !retained {
		if err := config.ClearCheckpoint(lockDir); err != nil {
			log.Printf("[daemon] Agent %s: failed to clear checkpoint: %v", ap.Entry.Worktree, err)
		}
		return
	}
	if taskID == "" {
		return
	}
	if yieldReason != "" {
		errClass = "Yielded"
	}
	if pendingFreeze == nil {
		pendingFreeze = &config.Checkpoint{AgentName: agentName, TaskID: taskID, EpicID: epicID, CaptureRef: result.Ref}
	}
	pendingFreeze.Retained = retained
	pendingFreeze.ExitCode = exitCode
	pendingFreeze.ErrorClass = errClass
	pendingFreeze.YieldReason = yieldReason
	pendingFreeze.Timestamp = time.Now()
	s.saveCaptureCheckpoint(ap, lockDir, pendingFreeze)
}

func (s *Supervisor) captureAndFreezeExit(ap *AgentProcess, agentName, taskID, taskTitle, epicID, yieldReason string, exitCode int, lockDir string) (agentcapture.Result, bool, *config.Checkpoint) {
	pendingFreeze, attempt := s.pendingExitFreeze(ap, agentName, taskID, epicID, yieldReason, exitCode, lockDir)
	if pendingFreeze != nil {
		ctx, cancel := context.WithTimeout(context.Background(), controlPlaneOperationTimeout)
		ready, err := driverfreeze.CaptureAlreadyFrozen(ctx, s.WorkspaceID, taskID, pendingFreeze.FreezeRepo, attempt)
		cancel()
		if err != nil {
			slog.Error("agent revision lookup needs attention", "task_id", taskID, "err", err)
			return agentcapture.Result{}, true, pendingFreeze
		}
		if ready {
			return agentcapture.Result{}, false, nil
		}
	}
	result, retained := s.captureExitWorktree(ap, agentName, taskID, taskTitle, attempt)
	if pendingFreeze == nil || retained {
		return result, retained, pendingFreeze
	}
	captureSHA := result.SHA
	pendingFreeze.CaptureRef = result.Ref
	if captureSHA == "" {
		captureSHA = automode.CaptureHEADRef(ap.WorktreePath)
		pendingFreeze.CaptureRef = captureSHA
	}
	s.saveCaptureCheckpoint(ap, lockDir, pendingFreeze)
	if ap.CaptureRetained {
		return result, true, pendingFreeze
	}
	if err := s.freezeCheckpoint(ap, pendingFreeze, captureSHA, result.Complete); err != nil {
		ap.Mu.Lock()
		ap.CaptureRetained = true
		ap.Mu.Unlock()
		slog.Error("agent revision freeze needs attention; worktree retained", "task_id", taskID, "err", err)
		return result, true, pendingFreeze
	}
	return result, false, nil
}

func (s *Supervisor) pendingExitFreeze(ap *AgentProcess, agentName, taskID, epicID, yieldReason string, exitCode int, lockDir string) (*config.Checkpoint, string) {
	ap.Mu.Lock()
	base, attempt := ap.BeforeRef, ap.AgentSessionID
	ap.Mu.Unlock()
	if s.WorkspaceID == "" || taskID == "" || base == "" || attempt == "" {
		return nil, attempt
	}
	state := "completed"
	if exitCode != 0 || isIncompleteRun(ap) {
		state = "failed"
	} else if yieldReason != "" {
		state = "cancelled"
	}
	cp := &config.Checkpoint{AgentName: agentName, TaskID: taskID, EpicID: epicID,
		FreezeBase: base, FreezeID: attempt, FreezeRepo: s.freezeRepoName(ap),
		FreezeState: state, Timestamp: time.Now()}
	s.saveCaptureCheckpoint(ap, lockDir, cp)
	return cp, attempt
}

func (s *Supervisor) freezeRepoName(ap *AgentProcess) string {
	if ap.WorktreeRepo != "" {
		return ap.WorktreeRepo
	}
	return filepath.Base(ap.WorktreePath)
}

func (s *Supervisor) freezeSourcePath(ap *AgentProcess) string {
	if ap.RepoConfig != nil {
		return ap.RepoConfig.ResolveAbsPath(s.ProjectDir)
	}
	return ap.WorktreePath
}

func (s *Supervisor) freezeCheckpoint(ap *AgentProcess, cp *config.Checkpoint, captureSHA string, complete bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	source := ""
	if ap.RepoConfig != nil {
		source = s.freezeSourcePath(ap)
	}
	_, err := driverfreeze.FreezeCapture(ctx, driverfreeze.CaptureRequest{
		Workspace: s.WorkspaceID, Task: cp.TaskID, Repo: cp.FreezeRepo, Attempt: cp.FreezeID,
		Worktree: ap.WorktreePath, Base: cp.FreezeBase, CaptureSHA: captureSHA,
		SourceRepo: source, Outcome: cp.FreezeState, Complete: complete, SkipRetention: true,
	})
	return err
}

func (s *Supervisor) reconcilePendingFreeze(ap *AgentProcess) error {
	lockDir := cli.ResolveLockDir(ap.WorktreePath)
	cp, err := config.LoadCheckpoint(lockDir)
	if err != nil || cp == nil || cp.FreezeID == "" {
		return err
	}
	if cp.CaptureRef == "" {
		result, retained := s.captureExitWorktree(ap, cp.AgentName, cp.TaskID, "", cp.FreezeID)
		if retained {
			return fmt.Errorf("pending revision capture is incomplete")
		}
		cp.CaptureRef = result.Ref
		if cp.CaptureRef == "" {
			cp.CaptureRef = automode.CaptureHEADRef(ap.WorktreePath)
		}
		if err := config.SaveCheckpoint(lockDir, cp); err != nil {
			return err
		}
	}
	sha, err := cli.RunGitCommand(ap.WorktreePath, "rev-parse", cp.CaptureRef)
	if err != nil {
		return err
	}
	if err := s.freezeCheckpoint(ap, cp, strings.TrimSpace(sha), true); err != nil {
		return err
	}
	cp.FreezeID = ""
	return config.SaveCheckpoint(lockDir, cp)
}

func (s *Supervisor) saveCaptureCheckpoint(ap *AgentProcess, lockDir string, cp *config.Checkpoint) {
	if err := config.SaveCheckpoint(lockDir, cp); err != nil {
		slog.Error("agent checkpoint needs attention; worktree retained", "worktree", cp.AgentName, "err", err)
		ap.Mu.Lock()
		ap.CaptureRetained = true
		ap.Mu.Unlock()
	} else {
		log.Printf("[daemon] Agent %s: saved capture checkpoint for task %s", ap.Entry.Worktree, cp.TaskID)
	}
}

func (s *Supervisor) captureExitWorktree(ap *AgentProcess, agentName, taskID, taskTitle, attempt string) (agentcapture.Result, bool) {
	workspace := s.WorkspaceID
	if workspace == "" {
		workspace = agentName
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	captureFn := s.captureWorktree
	if captureFn == nil {
		source := s.freezeSourcePath(ap)
		captureFn = func(ctx context.Context, copyPath, workspace, attempt, taskID, taskTitle string) (agentcapture.Result, error) {
			return agentcapture.CaptureTaskCopy(ctx, source, copyPath, workspace, attempt, taskID, taskTitle)
		}
	}
	if attempt == "" {
		attempt = uuid.NewString()
	}
	result, captureErr := captureFn(ctx, ap.WorktreePath, workspace, attempt, taskID, taskTitle)
	retained := captureErr != nil || !result.Complete
	if retained {
		ap.Mu.Lock()
		ap.CaptureRetained = true
		ap.Mu.Unlock()
		if captureErr == nil {
			captureErr = fmt.Errorf("capture manifest is incomplete")
		}
		slog.Error("agent capture needs attention; worktree retained", "worktree", agentName, "task_id", taskID, "err", captureErr)
	}
	return result, retained
}

func (s *Supervisor) taskIDForLifecycle(ap *AgentProcess, lockInfo *cli.LockInfo) string {
	if lockInfo != nil && lockInfo.TaskID != "" {
		return lockInfo.TaskID
	}
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	return ap.AssignedTaskID
}
