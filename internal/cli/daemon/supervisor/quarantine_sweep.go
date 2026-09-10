package supervisor

// The task-quarantine sweep and write, split out of quarantine.go (LOC gate).
// The ledger, its persistence and the exit hook stay in quarantine.go.

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agentpolicy"
	"github.com/tysonthomas9/loomcli/internal/backend"
)

// ---------------------------------------------------------------------------
// Sweep: the quarantine write
// ---------------------------------------------------------------------------

// sweepQuarantineDue scans the ledger and quarantines every record meeting
// the sweep predicate: Count >= threshold && QuarantinedAt.IsZero() &&
// !inFlight. This predicate is the ONLY trigger — a failed write leaves
// Count >= threshold with a zero latch, so it re-qualifies naturally.
//
// Scanning (rather than acting only on this agent's task) is deliberate:
// worker-self-picked tasks have an empty AssignedTaskID and a cleared lock by
// this point in the exit sequence, and write-failure retries heal on ANY
// agent's next cycle, not just the same task's next kill. Runs after
// postMortemRecovery reset the task to open, so the write transitions
// open→blocked.
func (s *Supervisor) sweepQuarantineDue(ap *AgentProcess) {
	threshold := s.quarantineThreshold()
	if threshold <= 0 || s.IssueBackend == nil {
		return
	}
	for _, due := range s.qrec().takeDue(threshold, s.deadlineQuarantineThreshold()) {
		s.quarantineTask(ap, due)
	}
}

// dueTask is the snapshot of a record meeting the sweep predicate, taken
// under the ledger mutex so the network calls run without holding it.
type dueTask struct {
	taskID string
	// bucket / count / threshold describe the counter that TOPPED OUT, so the
	// log line and the kill-timeline comment name the one that actually fired
	// rather than always the no-progress one.
	bucket        agentpolicy.QuarantineBucket
	count         int
	threshold     int
	kills         []killEvent
	baselineKnown bool
	baseline      issueBaseline
}

// takeDue collects every record meeting the sweep predicate and marks it
// inFlight so a concurrently-exiting agent's sweep cannot double-write. The
// caller MUST resolve each returned task (latch / release / evict).
func (q *taskQuarantine) takeDue(threshold, deadlineThreshold int) []dueTask {
	defer q.persistAfter()
	q.mu.Lock()
	defer q.mu.Unlock()
	var due []dueTask
	for id, rec := range q.rec {
		if !rec.QuarantinedAt.IsZero() || rec.inFlight {
			continue
		}
		// Either counter topping out its OWN threshold is due. A task
		// alternating expiries and watchdog kills advances both and quarantines
		// on whichever gets there first — correct, since the union is still
		// "this task cannot be finished". No-progress is checked first so a
		// record at both thresholds reports the crash bucket, the more serious
		// of the two.
		// Declared, not assigned: every reachable arm below sets all three or
		// `continue`s, so QuarantineNone/0/0 are only the zero values.
		var (
			bucket         agentpolicy.QuarantineBucket
			count, applied int
		)
		switch {
		case rec.Count >= threshold:
			bucket, count, applied = agentpolicy.QuarantineNoProgress, rec.Count, threshold
		case deadlineThreshold > 0 && rec.DeadlineCount >= deadlineThreshold:
			bucket, count, applied = agentpolicy.QuarantineDeadline, rec.DeadlineCount, deadlineThreshold
		default:
			continue
		}
		rec.inFlight = true
		kills := make([]killEvent, len(rec.Kills))
		copy(kills, rec.Kills)
		due = append(due, dueTask{
			taskID:        id,
			bucket:        bucket,
			count:         count,
			threshold:     applied,
			kills:         kills,
			baselineKnown: rec.BaselineKnown,
			baseline:      rec.baseline(),
		})
	}
	return due
}

// quarantineVerdict is the read-back guard's decision for one due task.
type quarantineVerdict int

const (
	quarantineProceed         quarantineVerdict = iota
	quarantineLatchResolved                     // already terminal/blocked/deferred: latch without writing
	quarantineStayDue                           // actively in_progress: never block mid-run; stay due
	quarantineRetryFailed                       // GET failed: stay due, flag the failed attempt
	quarantineEvictProgressed                   // open but the field baseline moved: release from the spiral
)

