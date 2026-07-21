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
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/serveadapter"
	driverexecutor "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/trigger"
	"github.com/tysonthomas9/loomcli/internal/webui"
)

func validateExecutionRuntimePassCapabilities(
	st store.Store,
	executionCapability webui.ExecutionCapability,
	artifactsCapability webui.ArtifactsCapability,
) error {
	if st == nil {
		return fmt.Errorf("compose Execution compatibility passes: store is required")
	}
	if executionCapability == nil || executionCapability.DriverRunAPI() == nil ||
		executionCapability.DriverRunAuthorityResolver() == nil || executionCapability.SystemAuthorityResolver() == nil {
		return fmt.Errorf("compose Execution compatibility passes: DriverRun API and authority resolvers are required")
	}
	if artifactsCapability == nil || artifactsCapability.ArtifactsAPI() == nil {
		return fmt.Errorf("compose Execution compatibility passes: Artifacts API is required")
	}
	return nil
}

// buildExecutionRuntimePasses is the bounded Phase-4 compatibility adapter
// for legacy driver helpers that still require the composite Store. It stays
// in an existing CLI composition seam rather than widening Execution's owner
// APIs or creating another composite Store consumer.
func buildExecutionRuntimePasses(
	st store.Store,
	runOutcomes driverexecutor.RunOutcomePublisher,
	executionCapability webui.ExecutionCapability,
	artifactsCapability webui.ArtifactsCapability,
) (serveadapter.ExecutionRuntimePasses, error) {
	if err := validateExecutionRuntimePassCapabilities(st, executionCapability, artifactsCapability); err != nil {
		return serveadapter.ExecutionRuntimePasses{}, err
	}
	awaitTimeoutSweeper := &driverexecutor.AwaitTimeoutSweeper{
		Store: st, WorkspaceKey: driverAutomationWorkspaceScope(),
		Resolver:   serveadapter.NewAwaitTimeoutExecutionResolver(executionCapability),
		BatchLimit: boundedIntEnv(envLoomAwaitSweepBatch, driverexecutor.DefaultAwaitTimeoutSweepBatch, 500),
	}
	passes := serveadapter.ExecutionRuntimePasses{
		AwaitTimeouts: serveadapter.BuildAwaitTimeoutRuntimePass(awaitTimeoutSweeper.RunOnce),
	}
	if !driverExecutorEnabled() {
		return passes, nil
	}
	workDir, err := os.Getwd()
	if err != nil {
		return serveadapter.ExecutionRuntimePasses{}, fmt.Errorf("compose Execution driver executor: resolve work dir: %w", err)
	}
	// Executor and TaskWorker slots register the same process node. Resolve the
	// configured concurrency once and pass that one capacity to every registrar
	// so a later DriverRun claim cannot downgrade the shared node to capacity 1.
	nodeCapacity := driverTaskWorkerConcurrency()
	executor, ok := buildDriverExecutor(st, workDir, runOutcomes, executionCapability, nodeCapacity)
	if !ok {
		return serveadapter.ExecutionRuntimePasses{}, fmt.Errorf("compose Execution driver executor: sandbox configuration rejected")
	}
	taskWorkerTemplate := driverexecutor.TaskWorker{
		Store: st, WorkspaceKey: executor.WorkspaceKey, WorkDir: workDir,
		Artifacts: artifactsCapability.ArtifactsAPI(),
		NodeID:    executor.NodeID, NodeCapacity: nodeCapacity, RunnerID: os.Getenv("LOOM_DRIVER_TASK_WORKER_RUNNER_ID"),
		MaxAttempts: driverTaskRunMaxAttempts(), APIBaseURL: driverAPIBaseURL(),
		LocalSettingsDir: bootstrap.LoomDir(), Execution: executionCapability.TaskRunWorkerAPI(),
		TaskRunAuthorities: executionCapability.TaskRunAuthorityResolver(),
		Convergence:        executionCapability.TaskRunConvergenceAPI(), ExecutionAuthorities: executionCapability.SystemAuthorityResolver(),
	}
	passes.DriverExecutor, passes.TaskWorkers = serveadapter.BuildSharedNodeExecutionRuntimePasses(executor, taskWorkerTemplate, nodeCapacity)
	return passes, nil
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
func startIssueJournalBridge(
	ctx context.Context,
	st store.Store,
	issueLookup trigger.TaskReadyIssueLookup,
	readySnapshots trigger.TaskReadySnapshotLister,
	repositoryRequiredBlocker trigger.TaskReadyRepositoryRequiredBlocker,
	source trigger.InternalEventEmitter,
) {
	bridge := buildIssueJournalBridge(st, issueLookup, readySnapshots, repositoryRequiredBlocker, source)
	if bridge == nil {
		return
	}
	interval := issueBridgeInterval()
	slog.Info("Issue journal bridge enabled", "workspace", bridge.WorkspaceKey, "interval", interval,
		"state_path", issueBridgeStatePath(), "task_ready_events", bridge.EmitTaskReady)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			if result, err := bridge.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				slog.Error("issue journal bridge pass failed", "err", err)
			} else if result != nil && (result.Emitted > 0 || result.TaskReadyEmitted > 0 || result.TaskReadyBlocked > 0 || result.FastForwarded > 0) {
				slog.Info("issue journal bridge pass", "emitted", result.Emitted, "task_ready_emitted", result.TaskReadyEmitted,
					"task_ready_blocked", result.TaskReadyBlocked, "skipped", result.Skipped,
					"fast_forwarded", result.FastForwarded, "backed_off", result.BackedOff)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func buildIssueJournalBridge(
	st store.Store,
	issueLookup trigger.TaskReadyIssueLookup,
	readySnapshots trigger.TaskReadySnapshotLister,
	repositoryRequiredBlocker trigger.TaskReadyRepositoryRequiredBlocker,
	source trigger.InternalEventEmitter,
) *trigger.IssueJournalBridge {
	if st == nil || source == nil {
		if st != nil {
			slog.Info("issue journal bridge disabled: Automation system eventing is unavailable")
		}
		return nil
	}
	if issueBridgeDisabled() {
		slog.Info("issue journal bridge disabled: LOOM_ISSUE_BRIDGE_DISABLED set")
		return nil
	}
	reader, ok := st.TriggerEvents().(store.IssueJournalReader)
	if !ok {
		slog.Info("issue journal bridge disabled: store has no journal reader")
		return nil
	}
	cursors, err := trigger.NewFileIssueJournalCursorStore(issueBridgeStatePath(), slog.Default())
	if err != nil {
		slog.Error("issue journal bridge disabled: cannot load cursor state", "err", err)
		return nil
	}
	return &trigger.IssueJournalBridge{
		Store:  st,
		Source: source,
		Reader: reader,
		// Resolve via the SHARED driver-automation scope, like the cron scheduler
		// and the run executor. The bridge feeds task.ready events that fire prompt-
		// agent bindings whose runs the executor must then claim; if the bridge
		// ingested in a workspace the executor won't run in, those runs queue
		// forever (the SANDBOX-vs-LOCALMODE bug). Keeps the env-override pattern:
		// LOOM_DRIVER_EXECUTOR_WORKSPACE overrides, "*" unscopes to all workspaces.
		WorkspaceKey:              driverAutomationWorkspaceScope(),
		Cursors:                   cursors,
		EmitTaskReady:             taskReadyEventsEnabled(),
		IssueLookup:               issueLookup,
		ReadySnapshots:            readySnapshots,
		RepositoryRequiredBlocker: repositoryRequiredBlocker,
	}
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

// taskReadyEventsEnabled reports whether the issue-journal bridge emits the
// task.ready lane. It defaults on because task-ready bindings are the normal
// autonomous-agent launch path; LOOM_TASK_READY_EVENTS remains an explicit
// rollback switch for deployments that need to disable it.
func taskReadyEventsEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(envLoomTaskReadyEvents))) {
	case "0", "false", "off", "no":
		return false
	default:
		return true
	}
}

