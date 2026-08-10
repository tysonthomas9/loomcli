package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backendnames"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/cli/sessionfinalize"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// leafUsage is the token/cost usage a canonical transcript's terminal `result`
// entry carries (the TS leaf serializes it into that entry's `output` field —
// local-task-runner resultEntry/taskUsageFromEntries). The Go leaf's raw stream
// has no such entry, so that decode yields the zero value; harnessLeafUsage is
// the second source that covers the Go leaf.
type leafUsage struct {
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	CostUSD          float64 `json:"cost_usd"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`
}

func (u leafUsage) cost() float64 {
	if u.CostUSD != 0 {
		return u.CostUSD
	}
	return u.EstimatedCostUSD
}

// readLeafTranscript reads the session's on-disk native transcript ONCE so the
// finalize can reuse it for both the on-disk token backfill (the supervisor's
// collector-less finalize otherwise lands tokens=0) and the control-plane
// transcript_ref artifact upload. ok=false when there is no transcript yet.
func (s *Supervisor) readLeafTranscript(sessionID string) (data []byte, usage leafUsage, ok bool) {
	if sessionID == "" {
		return nil, leafUsage{}, false
	}
	sessStore, err := sessions.NewStore(cli.GetWorkspaceRuntimeDir())
	if err != nil {
		return nil, leafUsage{}, false
	}
	data, err = os.ReadFile(sessStore.NativeTranscriptPath(sessionID)) //nolint:gosec // session-owned path
	if err != nil || len(data) == 0 {
		return nil, leafUsage{}, false
	}
	return data, extractLeafUsage(data), true
}

// extractLeafUsage scans a canonical transcript from the end for the terminal
// `result` entry and decodes the usage object the TS leaf serialized into its
// `output` field. Returns the zero value for a raw backend stream (no `result`).
func extractLeafUsage(data []byte) leafUsage {
	lines := bytes.Split(data, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}
		var ev struct {
			Type   string `json:"type"`
			Output string `json:"output"`
		}
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		if ev.Type != "result" || ev.Output == "" {
			continue
		}
		var u leafUsage
		if json.Unmarshal([]byte(ev.Output), &u) != nil {
			return leafUsage{}
		}
		return u
	}
	return leafUsage{}
}

// resolveLeafTokens applies the two-source precedence for a collector-less
// finalize: the TS leaf's own accounting when it reported any, otherwise the
// back-fill read from the harness's transcript.
//
// The TS leaf wins because its `result` entry is the runner's first-hand
// accounting for exactly this run, while the back-fill infers a session from
// what is newest on disk. backfill is a thunk so the harness read — which walks
// a project directory and parses JSONL — is skipped entirely on the TS path.
func resolveLeafTokens(fromResultEntry leafUsage, backfill func() leafUsage) leafUsage {
	if fromResultEntry != (leafUsage{}) {
		return fromResultEntry
	}
	return backfill()
}

// backfillHarnessUsage back-fills tokens for the Go leaf, which writes no
// canonical `result` entry for extractLeafUsage to find.
//
// It gathers the three things the harness transcript readers need — which
// backend ran, which working directory it ran in, and when the session started
// — then reads the totals straight out of the backend's own session log. The
// worktree lock's carried claude_session_id is passed as a hint; it is present
// after a failed run (the daemon keeps it for --resume) and cleared after a
// successful one, so the resolver falls back to the newest transcript written
// since the session began, exactly as the transcript mirror already does.
//
// Best-effort throughout: every miss returns the zero value, which is the
// tokens=0 that used to be recorded unconditionally.
func (s *Supervisor) backfillHarnessUsage(ap *AgentProcess, sess *sessions.Session) leafUsage {
	backend := s.GetEffectiveBackend(ap)
	if backend == "" || ap.WorktreePath == "" {
		return leafUsage{}
	}

	ap.Mu.Lock()
	since := ap.LastStart
	ap.Mu.Unlock()
	if sess != nil && !sess.Meta.StartedAt.IsZero() {
		since = sess.Meta.StartedAt
	}

	hint := ""
	if info, lockErr := cli.ReadLockFile(ap.WorktreePath); lockErr == nil {
		hint = info.ClaudeSessionID
	}
	return harnessLeafUsage(backend, ap.WorktreePath, hint, since)
}

