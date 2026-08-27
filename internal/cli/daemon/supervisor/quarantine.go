package supervisor

import (
	"context"
	"fmt"
	"hash/fnv"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/agentpolicy"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/automode"
)

// Task quarantine: a supervisor-scoped, task-ID-keyed ledger of repeated
// no-progress kills. When a backend silently stalls, the watchdog (or the
// ownership/lease path) kills the session, recovery resets the task to open,
// and the picker re-selects the same task — an infinite boomerang that
// agent-level machinery (restart budgets, blocks) cannot break, because the
// task returns to open every cycle and a sibling re-picks it. The ledger
// counts consecutive quarantine-eligible kills per task ACROSS agents; once a
// task accumulates quarantineThreshold() of them with no progress in between,
// the sweep sets it to blocked (status + label + kill-timeline comment).
//
// Eligibility is declared in the policy seat (agentpolicy.QuarantineEligible);
// this file owns the state — mirroring how BlockBudget is declared in the
// policy table but counted agent-side by the supervisor.

const (
	defaultQuarantineThreshold = 3
	quarantineLabel            = "loom:quarantined"
	quarantineWriteTimeout     = 10 * time.Second
	maxTrackedQuarantineTasks  = 512 // defensive cap on ledger size; oldest evicted
	maxKillEventsRetained      = 10  // kill-timeline cap per task
)