// quarantineTask performs the read-back guard plus the load-bearing blocked
// write for one due task. All calls are synchronous within the exiting
// agent's supervise loop, bounded by quarantineWriteTimeout — no spawned
// goroutines (keeps daemon shutdown, test determinism, and state-file
// visibility simple). Never fatal; never blocks the supervise loop beyond
// the timeout.
func (s *Supervisor) quarantineTask(ap *AgentProcess, due dueTask) {
	ctx, cancel := context.WithTimeout(context.Background(), quarantineWriteTimeout)
	defer cancel()
	q := s.qrec()

	switch s.checkQuarantineTarget(ctx, due) {
	case quarantineProceed:
		s.writeQuarantine(ctx, ap, due)
	case quarantineLatchResolved:
		q.latch(due.taskID, false)
	case quarantineEvictProgressed:
		slog.Info("task progressed since its kill spiral was recorded, releasing instead of quarantining",
			"task", due.taskID)
		q.evict(due.taskID)
	case quarantineStayDue:
		q.release(due.taskID)
	case quarantineRetryFailed:
		q.markWriteFailed(due.taskID)
	}
}

// checkQuarantineTarget is the read-back guard + stale-retry revalidation:
// between the kills and this sweep (or between a failed write and its retry)
// the task may have been re-picked, completed, or human-handled.
func (s *Supervisor) checkQuarantineTarget(ctx context.Context, due dueTask) quarantineVerdict {
	issue, err := s.IssueBackend.Get(ctx, due.taskID)
	if err != nil || issue == nil {
		return quarantineRetryFailed
	}
	switch issue.Status {
	case "open":
		// Same widening as the record hook, so the two comparisons cannot
		// disagree about what counts as progress.
		if due.baselineKnown && issueBaselineOf(issue).progressedFrom(due.baseline) {
			// Progressed since the spiral was recorded (a stale retry after
			// a failed write): release it instead of blocking. Commit
			// progress cannot be stale here — every run's exit passes the
			// record hook before any sweep in that cycle, and an in-flight
			// run is caught by the in_progress skip below.
			return quarantineEvictProgressed
		}
		return quarantineProceed
	case "in_progress":
		// Actively being worked (stale retry after the task was re-picked):
		// never block a task mid-run, and don't latch — the deciding
		// evidence arrives at that run's exit, whose record hook evicts or
		// increments before the next sweep acts.
		return quarantineStayDue
	default:
		// closed/tombstone: done. review: completed work awaiting approval.
		// blocked: already quarantined or human-blocked. deferred: a human
		// or scheduler deferred it — defer to that decision. Latch without
		// writing: no label, no comment, excluded from daemon status.
		return quarantineLatchResolved
	}
}

// writeQuarantine is the one load-bearing write: a single Update the fleet
// client decomposes in a verified-safe order (labels → release claim lock as
// current assignee → PATCH status=blocked → assign ""). The kill-timeline
// comment is best-effort after the status write lands.
func (s *Supervisor) writeQuarantine(ctx context.Context, ap *AgentProcess, due dueTask) {
	q := s.qrec()
	blocked := "blocked"
	unassigned := ""
	err := s.IssueBackend.Update(ctx, due.taskID, backend.UpdateParams{
		Status:    &blocked,
		Assignee:  &unassigned,
		AddLabels: []string{quarantineLabel},
	})
	if err != nil {
		slog.Warn("task quarantine write failed, will retry on a later sweep",
			"task", due.taskID, "err", err)
		q.markWriteFailed(due.taskID)
		return
	}
	// Message text is load-bearing: TestScenarioTaskQuarantine greps the
	// daemon log for "quarantined after repeated no-progress kills".
	slog.Info("task quarantined after repeated no-progress kills",
		"task", due.taskID, "kills", due.count, "bucket", quarantineBucketName(due.bucket),
		"threshold", due.threshold, "status", "blocked", "label", quarantineLabel)
	s.postQuarantineComment(ctx, ap, due)
	q.latch(due.taskID, true)
}

