package supervisor

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// Abandoned-run recorder: durable evidence for a run that was killed before it
// could report anything.
//
// A daemon-supervised run that dies mid-flight (daemon force-exit, SIGKILL,
// machine death) leaves NO trace on the task it held — no comment, no label, no
// error class. Every consumer of "how many attempts has this task had" counts
// only attempts that ended by reporting something, so a killed attempt is
// invisible and an attempt ceiling can never be reached: the task is re-claimed
// and re-run from scratch, forever.
//
// The evidence source is the control-plane agent_session row, which is written
// BEFORE the run starts (createControlPlaneAgentSession) and finished ONLY by
// the exit path (completeControlPlaneAgentSession). So a task-kind row with no
// FinishedAt and a non-terminal status is exactly a run that ended without
// reporting an outcome. This recorder runs in a DIFFERENT process from the one
// that was killed, so the kill cannot defeat it.
//
// Liveness is never derived from timestamps (see ownership.go — the row's
// LastHeartbeat is written exactly once, so its age is not a liveness signal).
// Authority comes from the arbiters that already exist:
//
//   - entry point 1 (recordAbandonedRunsForAgent): while this supervisor holds
//     the agent ownership lease for agent A, every unfinished row for A belongs
//     to a run that is over;
//   - entry point 2 (recordAbandonedRunsForTask): once claimTask succeeded we
//     hold fleet-db's per-issue claim lock for task T, so any unfinished row
//     for T — from any agent, on any node — is likewise dead.
//
// It RECORDS; it does not enforce. No ceiling lives in this code: policies that
// already read the task (the integrator prompt, a human, `loom data show`) can
// now see killed attempts alongside reported failures.
const (
	// abandonedRunErrorClass latches the session row so it can never be
	// selected again.
	abandonedRunErrorClass = "abandoned_run"
	// attemptLabelPrefix + <agentID> + "=" + N is the mechanical attempt
	// counter, shaped like domain.DefaultCycleLabelPrefix's "review-cycle=N".
	// fleet-db label values forbid only "," and ";", so ":" and "=" are legal.
	attemptLabelPrefix = "loom:attempt:"
	// abandonedRunMarker + <sessionID> is embedded in the evidence comment and
	// is the dedupe key that collapses the at-least-once pipeline to
	// exactly-once in practice.
	abandonedRunMarker = "loom-abandoned-run:"
	// timeoutRunMarker + <run id> is the dedupe key for a run that hit a
	// wall-clock ceiling. Deliberately distinct from abandonedRunMarker: an
	// abandoned run vanished, a timed-out run was killed on purpose, and the
	// two want different bodies and different board-visible labels.
	timeoutRunMarker = "loom-timeout-run:"
	// timeoutPartialLabel marks a task whose last run was cut off by a ceiling
	// with work possibly half-done. Free-form on fleet-db; no schema change.
	timeoutPartialLabel = "timeout-partial"
	// maxAbandonedPerPass bounds one reconcile pass; the remainder is picked up
	// on the next one.
	maxAbandonedPerPass = 20
	abandonedOpTimeout  = 10 * time.Second
)

// attemptLabel renders the counter label for agentID at count n.
func attemptLabel(agentID string, n int) string {
	return fmt.Sprintf("%s%s=%d", attemptLabelPrefix, agentID, n)
}

// parseAttemptCounter returns the count encoded in label for agentID, or 0 when
// the label is not a counter for that agent. Deliberately strict, like
// domain.AgentHookCycle.ParseCounter: a stray "loom:attempt:x=1.5" is ignored
// rather than rounded into an attempt count.
func parseAttemptCounter(agentID, label string) int {
	if agentID == "" {
		return 0
	}
	prefix := attemptLabelPrefix + agentID + "="
	if !strings.HasPrefix(label, prefix) {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(label, prefix)))
	if err != nil || n < 1 {
		return 0
	}
	return n
}

// recordedAttempts is the highest counter present for agentID. Max rather than
// sum keeps a leftover counter from a crashed cleanup harmless.
func recordedAttempts(agentID string, labels []string) int {
	highest := 0
	for _, l := range labels {
		if n := parseAttemptCounter(agentID, l); n > highest {
			highest = n
		}
	}
	return highest
}