// harnessLeafUsage is the pure half: resolve the harness session id, read its
// cumulative usage, and price it.
//
// The token counts are carried across verbatim — see
// backends.SessionTokensFromHarnessUsage for why Claude's and Codex's
// input_tokens must not be reconciled with each other. The cost is an ESTIMATE
// from loom's own pricing table, not a harness-reported figure, so it lands in
// EstimatedCostUSD and leafUsage.cost() still lets a real cost_usd win wherever
// one exists.
func harnessLeafUsage(backend, workDir, hintSessionID string, since time.Time) leafUsage {
	sessionID := sessions.LatestHarnessSessionID(backend, workDir, hintSessionID, since)
	u := backends.ReadHarnessUsage(backend, sessionID, workDir)
	if u == nil {
		return leafUsage{}
	}
	input, output, cacheRead, cacheWrite := backends.SessionTokensFromHarnessUsage(u)
	out := leafUsage{
		InputTokens:      input,
		OutputTokens:     output,
		CacheReadTokens:  cacheRead,
		CacheWriteTokens: cacheWrite,
	}
	out.EstimatedCostUSD = usage.EstimateCost(usage.ResolvePricing(backend), usage.SessionUsage{
		InputTokens:      out.InputTokens,
		OutputTokens:     out.OutputTokens,
		CacheReadTokens:  out.CacheReadTokens,
		CacheWriteTokens: out.CacheWriteTokens,
	})
	return out
}

func (s *Supervisor) completeBackendUnavailableCleanup(ap *AgentProcess) {
	state := takeAgentSessionForFinalize(ap)
	taskID := s.taskIDForLifecycle(ap, nil)
	if state.session != nil {
		_ = state.session.Finalize(sessions.FinalizeOptions{ExitCode: -1, ErrorClass: "backend_unavailable"})
	}
	if state.sessionID != "" {
		s.completeControlPlaneAgentSession(ap, agentSessionCompletionInput{
			sessionID:  state.sessionID,
			leaseID:    state.leaseID,
			leaseToken: state.leaseToken,
			exitCode:   -1,
			errClass:   "backend_unavailable",
			taskID:     taskID,
		})
		return
	}
	if taskID == "" {
		return
	}
	s.releaseAssignedTaskClaim(ap, taskID)
	s.deregisterWorker(ap)
}

type agentSessionFinalizeState struct {
	session    *sessions.Session
	sessionID  string
	leaseID    string
	leaseToken string
	beforeRef  string
}

// finalizeAgentSession finalizes the daemon-created session after agent exit.
func (s *Supervisor) finalizeAgentSession(ap *AgentProcess, exitCode int) {
	state := takeAgentSessionForFinalize(ap)
	if state.session == nil && state.sessionID == "" {
		return
	}
	taskID := s.taskIDForFinalize(ap)
	errClass := agentErrorClass(ap)
	// Read the leaf transcript once: it feeds both the on-disk token backfill (via
	// finalizeLocalSession) and the control-plane transcript_ref artifact upload.
	// Read before finalizeLocalSession, whose codex/claude re-sync can rewrite the
	// on-disk file — this captures the TS leaf's canonical transcript verbatim.
	transcriptData, resultEntryTokens, _ := s.readLeafTranscript(state.sessionID)
	leafTokens := resolveLeafTokens(resultEntryTokens, func() leafUsage {
		return s.backfillHarnessUsage(ap, state.session)
	})
	diffResult := finalizeLocalSession(state.session, ap, state.beforeRef, taskID, exitCode, errClass, leafTokens)
	// KNOWN GAP — local session only. leafTokens lands on the on-disk session
	// record; it does NOT reach the control plane, because store.AgentSessionUpdate
	// has no token or cost fields (only Status/TaskID/FinishedAt/ErrorClass/
	// ExitCode/Metadata). The one hole it would fit through is the untyped
	// Metadata map, and smuggling stringified token counts through there would
	// invent a wire contract fleet-db does not agree to. Carrying usage to the
	// control plane needs typed fields on AgentSessionUpdate first; that is a
	// fleet-db schema change, deliberately not made here.
	s.completeControlPlaneAgentSession(ap, agentSessionCompletionInput{
		sessionID:      state.sessionID,
		leaseID:        state.leaseID,
		leaseToken:     state.leaseToken,
		exitCode:       exitCode,
		errClass:       errClass,
		taskID:         taskID,
		diffResult:     diffResult,
		transcriptData: transcriptData,
	})
}

func takeAgentSessionForFinalize(ap *AgentProcess) agentSessionFinalizeState {
	ap.Mu.Lock()
	state := agentSessionFinalizeState{
		session:    ap.Session,
		sessionID:  ap.AgentSessionID,
		leaseID:    ap.AgentLeaseID,
		leaseToken: ap.AgentLeaseToken,
		beforeRef:  ap.BeforeRef,
	}
	ap.Session = nil
	ap.AgentSessionID = ""
	ap.AgentLeaseID = ""
	ap.AgentLeaseToken = ""
	ap.Mu.Unlock()
	return state
}