// postQuarantineComment posts the kill timeline. Best-effort: the status
// write already landed, so a comment failure logs and does NOT unlatch.
// fleet-db drops the Author param on the wire; attribution lives in the text.
func (s *Supervisor) postQuarantineComment(ctx context.Context, ap *AgentProcess, due dueTask) {
	text := formatKillTimeline(due.taskID, due.bucket, due.threshold, due.count, due.kills)
	if _, err := s.IssueBackend.AddComment(ctx, backend.CommentAddParams{
		IssueID: due.taskID,
		Author:  ap.Entry.Worktree,
		Text:    text,
	}); err != nil {
		slog.Warn("quarantine kill-timeline comment failed (status write already landed)",
			"task", due.taskID, "err", err)
	}
}

// formatKillTimeline renders the quarantine comment: an ASCII-only markdown
// kill table plus release instructions. Daemon-generated operational text —
// no emoji or non-ASCII; session ids truncate to short prefixes; an empty
// StopReason renders via killEvent.killKind ("crash", or "expiry" for a
// run-turn deadline). The preamble is bucket-specific: a deadline spiral needs
// a different instruction than a stalled backend.
func formatKillTimeline(taskID string, bucket agentpolicy.QuarantineBucket, threshold, count int, kills []killEvent) string {
	var b strings.Builder
	if bucket == agentpolicy.QuarantineDeadline {
		fmt.Fprintf(&b, "**Task quarantined by loom daemon** -- %d consecutive run-turn deadline expiries.\n\n", count)
		fmt.Fprintf(&b, "Claimed %dx and every turn ran out of loom's per-turn deadline (the role's\n", count)
		b.WriteString("max_run_duration minus 120s) with no commit, design, notes, comment or label\n")
		b.WriteString("progress between attempts. These are clean stops, not crashes -- the task simply\n")
		b.WriteString("does not fit the time budget. Raise the role's max_run_duration or split the task.\n")
		b.WriteString("Set to **blocked** and unassigned to stop the boomerang.\n\n")
	} else {
		fmt.Fprintf(&b, "**Task quarantined by loom daemon** -- %d consecutive no-progress kills.\n\n", count)
		fmt.Fprintf(&b, "Claimed and killed %dx with no commit, design, notes, comment or label progress\n", count)
		b.WriteString("(backend stall -> watchdog/ownership kill -> reset -> re-pick -> identical freeze).\n")
		b.WriteString("Set to **blocked** and unassigned to stop the boomerang.\n\n")
	}
	b.WriteString("| # | time (UTC) | agent | kill | class | exit | fleet session | claude session | note |\n")
	b.WriteString("|---|-----------|-------|------|-------|------|---------------|----------------|------|\n")
	for i, ev := range kills {
		kind := ev.killKind()
		class := ev.ErrClass
		if class == "" {
			class = "-"
		}
		// The note column is why a reader can trust the count: kills the
		// ledger discounted are still listed, marked with the reason they
		// were not charged to the task.
		note := ev.NotCounted
		if note == "" {
			note = "-"
		} else {
			note = "not counted: " + note
		}
		fmt.Fprintf(&b, "| %d | %s | %s | %s | %s | %d | %s | %s | %s |\n",
			i+1, ev.At.UTC().Format(time.RFC3339), ev.Agent, kind, class, ev.ExitCode,
			shortSessionID(ev.FleetSessionID), shortSessionID(ev.ClaudeSessionID), note)
	}
	fmt.Fprintf(&b, "\nTo release: investigate the stall, then `loom data update %s --status open`\n", taskID)
	fmt.Fprintf(&b, "(the %s label stays as an audit marker; clear it via the fleet-db API\n", quarantineLabel)
	fmt.Fprintf(&b, "`DELETE /issues/%s/labels/%s` if desired). Manual `loom claim %s` also\n", taskID, quarantineLabel, taskID)
	kindLabel := "no-progress kills"
	if bucket == agentpolicy.QuarantineDeadline {
		kindLabel = "deadline expiries"
	}
	fmt.Fprintf(&b, "works (blocked is claimable) -- it will re-quarantine after %d fresh %s.\n", threshold, kindLabel)
	return b.String()
}

// shortSessionID truncates a session id to an 8-char prefix for table
// readability; empty ids render as "-".
func shortSessionID(id string) string {
	if id == "" {
		return "-"
	}
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