// killEvent is one observed kill of a task-holding agent, captured at exit
// time (after classifyAgentExit, before finalize/recovery clear the session
// and lock state it reads).
type killEvent struct {
	At              time.Time
	Agent           string // ap.Entry.Worktree
	StopReason      string // e.g. "watchdog"; empty for a bare crash / ownership kill
	ErrClass        string // classified outcome (Unknown | Timeout | Transient | ContextOverflow)
	ExitCode        int
	FleetSessionID  string // ap.AgentSessionID — captured before finalize clears it
	ClaudeSessionID string // lock ClaudeSessionID (best-effort; empty if absent)
	RunID           string // lock RunID (best-effort)
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

// taskFailureRecord accumulates consecutive no-progress kills for one task.
type taskFailureRecord struct {
	Count int         // consecutive eligible no-progress kills since last reset/quarantine
	Kills []killEvent // capped timeline (last maxKillEventsRetained)

	// QuarantinedAt latches once the record is resolved: the daemon wrote
	// blocked, OR the read-back guard found the task already terminal/
	// blocked/deferred. Count is zeroed at latch time; the first fresh
	// eligible kill clears the latch (re-arm), so N fresh kills are needed
	// to re-quarantine after a human release.
	QuarantinedAt time.Time
	DaemonWrote   bool // true only when WE performed the blocked-write (only these surface in daemon status)
	WriteFailed   bool // informational: last write attempt failed (retry is driven by the sweep predicate, not this flag)
	inFlight      bool // an agent's supervise loop is mid-write right now (guards concurrent sweeps)

	LastUpdated time.Time // touched on create/increment/latch/write-attempt — the eviction key

	// Field-delta progress baseline (covers agents whose artifact is not a
	// worktree commit: a plan role writes design/notes, and the critic /
	// integrator / tester / decomposer roles write a COMMENT, a LABEL or a
	// PR). Populated by the first successful issue GET; comparisons apply
	// ONLY when known — "unknown" is never progress, and zero-value
	// baselines are never compared.
	//
	// Status and UpdatedAt are deliberately NOT tracked: the daemon itself
	// drives open -> in_progress -> open on every pick and recovery, and any
	// write bumps UpdatedAt, so either would report progress on every cycle
	// and disable the breaker outright.
	BaselineKnown      bool
	BaselineDesignHash uint64
	BaselineNotesHash  uint64
	// BaselineMaxCommentID is monotone (ids are assigned increasing), so it
	// is compared with > and never !=: a DELETED comment lowers the max and
	// must not read as progress.
	BaselineMaxCommentID int64  `json:"baseline_max_comment_id,omitempty"`
	BaselineLabelsHash   uint64 `json:"baseline_labels_hash,omitempty"` // FNV-1a over the sorted label set

	LastKillReason  string
	QuarantineKills int // Count captured at latch time (display-only; Count itself zeroes as the re-arm baseline)
}

// taskQuarantine is the daemon-wide ledger. One shared map per supervisor:
// kills of the same task from different agents accumulate on one record —
// the exact incident shape (a task boomeranging across siblings).
type taskQuarantine struct {
	mu  sync.Mutex
	rec map[string]*taskFailureRecord
}

// qrec lazily initializes the quarantine ledger. The Supervisor is built as a
// cross-package composite literal (daemon.go), so lazy init avoids touching
// every construction site.
func (s *Supervisor) qrec() *taskQuarantine {
	s.quarantineOnce.Do(func() {
		s.quarantine = &taskQuarantine{rec: make(map[string]*taskFailureRecord)}
	})
	return s.quarantine
}

// quarantineThreshold is the consecutive no-progress-kill count at which a
// task is quarantined. LOOM_TASK_QUARANTINE_THRESHOLD wins when set (mirrors
// GetOutputTimeout — fleet-db's wire schema does not persist such daemon
// config fields); <= 0 disables both quarantine hooks (operator kill-switch).
func (s *Supervisor) quarantineThreshold() int {
	if v := os.Getenv("LOOM_TASK_QUARANTINE_THRESHOLD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return defaultQuarantineThreshold
}

// recordTaskExitForQuarantine is the ledger hook. It runs in spawnAndWait
// immediately after classifyAgentExit: ap.LastError is set, the lock file is
// still present (recovery has not cleared it), and ap.AgentSessionID has not
// been cleared by finalize — the only point in the exit sequence where all
// three are observable together.
func (s *Supervisor) recordTaskExitForQuarantine(ap *AgentProcess, exitCode int) {
	if s.quarantineThreshold() <= 0 {
		return
	}
	lockInfo, _, _ := cli.CheckLock(ap.WorktreePath)
	taskID := s.taskIDForLifecycle(ap, lockInfo)
	if taskID == "" {
		return // no task attached (idle watchdog kills classify as NoWork anyway)
	}
	snap := snapshotTaskExit(ap, lockInfo, exitCode)
	q := s.qrec()
	// An incomplete run (exit 0, claim never released) is deliberately NOT
	// clean: classifyAgentExit gives it a LastError, so snap.clean is false and
	// the record survives. Evicting on that shape is what made this spiral
	// invisible — a task could alternate real kills with unfinished turns and
	// the ledger reset to zero on every one of them, so the threshold was never
	// reached. It still does not INCREMENT: IncompleteRun is a domain outcome
	// and QuarantineEligible rejects those, which is right — a turn that ran out
	// is a coordination signal, not a task-fault kill, and counting it would
	// quarantine tasks whose agents are progressing without committing.
	if snap.clean || commitProgressed(ap.WorktreePath, snap.beforeRef) {
		q.evict(taskID)
		return
	}
	if !agentpolicy.QuarantineEligible(snap.outcome) {
		return
	}
	base, baselineKnown := s.fetchIssueBaseline(taskID)
	count, progressed := q.recordEligibleKill(taskID, snap.event, base, baselineKnown)
	if progressed {
		slog.Info("task progressed between kills (design/notes/comment/label delta), dropping quarantine record",
			"task", taskID, "agent", ap.Entry.Worktree)
		return
	}
	slog.Info("recorded no-progress kill for task",
		"task", taskID, "agent", ap.Entry.Worktree, "kill", snap.event.reason(),
		"count", count, "threshold", s.quarantineThreshold())
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
	}
	if lockInfo != nil {
		snap.event.ClaudeSessionID = lockInfo.ClaudeSessionID
		snap.event.RunID = lockInfo.RunID
	}
	return snap
}

// commitProgressed reports whether the worktree HEAD moved past the ref
// captured at session creation. An unknown baseline (BeforeRef empty — it is
// set only after session creation succeeds) or an unreadable current HEAD is
// NOT progress: comparing HEAD against "" would fake progress on every
// session-creation-failure exit and suppress quarantine for that failure mode.
func commitProgressed(worktreePath, beforeRef string) bool {
	if beforeRef == "" {
		return false
	}
	head := automode.CaptureHEADRef(worktreePath)
	return head != "" && head != beforeRef
}

// issueBaseline is the field-delta progress fingerprint of one issue, read
// off a single Get response — every component comes from data the GET already
// returned, so widening it costs no extra network call.
type issueBaseline struct {
	designHash   uint64
	notesHash    uint64
	maxCommentID int64
	labelsHash   uint64
}

// progressedFrom reports whether this (freshly read) baseline shows movement
// past the recorded one. Hashes compare by inequality; the comment id compares
// by > because it is monotone and a deletion must not read as progress.
func (b issueBaseline) progressedFrom(prev issueBaseline) bool {
	return b.designHash != prev.designHash ||
		b.notesHash != prev.notesHash ||
		b.maxCommentID > prev.maxCommentID ||
		b.labelsHash != prev.labelsHash
}

// issueBaselineOf fingerprints a Get response. Comments and Labels ride on the
// same IssueDetailData the design/notes hashes already came from.
func issueBaselineOf(issue *backend.IssueDetailData) issueBaseline {
	return issueBaseline{
		designHash:   hashIssueField(issue.Design),
		notesHash:    hashIssueField(issue.Notes),
		maxCommentID: maxCommentID(issue.Comments),
		labelsHash:   hashLabelSet(issue.Labels),
	}
}

// fetchIssueBaseline GETs the issue once per eligible kill and fingerprints
// it. ok=false (no backend, GET failed) means "unknown": the increment
// proceeds regardless, and the caller never compares against a zero baseline.
func (s *Supervisor) fetchIssueBaseline(taskID string) (base issueBaseline, ok bool) {
	if s.IssueBackend == nil {
		return issueBaseline{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), quarantineWriteTimeout)
	defer cancel()
	issue, err := s.IssueBackend.Get(ctx, taskID)
	if err != nil || issue == nil {
		return issueBaseline{}, false
	}
	return issueBaselineOf(issue), true
}

func hashIssueField(v string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(v))
	return h.Sum64()
}

