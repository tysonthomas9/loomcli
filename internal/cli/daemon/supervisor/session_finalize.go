package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"
	"unicode/utf8"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/sessionfinalize"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// leafUsage is the token/cost usage a canonical transcript's terminal `result`
// entry carries (the TS leaf serializes it into that entry's `output` field —
// local-task-runner resultEntry/taskUsageFromEntries). The Go leaf's raw stream
// has no such entry, so it decodes to the zero value (usage stays 0, unchanged).
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
	transcriptData, usage, _ := s.readLeafTranscript(state.sessionID)
	diffResult := finalizeLocalSession(state.session, ap, state.beforeRef, taskID, exitCode, errClass, usage)
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
	usage leafUsage,
) sessionfinalize.WithWorktreeResult {
	result, err := sessionfinalize.WithWorktree(sess, sessionfinalize.WithWorktreeOptions{
		WorktreePath: ap.WorktreePath,
		BeforeRef:    beforeRef,
		TaskID:       taskID,
		ExitCode:     exitCode,
		ErrorClass:   errClass,
		// Carry the leaf's reported usage so the supervisor's collector-less finalize
		// records non-zero tokens on the session (otherwise the reaped worker's
		// collector-aware finalize never runs and tokens land 0). Zero for the Go leaf.
		InputTokens:      usage.InputTokens,
		OutputTokens:     usage.OutputTokens,
		CacheReadTokens:  usage.CacheReadTokens,
		CacheWriteTokens: usage.CacheWriteTokens,
		EstimatedCostUSD: usage.cost(),
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
		return fmt.Errorf("read labels: %w", err)
	}
	completed := cycle.CompletedRounds(issue.Labels) + 1 // this pass finished a round

	if completed >= cycle.Threshold {
		slog.InfoContext(ctx, "review cycle complete; shipping",
			"task", taskID, "rounds", completed, "threshold", cycle.Threshold, "ship_label", cycle.ShipLabel)
		return s.IssueBackend.AddLabel(ctx, taskID, cycle.ShipLabel)
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
	slog.InfoContext(ctx, "review cycle re-armed",
		"task", taskID, "round", completed, "threshold", cycle.Threshold, "rearmed", cycle.RearmLabel)
	return nil
}

// currentCompletionHooks reads the agent's pipeline from the live config rather
// than from the Entry captured when the AgentProcess was constructed.
//
// Hooks are supervisor-owned writes decided at finalize, not spawn parameters,
// so binding them at construction made `agentdef update --on-complete-*`
// silently inert against a running agent: the CLI printed the pipeline, fleet-db
// stored it, `agentdef list` showed it, and the run did nothing. Only a daemon
// restart applied it (DOGFOOD-69). The daemon already refreshes its config every
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
	if hooks.IsEmpty() || exitCode != 0 || s.IssueBackend == nil {
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
		reply, err = s.finalAssistantReply(ctx, sessionID)
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
		case domain.AgentHookActionAddLabel:
			err = s.IssueBackend.AddLabel(ctx, taskID, action.Value)
		case domain.AgentHookActionCycle:
			err = s.advanceReviewCycle(ctx, taskID, action.Cycle)
		case domain.AgentHookActionClose:
			// Ordered last by Validate, so every write above has already
			// landed. Closing here rather than letting the agent do it is the
			// whole point: an agent-side close makes the preceding writes fail
			// against a terminal issue, which silently strands the hand-off.
			_, err = s.IssueBackend.Close(ctx, taskID, backend.CloseParams{
				Reason:  "completed by agent " + ap.Entry.Worktree,
				Session: sessionID,
			})
		default:
			err = fmt.Errorf("unsupported action type %q", action.Type)
		}
		if err != nil {
			return fmt.Errorf("on_complete[%d] (%s): %w", i, action.Type, err)
		}
	}
	return nil
}

func completionHooksNeedReply(hooks *domain.AgentHooks) bool {
	for _, a := range hooks.OnComplete {
		if a.Type == domain.AgentHookActionComment {
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

// finalAssistantReply returns the run's final assistant prose, waiting a
// bounded window for the leaf to flush its transcript. Missing or empty output
// is an error: a configured comment action must never be silently skipped.
func (s *Supervisor) finalAssistantReply(ctx context.Context, sessionID string) (string, error) {
	if sessionID == "" {
		return "", fmt.Errorf("extract final reply: no session id for this run")
	}
	sessStore, err := sessions.NewStore(cli.GetWorkspaceRuntimeDir())
	if err != nil {
		return "", fmt.Errorf("extract final reply: open session store: %w", err)
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
		return "", fmt.Errorf("extract final reply for session %s: %w", sessionID, lastErr)
	}
	return "", fmt.Errorf("extract final reply for session %s: no substantive assistant output within %s", sessionID, transcriptFlushWindow)
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
