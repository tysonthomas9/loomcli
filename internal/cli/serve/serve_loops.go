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
		WorkspaceKey: os.Getenv(bootstrap.EnvWorkspace),
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
		WorkspaceKey: os.Getenv(bootstrap.EnvWorkspace),
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

// startTriggerCronScheduler launches the always-on built-in cron event
// source: a sweep loop that fires cron.tick TriggerEvents for enabled
// source_kind=cron bindings into the normal trigger-route dispatch path.
// Like the outbox dispatcher it is NOT gated behind LOOM_DRIVER_EXECUTOR —
// schedule evaluation is server policy. Tick-level idempotency keys make
// overlapping schedulers safe. LOOM_TRIGGER_CRON_INTERVAL tunes the sweep
// interval in seconds (default 30s).
func startTriggerCronScheduler(ctx context.Context, st store.Store) {
	if st == nil {
		return
	}
	scheduler := &trigger.CronScheduler{
		Store:        st,
		WorkspaceKey: os.Getenv(bootstrap.EnvWorkspace),
	}
	interval := triggerCronInterval()
	slog.Info("Trigger cron scheduler enabled", "workspace", scheduler.WorkspaceKey, "interval", interval)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			if result, err := scheduler.RunOnce(ctx, time.Now()); err != nil && !errors.Is(err, context.Canceled) {
				slog.Error("trigger cron scheduler sweep failed", "err", err)
			} else if result != nil && result.Fired > 0 {
				slog.Info("trigger cron scheduler fired ticks", "count", result.Fired)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// startTriggerDeliverySweeper launches the always-on delivery retry sweeper:
// it re-drives retryable failed trigger deliveries and promotes held
// (queue-policy) ones with exponential backoff and bounded attempts. Like the
// outbox dispatcher it is NOT gated behind LOOM_DRIVER_EXECUTOR — delivery
// retry is server policy. Single serve instance today, so no distributed
// lock; for multi-replica later use fleet-db's SetNX sweep-lock recipe (lock
// key with TTL just above the interval; per-leg idempotency already makes
// overlapping sweeps safe). LOOM_TRIGGER_SWEEP_INTERVAL tunes the sweep
// interval in seconds (default 15s, capped at one hour) and
// LOOM_TRIGGER_SWEEP_BATCH the per-workspace ListDue batch (default 50,
// capped at 500).
func startTriggerDeliverySweeper(ctx context.Context, st store.Store) {
	if st == nil {
		return
	}
	const (
		envLoomTriggerSweepInterval = "LOOM_TRIGGER_SWEEP_INTERVAL"
		envLoomTriggerSweepBatch    = "LOOM_TRIGGER_SWEEP_BATCH"
	)
	sweeper := &trigger.DeliverySweeper{
		Store:        st,
		WorkspaceKey: os.Getenv(bootstrap.EnvWorkspace),
		BatchLimit:   boundedIntEnv(envLoomTriggerSweepBatch, trigger.DefaultDeliverySweepBatch, 500),
	}
	interval := time.Duration(boundedIntEnv(envLoomTriggerSweepInterval, 15, 3600)) * time.Second
	slog.Info("Trigger delivery sweeper enabled", "workspace", sweeper.WorkspaceKey, "interval", interval, "batch", sweeper.BatchLimit)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			if result, err := sweeper.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				slog.Error("trigger delivery sweeper failed", "err", err)
			} else if result != nil && result.Dispatched+result.Rescheduled+result.Exhausted > 0 {
				slog.Info("trigger delivery sweeper pass", "dispatched", result.Dispatched, "rescheduled", result.Rescheduled, "exhausted", result.Exhausted)
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
		WorkspaceKey: os.Getenv(bootstrap.EnvWorkspace),
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
func startIssueJournalBridge(ctx context.Context, st store.Store) {
	if st == nil {
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
		Store:        st,
		Source:       &trigger.InternalSource{Store: st},
		Reader:       reader,
		WorkspaceKey: os.Getenv(bootstrap.EnvWorkspace),
		Cursors:      cursors,
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

// triggerCronInterval reads the cron sweep interval in seconds from
// LOOM_TRIGGER_CRON_INTERVAL (default 30s, capped at one hour).
func triggerCronInterval() time.Duration {
	return time.Duration(boundedIntEnv(envLoomTriggerCronInterval, 30, 3600)) * time.Second
}

// driverStaleTaskMaxAge reads the stale TaskRun heartbeat threshold in
// seconds from LOOM_DRIVER_STALE_TASK_MAX_AGE (default 300s, capped at one
// day).
func driverStaleTaskMaxAge() time.Duration {
	return time.Duration(boundedIntEnv(envLoomDriverStaleTaskMaxAge, 300, 86400)) * time.Second
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
