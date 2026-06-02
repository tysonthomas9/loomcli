package agent

import (
	"fmt"
	"os"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
)

// defaultResumeTTL bounds how old a carried-forward Claude session may be before
// a daemon restart treats it as stale and cold-starts instead of resuming.
const defaultResumeTTL = 30 * time.Minute

// ResumeTTL returns the resume staleness bound, overridable via LOOM_RESUME_TTL
// (a Go duration string, e.g. "45m"). Falls back to defaultResumeTTL. Exported
// so the daemon supervisor's resume-detection shares the exact same bound as the
// agent-side resume decision (maybeResumeDaemonSession).
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
// is a SAME-TASK daemon restart within the TTL. It is deliberately conservative
// (the resume DECISION + guards live here; the lock-level carry-forward is in
// cli.AcquireLock): it resumes only for an explicit assigned task that matches
// the carried lock's TaskID, so a restart for a different task — or a
// stale/absent session — cold-starts instead.
//
// The caller clears the lock session id on a SUCCESSFUL run (so the next restart
// cold-starts the next task); a failed run keeps it for carry-forward.
func maybeResumeDaemonSession(worktreePath, assignedTaskID string) {
	if assignedTaskID == "" {
		return // no specific task assigned → cold start (can't verify same-task)
	}
	info, err := cli.ReadLockFile(worktreePath)
	if err != nil || info == nil || info.ClaudeSessionID == "" {
		return
	}
	if info.TaskID != assignedTaskID {
		return // carried session belongs to a different task → cold start
	}
	if ttl := ResumeTTL(); ttl > 0 && !info.TaskStartedAt.IsZero() && time.Since(info.TaskStartedAt) > ttl {
		fmt.Printf("[daemon] prior Claude session for %s is stale (%s old); cold-starting\n",
			assignedTaskID, time.Since(info.TaskStartedAt).Round(time.Second))
		return
	}
	backends.SetResumeSessionID(info.ClaudeSessionID)
	fmt.Printf("[daemon] resuming Claude session for task %s (run %s)\n", assignedTaskID, info.RunID)
}

// clearDaemonResumeOnSuccess clears the carried session id after a successful
// daemon run so the next restart starts the next task fresh (resume-first /
// checkpoint-fallback: a failed run instead KEEPS the session for carry-forward).
func clearDaemonResumeOnSuccess(worktreePath string) {
	if err := cli.ClearLockClaudeSessionID(worktreePath); err != nil {
		fmt.Fprintf(os.Stderr, "[daemon] failed to clear claude session ID after success: %v\n", err)
	}
}
