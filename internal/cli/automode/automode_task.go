package automode

import (
	"errors"
	"fmt"
	"log"
	"os/exec"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/agentpolicy"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/cli/git"
	"github.com/tysonthomas9/loomcli/internal/cli/sessionfinalize"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/infra/sessionstoreadapter"
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

func createAutoSession(ctx *autoLoopCtx, prompt string) *sessions.Session {
	if ctx.sessStore == nil {
		return nil
	}
	sess, _ := sessionstoreadapter.Create(ctx.sessStore, sessions.CreateOptions{
		AgentName: ctx.opts.AgentName, Backend: cli.ResolveBackendName(),
		EpicID: ctx.opts.ParentID, Prompt: prompt, AttemptNum: ctx.state.TasksCompleted + 1,
	})
	if sess != nil {
		backends.SetActiveSessionRuntimeEnv(cli.GetWorkspaceRuntimeDir(), sess.SessionID())
		go sessions.NotifyWebUI(cmdstore.RootContext(), backends.ResolveWebUIURL(), "", sess.SessionID(), sessions.StatusRunning, backends.ResolveNotifyToken())
	}
	return sess
}

func finalizeAutoSession(ctx *autoLoopCtx, sess *sessions.Session, beforeRef string, err error, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int64, estimatedCostUSD float64) {
	if sess == nil {
		return
	}
	exitCode := 0
	if err != nil {
		exitCode = 1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}
	taskID := ""
	if info, lockErr := ctx.readLock(); lockErr == nil && info != nil {
		taskID = info.TaskID
	}
	_, _ = sessionfinalize.WithWorktree(sess, sessionfinalize.WithWorktreeOptions{
		WorktreePath:     ctx.opts.WorktreePath,
		BeforeRef:        beforeRef,
		TaskID:           taskID,
		ExitCode:         exitCode,
		InputTokens:      inputTokens,
		OutputTokens:     outputTokens,
		CacheReadTokens:  cacheReadTokens,
		CacheWriteTokens: cacheWriteTokens,
		EstimatedCostUSD: estimatedCostUSD,
	})
	backends.ClearActiveSessionEnv()
	go sessions.NotifyWebUI(cmdstore.RootContext(), backends.ResolveWebUIURL(), taskID, sess.SessionID(), sess.Meta.Status, backends.ResolveNotifyToken())
}

// maxSameTaskFailures is the consecutive same-task-ID failure threshold that
// triggers stuck-task detection: skip the task and continue the loop instead
// of treating each failure as a generic ConsecutiveErrors strike.
const maxSameTaskFailures = 3

// maxConsecutiveRateLimits is the process-wide safety net: if this many
// consecutive invocations hit rate limits despite the breaker's cooldowns,
// auto-mode exits entirely.
const maxConsecutiveRateLimits = 5

func handleAutoTaskError(ctx *autoLoopCtx, ae *agenterr.AgentError, rawErr error, shutdown chan struct{}) bool {
	fmt.Printf("[auto] Agent error: [%s] %s\n", ae.Class, ae.Message)
	trackResumeFailures(ctx)
	failedTaskID := readFailedTaskID(ctx)
	emitTaskFailedEvent(ctx, ae, rawErr, failedTaskID)

	d := agentpolicy.Decide(ae.Class)

	// Terminal decisions exit immediately — fatal (auth/billing), fast-fail
	// (deterministic, e.g. context overflow), and failover (auto-mode has no
	// fallback backend, so a wrong model is terminal here). These never
	// retry, so per-task tracking would only accumulate stale state.
	if d.Decision == agentpolicy.StopFatal || d.Decision == agentpolicy.FastFail || d.Decision == agentpolicy.Failover {
		return exitWithReason(ctx, fmt.Sprintf("fatal error: %s", ae.Message))
	}
	if ae.Class.Is(agenterr.NoWorkOutcome) {
		return exitWithReason(ctx, "no work available")
	}

	// If the failed task is already in the skip set (agent re-claimed it after
	// a prior stuck classification), don't re-run per-task tracking or
	// re-emit task.stuck. Mirror handleAutoTaskSuccess's no-progress handling:
	// clear the lock's TaskID and let the loop try a different task.
	if failedTaskID != "" && ctx.stuckTaskIDs[failedTaskID] {
		fmt.Printf("[auto] Stuck task %s failed again, skipping without re-classification\n", failedTaskID)
		tryClearTaskID(ctx)
		return true
	}

	// Rate limits are global API throttling, not per-task failures
	// (RetryUncounted; NoWork — the only other uncounted outcome — exited
	// above). Route them before per-task tracking so a sustained rate limit
	// against a single task doesn't drain its stuck-task budget — and so the
	// rate-limit breaker and ConsecutiveRateLimits counter record every hit.
	if d.Decision == agentpolicy.RetryUncounted {
		return handleRateLimitError(ctx, ae, shutdown)
	}

	// Only update per-task tracking for errors that will lead to a retry —
	// otherwise sameTaskFailures can advance past the threshold without ever
	// triggering skipStuckTask, leaving inconsistent state for any future
	// invocation that shares the ctx.
	trackSameTaskFailures(ctx, failedTaskID)

	// Stuck task detection: same task ID failed maxSameTaskFailures times in a
	// row. Skip the task, reset error counters, and continue the loop so other
	// tasks can still make progress.
	if isStuckTaskThresholdReached(ctx, failedTaskID) {
		return skipStuckTask(ctx, failedTaskID, rawErr)
	}

	// Block-decision outcomes (backend binary missing): fixed-interval wait
	// for the binary to return, 3-error exit.
	if d.Decision == agentpolicy.Block {
		return handleDefaultError(ctx, shutdown)
	}

	// Counted retries (timeout/transient/unknown/spawn/lock-conflict):
	// exponential backoff, 3-error exit.
	return handleTransientError(ctx, shutdown)
}

// readFailedTaskID returns the TaskID recorded in the lock file, or "" if no
// lock is available (agent crashed before claiming, or readLock not wired up).
func readFailedTaskID(ctx *autoLoopCtx) string {
	if ctx.readLock == nil {
		return ""
	}
	info, err := ctx.readLock()
	if err != nil || info == nil {
		return ""
	}
	return info.TaskID
}

// trackSameTaskFailures updates the per-task-ID consecutive failure counter.
// When the same task fails repeatedly, sameTaskFailures grows; when a different
// task fails (or no task ID is available) the counter resets.
func trackSameTaskFailures(ctx *autoLoopCtx, failedTaskID string) {
	if failedTaskID == "" {
		// Agent crashed before claiming a task — systemic failure, not a
		// stuck-task condition. Don't touch per-task tracking.
		return
	}
	if failedTaskID == ctx.lastFailedTaskID {
		ctx.sameTaskFailures++
		return
	}
	ctx.lastFailedTaskID = failedTaskID
	ctx.sameTaskFailures = 1
}

// isStuckTaskThresholdReached reports whether sameTaskFailures has hit the
// stuck-task threshold for a non-empty task ID.
func isStuckTaskThresholdReached(ctx *autoLoopCtx, failedTaskID string) bool {
	return failedTaskID != "" && ctx.sameTaskFailures >= maxSameTaskFailures
}

// skipStuckTask records the task as stuck, emits a TaskStuck event, resets the
// generic error counters (skipping a stuck task is recovery, not a strike),
// clears the lock file's TaskID, and returns true so the loop continues with a
// fresh task on the next iteration.
func skipStuckTask(ctx *autoLoopCtx, failedTaskID string, rawErr error) bool {
	failures := ctx.sameTaskFailures
	fmt.Printf("[auto] Task %s is stuck (%d consecutive failures), skipping\n", failedTaskID, failures)

	lastErr := ""
	if rawErr != nil {
		lastErr = rawErr.Error()
	}
	if evt, evtErr := events.NewEvent(events.TaskStuck, ctx.opts.AgentName, "", "", events.TaskStuckData{
		TaskID:              failedTaskID,
		ConsecutiveFailures: failures,
		LastError:           lastErr,
	}); evtErr == nil {
		if emitErr := ctx.opts.EventBus.Emit(evt); emitErr != nil {
			log.Printf("[auto] Failed to emit task_stuck event: %v", emitErr)
		}
	}

	if ctx.stuckTaskIDs == nil {
		ctx.stuckTaskIDs = make(map[string]bool)
	}
	// Cap the skip set to prevent unbounded growth in long-lived loops.
	// Evict the oldest-inserted entry (FIFO) so eviction is deterministic
	// and predictable in tests — Go map iteration order is randomized.
	const maxStuckTasks = 100
	if len(ctx.stuckTaskIDs) >= maxStuckTasks && len(ctx.stuckTaskOrder) > 0 {
		oldest := ctx.stuckTaskOrder[0]
		ctx.stuckTaskOrder = ctx.stuckTaskOrder[1:]
		delete(ctx.stuckTaskIDs, oldest)
	}
	if !ctx.stuckTaskIDs[failedTaskID] {
		ctx.stuckTaskOrder = append(ctx.stuckTaskOrder, failedTaskID)
	}
	ctx.stuckTaskIDs[failedTaskID] = true
	ctx.sameTaskFailures = 0
	ctx.lastFailedTaskID = ""
	ctx.state.ConsecutiveErrors = 0
	ctx.state.ConsecutiveRateLimits = 0

	tryClearTaskID(ctx)
	return true
}

func trackResumeFailures(ctx *autoLoopCtx) {
	if !ctx.resumeAttempted {
		return
	}
	ctx.resumeFailures++
	if ctx.resumeFailures >= 2 {
		fmt.Printf("[auto] Resume failed %d times, falling back to cold-start\n", ctx.resumeFailures)
		ctx.lastClaudeSessionID = ""
		ctx.resumeFailures = 0
	}
}

func emitTaskFailedEvent(ctx *autoLoopCtx, ae *agenterr.AgentError, rawErr error, failedTaskID string) {
	evtData := events.TaskFailedData{
		TaskID:     failedTaskID,
		Error:      rawErr.Error(),
		ErrorClass: ae.Class.String(),
	}
	if ae.RetryAfter > 0 {
		evtData.RetryAfter = ae.RetryAfter.String()
	}
	if evt, evtErr := events.NewEvent(events.TaskFailed, ctx.opts.AgentName, "", "", evtData); evtErr == nil {
		if emitErr := ctx.opts.EventBus.Emit(evt); emitErr != nil {
			log.Printf("[auto] Failed to emit task_failed event: %v", emitErr)
		}
	}
}

// tryClearTaskID clears the lock file's TaskID if the clearer is wired up.
func tryClearTaskID(ctx *autoLoopCtx) {
	if ctx.clearTaskID != nil {
		if clearErr := ctx.clearTaskID(); clearErr != nil {
			fmt.Printf("[auto] Warning: failed to clear stuck task ID: %v\n", clearErr)
		}
	}
}

func exitWithReason(ctx *autoLoopCtx, reason string) bool {
	ctx.state.ShouldExit = true
	ctx.state.ExitReason = reason
	return false
}

func handleRateLimitError(ctx *autoLoopCtx, ae *agenterr.AgentError, shutdown chan struct{}) bool {
	ctx.state.ConsecutiveErrors = 0
	ctx.state.ConsecutiveRateLimits++
	recordRateLimitOnBreaker(ctx)
	// ConsecutiveRateLimits is a process-wide safety net: if five consecutive
	// invocations hit rate limits despite the breaker's cooldowns, the API is
	// sustained-overloaded and we should stop spending retry budget. The
	// breaker's cooldown for the most recent trip is abandoned on exit — this
	// is intentional, the process-exit is the ultimate form of backoff.
	if ctx.state.ConsecutiveRateLimits >= maxConsecutiveRateLimits {
		return exitWithReason(ctx, "too many consecutive rate limits")
	}
	wait := 60 * time.Second
	if ae.RetryAfter > 0 {
		wait = ae.RetryAfter
	}
	fmt.Printf("[auto] Rate limited, waiting %s before retry...\n", wait)
	return sleepOrShutdown(ctx, wait, shutdown)
}

// recordRateLimitOnBreaker drives the sliding-window circuit breaker on each
// rate-limit error. When the breaker trips (Closed→Open or HalfOpen→Open), it
// emits a circuit.opened event and logs the window count / cooldown so the
// operator can see why auto-mode is about to pause.
func recordRateLimitOnBreaker(ctx *autoLoopCtx) {
	if ctx.rateLimitBreaker == nil {
		return
	}
	prevState := ctx.rateLimitBreaker.State()
	newState := ctx.rateLimitBreaker.RecordRateLimit()
	if newState != breakerOpen || prevState == breakerOpen {
		return
	}
	count := ctx.rateLimitBreaker.WindowCount()
	fmt.Printf("[auto] Rate-limit circuit breaker OPEN: %d rate limits in last %s, pausing for %s\n",
		count, ctx.opts.RateLimitWindow, ctx.opts.RateLimitCooldown)
	if evt, evtErr := events.NewEvent(events.CircuitOpened, ctx.opts.AgentName, "", "", events.CircuitOpenedData{
		RateLimitCount:   count,
		WindowDuration:   events.Duration{Duration: ctx.opts.RateLimitWindow},
		CooldownDuration: events.Duration{Duration: ctx.opts.RateLimitCooldown},
	}); evtErr == nil {
		if emitErr := ctx.opts.EventBus.Emit(evt); emitErr != nil {
			log.Printf("[auto] Failed to emit circuit_opened event: %v", emitErr)
		}
	}
}

// maxConsecutiveErrors is the threshold at which consecutive non-rate-limit
// errors cause auto-mode to exit.
const maxConsecutiveErrors = 3

func handleRetryableError(ctx *autoLoopCtx, backoff time.Duration, label string, shutdown chan struct{}) bool {
	ctx.state.ConsecutiveRateLimits = 0
	ctx.state.ConsecutiveErrors++
	if ctx.state.ConsecutiveErrors >= maxConsecutiveErrors {
		return exitWithReason(ctx, "too many consecutive errors")
	}
	fmt.Printf("[auto] %s, retrying in %s...\n", label, backoff)
	return sleepOrShutdown(ctx, backoff, shutdown)
}

func handleTransientError(ctx *autoLoopCtx, shutdown chan struct{}) bool {
	backoff := ctx.opts.BackoffBase << (ctx.state.ConsecutiveErrors)
	return handleRetryableError(ctx, backoff, "Transient error", shutdown)
}

func handleDefaultError(ctx *autoLoopCtx, shutdown chan struct{}) bool {
	return handleRetryableError(ctx, time.Duration(ctx.opts.Interval)*time.Second, fmt.Sprintf("Waiting %ds", ctx.opts.Interval), shutdown)
}

func sleepOrShutdown(ctx *autoLoopCtx, d time.Duration, shutdown chan struct{}) bool {
	if interruptibleSleep(d, shutdown) {
		return exitWithReason(ctx, "shutdown signal received")
	}
	return true
}

func handleAutoTaskSuccess(ctx *autoLoopCtx, beforeRef string, startedAt, endedAt time.Time, shutdown chan struct{}) bool {
	ctx.state.ConsecutiveErrors = 0
	ctx.state.ConsecutiveRateLimits = 0
	recordProbeSuccessOnBreaker(ctx)

	if !agentClaimedTask(ctx.opts.WorktreePath, ctx.opts.AgentName, ctx.opts.LockBridge) {
		return handleNoProgress(ctx, shutdown)
	}

	// If the agent re-claimed a task we've already declared stuck, treat the
	// session as no-progress: clear the lock's TaskID so the next iteration
	// gets a fresh chance, and don't count this as a completed task.
	if isStuckTaskClaimed(ctx) {
		fmt.Printf("[auto] Agent re-claimed stuck task, treating as no-progress\n")
		tryClearTaskID(ctx)
		return handleNoProgress(ctx, shutdown)
	}

	return handleTaskClaimed(ctx, beforeRef, startedAt, endedAt, shutdown)
}

// isStuckTaskClaimed reports whether the task currently recorded in the lock
// file is in the stuckTaskIDs skip set.
func isStuckTaskClaimed(ctx *autoLoopCtx) bool {
	if len(ctx.stuckTaskIDs) == 0 || ctx.readLock == nil {
		return false
	}
	info, err := ctx.readLock()
	if err != nil || info == nil || info.TaskID == "" {
		return false
	}
	return ctx.stuckTaskIDs[info.TaskID]
}

func handleTaskClaimed(ctx *autoLoopCtx, beforeRef string, startedAt, endedAt time.Time, shutdown chan struct{}) bool {
	ctx.state.TasksCompleted++
	ctx.state.ConsecutiveNoProgress = 0
	ctx.state.LastTaskTime = time.Now()
	ctx.lastClaudeSessionID = "" // new task = fresh prompt, no resume
	ctx.resumeFailures = 0
	ctx.lastFailedTaskID = "" // task succeeded, clear per-task failure tracking
	ctx.sameTaskFailures = 0

	taskID := ""
	if info, readErr := ctx.readLock(); readErr == nil && info != nil {
		taskID = info.TaskID
	}
	diffStats := git.ComputeDiffStats(ctx.opts.WorktreePath, beforeRef)
	duration := events.Duration{Duration: endedAt.Sub(startedAt)}
	if evt, evtErr := events.NewEvent(events.TaskCompleted, ctx.opts.AgentName, "", "", events.TaskCompletedData{
		TaskID: taskID, Duration: duration, FilesChanged: diffStats.FilesChanged,
		LinesAdded: diffStats.LinesAdded, LinesRemoved: diffStats.LinesRemoved,
	}); evtErr == nil {
		if emitErr := ctx.opts.EventBus.Emit(evt); emitErr != nil {
			log.Printf("[auto] Failed to emit task_completed event: %v", emitErr)
		}
	}

	fmt.Printf("\n[auto] Task completed. Total: %d\n\n", ctx.state.TasksCompleted)
	if interruptibleSleep(ctx.opts.TaskPause, shutdown) {
		ctx.state.ShouldExit = true
		ctx.state.ExitReason = "shutdown signal received"
		return false
	}
	return true
}

// recordProbeSuccessOnBreaker closes the circuit breaker when a HalfOpen probe
// succeeds. Emits a circuit.closed event so observers (web UI, metrics) see the
// transition. A Closed breaker ignores this call — only a HalfOpen→Closed
// transition is informative.
func recordProbeSuccessOnBreaker(ctx *autoLoopCtx) {
	if ctx.rateLimitBreaker == nil {
		return
	}
	if ctx.rateLimitBreaker.State() != breakerHalfOpen {
		return
	}
	ctx.rateLimitBreaker.RecordSuccess()
	fmt.Println("[auto] Rate-limit circuit breaker CLOSED: probe succeeded, resuming normal operation")
	if evt, evtErr := events.NewEvent(events.CircuitClosed, ctx.opts.AgentName, "", "", events.CircuitClosedData{
		Reason: "probe_success",
	}); evtErr == nil {
		if emitErr := ctx.opts.EventBus.Emit(evt); emitErr != nil {
			log.Printf("[auto] Failed to emit circuit_closed event: %v", emitErr)
		}
	}
}

func handleNoProgress(ctx *autoLoopCtx, shutdown chan struct{}) bool {
	ctx.state.ConsecutiveNoProgress++
	ctx.lastClaudeSessionID = "" // no task claimed, resuming won't help
	ctx.resumeFailures = 0
	fmt.Printf("\n[auto] Agent exited without claiming a task (%d consecutive)\n", ctx.state.ConsecutiveNoProgress)

	if ctx.state.ConsecutiveNoProgress >= 3 {
		ctx.state.ShouldExit = true
		ctx.state.ExitReason = fmt.Sprintf("no tasks claimed in %d consecutive sessions", ctx.state.ConsecutiveNoProgress)
		return false
	}

	backoff := ctx.opts.BackoffBase << (ctx.state.ConsecutiveNoProgress - 1)
	fmt.Printf("[auto] Backing off for %s before retry...\n\n", backoff)
	if interruptibleSleep(backoff, shutdown) {
		ctx.state.ShouldExit = true
		ctx.state.ExitReason = "shutdown signal received"
		return false
	}
	return true
}
