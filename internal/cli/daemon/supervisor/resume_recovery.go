package supervisor

import (
	"log/slog"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/agent"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/types"
)

// recoveryMode classifies how a supervise cycle recovers a worktree after a
// crash. The zero value is recoverCold (no recovery — claim a fresh task).
type recoveryMode int

const (
	recoverCold       recoveryMode = iota // destructive recover + claim a fresh task
	recoverResume                         // preserve lock + re-claim the same task + `--resume`
	recoverCheckpoint                     // re-claim the same task, cold-start with checkpoint injection (no `--resume`)
)

// maxResumeFailures is the number of consecutive `--resume` attempts before the
// supervisor stops resuming. The (maxResumeFailures)th failure escalates to a
// single checkpoint retry of the SAME task; a failure beyond that cold-starts a
// fresh task. So the escalation is: resume × maxResumeFailures → checkpoint × 1
// → cold-start. Mirrors the automode resumeFailures>=2 ceiling.
const maxResumeFailures = 2

// detectRecovery inspects the worktree's surviving lock and the persisted
// failure count to decide how this supervise cycle should recover: RESUME the
// interrupted task's Claude session, retry the same task with CHECKPOINT
// injection (resume-first / checkpoint-fallback), or COLD-start a fresh task. It
// returns the task id to re-claim ("" for cold) and the mode.
//
// The lock is deliberately NOT mutated here; the resume/checkpoint paths decide
// what to preserve vs clear. A resume needs a carried Claude session id; a
// checkpoint does not (the agent re-derives the prior attempt's WIP from the
// saved checkpoint + worktree diff).
func (s *Supervisor) detectRecovery(ap *AgentProcess) (string, recoveryMode) {
	info, running, err := cli.CheckLock(ap.WorktreePath)
	if err == nil && info != nil && !running && info.TaskID != "" {
		if ttl := agent.ResumeTTL(); ttl > 0 && !info.TaskStartedAt.IsZero() && time.Since(info.TaskStartedAt) > ttl {
			slog.Info("interrupted task too old to recover; cold-starting",
				"worktree", ap.Entry.Worktree, "task_id", info.TaskID,
				"age", time.Since(info.TaskStartedAt).Round(time.Second))
			return "", recoverCold
		}
		return s.recoveryModeForLock(ap, info)
	}
	// Incomplete exit-0 recovery clears the lock after saving its checkpoint.
	// Carry that checkpoint into the next fresh claim before cold recovery can
	// discard the committed task worktree. A daemon restart resets WorktreePath
	// to the agent's home, so the home copy is checked as well.
	for _, lockDir := range checkpointLockDirs(ap) {
		if cp, cpErr := config.LoadCheckpoint(lockDir); cpErr == nil && cp != nil && cp.TaskID != "" && (cp.AgentName == "" || cp.AgentName == ap.Entry.Worktree) {
			return cp.TaskID, recoverCheckpoint
		}
	}
	return "", recoverCold // no crash remnant / agent still alive / no task to recover
}

// recoveryTaskAvailable verifies that a task selected from a crash remnant is
// still this agent's to continue before recovery re-claims it.
//
// It refuses exactly one shape: blocked. That is what the task quarantine
// ledger writes (blocked + unassigned + a loom:quarantined label), and blocked
// is still claimable in fleet-db, so without this check the very agent whose
// no-progress kills triggered the quarantine re-claimed the same task on its
// next cycle — sixteen seconds later, in the run that found this. A task a
// human blocked by hand is refused for the same reason.
//
// Every other status stays recoverable on purpose. review, deferred and hooked
// are ordinary outcomes of an incomplete exit-0 run — the shapes resume and
// checkpoint recovery exist for — and closed or tombstone tasks are not
// claimable in fleet-db, so the claim fails on its own without a guard here.
// A failed read is deliberately permissive: the remnant is still the best
// evidence of an interrupted run.
func (s *Supervisor) recoveryTaskAvailable(ap *AgentProcess, taskID string) bool {
	if s.IssueBackend == nil || taskID == "" {
		return true
	}
	ctx, cancel := s.operationContext(claimOperationTimeout)
	issue, err := s.IssueBackend.Get(ctx, taskID)
	cancel()
	if err != nil || issue == nil {
		return true
	}
	if issue.Status != string(types.StatusBlocked) {
		return true
	}
	slog.Info("recovery task is blocked; cold-starting instead of re-claiming it",
		"task_id", taskID, "status", issue.Status, "agent", ap.Entry.Worktree)
	return false
}