func (s *Supervisor) taskIDForFinalize(ap *AgentProcess) string {
	taskID := ""
	if info, lockErr := cli.ReadLockFile(ap.WorktreePath); lockErr == nil {
		taskID = info.TaskID
	}
	if taskID == "" {
		taskID = s.taskIDForLifecycle(ap, nil)
	}
	return taskID
}

func agentErrorClass(ap *AgentProcess) string {
	ap.Mu.Lock()
	errClass := ""
	if ap.LastError != nil {
		errClass = ap.LastError.Class.String()
	}
	ap.Mu.Unlock()
	return errClass
}

func finalizeLocalSession(
	sess *sessions.Session,
	ap *AgentProcess,
	beforeRef string,
	taskID string,
	exitCode int,
	errClass string,
	leafTokens leafUsage,
) sessionfinalize.WithWorktreeResult {
	result, err := sessionfinalize.WithWorktree(sess, sessionfinalize.WithWorktreeOptions{
		WorktreePath: ap.WorktreePath,
		BeforeRef:    beforeRef,
		TaskID:       taskID,
		ExitCode:     exitCode,
		ErrorClass:   errClass,
		// Carry the leaf's reported usage so the supervisor's collector-less finalize
		// records non-zero tokens on the session (otherwise the reaped worker's
		// collector-aware finalize never runs and tokens land 0). Sourced from the
		// TS leaf's `result` entry, else from the harness's own transcript.
		InputTokens:      leafTokens.InputTokens,
		OutputTokens:     leafTokens.OutputTokens,
		CacheReadTokens:  leafTokens.CacheReadTokens,
		CacheWriteTokens: leafTokens.CacheWriteTokens,
		EstimatedCostUSD: leafTokens.cost(),
	})
	if err != nil {
		slog.Warn("session finalization failed", "worktree", ap.Entry.Worktree, "err", err)
	}
	return result
}

// ---------------------------------------------------------------------------
// Completion hooks
//
// The supervisor-owned on_complete pipeline. It lives beside session finalize
// because it runs at the same seam and reuses the same per-session transcript
// the finalize path reads.
// ---------------------------------------------------------------------------

const (
	// completionHookTimeout bounds the whole pipeline. A hook that cannot
	// finish in this window fails closed rather than holding the claim open.
	completionHookTimeout = 60 * time.Second

	transcriptPollEvery = 250 * time.Millisecond

	// maxCommentBytes mirrors fleet-db's models.MaxCommentBodyLength. The
	// reserve leaves room for the per-chunk header inside that budget.
	maxCommentBytes    = 10000
	chunkHeaderReserve = 64
)

// transcriptFlushWindow bounds how long we wait for the leaf to finish writing
// its transcript after the process exited. Past it we fail closed: a missing
// artifact must reopen the task, never silently skip the comment. Variable so
// tests can shrink it.
var transcriptFlushWindow = 15 * time.Second

// completionHookSkipReasons are the stop reasons that mean the turn did not
// conclude on its own. A hook must not certify a run that was cut short.
var completionHookSkipReasons = map[StopReason]bool{
	StopReasonShutdown:           true,
	StopReasonYielded:            true,
	StopReasonWatchdog:           true,
	StopReasonManualStop:         true,
	StopReasonConfigRemoved:      true,
	StopReasonBackendUnavailable: true,
}

// runCompletionHooks executes the agent's configured on_complete pipeline and
// returns the effective exit code for the rest of the exit path.
//
// It runs after exit classification but BEFORE finalizeAgentSession, because
// the session id, the claim lock, and the transcript all still exist at that
// point, and because a hook failure must be able to demote the run before the
// session status, checkpoint, and post-mortem recovery are decided. On failure
// it records a synthetic failure (exit -1 + CompletionHookFailure) so the owned
// task is reopened and retried under normal policy; the already-emitted
// AgentStopped process event keeps its factual exit code 0.
func (s *Supervisor) runCompletionHooks(ap *AgentProcess, exitCode int) int {
	hooks, taskID, ok := s.completionHookTarget(ap, exitCode)
	if !ok {
		return exitCode
	}

	ap.Mu.Lock()
	sessionID := ap.AgentSessionID
	ap.Mu.Unlock()

	ctx, cancel := s.operationContext(completionHookTimeout)
	defer cancel()

	if err := s.executeCompletionHooks(ctx, ap, hooks, taskID, sessionID); err != nil {
		slog.Warn("completion hook failed; demoting run so the task is reopened",
			"worktree", ap.Entry.Worktree, "task_id", taskID, "session_id", sessionID, "err", err)
		s.markCompletionHookFailure(ap, err)
		return -1
	}
	slog.Info("completion hooks applied",
		"worktree", ap.Entry.Worktree, "task_id", taskID, "actions", len(hooks.OnComplete))
	return exitCode
}