// abandonedRecorderReady reports whether the recorder has everything it needs:
// a control plane to read the session rows from and an issue backend to write
// the evidence to. A deployment without a control plane gets a debug log and no
// recording — there is no second evidence store to invent.
func (s *Supervisor) abandonedRecorderReady() bool {
	if s.ControlStore == nil || s.WorkspaceID == "" {
		slog.Debug("abandoned-run recorder disabled (no control plane)", "workspace", s.WorkspaceID)
		return false
	}
	return s.issueEvidenceReady()
}

// issueEvidenceReady reports whether a ticket write is possible at all. It is
// the weaker half of abandonedRecorderReady: the timeout recorder runs on the
// exit path and reads no session rows, so a deployment with an issue backend
// and no control plane still records its ceiling hits.
func (s *Supervisor) issueEvidenceReady() bool {
	if s.IssueBackend == nil {
		slog.Debug("run-evidence recorder disabled (no issue backend)", "workspace", s.WorkspaceID)
		return false
	}
	return true
}

// recordAbandonedRunsForAgent is entry point 1: reconcile the rows this agent
// left unfinished in a previous daemon process. Runs once per AgentProcess per
// daemon lifetime (AbandonedRunsChecked) — within one process every run is
// finished by the exit path, so re-scanning each cycle would cost two GETs a
// cycle and find nothing.
//
// Must be called only after ownership was acquired: the lease is the proof of
// exclusivity. An acquire that fell through to "continuing without ownership
// guard" leaves OwnershipLeaseToken empty and is skipped.
func (s *Supervisor) recordAbandonedRunsForAgent(ap *AgentProcess) {
	if ap == nil || !s.abandonedRecorderReady() {
		return
	}
	ap.Mu.Lock()
	checked := ap.AbandonedRunsChecked
	owned := ap.OwnershipLeaseToken != ""
	if !checked {
		ap.AbandonedRunsChecked = true
	}
	ap.Mu.Unlock()
	if checked {
		return
	}
	if !owned {
		slog.Debug("skipping abandoned-run reconcile (no ownership lease)", "worktree", ap.Entry.Worktree)
		return
	}
	s.recordAbandonedRuns(store.AgentSessionFilter{
		AgentID: ap.Entry.Worktree,
		Kind:    domain.AgentSessionKindTask,
	})
}

// recordAbandonedRunsForTask is entry point 2: reconcile the rows left
// unfinished for the task we just claimed, whatever agent or node held them.
// The issue claim lock is the proof of exclusivity, so no ownership token is
// required here. Call it BEFORE this run's own session row is created, so that
// row cannot be mistaken for an abandoned one.
func (s *Supervisor) recordAbandonedRunsForTask(ap *AgentProcess, taskID string) {
	if ap == nil || taskID == "" || !s.abandonedRecorderReady() {
		return
	}
	s.recordAbandonedRuns(store.AgentSessionFilter{
		TaskID: taskID,
		Kind:   domain.AgentSessionKindTask,
	})
}

// recordAbandonedRuns lists the candidates for a filter and records each one.
// Never fatal and never blocks the supervise loop beyond the per-operation
// timeout: a total failure of the recorder leaves exactly today's behavior.
func (s *Supervisor) recordAbandonedRuns(filter store.AgentSessionFilter) {
	listCtx, listCancel := s.operationContext(abandonedOpTimeout)
	sessions := s.unfinishedTaskSessions(listCtx, filter)
	listCancel()

	for _, sess := range sessions {
		ctx, cancel := s.operationContext(abandonedOpTimeout)
		err := s.recordAbandonedRun(ctx, sess)
		cancel()
		if err != nil {
			// Not latched: the row stays selectable and the whole pipeline
			// retries on the next cycle or the next claim. At-least-once is
			// deliberate — the failure this exists to kill is "attempt
			// silently vanishes".
			slog.Warn("recording abandoned run failed, will retry",
				"session_id", sess.SessionID, "agent", sess.AgentID, "task", sess.TaskID, "err", err)
		}
	}
}

