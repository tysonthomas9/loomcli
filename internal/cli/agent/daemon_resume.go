package agent

import (
	"fmt"
	"os"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
)

// defaultResumeTTL bounds how long a carried-forward Claude session may sit IDLE
// — measured from when the last run in the worktree ended — before a restart
// treats it as stale and cold-starts instead of resuming.
//
// It is idle time, not session age, and the two hours are sized so a single
// long run can never age out its own session. The previous bound was 30 minutes
// of TASK age (LockInfo.TaskStartedAt), which deliberately survives a resume
// cycle: a run killed at the 43-minute per-turn ceiling therefore always
// presented a task older than the TTL and always cold-started, making the
// resume machinery unreachable for exactly the failure it was built for.
const defaultResumeTTL = 2 * time.Hour

// ResumeTTL returns the resume staleness bound — the maximum IDLE time since
// the last run ended — overridable via LOOM_RESUME_TTL (a Go duration string,
// e.g. "45m"). Falls back to defaultResumeTTL. Exported so the daemon
// supervisor's resume-detection shares the exact same bound as the agent-side
// resume decision (maybeResumeDaemonSession).
func ResumeTTL() time.Duration {
	if v := os.Getenv("LOOM_RESUME_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 0 {
			return d
		}
	}
	return defaultResumeTTL
}

// maybeResumeDaemonSession arms backend session-resume from the lock that
// AcquireLock carried forward from a prior (dead) attempt — but only when this
// is a SAME-TASK daemon restart whose session has not been idle past the TTL.
// It is deliberately conservative (the resume DECISION + guards live here; the
// lock-level carry-forward is in cli.AcquireLock): it resumes only for an
// explicit assigned task that matches the carried lock's TaskID, so a restart
// for a different task — or a stale/absent session — cold-starts instead.
//
// The caller clears the lock session id on a SUCCESSFUL run (so the next restart
// cold-starts the next task); a failed run keeps it for carry-forward.
func maybeResumeDaemonSession(worktreePath, assignedTaskID string) {
	if assignedTaskID == "" {
		// no specific task assigned → cold start (can't verify same-task)
		logResumeSkip("no assigned task id", "")
		return
	}
	info, err := cli.ReadLockFile(worktreePath)
	if err != nil {
		logResumeSkip("lock unreadable", "%s: %v", worktreePath, err)
		return
	}
	if info == nil {
		logResumeSkip("no lock file", "%s", worktreePath)
		return
	}
	if info.ClaudeSessionID == "" {
		logResumeSkip("lock carries no claude session id", "task %s", info.TaskID)
		return
	}
	if info.TaskID != assignedTaskID {
		// carried session belongs to a different task → cold start
		logResumeSkip("task id mismatch", "lock has %q, assigned %q", info.TaskID, assignedTaskID)
		return
	}
	since, clock := ResumeStalenessClock(info)
	if ttl := ResumeTTL(); ttl > 0 && !since.IsZero() && time.Since(since) > ttl {
		fmt.Printf("[daemon] prior Claude session for %s is stale (idle %s by %s); cold-starting\n",
			assignedTaskID, time.Since(since).Round(time.Second), clock)
		return
	}
	backends.SetResumeSessionID(info.ClaudeSessionID)
	fmt.Printf("[daemon] resuming Claude session for task %s (run %s)\n", assignedTaskID, info.RunID)
}

// ResumeStalenessClock picks the timestamp the TTL is measured from, and names
// it so the cold-start line says which one it used.
//
// LastRunEndedAt is the right clock: staleness is idle time, not task age. It is
// zero on a lock written by an older binary, and a zero timestamp must never
// read as "infinitely fresh" — so that case falls back to TaskStartedAt, which
// is the bound the old code applied and is at worst conservative. Both zero
// means there is nothing to measure and the other guards (same task id,
// non-empty session id, per-worktree lock) carry the decision alone.
func ResumeStalenessClock(info *cli.LockInfo) (time.Time, string) {
	if !info.LastRunEndedAt.IsZero() {
		return info.LastRunEndedAt, "last run end"
	}
	return info.TaskStartedAt, "task start (no last-run-end on this lock)"
}

// markDaemonRunEnded stamps the lock with this run's end time — the clock the
// NEXT attempt measures the resume TTL against. Called however the run ended,
// because a run that ended badly is the one worth resuming. Best effort: a lock
// this process no longer owns (or that is already gone) leaves the timestamp
// unwritten, and ResumeStalenessClock falls back to TaskStartedAt.
func markDaemonRunEnded(worktreePath string) {
	if err := cli.MarkLockRunEnded(worktreePath); err != nil {
		fmt.Fprintf(os.Stderr, "[daemon] failed to record run end on lock: %v\n", err)
	}
}

// logResumeSkip reports WHY maybeResumeDaemonSession declined to resume. Every
// early return names itself here on purpose: this function used to return
// silently on four of its five exits, so a fleet whose runs never resumed
// looked identical in the logs to a fleet with nothing to resume — and telling
// them apart took a fleet-wide measurement instead of a grep.
func logResumeSkip(reason, detail string, args ...any) {
	msg := "[daemon] not resuming Claude session: " + reason
	if detail != "" {
		msg += " (" + fmt.Sprintf(detail, args...) + ")"
	}
	fmt.Println(msg)
}

// persistAssignedTaskToLock records the daemon-assigned task on the WORKTREE
// lock so a crash mid-run leaves a resumable remnant that the next restart's
// detectRecovery can find. The agent's own `loom claim` sets the task on its
// CWD's lock, and planners run it from the workspace root (per the prompt), so
// it cannot be relied on to populate the per-worktree lock the daemon reads.
//
// MUST be called AFTER maybeResumeDaemonSession so the resume decision reads the
// carried-forward task id, not this run's. Skips when the lock already carries
// this task (a resume/checkpoint cycle) so TaskStartedAt — the resume-TTL clock
// — is not reset.
func persistAssignedTaskToLock(worktreePath, assignedTaskID string) {
	if assignedTaskID == "" {
		return
	}
	if info, err := cli.ReadLockFile(worktreePath); err == nil && info != nil && info.TaskID == assignedTaskID {
		return // already recorded (carried forward) — keep the TTL clock
	}
	if err := cli.UpdateLockTask(worktreePath, assignedTaskID, ""); err != nil {
		fmt.Fprintf(os.Stderr, "[daemon] failed to persist assigned task %s on lock: %v\n", assignedTaskID, err)
	}
}

// clearDaemonResumeOnSuccess clears the carried session id after a COMPLETED
// daemon run so the next restart starts the next task fresh (resume-first /
// checkpoint-fallback: a failed run instead KEEPS the session for carry-forward).
//
// A nil invoke error is not by itself proof the task finished — an agent whose
// turn ends mid-task returns just as cleanly as one that worked the task to a
// conclusion. `loom complete` is what actually separates them (see
// ClaimStillHeld), so a claim this worktree still holds means the run stopped
// short, and the session id is kept exactly as it would be for a failed run.
// Dropping it there is what forced the next cycle to cold-start with no memory
// of the turn it is continuing.
func clearDaemonResumeOnSuccess(worktreePath string) {
	if claimStillHeldForWorktree(worktreePath) {
		fmt.Println("[daemon] run ended with its task still claimed; keeping the Claude session for resume")
		return
	}
	if err := cli.ClearLockClaudeSessionID(worktreePath); err != nil {
		fmt.Fprintf(os.Stderr, "[daemon] failed to clear claude session ID after success: %v\n", err)
	}
}