// maxCommentID is the highest comment id on the issue (0 when there are
// none). fleet-db assigns comment ids monotonically, so this is edit-proof:
// editing a comment leaves the max alone, and only a NEW comment raises it.
func maxCommentID(comments []backend.CommentData) int64 {
	var highest int64
	for _, c := range comments {
		if c.ID > highest {
			highest = c.ID
		}
	}
	return highest
}

// hashLabelSet is FNV-1a over the sorted, NUL-delimited label set — order
// independent (label order is not meaningful) and unambiguous across
// concatenations.
func hashLabelSet(labels []string) uint64 {
	sorted := make([]string, len(labels))
	copy(sorted, labels)
	sort.Strings(sorted)
	h := fnv.New64a()
	for _, l := range sorted {
		_, _ = h.Write([]byte(l))
		_, _ = h.Write([]byte{0})
	}
	return h.Sum64()
}

// evict drops a task's failure record (clean exit or progress observed).
func (q *taskQuarantine) evict(taskID string) {
	q.mu.Lock()
	delete(q.rec, taskID)
	q.mu.Unlock()
}

// recordEligibleKill folds one quarantine-eligible kill into the ledger and
// returns the record's new count. Field-delta progress against a known
// baseline — a changed Design/Notes hash, a NEW comment, or a changed label
// set — evicts the record instead of incrementing: the task IS moving, just
// not via commits. The comment and label arms are what make the review roles
// visible; a critic that posted its verdict and was then reaped used to
// register as a no-progress kill on the task it had just advanced.
func (q *taskQuarantine) recordEligibleKill(taskID string, ev killEvent, base issueBaseline, baselineKnown bool) (count int, progressed bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	rec := q.rec[taskID]
	if rec != nil && rec.BaselineKnown && baselineKnown && base.progressedFrom(rec.baseline()) {
		delete(q.rec, taskID)
		return 0, true
	}
	if rec == nil {
		q.evictOldestLocked()
		rec = &taskFailureRecord{}
		q.rec[taskID] = rec
	}
	if baselineKnown && !rec.BaselineKnown {
		// First successful GET establishes the baseline; never inferred as
		// progress (zero-value baselines are never compared).
		rec.setBaseline(base)
	}
	if !rec.QuarantinedAt.IsZero() {
		// Latched record seeing a fresh kill: the task was released (human
		// or undefer) and stalled again. Re-arm — N fresh kills are required
		// before it re-quarantines.
		rec.QuarantinedAt = time.Time{}
		rec.DaemonWrote = false
		rec.WriteFailed = false
		rec.Count = 0
	}
	rec.Count++
	rec.Kills = append(rec.Kills, ev)
	if len(rec.Kills) > maxKillEventsRetained {
		rec.Kills = rec.Kills[len(rec.Kills)-maxKillEventsRetained:]
	}
	rec.LastKillReason = ev.reason()
	rec.LastUpdated = time.Now()
	return rec.Count, false
}

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
	for _, due := range s.qrec().takeDue(threshold) {
		s.quarantineTask(ap, due)
	}
}

// dueTask is the snapshot of a record meeting the sweep predicate, taken
// under the ledger mutex so the network calls run without holding it.
type dueTask struct {
	taskID        string
	count         int
	kills         []killEvent
	baselineKnown bool
	baseline      issueBaseline
}