// unfinishedTaskSessions lists the abandoned candidates for a filter. The
// filter has no "unfinished" predicate, so it issues one List per non-terminal
// status and merges; the rest of the predicate is applied client-side. Rows
// with an empty TaskID are kept (they are latched without issue writes — see
// recordAbandonedRun); rows whose session is live in THIS process are dropped.
// Oldest first, capped at maxAbandonedPerPass.
func (s *Supervisor) unfinishedTaskSessions(ctx context.Context, filter store.AgentSessionFilter) []*domain.AgentSession {
	live := s.liveAgentSessionIDs()
	seen := make(map[string]struct{})
	var out []*domain.AgentSession
	for _, status := range []domain.AgentSessionStatus{domain.AgentSessionStarting, domain.AgentSessionRunning} {
		scoped := filter
		scoped.Status = status
		rows, err := s.ControlStore.AgentSessions().List(ctx, s.WorkspaceID, scoped)
		if err != nil {
			slog.Warn("listing unfinished agent sessions failed",
				"workspace", s.WorkspaceID, "status", status, "err", err)
			continue
		}
		for _, sess := range rows {
			if sess == nil || sess.SessionID == "" {
				continue
			}
			if sess.Kind != domain.AgentSessionKindTask || sess.FinishedAt != nil {
				continue
			}
			if _, isLive := live[sess.SessionID]; isLive {
				continue
			}
			if _, dup := seen[sess.SessionID]; dup {
				continue
			}
			seen[sess.SessionID] = struct{}{}
			out = append(out, sess)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return sessionStartOrder(out[i]).Before(sessionStartOrder(out[j]))
	})
	if len(out) > maxAbandonedPerPass {
		out = out[:maxAbandonedPerPass]
	}
	return out
}

// sessionStartOrder is the ordering key: StartedAt when the backend populates
// it, CreatedAt otherwise. Both are server-written and are used only to order
// rows already proven dead — never to decide liveness.
func sessionStartOrder(sess *domain.AgentSession) time.Time {
	if !sess.StartedAt.IsZero() {
		return sess.StartedAt
	}
	return sess.CreatedAt
}

// liveAgentSessionIDs snapshots the control-plane session ids currently held by
// this process's agents, so entry point 2 cannot mistake a sibling's running
// session for an abandoned one.
func (s *Supervisor) liveAgentSessionIDs() map[string]struct{} {
	s.AgentsMu.RLock()
	snapshot := make([]*AgentProcess, len(s.Agents))
	copy(snapshot, s.Agents)
	s.AgentsMu.RUnlock()

	live := make(map[string]struct{}, len(snapshot))
	for _, ap := range snapshot {
		if ap == nil {
			continue
		}
		ap.Mu.Lock()
		id := ap.AgentSessionID
		ap.Mu.Unlock()
		if id != "" {
			live[id] = struct{}{}
		}
	}
	return live
}

// recordAbandonedRun runs the four-step pipeline for one session row:
//
//  1. ListComments and look for the marker — if the evidence is already there,
//     skip straight to the latch (this is what makes a crash between 2 and 4
//     harmless);
//  2. AddComment — the human/agent-readable record;
//  3. AddLabel loom:attempt:<agent>=N, then best-effort remove superseded
//     lower counters;
//  4. latch the session row.
//
// The latch is LAST on purpose: a failure at step 2 or 3 leaves the row
// selectable and the pass retries. Returns an error only when the row must NOT
// be latched.
func (s *Supervisor) recordAbandonedRun(ctx context.Context, sess *domain.AgentSession) error {
	if sess.TaskID == "" {
		// Killed before it claimed anything: there is nothing to write to, but
		// the row must still stop being "running" forever.
		return s.latchAbandonedSession(ctx, sess.SessionID)
	}
	recorded, attempt, err := s.writeRunEvidence(ctx, evidenceWrite{
		taskID:  sess.TaskID,
		agentID: sess.AgentID,
		marker:  abandonedRunMarker + sess.SessionID,
		render: func(attempt int) string {
			return formatAbandonedRunComment(sess, attempt)
		},
	})
	if err != nil {
		return err
	}
	if recorded {
		slog.Info("recorded a run that ended without reporting an outcome",
			"task", sess.TaskID, "agent", sess.AgentID, "session_id", sess.SessionID,
			"attempt", attempt, "label", attemptLabel(sess.AgentID, attempt))
	}
	return s.latchAbandonedSession(ctx, sess.SessionID)
}