// advanceReviewCycle either re-arms the previous stage for another round or
// stamps the ship label once the threshold is reached.
//
// The counter lives in the label set as <prefix><n>; CompletedRounds takes the
// max, so a counter left behind by a crashed cleanup is harmless.
//
// ORDER IS THE CRASH-SAFETY MECHANISM. There is no atomic multi-label write, so:
//
//  1. remove the re-arm label FIRST — this hands the task back to the previous
//     stage. A crash between 1 and 2 repeats a round: at worst one extra review,
//     never a skipped one.
//  2. bump the counter.
//  3. drop stale lower counters.
//
// Bumping first would be wrong in a way that hides: a crash in between leaves
// the re-arm label present alongside the advanced counter, indistinguishable
// from a round that already ran, so the next pass skips a review and the task
// ships under-reviewed.
//
// The ship branch writes NO counter, so a shipped task's highest counter is
// threshold-1, and a threshold of 1 ships with no counter at all. "N rounds ran"
// is observable from the stage's comments, not from the label.
func (s *Supervisor) advanceReviewCycle(ctx context.Context, taskID string, cycle *domain.AgentHookCycle) error {
	if cycle == nil {
		return fmt.Errorf("cycle action has no cycle block")
	}
	issue, err := s.IssueBackend.Get(ctx, taskID)
	if err != nil {
		return fmt.Errorf("read status and labels: %w", err)
	}

	// The deliberate-stop check comes FIRST, before any write, for two reasons.
	// Semantically, `closed` and `blocked` are decisions somebody made — closed
	// is terminal, blocked is how a stage escalates to a human — and the loop
	// must not drag a task out of either. Mechanically, a terminal issue also
	// rejects label mutation server-side (fleet-db's ValidateModifiable), so
	// checking after the writes would never reach this branch on a real
	// backend: the run would already have failed on the label instead.
	//
	// `review` is deliberately NOT a gate, a departure from the design this is
	// modeled on. There a reviewer *elects* `review` to escalate, so it is a
	// deliberate gesture; in loom a planning stage lands there as its ordinary
	// completion, so honoring it would stall every loop at round one. Use
	// `blocked` to stop a loop for a human.
	if issue.Status == "closed" || issue.Status == "blocked" {
		slog.InfoContext(ctx, "review cycle stopped: task is not available to advance",
			"task", taskID, "status", issue.Status)
		return nil
	}

	completed := cycle.CompletedRounds(issue.Labels) + 1 // this pass finished a round

	if completed >= cycle.Threshold {
		if err := s.IssueBackend.AddLabel(ctx, taskID, cycle.ShipLabel); err != nil {
			return fmt.Errorf("stamp %q: %w", cycle.ShipLabel, err)
		}
		// The ship label routes the task to the next stage, and that stage can
		// only claim an `open` task — exactly like the re-arm below. Without
		// this the loop bounds correctly and then stalls at the hand-off:
		// ship label stamped, nothing able to act on it.
		if err := s.reopenForNextStage(ctx, taskID, issue.Status); err != nil {
			return err
		}
		slog.InfoContext(ctx, "review cycle complete; shipping",
			"task", taskID, "rounds", completed, "threshold", cycle.Threshold, "ship_label", cycle.ShipLabel)
		return nil
	}

	if err := s.IssueBackend.RemoveLabel(ctx, taskID, cycle.RearmLabel); err != nil {
		return fmt.Errorf("re-arm %q: %w", cycle.RearmLabel, err)
	}
	if err := s.IssueBackend.AddLabel(ctx, taskID, cycle.CounterLabel(completed)); err != nil {
		return fmt.Errorf("bump counter to %d: %w", completed, err)
	}
	for _, label := range issue.Labels {
		if n := cycle.ParseCounter(label); n > 0 && n < completed {
			if err := s.IssueBackend.RemoveLabel(ctx, taskID, label); err != nil {
				// Cleanup only. CompletedRounds takes the max, so a survivor
				// cannot change the next decision — do not fail the pipeline.
				slog.WarnContext(ctx, "stale cycle counter left in place",
					"task", taskID, "label", label, "err", err)
			}
		}
	}
	// The re-arm is only real if the previous stage can claim the task again.
	if err := s.reopenForNextStage(ctx, taskID, issue.Status); err != nil {
		return err
	}
	slog.InfoContext(ctx, "review cycle re-armed",
		"task", taskID, "round", completed, "threshold", cycle.Threshold, "rearmed", cycle.RearmLabel)
	return nil
}