// takeDue collects every record meeting the sweep predicate and marks it
// inFlight so a concurrently-exiting agent's sweep cannot double-write. The
// caller MUST resolve each returned task (latch / release / evict).
func (q *taskQuarantine) takeDue(threshold int) []dueTask {
	q.mu.Lock()
	defer q.mu.Unlock()
	var due []dueTask
	for id, rec := range q.rec {
		if rec.Count < threshold || !rec.QuarantinedAt.IsZero() || rec.inFlight {
			continue
		}
		rec.inFlight = true
		kills := make([]killEvent, len(rec.Kills))
		copy(kills, rec.Kills)
		due = append(due, dueTask{
			taskID:        id,
			count:         rec.Count,
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
		"task", due.taskID, "kills", due.count, "threshold", s.quarantineThreshold(),
		"status", "blocked", "label", quarantineLabel)
	s.postQuarantineComment(ctx, ap, due)
	q.latch(due.taskID, true)
}

// postQuarantineComment posts the kill timeline. Best-effort: the status
// write already landed, so a comment failure logs and does NOT unlatch.
// fleet-db drops the Author param on the wire; attribution lives in the text.
func (s *Supervisor) postQuarantineComment(ctx context.Context, ap *AgentProcess, due dueTask) {
	text := formatKillTimeline(due.taskID, s.quarantineThreshold(), due.count, due.kills)
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
// StopReason renders as "crash".
func formatKillTimeline(taskID string, threshold, count int, kills []killEvent) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**Task quarantined by loom daemon** -- %d consecutive no-progress kills.\n\n", count)
	fmt.Fprintf(&b, "Claimed and killed %dx with no commit, design, notes, comment or label progress\n", count)
	b.WriteString("(backend stall -> watchdog/ownership kill -> reset -> re-pick -> identical freeze).\n")
	b.WriteString("Set to **blocked** and unassigned to stop the boomerang.\n\n")
	b.WriteString("| # | time (UTC) | agent | kill | class | exit | fleet session | claude session |\n")
	b.WriteString("|---|-----------|-------|------|-------|------|---------------|----------------|\n")
	for i, ev := range kills {
		kind := ev.StopReason
		if kind == "" {
			kind = "crash"
		}
		class := ev.ErrClass
		if class == "" {
			class = "-"
		}
		fmt.Fprintf(&b, "| %d | %s | %s | %s | %s | %d | %s | %s |\n",
			i+1, ev.At.UTC().Format(time.RFC3339), ev.Agent, kind, class, ev.ExitCode,
			shortSessionID(ev.FleetSessionID), shortSessionID(ev.ClaudeSessionID))
	}
	fmt.Fprintf(&b, "\nTo release: investigate the stall, then `loom data update %s --status open`\n", taskID)
	fmt.Fprintf(&b, "(the %s label stays as an audit marker; clear it via the fleet-db API\n", quarantineLabel)
	fmt.Fprintf(&b, "`DELETE /issues/%s/labels/%s` if desired). Manual `loom claim %s` also\n", taskID, quarantineLabel, taskID)
	fmt.Fprintf(&b, "works (blocked is claimable) -- it will re-quarantine after %d fresh no-progress kills.\n", threshold)
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

// release clears inFlight leaving the record due (skipped this round): it
// re-qualifies on any agent's next sweep.
func (q *taskQuarantine) release(taskID string) {
	q.mu.Lock()
	if rec := q.rec[taskID]; rec != nil {
		rec.inFlight = false
		rec.LastUpdated = time.Now()
	}
	q.mu.Unlock()
}

// markWriteFailed clears inFlight and flags the failed attempt. Informational
// only — the retry is driven by the sweep predicate (Count still >= threshold,
// latch still zero), not by this flag.
func (q *taskQuarantine) markWriteFailed(taskID string) {
	q.mu.Lock()
	if rec := q.rec[taskID]; rec != nil {
		rec.inFlight = false
		rec.WriteFailed = true
		rec.LastUpdated = time.Now()
	}
	q.mu.Unlock()
}

// latch marks a record resolved: Count zeroed (the re-arm baseline) and
// QuarantinedAt stamped — the latch can never satisfy the sweep predicate by
// itself. daemonWrote records whether WE performed the blocked-write (only
// those surface in daemon status; a guard-latched human-blocked/deferred/
// closed task is tracked internally but never presented as quarantined).
func (q *taskQuarantine) latch(taskID string, daemonWrote bool) {
	q.mu.Lock()
	if rec := q.rec[taskID]; rec != nil {
		rec.inFlight = false
		rec.QuarantineKills = rec.Count
		rec.Count = 0
		rec.QuarantinedAt = time.Now()
		rec.DaemonWrote = daemonWrote
		rec.WriteFailed = false
		rec.LastUpdated = time.Now()
		// Clear the baseline, do not refresh it. writeQuarantine posts the
		// kill-timeline comment (and adds the quarantine label) right before
		// this latch, so a FROZEN baseline would let the daemon read its own
		// write as task progress on the next kill. Clearing is strictly safer
		// than refreshing: unknown is never progress, so the next kill simply
		// re-establishes it.
		rec.clearBaseline()
	}
	q.mu.Unlock()
}

// baseline reads the record's four baseline components back out. Caller holds
// q.mu.
func (rec *taskFailureRecord) baseline() issueBaseline {
	return issueBaseline{
		designHash:   rec.BaselineDesignHash,
		notesHash:    rec.BaselineNotesHash,
		maxCommentID: rec.BaselineMaxCommentID,
		labelsHash:   rec.BaselineLabelsHash,
	}
}

// setBaseline anchors all four components together — they are only ever
// known or unknown as a set. Caller holds q.mu.
func (rec *taskFailureRecord) setBaseline(b issueBaseline) {
	rec.BaselineKnown = true
	rec.BaselineDesignHash = b.designHash
	rec.BaselineNotesHash = b.notesHash
	rec.BaselineMaxCommentID = b.maxCommentID
	rec.BaselineLabelsHash = b.labelsHash
}

// clearBaseline returns the record to "unknown", so the next successful GET
// re-establishes it. Caller holds q.mu.
func (rec *taskFailureRecord) clearBaseline() {
	rec.BaselineKnown = false
	rec.BaselineDesignHash = 0
	rec.BaselineNotesHash = 0
	rec.BaselineMaxCommentID = 0
	rec.BaselineLabelsHash = 0
}

// evictOldestLocked makes room when the ledger is at capacity by dropping the
// non-inFlight record with the oldest LastUpdated. Hot spirals (touched on
// every kill) are never evicted in favor of stale residue. Caller holds q.mu.
func (q *taskQuarantine) evictOldestLocked() {
	if len(q.rec) < maxTrackedQuarantineTasks {
		return
	}
	var oldestID string
	var oldestAt time.Time
	for id, r := range q.rec {
		if r.inFlight {
			continue
		}
		if oldestID == "" || r.LastUpdated.Before(oldestAt) {
			oldestID, oldestAt = id, r.LastUpdated
		}
	}
	if oldestID != "" {
		delete(q.rec, oldestID)
	}
}

// ---------------------------------------------------------------------------
// Daemon-status surfacing
// ---------------------------------------------------------------------------

// QuarantinedTaskInfo is the JSON-serializable snapshot of one quarantined
// (or quarantine-pending) task, surfaced in daemon-agents.json and
// `loom daemon status` — mirroring how agent blocks surface daemon-status-only.
type QuarantinedTaskInfo struct {
	TaskID string `json:"task_id"`
	// Count is the number of no-progress kills behind the quarantine: the
	// count captured when the write landed, or the live count for a
	// pending (write-failed, retrying) record.
	Count          int       `json:"count"`
	QuarantinedAt  time.Time `json:"quarantined_at,omitzero"`
	LastKillReason string    `json:"last_kill_reason,omitempty"`
	WriteFailed    bool      `json:"write_failed,omitempty"`
}

// QuarantinedTasks returns the daemon-status snapshot: tasks the daemon
// actually quarantined (DaemonWrote) plus due tasks whose blocked-write is
// failing and retrying (WriteFailed). Guard-latched records — tasks the
// read-back found already human-blocked/deferred/closed — are tracked
// internally but never surfaced as quarantined; the loom:quarantined label
// is the on-issue discriminator.
func (s *Supervisor) QuarantinedTasks() []QuarantinedTaskInfo {
	q := s.qrec()
	q.mu.Lock()
	defer q.mu.Unlock()
	out := []QuarantinedTaskInfo{}
	for id, rec := range q.rec {
		switch {
		case rec.DaemonWrote:
			out = append(out, QuarantinedTaskInfo{
				TaskID:         id,
				Count:          rec.QuarantineKills,
				QuarantinedAt:  rec.QuarantinedAt,
				LastKillReason: rec.LastKillReason,
			})
		case rec.WriteFailed && rec.QuarantinedAt.IsZero():
			out = append(out, QuarantinedTaskInfo{
				TaskID:         id,
				Count:          rec.Count,
				LastKillReason: rec.LastKillReason,
				WriteFailed:    true,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TaskID < out[j].TaskID })
	return out
}