// evidenceWrite is one ticket-visible record of a run that produced no other
// trace. marker is the dedupe key (a marker constant plus a run-unique id);
// render turns the attempt number the writer computes into the comment body,
// which MUST embed marker verbatim.
type evidenceWrite struct {
	taskID     string
	agentID    string
	marker     string
	extraLabel string // "" when only the attempt counter is wanted
	render     func(attempt int) string
}

// writeRunEvidence performs the shared write: read the issue, skip terminal or
// unreadable ones, dedupe on the marker, AddComment, AddLabel the attempt
// counter (plus an optional extra label), then drop superseded counters.
//
// Returns (recorded, attempt, err). recorded=false with a nil error means there
// was nothing to write — the issue is gone, terminal, or already carries this
// marker — and is never an error for the caller. An error means the ticket was
// left without its record and, where the caller has one, the source row must
// NOT be latched.
func (s *Supervisor) writeRunEvidence(ctx context.Context, w evidenceWrite) (bool, int, error) {
	issue, err := s.IssueBackend.Get(ctx, w.taskID)
	if err != nil || issue == nil {
		slog.Debug("run-evidence target is unreadable, skipping",
			"task", w.taskID, "marker", w.marker, "err", err)
		return false, 0, nil
	}
	if issue.Status == "closed" || issue.Status == "tombstone" {
		// fleet-db's ValidateModifiable rejects label writes on terminal
		// issues. blocked and deferred are NOT terminal and do get the
		// evidence.
		slog.Debug("run-evidence target is terminal, skipping",
			"task", w.taskID, "status", issue.Status, "marker", w.marker)
		return false, 0, nil
	}
	if s.evidenceRecorded(ctx, w.taskID, w.marker) {
		return false, 0, nil
	}

	attempt := recordedAttempts(w.agentID, issue.Labels) + 1
	if _, err := s.IssueBackend.AddComment(ctx, backend.CommentAddParams{
		IssueID: w.taskID,
		Author:  w.agentID,
		Text:    truncateCommentBody(w.render(attempt)),
	}); err != nil {
		return false, 0, fmt.Errorf("add run-evidence comment to %s: %w", w.taskID, err)
	}
	if err := s.IssueBackend.AddLabel(ctx, w.taskID, attemptLabel(w.agentID, attempt)); err != nil {
		return false, 0, fmt.Errorf("add attempt label to %s: %w", w.taskID, err)
	}
	if w.extraLabel != "" {
		// Non-fatal: the counter is the load-bearing label and it already
		// landed, so a failure here must not re-run the whole pipeline.
		if err := s.IssueBackend.AddLabel(ctx, w.taskID, w.extraLabel); err != nil {
			slog.Debug("adding the evidence side label failed (harmless)",
				"task", w.taskID, "label", w.extraLabel, "err", err)
		}
	}
	s.removeSupersededAttemptLabels(ctx, w.taskID, w.agentID, issue.Labels, attempt)
	return true, attempt, nil
}

// evidenceRecorded reports whether a marker is already on the task. A
// ListComments failure is treated as "not found" and the pipeline proceeds:
// at-least-once beats losing the record, and a duplicate comment is visible and
// harmless.
func (s *Supervisor) evidenceRecorded(ctx context.Context, taskID, marker string) bool {
	comments, err := s.IssueBackend.ListComments(ctx, taskID)
	if err != nil {
		slog.Debug("could not read comments for run-evidence dedupe, proceeding",
			"task", taskID, "marker", marker, "err", err)
		return false
	}
	for _, c := range comments {
		if strings.Contains(c.Text, marker) {
			return true
		}
	}
	return false
}

