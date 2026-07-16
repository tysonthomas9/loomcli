package serve

// Always-on serve-side background loops: the stale-task, outbox, cron,
// delivery-retry and await-timeout sweepers, plus the A4 issue-journal bridge.
// Extracted from serve.go to keep that file under the 1000-LOC convention; the
// startup wiring in runServe calls each start* once. Every loop is RunOnce per
// tick, context-cancel exit, results logged via slog — the established sweeper
// shape.

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	driverexecutor "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/trigger"
)

// startStaleTaskSweeper launches the always-on server-side stale TaskRun
// sweeper. Unlike the driver executor it is NOT gated behind
// LOOM_DRIVER_EXECUTOR: stale-task fault recovery is server policy, so it
// runs whenever serve has a store. Workflows must not call recoverStale
// themselves.
func startStaleTaskSweeper(ctx context.Context, st store.Store) {
	if st == nil {
		return
	}
	sweeper := &driverexecutor.StaleTaskSweeper{
		Store:        st,
		WorkspaceKey: driverAutomationWorkspaceScope(),
		MaxAge:       driverStaleTaskMaxAge(),
	}
	slog.Info("Stale task sweeper enabled", "workspace", sweeper.WorkspaceKey, "max_age", sweeper.MaxAge)
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			if result, err := sweeper.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				slog.Error("stale task sweeper failed", "err", err)
			} else if result != nil && result.Recovered > 0 {
				slog.Info("stale task sweeper recovered stale task runs", "count", result.Recovered, "task_run_ids", result.RecoveredTaskRunIDs)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// startOutboxDispatcher launches the always-on server-side outbox delivery
// loop. Like the stale task sweeper it is NOT gated behind
// LOOM_DRIVER_EXECUTOR: lead-notification delivery with retry/backoff is
// server policy, so it runs whenever serve has a store. It replaces the
// workflow-side lead-delivery retry loops.
func startOutboxDispatcher(ctx context.Context, st store.Store) {
	if st == nil {
		return
	}
	dispatcher := &driverexecutor.OutboxDispatcher{
		Store:        st,
		WorkspaceKey: driverAutomationWorkspaceScope(),
	}
	slog.Info("Outbox dispatcher enabled", "workspace", dispatcher.WorkspaceKey)
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			if delivered, err := dispatcher.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				slog.Error("outbox dispatcher failed", "err", err)
			} else if delivered > 0 {
				slog.Info("outbox dispatcher delivered notifications", "count", delivered)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// startAwaitTimeoutSweeper launches the always-on await deadline sweeper
// (RULE 5 enforcement, AW8): past-deadline await instances are resolved with
// a synthetic timeout event and their runs re-queued onto the workflow's
// timeout arm — the sweeper never terminalizes a run itself. Like the
// delivery sweeper it is NOT gated behind LOOM_DRIVER_EXECUTOR — await
// expiry is server policy. LOOM_AWAIT_SWEEP_INTERVAL tunes the interval in
// seconds (default 30, capped at one hour); LOOM_AWAIT_SWEEP_BATCH the
// per-workspace ListDueAwaitDeadlines page (default 50, capped at 500).
func startAwaitTimeoutSweeper(ctx context.Context, st store.Store) {
	if st == nil {
		return
	}
	const (
		envLoomAwaitSweepInterval = "LOOM_AWAIT_SWEEP_INTERVAL"
		envLoomAwaitSweepBatch    = "LOOM_AWAIT_SWEEP_BATCH"
	)
	sweeper := &driverexecutor.AwaitTimeoutSweeper{
		Store:        st,
		WorkspaceKey: driverAutomationWorkspaceScope(),
		BatchLimit:   boundedIntEnv(envLoomAwaitSweepBatch, driverexecutor.DefaultAwaitTimeoutSweepBatch, 500),
	}
	interval := time.Duration(boundedIntEnv(envLoomAwaitSweepInterval, 30, 3600)) * time.Second
	slog.Info("Await timeout sweeper enabled", "workspace", sweeper.WorkspaceKey, "interval", interval, "batch", sweeper.BatchLimit)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			if result, err := sweeper.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				slog.Error("await timeout sweeper failed", "err", err)
			} else if result != nil && result.TimedOut+result.ResumeDeferred > 0 {
				slog.Info("await timeout sweeper resolved due awaits",
					"timed_out", result.TimedOut, "resume_deferred", result.ResumeDeferred,
					"instance_keys", result.TimedOutInstanceKeys)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// startIssueJournalBridge launches the A4 always-on issue-journal bridge: it
// polls fleet-db's issue mutation journal and re-enters each allowed entry into
// the trigger router as a system-origin internal event (IssueJournalBridge).
// Following the sweeper precedents it is RunOnce per tick, context-cancel exit,
// results logged via slog, and like the other always-on loops it is NOT gated
// behind LOOM_DRIVER_EXECUTOR — journal ingestion is server policy.
//
// CAPABILITY GATE. The bridge needs store.IssueJournalReader, which only the
// fleet-db client implements; a memstore-backed serve (no journal reader) logs
// once and starts no goroutine, so a no-store / local serve is a clean no-op.
//
// CURSOR PERSISTENCE. A file-backed per-workspace cursor (default
// <LoomDir>/issue-bridge-cursor.json, override LOOM_ISSUE_BRIDGE_STATE_PATH)
// survives restart. Because the bridge derives deterministic loopback event ids
// a lost or unreadable file only costs a dedup-absorbed rescan, never duplicate
// triage; a corrupt file aborts only bridge startup (logged), not serve.
//
// ENV KNOBS. LOOM_ISSUE_BRIDGE_DISABLED=1 skips the loop entirely;
// LOOM_ISSUE_BRIDGE_INTERVAL sets the poll cadence in seconds (default 2,
// matching the sweepers, capped at one hour); LOOM_ISSUE_BRIDGE_STATE_PATH
// overrides the cursor file; LOOM_ISSUE_BRIDGE_REPLAY=1 opts into
// replay-from-zero on first observation (handled inside the bridge).
func startIssueJournalBridge(ctx context.Context, st store.Store, issueLookup func(ctx context.Context, workspace, issueID string) (string, []string, string, error), source trigger.InternalEventEmitter) {
	if st == nil || source == nil {
		if st != nil {
			slog.Info("issue journal bridge disabled: Automation system eventing is unavailable")
		}
		return
	}
	if issueBridgeDisabled() {
		slog.Info("issue journal bridge disabled: LOOM_ISSUE_BRIDGE_DISABLED set")
		return
	}
	reader, ok := st.TriggerEvents().(store.IssueJournalReader)
	if !ok {
		slog.Info("issue journal bridge disabled: store has no journal reader")
		return
	}
	cursors, err := trigger.NewFileIssueJournalCursorStore(issueBridgeStatePath(), slog.Default())
	if err != nil {
		slog.Error("issue journal bridge disabled: cannot load cursor state", "err", err)
		return
	}
	bridge := &trigger.IssueJournalBridge{
		Store:  st,
		Source: source,
		Reader: reader,
		// Resolve via the SHARED driver-automation scope, like the cron scheduler
		// and the run executor. The bridge feeds task.ready events that fire prompt-
		// agent bindings whose runs the executor must then claim; if the bridge
		// ingested in a workspace the executor won't run in, those runs queue
		// forever (the SANDBOX-vs-LOCALMODE bug). Keeps the env-override pattern:
		// LOOM_DRIVER_EXECUTOR_WORKSPACE overrides, "*" unscopes to all workspaces.
		WorkspaceKey:  driverAutomationWorkspaceScope(),
		Cursors:       cursors,
		EmitTaskReady: taskReadyEventsEnabled(),
		IssueLookup:   issueLookup,
	}
	interval := issueBridgeInterval()
	slog.Info("Issue journal bridge enabled", "workspace", bridge.WorkspaceKey, "interval", interval, "state_path", issueBridgeStatePath())
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			if result, err := bridge.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				slog.Error("issue journal bridge pass failed", "err", err)
			} else if result != nil && (result.Emitted > 0 || result.FastForwarded > 0) {
				slog.Info("issue journal bridge pass", "emitted", result.Emitted, "skipped", result.Skipped, "fast_forwarded", result.FastForwarded, "backed_off", result.BackedOff)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// driverStaleTaskMaxAge reads the stale TaskRun heartbeat threshold in
// seconds from LOOM_DRIVER_STALE_TASK_MAX_AGE (default 1200s, capped at one
// day). The default is sourced from driver.StaleTaskSweeper so serve cannot
// silently override the recovery policy with a shorter threshold.
func driverStaleTaskMaxAge() time.Duration {
	defaultSeconds := int(driverexecutor.DefaultStaleTaskRunMaxAge / time.Second)
	return time.Duration(boundedIntEnv(envLoomDriverStaleTaskMaxAge, defaultSeconds, 86400)) * time.Second
}

// issueBridgeInterval reads the issue-journal bridge poll cadence in seconds
// from LOOM_ISSUE_BRIDGE_INTERVAL (default 2s — the sweeper cadence — capped at
// one hour).
func issueBridgeInterval() time.Duration {
	return time.Duration(boundedIntEnv(envLoomIssueBridgeInterval, 2, 3600)) * time.Second
}

// issueBridgeDisabled reports whether LOOM_ISSUE_BRIDGE_DISABLED opts the bridge
// out (1/true/yes/on).
func issueBridgeDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(envLoomIssueBridgeDisabled))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// taskReadyEventsEnabled reports whether LOOM_TASK_READY_EVENTS opts the
// issue-journal bridge into the flag-gated task.ready lane (1/true/yes/on).
// Default off so the bridge's default behavior is unchanged.
func taskReadyEventsEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(envLoomTaskReadyEvents))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// issueBridgeStatePath resolves the cursor state file: LOOM_ISSUE_BRIDGE_STATE_PATH
// when set, otherwise <LoomDir>/issue-bridge-cursor.json. Falls back to the cwd
// when LoomDir cannot be resolved so the bridge still persists somewhere rather
// than aborting.
func issueBridgeStatePath() string {
	if p := strings.TrimSpace(os.Getenv(envLoomIssueBridgeStatePath)); p != "" {
		return p
	}
	dir := bootstrap.LoomDir()
	if dir == "" {
		dir = "."
	}
	return filepath.Join(dir, issueBridgeCursorFileName)
}

// issueBridgeCursorFileName is the default cursor state file name under the
// serve state dir (LoomDir).
const issueBridgeCursorFileName = "issue-bridge-cursor.json"
