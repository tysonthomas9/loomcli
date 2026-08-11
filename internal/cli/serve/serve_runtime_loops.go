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
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/serveadapter"
	driverexecutor "github.com/tysonthomas9/loomcli/internal/driver"
	trigger "github.com/tysonthomas9/loomcli/internal/infra/automationruntime"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/store"
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
		executionCapability.DriverRunAuthorityResolver() == nil || executionCapability.SystemAuthorityResolver() == nil ||
		executionCapability.TaskRunRecoveryAPI() == nil {
		return fmt.Errorf("compose Execution compatibility passes: DriverRun, stale TaskRun recovery, and authority APIs are required")
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
	sourceControl sourcecontrol.Materializer,
	stackBindings sourcecontrol.StackBindingResolver,
	taskOutcomes sourcecontrol.TaskOutcomeRecorder,
	config serveRuntimeConfig,
) (serveadapter.ExecutionRuntimePasses, error) {
	if err := validateExecutionRuntimePassCapabilities(st, executionCapability, artifactsCapability); err != nil {
		return serveadapter.ExecutionRuntimePasses{}, err
	}
	awaitTimeoutSweeper := &trigger.AwaitTimeoutSweeper{
		Store: st, WorkspaceKey: config.WorkspaceScope,
		Resolver:   serveadapter.NewAwaitTimeoutExecutionResolver(executionCapability),
		BatchLimit: config.AwaitSweepBatch,
	}
	passes := serveadapter.ExecutionRuntimePasses{
		AwaitTimeouts: serveadapter.BuildAwaitTimeoutRuntimePass(func(ctx context.Context) (serveadapter.AwaitTimeoutRuntimeResult, error) {
			return awaitTimeoutSweeper.RunOnce(ctx)
		}),
	}
	if !config.DriverExecutorEnabled {
		return passes, nil
	}
	if sourceControl == nil || stackBindings == nil || taskOutcomes == nil {
		return serveadapter.ExecutionRuntimePasses{}, fmt.Errorf(
			"compose Execution task workers: Source Control materializer, stack bindings, and task outcomes are required",
		)
	}
	workDir, err := os.Getwd()
	if err != nil {
		return serveadapter.ExecutionRuntimePasses{}, fmt.Errorf("compose Execution driver executor: resolve work dir: %w", err)
	}
	// Executor and TaskWorker slots register the same process node. Resolve the
	// configured concurrency once and pass that one capacity to every registrar
	// so a later DriverRun claim cannot downgrade the shared node to capacity 1.
	nodeCapacity := config.TaskWorkerConcurrency
	executor, ok := config.BuildDriverExecutor(st, workDir, runOutcomes, executionCapability, nodeCapacity)
	if !ok {
		return serveadapter.ExecutionRuntimePasses{}, fmt.Errorf("compose Execution driver executor: sandbox configuration rejected")
	}
	taskWorkerTemplate := newExecutionTaskWorker(
		st, executor, artifactsCapability, executionCapability,
		sourceControl, stackBindings, taskOutcomes, config, workDir, nodeCapacity,
	)
	passes.DriverExecutor, passes.TaskWorkers = serveadapter.BuildSharedNodeExecutionRuntimePasses(executor, taskWorkerTemplate, nodeCapacity)
	return passes, nil
}

func newExecutionTaskWorker(
	st store.Store,
	executor *driverexecutor.Executor,
	artifactsCapability webui.ArtifactsCapability,
	executionCapability webui.ExecutionCapability,
	sourceControl sourcecontrol.Materializer,
	stackBindings sourcecontrol.StackBindingResolver,
	taskOutcomes sourcecontrol.TaskOutcomeRecorder,
	config serveRuntimeConfig,
	workDir string,
	nodeCapacity int,
) driverexecutor.TaskWorker {
	return driverexecutor.TaskWorker{
		Store: st, WorkspaceKey: executor.WorkspaceKey, WorkDir: workDir,
		Artifacts: artifactsCapability.ArtifactsAPI(),
		NodeID:    executor.NodeID, NodeCapacity: nodeCapacity, RunnerID: config.TaskWorkerRunnerID,
		MaxAttempts: config.TaskRunMaxAttempts, APIBaseURL: config.DriverAPIBaseURL,
		LocalSettingsDir: config.LocalSettingsDir, Execution: executionCapability.TaskRunWorkerAPI(),
		SourceControl:        sourceControl,
		StackBindings:        stackBindings,
		TaskOutcomes:         taskOutcomes,
		TaskRunAuthorities:   executionCapability.TaskRunAuthorityResolver(),
		Convergence:          executionCapability.TaskRunConvergenceAPI(),
		ExecutionAuthorities: executionCapability.SystemAuthorityResolver(),
	}
}

