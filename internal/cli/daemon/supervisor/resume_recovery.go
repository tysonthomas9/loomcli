package supervisor

import (
	"log/slog"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/agent"
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
	if err != nil || info == nil || running || info.TaskID == "" {
		return "", recoverCold // no crash remnant / agent still alive / no task to recover
	}
	if ttl := agent.ResumeTTL(); ttl > 0 && !info.TaskStartedAt.IsZero() && time.Since(info.TaskStartedAt) > ttl {
		slog.Info("interrupted task too old to recover; cold-starting",
			"worktree", ap.Entry.Worktree, "task_id", info.TaskID,
			"age", time.Since(info.TaskStartedAt).Round(time.Second))
		return "", recoverCold
	}
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
	if err := cli.ClearLockClaudeSessionID(ap.WorktreePath); err != nil {
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