// truncateCommentBody clamps an evidence body to fleet-db's body cap, cutting on
// a UTF-8 boundary and keeping the TAIL: the marker line is load-bearing and
// lives at the end, and a head-truncated body would lose the dedupe key and
// re-comment forever.
func truncateCommentBody(body string) string {
	if len(body) <= maxCommentBytes {
		return body
	}
	const prefix = "[truncated]\n"
	tail := body[len(body)-(maxCommentBytes-len(prefix)):]
	for i := 0; i < len(tail); i++ {
		if utf8.RuneStart(tail[i]) {
			tail = tail[i:]
			break
		}
	}
	return prefix + tail
}

// removeSupersededAttemptLabels drops this agent's lower counters once the new
// one has landed. Best-effort and never fails the pass — recordedAttempts takes
// the max, so a leftover "=1" beside "=2" is harmless (same rule as the review
// cycle's counter cleanup).
func (s *Supervisor) removeSupersededAttemptLabels(ctx context.Context, taskID, agentID string, labels []string, attempt int) {
	for _, l := range labels {
		n := parseAttemptCounter(agentID, l)
		if n == 0 || n >= attempt {
			continue
		}
		if err := s.IssueBackend.RemoveLabel(ctx, taskID, l); err != nil {
			slog.Debug("removing superseded attempt label failed (harmless)",
				"task", taskID, "label", l, "err", err)
		}
	}
}

// latchAbandonedSession writes the terminal session update. Once set, the row
// can never be selected again — which is why it is the last step.
func (s *Supervisor) latchAbandonedSession(ctx context.Context, sessionID string) error {
	status := domain.AgentSessionFailed
	errClass := abandonedRunErrorClass
	finishedAt := time.Now().UTC()
	finishedAtPtr := &finishedAt
	exitCode := -1
	exitCodePtr := &exitCode
	if _, err := s.ControlStore.AgentSessions().Update(ctx, s.WorkspaceID, sessionID, store.AgentSessionUpdate{
		Status:     &status,
		FinishedAt: &finishedAtPtr,
		ErrorClass: &errClass,
		ExitCode:   &exitCodePtr,
	}); err != nil {
		return fmt.Errorf("latch abandoned session %s: %w", sessionID, err)
	}
	return nil
}

// formatAbandonedRunComment renders the evidence body. Daemon-generated
// operational text: ASCII only, same house style as formatKillTimeline. The
// marker line is load-bearing — it is the dedupe key read back by
// abandonedEvidenceRecorded.
func formatAbandonedRunComment(sess *domain.AgentSession, attempt int) string {
	var b strings.Builder
	b.WriteString("**Run ended without reporting an outcome** -- recorded by the loom daemon.\n\n")
	fmt.Fprintf(&b, "Agent `%s` claimed %s and its run was killed before it could report a\n", sess.AgentID, sess.TaskID)
	b.WriteString("verdict, a hand-back, or a delivery. No comment, label change or error class\n")
	b.WriteString("was written by that run; this note is the only record of it.\n\n")
	b.WriteString("| field | value |\n|---|---|\n")
	fmt.Fprintf(&b, "| agent | %s |\n", sess.AgentID)
	fmt.Fprintf(&b, "| session | %s |\n", shortSessionID(sess.SessionID))
	fmt.Fprintf(&b, "| node | %s |\n", orDash(sess.NodeID))
	fmt.Fprintf(&b, "| started (UTC) | %s |\n", sessionStartOrder(sess).UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "| status when abandoned | %s |\n", sess.Status)
	fmt.Fprintf(&b, "| attempt | %d |\n\n", attempt)
	fmt.Fprintf(&b, "Counted as attempt %d for `%s` (label `%s`).\n", attempt, sess.AgentID, attemptLabel(sess.AgentID, attempt))
	b.WriteString("Attempt ceilings must count this the same as a reported failure.\n\n")
	fmt.Fprintf(&b, "%s%s\n", abandonedRunMarker, sess.SessionID)
	return b.String()
}

// orDash renders an empty field as "-" in the evidence table.
func orDash(v string) string {
	if v == "" {
		return "-"
	}
	return v
}