// reopenForNextStage returns the task to the only state another stage can claim
// from: task_router scores anything that is not `open` as 0 ("not open"), and
// the clean-exit recovery path trusts whatever status the agent left behind. So
// a stage that finished at `in_progress` or `review` has to be normalized here,
// or the hand-off — re-arm or ship — never happens.
func (s *Supervisor) reopenForNextStage(ctx context.Context, taskID, status string) error {
	if status == "open" {
		return nil
	}
	open := "open"
	if err := s.IssueBackend.Update(ctx, taskID, backend.UpdateParams{Status: &open}); err != nil {
		return fmt.Errorf("return the task to open for the next stage: %w", err)
	}
	return nil
}

// currentCompletionHooks reads the agent's pipeline from the live config rather
// than from the Entry captured when the AgentProcess was constructed.
//
// Hooks are supervisor-owned writes decided at finalize, not spawn parameters,
// so binding them at construction made `agentdef update --on-complete-*`
// silently inert against a running agent: the CLI printed the pipeline, fleet-db
// stored it, `agentdef list` showed it, and the run did nothing. Only a daemon
// restart applied it. The daemon already refreshes its config every
// 30s, so re-reading here costs a map lookup and makes the change take effect on
// the next run.
//
// Falls back to the captured Entry when the agent is absent from the snapshot —
// mid-removal, for instance — so a disappearing config never silently drops a
// pipeline the run was started with.
func (s *Supervisor) currentCompletionHooks(ap *AgentProcess) *domain.AgentHooks {
	if s.ConfigSnapshot == nil {
		return ap.Entry.Hooks
	}
	cfg := s.ConfigSnapshot()
	if cfg == nil {
		return ap.Entry.Hooks
	}
	for i := range cfg.Agents {
		if cfg.Agents[i].Worktree == ap.Entry.Worktree {
			return cfg.Agents[i].Hooks
		}
	}
	return ap.Entry.Hooks
}

// completionHookTarget reports whether this exit is eligible for hooks and, if
// so, the pipeline and the task to write to. Everything except a clean,
// self-concluded run that owned a task is skipped, preserving pre-hook behavior.
func (s *Supervisor) completionHookTarget(ap *AgentProcess, exitCode int) (*domain.AgentHooks, string, bool) {
	hooks := s.currentCompletionHooks(ap)
	if hooks.IsEmpty() || exitCode != 0 {
		return nil, "", false
	}
	if s.IssueBackend == nil {
		// The configured writes cannot happen at all. Skipping silently would
		// report success for a run whose hooks never ran, so say so loudly.
		// It stays a skip rather than a demotion because no retry can conjure a
		// backend: demoting would only reopen the task and loop until the
		// agent's block budget is gone.
		slog.Warn("completion hooks configured but no issue backend is available; skipping them",
			"worktree", ap.Entry.Worktree, "actions", len(hooks.OnComplete))
		return nil, "", false
	}
	ap.Mu.Lock()
	lastErr := ap.LastError
	stopReason := ap.StopReason
	ap.Mu.Unlock()
	// classifyAgentExit leaves LastError nil only on a clean, task-bearing exit;
	// a no-work idle exit sets NoWorkOutcome and is not a completed turn.
	if lastErr != nil || completionHookSkipReasons[stopReason] {
		return nil, "", false
	}
	if IsYieldRequested(ap.WorktreePath) {
		return nil, "", false
	}
	taskID := s.taskIDForFinalize(ap)
	if taskID == "" {
		return nil, "", false
	}
	return hooks, taskID, true
}