// guardRecovery downgrades a recovery the task itself has revoked to a cold
// start. It deliberately mutates NOTHING on disk: the refused task's worktree
// still holds the interrupted run's uncommitted work, and its checkpoint still
// holds that run's diff. Destroying either here would lose work that no
// checkpoint can restore (`git clean` removes untracked files; captureGitDiff
// only records tracked ones) and would leave the task permanently unclaimable
// once a human releases it. The remnant survives, so recovery re-arms by itself
// the moment the task is unblocked.
//
// The cold recovery preFlightSetup then runs must be the non-destructive form,
// which is what the returned flag is for.
func (s *Supervisor) guardRecovery(ap *AgentProcess, taskID string, mode recoveryMode) (string, recoveryMode, bool) {
	if mode == recoverCold || s.recoveryTaskAvailable(ap, taskID) {
		return taskID, mode, false
	}
	return "", recoverCold, true
}

func (s *Supervisor) recoveryModeForLock(ap *AgentProcess, info *cli.LockInfo) (string, recoveryMode) {
	ap.Mu.Lock()
	fails := ap.ResumeFailures
	ap.Mu.Unlock()
	switch {
	case fails > maxResumeFailures:
		// resume (×maxResumeFailures) + checkpoint (×1) both exhausted → stop
		// retrying this task and claim a fresh one.
		slog.Warn("recovery exhausted; cold-starting a fresh task",
			"worktree", ap.Entry.Worktree, "task_id", info.TaskID, "failures", fails)
		return "", recoverCold
	case fails == maxResumeFailures:
		// `--resume` kept failing → one checkpoint retry of the SAME task.
		return info.TaskID, recoverCheckpoint
	case info.ClaudeSessionID != "":
		return info.TaskID, recoverResume
	default:
		// A task remnant with no captured session can't be `--resume`d; that is
		// out of scope for resume-recovery — cold-start.
		return "", recoverCold
	}
}

// prepareResume sets up a `--resume` cycle: kill any orphaned backend the crashed
// run left under this worktree (so two CLIs never share one session) but PRESERVE
// the lock, the in-progress worktree, and the fleet claim — the opposite of the
// destructive recoverAgent path — then target the interrupted task so claimTask
// self-recovers it. The preserved lock carries the Claude session id forward
// (cli.AcquireLock) so agent-side maybeResumeDaemonSession arms `--resume`.
func (s *Supervisor) prepareResume(ap *AgentProcess, taskID string) {
	s.sweepWorktreeBackends(ap)
	ap.Mu.Lock()
	ap.ResumeTaskID = taskID
	ap.Mu.Unlock()
	slog.Info("resuming interrupted task", "worktree", ap.Entry.Worktree, "task_id", taskID)
}

// prepareCheckpointRetry sets up a CHECKPOINT cycle after `--resume` is
// exhausted: re-claim the SAME task but CLEAR the carried Claude session id so
// the agent cold-starts (no `--resume`) and injectCheckpointIfNotResuming
// re-derives the prior attempt's WIP from the saved checkpoint. The worktree
// (and its diff) is preserved — recoverAgent is skipped — so the checkpoint has
// content.
func (s *Supervisor) prepareCheckpointRetry(ap *AgentProcess, taskID string) {
	s.sweepWorktreeBackends(ap)
	// Drop the carried session so maybeResumeDaemonSession won't arm `--resume`;
	// the agent then falls back to checkpoint injection for this task.
	if err := cli.ClearStaleLockClaudeSessionID(ap.WorktreePath); err != nil {
		slog.Warn("checkpoint retry: failed to clear carried session id",
			"worktree", ap.Entry.Worktree, "task_id", taskID, "err", err)
	}
	ap.Mu.Lock()
	ap.ResumeTaskID = taskID
	ap.Mu.Unlock()
	slog.Info("resume exhausted; retrying task with checkpoint",
		"worktree", ap.Entry.Worktree, "task_id", taskID)
}

// sweepWorktreeBackends kills any orphaned backend process still running under
// this worktree from a crashed run, scoped so the daemon never signals
// processes that are not its own.
func (s *Supervisor) sweepWorktreeBackends(ap *AgentProcess) {
	if killed := s.killOrphanedWorktreeProcesses([]string{ap.WorktreePath}); killed > 0 {
		slog.Info("killed orphaned backend before recovery",
			"worktree", ap.Entry.Worktree, "count", killed)
	}
}

// recordResumeOutcome updates the persisted recovery-failure counter after a
// supervised run. Only recovery cycles (resume or checkpoint) count: a clean
// exit clears the counter (the task progressed), a failure advances it toward
// the cold-start ceiling. A non-recovery (cold) cycle is ignored.
func (s *Supervisor) recordResumeOutcome(ap *AgentProcess) {
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.RecoveryMode == recoverCold {
		return // this cycle was not a recovery
	}
	if ap.LastExitCode == 0 {
		ap.ResumeFailures = 0
	} else {
		ap.ResumeFailures++
	}
}
