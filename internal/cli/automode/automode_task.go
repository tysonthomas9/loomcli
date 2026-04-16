package automode

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/cli/git"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

func createAutoSession(ctx *autoLoopCtx, prompt string) *sessions.Session {
	if ctx.sessStore == nil {
		return nil
	}
	sess, _ := ctx.sessStore.CreateSession(sessions.CreateOptions{
		AgentName: ctx.opts.AgentName, Backend: cli.ResolveBackendName(),
		EpicID: ctx.opts.ParentID, Prompt: prompt, AttemptNum: ctx.state.TasksCompleted + 1,
	})
	if sess != nil {
		backends.SetActiveSessionEnv(cli.GetBeadsDir(), sess.SessionID())
		go sessions.NotifyWebUI(context.Background(), backends.ResolveWebUIURL(), "", sess.SessionID(), sessions.StatusRunning, backends.ResolveNotifyToken())
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
	diffStats := git.ComputeDiffStats(ctx.opts.WorktreePath, beforeRef)
	_ = sess.Finalize(sessions.FinalizeOptions{
		TaskID: taskID, ExitCode: exitCode, FilesTouched: diffStats.FilesTouched,
		DiffStats: sessions.DiffStats{
			FilesChanged: diffStats.FilesChanged, LinesAdded: diffStats.LinesAdded, LinesRemoved: diffStats.LinesRemoved,
		},
		InputTokens:      inputTokens,
		OutputTokens:     outputTokens,
		CacheReadTokens:  cacheReadTokens,
		CacheWriteTokens: cacheWriteTokens,
		EstimatedCostUSD: estimatedCostUSD,
	})
	backends.ClearActiveSessionEnv()
	go sessions.NotifyWebUI(context.Background(), backends.ResolveWebUIURL(), taskID, sess.SessionID(), sess.Meta.Status, backends.ResolveNotifyToken())
}

func handleAutoTaskError(ctx *autoLoopCtx, ae *agenterr.AgentError, rawErr error, shutdown chan struct{}) bool {
	fmt.Printf("[auto] Agent error: [%s] %s\n", ae.Class, ae.Message)
	trackResumeFailures(ctx)
	emitTaskFailedEvent(ctx, ae, rawErr)

	// Fatal errors (auth, billing) and non-retryable specifics: exit immediately.
	if ae.IsFatal() || ae.Class == agenterr.ModelNotFound {
		return exitWithReason(ctx, fmt.Sprintf("fatal error: %s", ae.Message))
	}
	if ae.Class == agenterr.NoWork {
		return exitWithReason(ctx, "no work available")
	}

	// Rate limit: separate counter, generous retry.
	if ae.Class == agenterr.RateLimited {
		return handleRateLimitError(ctx, ae, shutdown)
	}

	// Retryable transient errors: exponential backoff.
	if ae.IsRetryable() {
		return handleTransientError(ctx, shutdown)
	}

	// Default: Unknown, ContextOverflow — use fixed interval, 3-error exit.
	return handleDefaultError(ctx, shutdown)
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

func emitTaskFailedEvent(ctx *autoLoopCtx, ae *agenterr.AgentError, rawErr error) {
	failedTaskID := ""
	if info, readErr := ctx.readLock(); readErr == nil && info != nil {
		failedTaskID = info.TaskID
	}
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

func exitWithReason(ctx *autoLoopCtx, reason string) bool {
	ctx.state.ShouldExit = true
	ctx.state.ExitReason = reason
	return false
}

func handleRateLimitError(ctx *autoLoopCtx, ae *agenterr.AgentError, shutdown chan struct{}) bool {
	ctx.state.ConsecutiveErrors = 0
	ctx.state.ConsecutiveRateLimits++
	if ctx.state.ConsecutiveRateLimits >= 5 {
		return exitWithReason(ctx, "too many consecutive rate limits")
	}
	wait := 60 * time.Second
	if ae.RetryAfter > 0 {
		wait = ae.RetryAfter
	}
	fmt.Printf("[auto] Rate limited, waiting %s before retry...\n", wait)
	return sleepOrShutdown(ctx, wait, shutdown)
}

func handleTransientError(ctx *autoLoopCtx, shutdown chan struct{}) bool {
	ctx.state.ConsecutiveRateLimits = 0
	ctx.state.ConsecutiveErrors++
	if ctx.state.ConsecutiveErrors >= 3 {
		return exitWithReason(ctx, "too many consecutive errors")
	}
	backoff := ctx.opts.BackoffBase << (ctx.state.ConsecutiveErrors - 1)
	fmt.Printf("[auto] Transient error, backing off %s before retry...\n", backoff)
	return sleepOrShutdown(ctx, backoff, shutdown)
}

func handleDefaultError(ctx *autoLoopCtx, shutdown chan struct{}) bool {
	ctx.state.ConsecutiveRateLimits = 0
	ctx.state.ConsecutiveErrors++
	if ctx.state.ConsecutiveErrors >= 3 {
		return exitWithReason(ctx, "too many consecutive errors")
	}
	fmt.Printf("[auto] Waiting %ds before retry...\n", ctx.opts.Interval)
	return sleepOrShutdown(ctx, time.Duration(ctx.opts.Interval)*time.Second, shutdown)
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

	if agentClaimedTask(ctx.opts.WorktreePath, ctx.opts.AgentName, ctx.opts.LockBridge) {
		return handleTaskClaimed(ctx, beforeRef, startedAt, endedAt, shutdown)
	}
	return handleNoProgress(ctx, shutdown)
}

func handleTaskClaimed(ctx *autoLoopCtx, beforeRef string, startedAt, endedAt time.Time, shutdown chan struct{}) bool {
	ctx.state.TasksCompleted++
	ctx.state.ConsecutiveNoProgress = 0
	ctx.state.LastTaskTime = time.Now()
	ctx.lastClaudeSessionID = "" // new task = fresh prompt, no resume
	ctx.resumeFailures = 0

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
