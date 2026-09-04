package supervisor

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

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
	if s.IssueBackend == nil {
		slog.Debug("abandoned-run recorder disabled (no issue backend)", "workspace", s.WorkspaceID)
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
	issue, err := s.IssueBackend.Get(ctx, sess.TaskID)
	if err != nil || issue == nil {
		slog.Debug("abandoned run's task is unreadable, latching the session only",
			"task", sess.TaskID, "session_id", sess.SessionID, "err", err)
		return s.latchAbandonedSession(ctx, sess.SessionID)
	}
	if issue.Status == "closed" || issue.Status == "tombstone" {
		// fleet-db's ValidateModifiable rejects label writes on terminal
		// issues. blocked and deferred are NOT terminal and do get the
		// evidence.
		slog.Debug("abandoned run's task is terminal, latching the session only",
			"task", sess.TaskID, "status", issue.Status, "session_id", sess.SessionID)
		return s.latchAbandonedSession(ctx, sess.SessionID)
	}

	if s.abandonedEvidenceRecorded(ctx, sess) {
		return s.latchAbandonedSession(ctx, sess.SessionID)
	}

	attempt := recordedAttempts(sess.AgentID, issue.Labels) + 1
	if _, err := s.IssueBackend.AddComment(ctx, backend.CommentAddParams{
		IssueID: sess.TaskID,
		Author:  sess.AgentID,
		Text:    formatAbandonedRunComment(sess, attempt),
	}); err != nil {
		return fmt.Errorf("add abandoned-run comment to %s: %w", sess.TaskID, err)
	}
	if err := s.IssueBackend.AddLabel(ctx, sess.TaskID, attemptLabel(sess.AgentID, attempt)); err != nil {
		return fmt.Errorf("add attempt label to %s: %w", sess.TaskID, err)
	}
	s.removeSupersededAttemptLabels(ctx, sess, issue.Labels, attempt)

	slog.Info("recorded a run that ended without reporting an outcome",
		"task", sess.TaskID, "agent", sess.AgentID, "session_id", sess.SessionID,
		"attempt", attempt, "label", attemptLabel(sess.AgentID, attempt))
	return s.latchAbandonedSession(ctx, sess.SessionID)
}

// abandonedEvidenceRecorded reports whether this session's evidence comment is
// already on the task. A ListComments failure is treated as "not found" and the
// pipeline proceeds: at-least-once beats losing the record, and a duplicate
// comment is visible and harmless.
func (s *Supervisor) abandonedEvidenceRecorded(ctx context.Context, sess *domain.AgentSession) bool {
	comments, err := s.IssueBackend.ListComments(ctx, sess.TaskID)
	if err != nil {
		slog.Debug("could not read comments for abandoned-run dedupe, proceeding",
			"task", sess.TaskID, "session_id", sess.SessionID, "err", err)
		return false
	}
	marker := abandonedRunMarker + sess.SessionID
	for _, c := range comments {
		if strings.Contains(c.Text, marker) {
			return true
		}
	}
	return false
}

// removeSupersededAttemptLabels drops this agent's lower counters once the new
// one has landed. Best-effort and never fails the pass — recordedAttempts takes
// the max, so a leftover "=1" beside "=2" is harmless (same rule as the review
// cycle's counter cleanup).
func (s *Supervisor) removeSupersededAttemptLabels(ctx context.Context, sess *domain.AgentSession, labels []string, attempt int) {
	for _, l := range labels {
		n := parseAttemptCounter(sess.AgentID, l)
		if n == 0 || n >= attempt {
			continue
		}
		if err := s.IssueBackend.RemoveLabel(ctx, sess.TaskID, l); err != nil {
			slog.Debug("removing superseded attempt label failed (harmless)",
				"task", sess.TaskID, "label", l, "err", err)
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