// ---------------------------------------------------------------------------
// Timeout-run recorder
// ---------------------------------------------------------------------------
//
// A run killed by a wall-clock ceiling is invisible to the two reconcilers
// above: it FINISHES its control-plane session row through the normal exit
// path, so unfinishedTaskSessions never selects it. It is equally invisible on
// the ticket, because completionHookTarget refuses to run a completion hook for
// any non-zero exit — correct (a turn that did not conclude must not stamp
// in-review) but it leaves no comment, no label and no status change, so a task
// that burned hours of agent time reads as untouched and the next claim redoes
// it from scratch.
//
// recordTimeoutRun closes that gap from the exit path, reusing the same
// marker-deduped comment plus attempt-counter primitive as the abandoned-run
// recorder. It has no session row to latch, so it is the write half only.

// recordTimeoutRun writes ticket-visible evidence for a run that was killed by a
// wall-clock ceiling. Never fatal: a failure is logged and the run continues its
// exit path.
//
// Unlike the abandoned-run recorder this is NOT at-least-once — there is no
// durable row that survives to trigger a retry. Retrying here would hold the
// role's concurrency slot and delay the restart, to cover a fleet-db outage that
// already loses the AgentStopped event, so the loss is accepted and logged at
// Warn instead.
func (s *Supervisor) recordTimeoutRun(ap *AgentProcess, exitCode int, sessionID string) {
	if ap == nil || !s.issueEvidenceReady() {
		return
	}
	if !isTimeoutExit(ap) {
		return
	}
	// Reads the worktree lock first; must therefore run BEFORE
	// postMortemRecovery, which clears that lock.
	taskID := s.taskIDForFinalize(ap)
	if taskID == "" {
		slog.Debug("timed-out run held no task, nothing to record", "worktree", ap.Entry.Worktree)
		return
	}

	agentID := ap.Entry.Worktree
	marker := timeoutRunMarker + timeoutRunID(ap, sessionID)
	facts := s.timeoutRunFactsFor(ap, taskID, sessionID, exitCode, marker)

	ctx, cancel := s.operationContext(abandonedOpTimeout)
	defer cancel()
	recorded, attempt, err := s.writeRunEvidence(ctx, evidenceWrite{
		taskID:     taskID,
		agentID:    agentID,
		marker:     marker,
		extraLabel: timeoutPartialLabel,
		render: func(attempt int) string {
			return formatTimeoutRunComment(facts, attempt)
		},
	})
	if err != nil {
		slog.Warn("recording a timeout run failed",
			"task", taskID, "agent", agentID, "session_id", sessionID, "err", err)
		return
	}
	if recorded {
		slog.Info("recorded a run that hit a time ceiling",
			"task", taskID, "agent", agentID, "session_id", sessionID,
			"cause", facts.cause, "ceiling", facts.ceiling, "attempt", attempt)
	}
}

// agentSessionIDSnapshot reads the control-plane session id under ap.Mu. Only
// valid before finalizeAgentSession, which clears the field.
func agentSessionIDSnapshot(ap *AgentProcess) string {
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	return ap.AgentSessionID
}

// isTimeoutExit reports whether the classified exit was a ceiling hit. IsClass
// (not Is) is the harness-class predicate used across this package, and it
// covers both producers: markRunDurationExceeded's run-duration cap and the
// silence watchdog's exit-137, which ClassifyFromLog resolves to ErrTimeout.
func isTimeoutExit(ap *AgentProcess) bool {
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	return ap.LastError != nil && ap.LastError.Class.IsClass(wrapper.ErrTimeout)
}

// timeoutRunID is the marker suffix: the control-plane session id when there is
// one, else "<agent>@<start RFC3339>". Never empty — an empty suffix would make
// the marker match every earlier timeout comment on the ticket and suppress
// every future record.
func timeoutRunID(ap *AgentProcess, sessionID string) string {
	if sessionID != "" {
		return sessionID
	}
	ap.Mu.Lock()
	start := ap.LastStart
	ap.Mu.Unlock()
	return fmt.Sprintf("%s@%s", ap.Entry.Worktree, start.UTC().Format(time.RFC3339))
}