// executeCompletionHooks performs the configured writes strictly in stored
// order, stopping at the first error. Reply text is never logged.
func (s *Supervisor) executeCompletionHooks(
	ctx context.Context,
	ap *AgentProcess,
	hooks *domain.AgentHooks,
	taskID, sessionID string,
) error {
	// Defensive re-validation: fleet-db rejects a bad pipeline at write time,
	// but a definition written by an older or newer peer could still violate
	// the write-before-stamp order. Refuse to execute it rather than silently
	// reordering or skipping the offending action.
	if err := hooks.Validate(); err != nil {
		return fmt.Errorf("stored on_complete pipeline is invalid: %w", err)
	}

	var reply string
	if completionHooksNeedReply(hooks) {
		var err error
		reply, err = s.finalAssistantReply(ctx, ap, sessionID)
		if err != nil {
			return err
		}
	}

	for i, action := range hooks.OnComplete {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("on_complete[%d] (%s): %w", i, action.Type, err)
		}
		var err error
		switch action.Type {
		case domain.AgentHookActionComment:
			err = s.postFinalReplyComment(ctx, ap, taskID, reply)
		case domain.AgentHookActionWriteDesign:
			// Same extracted reply the comment posts — resolved once above, for
			// both. A second extraction could disagree with the first about what
			// "the run's artifact" is, and a pipeline that comments one text and
			// records another as the design is worse than either alone.
			err = s.writeTaskDesign(ctx, taskID, reply)
		case domain.AgentHookActionAddLabel:
			err = s.IssueBackend.AddLabel(ctx, taskID, action.Value)
		case domain.AgentHookActionRemoveLabel:
			// Writes the same task as every other action here, and the loop
			// executes stored order verbatim — so the position that matters is
			// the one the pipeline was BUILT with (hooksFromFlags), and the
			// rule Validate enforces: after the comment, before the stamp.
			//
			// After the comment because a removal mutates the label set and is
			// therefore observable routing state, exactly like add_label:
			// write-before-stamp binds it, and Validate refuses a comment that
			// follows it.
			//
			// Before the add_label because add_label is the certifying write —
			// the token the next stage waits on. Removing after it would leave
			// a window where the task carries both the label that routed it
			// here and the label that hands it on, claimable by the upstream
			// and downstream stages at once. Removing first can only leave the
			// task briefly unrouted, which stalls visibly instead of forking.
			// Same reasoning as the cycle's remove-then-bump ordering above.
			//
			// CAUTION: if an upstream stage's filter EXCLUDES this label, this
			// removal re-arms that stage — it re-claims, re-stamps, and the
			// pipeline loops forever. See AgentHookActionRemoveLabel. Not
			// guarded here: this executor cannot see the upstream filter, so
			// any guard would be a guess at intent.
			err = s.IssueBackend.RemoveLabel(ctx, taskID, action.Value)
		case domain.AgentHookActionSetStatus:
			err = s.setTaskStatus(ctx, taskID, action)
		case domain.AgentHookActionCycle:
			err = s.advanceReviewCycle(ctx, taskID, action.Cycle)
		case domain.AgentHookActionClose:
			// Ordered last by Validate, so every write above has already
			// landed. Closing here rather than letting the agent do it is the
			// whole point: an agent-side close makes the preceding writes fail
			// against a terminal issue, which silently strands the hand-off.
			//
			// Closing an already-closed task is NOT a failure here: fleet-db's
			// close endpoint is idempotent (the handler swallows its own
			// already-closed error and replies 200 with the current issue), so
			// an agent whose prompt already closed the task, or a human closing
			// between exit and hooks, cannot demote an otherwise clean run. No
			// client-side tolerance is layered on top of that, deliberately —
			// every other close conflict (open blockers, dependencies) must
			// keep failing the pipeline.
			_, err = s.IssueBackend.Close(ctx, taskID, backend.CloseParams{
				Reason:  "completed by agent " + ap.Entry.Worktree,
				Session: sessionID,
			})
		default:
			// Unreachable: Validate above rejects unknown types. Kept so a new
			// action added to the vocabulary but not to this switch fails the
			// run instead of being silently skipped.
			err = fmt.Errorf("unsupported action type %q", action.Type)
		}
		if err != nil {
			return fmt.Errorf("on_complete[%d] (%s): %w", i, action.Type, err)
		}
	}
	return nil
}

// completionHooksNeedReply reports whether the pipeline contains a body write,
// i.e. whether the run's artifact has to be extracted before the loop starts.
// Both body-writing actions draw on the SAME extraction, so this stays a single
// "do we need it at all?" question rather than one read per action.
func completionHooksNeedReply(hooks *domain.AgentHooks) bool {
	for _, a := range hooks.OnComplete {
		if a.Type == domain.AgentHookActionComment || a.Type == domain.AgentHookActionWriteDesign {
			return true
		}
	}
	return false
}

// postFinalReplyComment posts the reply as one or more comments, in order. A
// reply longer than the server's byte cap is split at rune boundaries with
// stable part headers; every chunk must land before the caller moves on to a
// label action.
func (s *Supervisor) postFinalReplyComment(ctx context.Context, ap *AgentProcess, taskID, reply string) error {
	chunks := chunkComment(reply, maxCommentBytes-chunkHeaderReserve)
	for i, chunk := range chunks {
		text := chunk
		if len(chunks) > 1 {
			text = fmt.Sprintf("[final reply - part %d/%d]\n\n%s", i+1, len(chunks), chunk)
		}
		if _, err := s.IssueBackend.AddComment(ctx, backend.CommentAddParams{
			IssueID: taskID,
			Author:  ap.Entry.Worktree,
			Text:    text,
		}); err != nil {
			return fmt.Errorf("post comment chunk %d/%d: %w", i+1, len(chunks), err)
		}
	}
	return nil
}