// startOutboxDispatcher launches the always-on server-side outbox delivery
// loop. Like the stale task sweeper it is NOT gated behind
// LOOM_DRIVER_EXECUTOR: lead-notification delivery with retry/backoff is
// server policy, so it runs whenever serve has a store. It replaces the
// workflow-side lead-delivery retry loops.
func startOutboxDispatcher(
	ctx context.Context,
	st store.Store,
	executionCapability webui.ExecutionCapability,
	chat interaction.ChatMessenger,
	workspaceScope string,
) {
	if st == nil || executionCapability == nil || executionCapability.OutboxDeliveryAPI() == nil ||
		executionCapability.SystemAuthorityResolver() == nil || chat == nil {
		return
	}
	dispatcher := &driverexecutor.OutboxDispatcher{
		Delivery:     executionCapability.OutboxDeliveryAPI(),
		Authorities:  executionCapability.SystemAuthorityResolver(),
		Workspaces:   outboxWorkspaceLister{store: st.Workspaces()},
		WorkspaceKey: workspaceScope,
		Chat:         chat,
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

type outboxWorkspaceLister struct {
	store store.WorkspaceStore
}

func (lister outboxWorkspaceLister) ListWorkspaceKeys(ctx context.Context) ([]string, error) {
	values, err := lister.store.List(ctx)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(values))
	for _, value := range values {
		if value != nil {
			keys = append(keys, value.Key)
		}
	}
	return keys, nil
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
// replay-from-zero on first observation (handled inside the bridge);
// Task-ready and task-review are generic platform event lanes and are always
// enabled whenever the issue-journal bridge is enabled.
func startIssueJournalBridge(
	ctx context.Context,
	st store.Store,
	issueLookup trigger.TaskReadyIssueLookup,
	readySnapshots trigger.TaskReadySnapshotLister,
	repositoryRequiredBlocker trigger.TaskReadyRepositoryRequiredBlocker,
	source trigger.InternalEventEmitter,
	config issueJournalConfig,
) error {
	bridge, err := buildIssueJournalBridge(st, issueLookup, readySnapshots, repositoryRequiredBlocker, source, config)
	if err != nil {
		return err
	}
	if bridge == nil {
		return nil
	}
	interval := config.Interval
	slog.Info("Issue journal bridge enabled", "workspace", bridge.WorkspaceKey, "interval", interval,
		"state_path", config.StatePath, "task_ready_events", bridge.EmitTaskReady,
		"task_review_events", bridge.EmitTaskReview)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			if result, err := bridge.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				slog.Error("issue journal bridge pass failed", "err", err)
			} else if result != nil && (result.Emitted > 0 || result.TaskReadyEmitted > 0 || result.TaskReviewEmitted > 0 || result.TaskReadyBlocked > 0 || result.FastForwarded > 0) {
				slog.Info("issue journal bridge pass", "emitted", result.Emitted, "task_ready_emitted", result.TaskReadyEmitted,
					"task_review_emitted", result.TaskReviewEmitted, "task_ready_blocked", result.TaskReadyBlocked, "skipped", result.Skipped,
					"fast_forwarded", result.FastForwarded, "backed_off", result.BackedOff)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return nil
}

func buildIssueJournalBridge(
	st store.Store,
	issueLookup trigger.TaskReadyIssueLookup,
	readySnapshots trigger.TaskReadySnapshotLister,
	repositoryRequiredBlocker trigger.TaskReadyRepositoryRequiredBlocker,
	source trigger.InternalEventEmitter,
	config issueJournalConfig,
) (*trigger.IssueJournalBridge, error) {
	if st == nil || source == nil {
		if st != nil {
			slog.Info("issue journal bridge disabled: Automation system eventing is unavailable")
		}
		return nil, nil
	}
	if config.Disabled {
		slog.Info("issue journal bridge disabled: LOOM_ISSUE_BRIDGE_DISABLED set")
		return nil, nil
	}
	reader, ok := st.TriggerEvents().(store.IssueJournalReader)
	if !ok {
		slog.Info("issue journal bridge disabled: store has no journal reader")
		return nil, nil
	}
	if (config.EmitTaskReady || config.EmitTaskReview) && (issueLookup == nil || repositoryRequiredBlocker == nil) {
		return nil, fmt.Errorf("compose issue journal task lanes: current issue lookup and repository admission are required")
	}
	if config.EmitTaskReady && readySnapshots == nil {
		return nil, fmt.Errorf("compose issue journal task-ready lane: current ready snapshots are required")
	}
	cursors, err := trigger.NewFileIssueJournalCursorStore(config.StatePath, slog.Default())
	if err != nil {
		return nil, fmt.Errorf("compose issue journal cursor store: %w", err)
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
		WorkspaceKey:              config.WorkspaceScope,
		Cursors:                   cursors,
		EmitTaskReady:             config.EmitTaskReady,
		EmitTaskReview:            config.EmitTaskReview,
		IssueLookup:               issueLookup,
		ReadySnapshots:            readySnapshots,
		RepositoryRequiredBlocker: repositoryRequiredBlocker,
	}, nil
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
	status := strings.ToLower(strings.TrimSpace(canonical.Status))
	if (status != "open" && status != "review") ||
		strings.EqualFold(strings.TrimSpace(canonical.IssueType), "epic") {
		return nil, nil
	}
	return &canonical, nil
}