// timeoutRunFacts are the facts the evidence body names, snapshotted once.
type timeoutRunFacts struct {
	agent    string
	taskID   string
	session  string
	marker   string
	exitCode int
	started  time.Time
	elapsed  time.Duration
	cause    string
	ceiling  string
}

// timeoutRunFactsFor gathers the body's facts under one ap.Mu hold.
func (s *Supervisor) timeoutRunFactsFor(ap *AgentProcess, taskID, sessionID string, exitCode int, marker string) timeoutRunFacts {
	ap.Mu.Lock()
	start := ap.LastStart
	reason := ap.StopReason
	ap.Mu.Unlock()

	elapsed := time.Duration(0)
	if !start.IsZero() {
		elapsed = time.Since(start).Round(time.Second)
	}
	cause, ceiling := s.timeoutRunCause(ap, reason)
	return timeoutRunFacts{
		agent:    ap.Entry.Worktree,
		taskID:   taskID,
		session:  shortSessionID(sessionID),
		marker:   marker,
		exitCode: exitCode,
		started:  start,
		elapsed:  elapsed,
		cause:    cause,
		ceiling:  ceiling,
	}
}

// timeoutRunCause maps the supervisor's stop reason to the human-readable cause
// and the ceiling that was crossed. Naming the ceiling is what lets the
// timeout-partial population be re-measured from the board rather than only from
// the event stream.
func (s *Supervisor) timeoutRunCause(ap *AgentProcess, reason StopReason) (cause, ceiling string) {
	switch reason {
	case StopReasonRunDurationExceeded:
		return "run-duration cap", formatCeiling(s.maxRunDurationFor(ap))
	case StopReasonWatchdog:
		return "output-timeout watchdog (silence)", formatCeiling(time.Duration(s.GetOutputTimeout()) * time.Second)
	default:
		// Classified from the harness log with no supervisor stop reason: the
		// run timed out, but this process did not pick the ceiling.
		return "harness-reported timeout", "-"
	}
}

// formatCeiling renders a disabled (zero) ceiling as "-".
func formatCeiling(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	return d.String()
}

// formatTimeoutRunComment renders the evidence body. Same house style as
// formatAbandonedRunComment: ASCII only, a bolded lede, a fact table, and the
// marker on its own final line — that last line is the dedupe key read back by
// evidenceRecorded.
func formatTimeoutRunComment(f timeoutRunFacts, attempt int) string {
	var b strings.Builder
	b.WriteString("**Run hit a time ceiling** -- recorded by the loom daemon.\n\n")
	fmt.Fprintf(&b, "Agent `%s` was holding %s when its run was stopped for exceeding a\n", f.agent, f.taskID)
	b.WriteString("wall-clock ceiling. The turn did not conclude, so no verdict, hand-back or\n")
	b.WriteString("delivery was written by that run; any work it did is in the checkpoint, not\n")
	b.WriteString("on this ticket.\n\n")
	b.WriteString("| field | value |\n|---|---|\n")
	fmt.Fprintf(&b, "| agent | %s |\n", f.agent)
	fmt.Fprintf(&b, "| task | %s |\n", f.taskID)
	fmt.Fprintf(&b, "| session | %s |\n", orDash(f.session))
	fmt.Fprintf(&b, "| cause | %s |\n", f.cause)
	fmt.Fprintf(&b, "| ceiling | %s |\n", orDash(f.ceiling))
	fmt.Fprintf(&b, "| elapsed | %s |\n", f.elapsed)
	fmt.Fprintf(&b, "| started (UTC) | %s |\n", f.started.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "| exit code | %d |\n", f.exitCode)
	fmt.Fprintf(&b, "| attempt | %d |\n\n", attempt)
	fmt.Fprintf(&b, "Counted as attempt %d for `%s` (label `%s`).\n", attempt, f.agent, attemptLabel(f.agent, attempt))
	fmt.Fprintf(&b, "Labeled `%s`: the next claim should read the checkpoint before restarting\n", timeoutPartialLabel)
	b.WriteString("from scratch.\n\n")
	fmt.Fprintf(&b, "%s\n", f.marker)
	return b.String()
}