// writeTaskDesign records the run's final reply as the owned task's design.
//
// Unlike a comment this is NOT chunked: a design is one document, and the write
// is a plain field PATCH bounded only by fleet-db's request-body limit. It is
// also idempotent — a full replace — which is why hooksFromFlags puts it ahead
// of the comment: when a later action fails the run is demoted and retried, and
// re-running an overwrite costs nothing while re-running a comment leaves a
// duplicate behind.
//
// An empty reply fails the action rather than clearing the field. finalReply
// extraction already refuses to return "" (see finalAssistantReply), so this
// cannot trigger today; it stays because the failure it guards is silent —
// wiping a planner's design and reporting a clean run — and the caller is one
// refactor away from being able to hand us an empty string.
func (s *Supervisor) writeTaskDesign(ctx context.Context, taskID, reply string) error {
	if strings.TrimSpace(reply) == "" {
		return fmt.Errorf("write design: the run produced no final reply, refusing to write an empty design")
	}
	if err := s.IssueBackend.Update(ctx, taskID, backend.UpdateParams{Design: &reply}); err != nil {
		return fmt.Errorf("write design: %w", err)
	}
	return nil
}

// setTaskStatus moves the owned task to the action's status, carrying a blocked
// transition's reason with it.
//
// ONE Update, deliberately, rather than a status write plus a follow-up. The
// fleet client decomposes a composite update in a verified-safe order (PATCH
// body fields -> labels -> status transition -> assign), which is the same
// property writeQuarantine relies on for its blocked+unassign+label write. Two
// consequences here:
//
//   - The reason lands in the PATCH that PRECEDES the status transition, so the
//     card is never observably blocked without the note explaining it. That is
//     write-before-stamp again, one layer down. quarantine's split — status
//     write, then a best-effort comment that must not unlatch it — applies to a
//     genuinely optional follow-up; a blocked reason is not optional, so it does
//     not go second. Nothing here can fail after the status has landed.
//   - Assignee is deliberately NOT set. review and blocked transition out of
//     in_progress by releasing the claim lock AS current.Assignee inside the
//     fleet client (transitionToBlockedOrReview, LOOM-1), and
//     shouldAssignBeforeStatus exists to keep an assign from running first and
//     erasing the identity that release needs. Passing no assignee at all means
//     the ordering question never arises: the lock holder is still on the issue
//     when the release runs. The action carries no assignee either, so writing
//     one would be a routing decision its vocabulary cannot express.
//
// The reason goes into notes because that is where loom already keeps it: the
// board's needs-attention state is "blocked AND notes present", and
// enforceBlockReason tells operators to write it as --notes "BLOCKED: ...". A
// hook-written note therefore has to read the same as a hand-written one, prefix
// included. It replaces the field rather than appending, like every other writer
// of notes — the reason for the block that is happening NOW supersedes an older
// one, and appending would need a read-modify-write that could lose a concurrent
// edit anyway.
func (s *Supervisor) setTaskStatus(ctx context.Context, taskID string, action domain.AgentHookAction) error {
	status := action.Value
	params := backend.UpdateParams{Status: &status}
	// Validate guarantees a reason is present only on a set_status to blocked,
	// so the field's presence IS the condition. Re-testing the status here would
	// be a second rule free to drift from the one that is actually enforced.
	if action.Reason != "" {
		notes := "BLOCKED: " + action.Reason
		params.Notes = &notes
	}
	if err := s.IssueBackend.Update(ctx, taskID, params); err != nil {
		return fmt.Errorf("set status %q: %w", status, err)
	}
	return nil
}

// chunkComment splits s into pieces of at most budget bytes, never splitting a
// UTF-8 sequence. It always returns at least one chunk.
func chunkComment(s string, budget int) []string {
	if budget <= 0 || len(s) <= budget {
		return []string{s}
	}
	var chunks []string
	for len(s) > budget {
		cut := budget
		// Back off to the start of the rune that straddles the boundary.
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		if cut == 0 {
			// A single rune wider than the budget cannot happen for valid UTF-8
			// with a sane budget, but never emit an empty chunk and spin.
			cut = budget
		}
		chunks = append(chunks, s[:cut])
		s = s[cut:]
	}
	if s != "" {
		chunks = append(chunks, s)
	}
	return chunks
}

