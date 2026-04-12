package automode

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"time"

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

func handleAutoTaskError(ctx *autoLoopCtx, err error, shutdown chan struct{}) bool {
	fmt.Printf("[auto] Agent exited with error: %v\n", err)
	ctx.state.ConsecutiveErrors++

	// Track resume failures: if we attempted a resume (session ID was set) and
	// it failed, increment the counter. After 2 consecutive resume failures on
	// the same session ID, clear it so the next attempt is a cold-start.
	if ctx.lastClaudeSessionID != "" {
		ctx.resumeFailures++
		if ctx.resumeFailures >= 2 {
			fmt.Printf("[auto] Resume failed %d times, falling back to cold-start\n", ctx.resumeFailures)
			ctx.lastClaudeSessionID = ""
			ctx.resumeFailures = 0
		}
	}

	failedTaskID := ""
	if info, readErr := ctx.readLock(); readErr == nil && info != nil {
		failedTaskID = info.TaskID
	}
	if evt, evtErr := events.NewEvent(events.TaskFailed, ctx.opts.AgentName, "", "", events.TaskFailedData{TaskID: failedTaskID, Error: err.Error()}); evtErr == nil {
		if emitErr := ctx.opts.EventBus.Emit(evt); emitErr != nil {
			log.Printf("[auto] Failed to emit task_failed event: %v", emitErr)
		}
	}

	if ctx.state.ConsecutiveErrors >= 3 {
		ctx.state.ShouldExit = true
		ctx.state.ExitReason = "too many consecutive errors"
		return false
	}

	fmt.Printf("[auto] Waiting %ds before retry...\n", ctx.opts.Interval)
	if interruptibleSleep(time.Duration(ctx.opts.Interval)*time.Second, shutdown) {
		ctx.state.ShouldExit = true
		ctx.state.ExitReason = "shutdown signal received"
		return false
	}
	return true
}

func handleAutoTaskSuccess(ctx *autoLoopCtx, beforeRef string, startedAt, endedAt time.Time, shutdown chan struct{}) bool {
	ctx.state.ConsecutiveErrors = 0

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
	if cap := 4 * ctx.opts.BackoffBase; backoff > cap {
		backoff = cap
	}
	fmt.Printf("[auto] Backing off for %s before retry...\n\n", backoff)
	if interruptibleSleep(backoff, shutdown) {
		ctx.state.ShouldExit = true
		ctx.state.ExitReason = "shutdown signal received"
		return false
	}
	return true
}