// taskReadyRepositoryRequired reports whether a runnable issue without an
// explicit source repo lacks an unambiguous checkout. Exactly one workspace
// repo is safe because the local resolver has a deliberate single-repo
// fallback. Zero repos and multiple repos both require operator action. Epics
// remain workspace-scoped and an explicit source repo is always unambiguous.
func taskReadyRepositoryRequired(issueType, sourceRepo string, repoCount int) bool {
	return !strings.EqualFold(strings.TrimSpace(issueType), "epic") &&
		strings.TrimSpace(sourceRepo) == "" && repoCount != 1
}

// blockRepositoryRequiredTask invokes the Work Items owner's atomic
// repository-requirement command. It deliberately has no generic Update
// fallback: without commit-time revalidation, a stale ready snapshot could
// overwrite a concurrent claim or repository assignment.
func blockRepositoryRequiredTask(
	ctx context.Context,
	issueBackend backend.IssueBackend,
	issueID string,
) (trigger.TaskReadyRepositoryRequiredResult, error) {
	repositoryBackend, ok := issueBackend.(backend.RepositoryRequirementBackend)
	if !ok {
		return trigger.TaskReadyRepositoryRequiredResult{}, backend.ErrNotImplemented(
			"BlockRepositoryRequired",
			"issue backend does not support atomic repository-required admission",
		)
	}
	result, err := repositoryBackend.BlockRepositoryRequired(ctx, issueID)
	if err != nil {
		if backend.IsKind(err, backend.KindNotFound) {
			return trigger.TaskReadyRepositoryRequiredResult{}, nil
		}
		return trigger.TaskReadyRepositoryRequiredResult{}, fmt.Errorf("move repository-required task to blocked: %w", err)
	}
	if result == nil {
		return trigger.TaskReadyRepositoryRequiredResult{}, backend.ErrInternal(
			"BlockRepositoryRequired",
			"atomic repository-required admission returned no result",
			nil,
		)
	}

	// Applied and replayed repository blocks are terminal for this admission
	// generation. Changed retains the previous sweep-counter semantics: only a
	// newly moved card increments TaskReadyBlocked.
	if result.Changed || result.Replayed || result.Blocked {
		return trigger.TaskReadyRepositoryRequiredResult{Blocked: result.Changed}, nil
	}
	if !result.DispatchReady {
		// A stale request observed a claimed, closed, ordinary-blocked, epic, or
		// otherwise non-ready issue. Suppress it without trusting the old payload.
		return trigger.TaskReadyRepositoryRequiredResult{}, nil
	}
	canonical, err := repositoryRequiredDispatchSnapshot(result)
	if err != nil {
		return trigger.TaskReadyRepositoryRequiredResult{}, err
	}
	if canonical == nil {
		return trigger.TaskReadyRepositoryRequiredResult{}, nil
	}
	return trigger.TaskReadyRepositoryRequiredResult{DispatchReady: canonical}, nil
}

func repositoryRequiredDispatchSnapshot(result *backend.RepositoryRequirementResult) (*trigger.TaskReadySnapshot, error) {
	if result.Issue == nil || strings.TrimSpace(result.Issue.ID) == "" {
		return nil, backend.ErrInternal(
			"BlockRepositoryRequired",
			"dispatch-ready result is missing the canonical issue",
			nil,
		)
	}
	canonical := trigger.TaskReadySnapshot{
		TaskID:     result.Issue.ID,
		Status:     result.Issue.Status,
		HasDesign:  result.Issue.HasDesign,
		Labels:     append([]string(nil), result.Issue.Labels...),
		IssueType:  result.Issue.IssueType,
		SourceRepo: result.Issue.SourceRepo,
		// DispatchReady is the Work Items owner's commit-time proof that the
		// repository policy is satisfied, including the single-repo fallback.
		RepositoryRequired: false,
		UpdatedAt:          result.Issue.UpdatedAt,
	}
	if !strings.EqualFold(strings.TrimSpace(canonical.Status), "open") ||
		strings.EqualFold(strings.TrimSpace(canonical.IssueType), "epic") {
		return nil, nil
	}
	return &canonical, nil
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