// mirrorNativeTranscript copies the backend's own transcript into this run's
// session, the way sessionfinalize.WithWorktree does at finalize.
//
// Hooks run BEFORE finalize, so without this the raw backends have nothing for
// the extraction to read: codex mirrors its rollout only inside finalize, and
// the daemon-mode subprocess adopts the inherited session as nil, so its own
// finalize no-ops too. Claude usually needs nothing here because `loom hooks`
// mirrors its transcript live on every hook event — hence the caller reads
// first and only mirrors on a miss, which also keeps a canonical TS-leaf
// transcript (which finalize still reads for usage and the transcript_ref
// artifact) from being overwritten by a raw stream.
//
// Best-effort by design: the return value only sharpens the diagnosis when the
// reply cannot be found.
func (s *Supervisor) mirrorNativeTranscript(ap *AgentProcess) error {
	ap.Mu.Lock()
	sess := ap.Session
	ap.Mu.Unlock()
	if sess == nil {
		return fmt.Errorf("no local session handle for this run")
	}
	switch sess.Meta.Backend {
	case backendnames.Codex:
		path, err := sess.SyncLatestCodexRollout(ap.WorktreePath, sess.Meta.StartedAt)
		if err == nil && path == "" {
			return fmt.Errorf("no codex rollout for %s since %s",
				ap.WorktreePath, sess.Meta.StartedAt.Format(time.RFC3339))
		}
		return err
	case backendnames.Claude:
		// Empty claudeUUID: newest-by-mtime in the worktree's project dir, the
		// same resolution the supervisor's finalize uses.
		path, err := sess.SyncLatestClaudeTranscript(ap.WorktreePath, "", sess.Meta.StartedAt)
		if err == nil && path == "" {
			return fmt.Errorf("no claude transcript for %s since %s",
				ap.WorktreePath, sess.Meta.StartedAt.Format(time.RFC3339))
		}
		return err
	default:
		return nil
	}
}

// finalAssistantReply returns the run's final assistant prose, waiting a
// bounded window for the leaf to flush its transcript. Missing or empty output
// is an error: a configured comment action must never be silently skipped.
func (s *Supervisor) finalAssistantReply(ctx context.Context, ap *AgentProcess, sessionID string) (string, error) {
	if sessionID == "" {
		return "", fmt.Errorf("extract final reply: no session id for this run")
	}
	sessStore, err := sessions.NewStore(cli.GetWorkspaceRuntimeDir())
	if err != nil {
		return "", fmt.Errorf("extract final reply: open session store: %w", err)
	}
	// Read before mirroring: the TS leaf writes its canonical transcript before
	// exiting and Claude's live hook dispatch keeps the session's copy current,
	// so those runs answer immediately and never have their transcript rewritten.
	if reply, readErr := sessStore.FinalAssistantReply(sessionID); readErr == nil && reply != "" {
		return reply, nil
	}
	// Nothing to read yet — mirror the backend's native transcript here rather
	// than waiting for a finalize that only runs after this pipeline.
	mirrorNote := ""
	if mirrorErr := s.mirrorNativeTranscript(ap); mirrorErr != nil {
		mirrorNote = fmt.Sprintf(" (mirroring the backend transcript also failed: %v)", mirrorErr)
		slog.Warn("could not mirror the native transcript for a completion hook",
			"worktree", ap.Entry.Worktree, "session_id", sessionID, "err", mirrorErr)
	}
	deadline := time.Now().Add(transcriptFlushWindow)
	var lastErr error
	for {
		// Session ids are unique per attempt, so this can never read a prior
		// run's transcript.
		reply, err := sessStore.FinalAssistantReply(sessionID)
		if err != nil {
			lastErr = err
		} else if reply != "" {
			return reply, nil
		}
		if time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("extract final reply: %w", ctx.Err())
		case <-time.After(transcriptPollEvery):
		}
	}
	if lastErr != nil {
		return "", fmt.Errorf("extract final reply for session %s: %w%s", sessionID, lastErr, mirrorNote)
	}
	return "", fmt.Errorf("extract final reply for session %s: no substantive assistant output within %s%s",
		sessionID, transcriptFlushWindow, mirrorNote)
}

// markCompletionHookFailure converts the clean run into a synthetic failure so
// session finalize, the checkpoint, and post-mortem recovery all see a failed
// run and the owned task returns to open for a bounded retry.
func (s *Supervisor) markCompletionHookFailure(ap *AgentProcess, cause error) {
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	ap.LastExitCode = -1
	ap.LastNoWork = false
	ap.LastError = &agenterr.AgentError{
		Class:     agenterr.OutcomeFromDomain(agenterr.CompletionHookFailureOutcome),
		ExitCode:  -1,
		Message:   cause.Error(),
		Backend:   ap.Entry.Backend,
		Timestamp: time.Now(),
	}
}
