package supervisor

import (
	"log/slog"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/agent"
)

// maxResumeFailures bounds consecutive failed resume attempts for a worktree
// before the supervisor abandons resume and cold-starts instead (resume-first
// with a cold-start fallback). Mirrors the automode resumeFailures>=2 ceiling.
const maxResumeFailures = 2

// detectResumableTask inspects the worktree's surviving lock to decide whether
// this supervise cycle should RESUME an interrupted task rather than claim a
// fresh one. It returns the task id to resume, or "" to cold-start.
//
// A resume is offered only when the lock is a genuine crash remnant: a DEAD
// agent PID with a carried Claude session id + task id, within the resume TTL,
// and under the consecutive-failure ceiling. The lock is deliberately NOT
// touched here — preserving it lets the agent's AcquireLock carry the session
// id + task id forward (lock.go), so agent-side maybeResumeDaemonSession can
// arm `--resume`. (The destructive recoverAgent path would delete the lock and
// discard the session id, which is exactly what prevented resume before.)
func (s *Supervisor) detectResumableTask(ap *AgentProcess) string {
	info, running, err := cli.CheckLock(ap.WorktreePath)
	if err != nil || info == nil {
		return ""
	}
	if running {
		return "" // the prior agent is still alive — not a crash to recover
	}
	if info.TaskID == "" || info.ClaudeSessionID == "" {
		return "" // no carried task/session ⇒ nothing to resume
	}
	if ttl := agent.ResumeTTL(); ttl > 0 && !info.TaskStartedAt.IsZero() && time.Since(info.TaskStartedAt) > ttl {
		slog.Info("interrupted task too old to resume; cold-starting",
			"worktree", ap.Entry.Worktree, "task_id", info.TaskID,
			"age", time.Since(info.TaskStartedAt).Round(time.Second))
		return ""
	}
	ap.Mu.Lock()
	fails := ap.ResumeFailures
	ap.Mu.Unlock()
	if fails >= maxResumeFailures {
		slog.Warn("resume failure ceiling reached; cold-starting",
			"worktree", ap.Entry.Worktree, "task_id", info.TaskID, "failures", fails)
		return ""
	}
	return info.TaskID
}

// prepareResume sets up a resume cycle. It kills any orphaned backend the
// crashed run left under this worktree (so we never run two CLIs on one
// session) but PRESERVES the lock, the worktree's in-progress files, and the
// fleet claim — the opposite of the destructive recoverAgent path — then
// records the interrupted task so claimTask self-recovers it (claimResumeTask).
func (s *Supervisor) prepareResume(ap *AgentProcess, taskID string) {
	if killed := s.killOrphanedWorktreeProcesses([]string{ap.WorktreePath}); killed > 0 {
		slog.Info("killed orphaned backend before resume", "worktree", ap.Entry.Worktree, "count", killed)
	}
	ap.Mu.Lock()
	ap.ResumeTaskID = taskID
	ap.Mu.Unlock()
	slog.Info("resuming interrupted task", "worktree", ap.Entry.Worktree, "task_id", taskID)
}

// recordResumeOutcome updates the consecutive-resume-failure counter after a
// supervised run. A non-resume cycle is ignored; a clean resume clears the
// counter; a failed resume advances it toward the cold-start ceiling.
func (s *Supervisor) recordResumeOutcome(ap *AgentProcess) {
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.ResumeTaskID == "" {
		return // this cycle was not a resume
	}
	if ap.LastExitCode == 0 {
		ap.ResumeFailures = 0
	} else {
		ap.ResumeFailures++
	}
}
