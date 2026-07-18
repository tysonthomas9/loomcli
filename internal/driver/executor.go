package driver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver/runtypes"
	"github.com/tysonthomas9/loomcli/internal/driver/sandbox"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/store"
)

var ErrNoQueuedRun = errors.New("driver executor: no queued run")

type Runner interface {
	Run(ctx context.Context, req RunRequest) (RunResult, error)
}

// The driver's core run types and the SB1 sandbox seam live in the
// internal/driver/sandbox subpackage (extracted so the run/orchestration types
// and the sandbox launchers stop forming an import cycle). These aliases keep
// driver.RunRequest / driver.SandboxLauncher / the §9.6 audit consts available
// to in-package code, cross-package callers, and tests unchanged.
type (
	RunRequest        = runtypes.RunRequest
	RunResult         = runtypes.RunResult
	SandboxLauncher   = sandbox.SandboxLauncher
	IsolatingLauncher = sandbox.IsolatingLauncher
	LaunchSpec        = sandbox.LaunchSpec
	SandboxProcess    = sandbox.SandboxProcess
	SandboxExit       = sandbox.SandboxExit
	processLauncher   = sandbox.ProcessLauncher
)

const (
	ErrorClassSandboxRequired = sandbox.ErrorClassSandboxRequired
	ErrorCodeOutputKey        = sandbox.ErrorCodeOutputKey
	SandboxLauncherOutputKey  = sandbox.SandboxLauncherOutputKey
	TrustLevelOutputKey       = sandbox.TrustLevelOutputKey
	RetryableOutputKey        = sandbox.RetryableOutputKey
	SandboxProviderProcess    = sandbox.SandboxProviderProcess
	SandboxProviderContainer  = sandbox.SandboxProviderContainer
	SandboxPlacementOutputKey = sandbox.SandboxPlacementOutputKey
	SandboxModeEnvVar         = sandbox.SandboxModeEnvVar
	SandboxModeProcess        = sandbox.SandboxModeProcess
	SandboxModeContainer      = sandbox.SandboxModeContainer
	SandboxEgressEnvVar       = sandbox.SandboxEgressEnvVar
)

// ResolveSandboxLauncher re-exports the sandbox subpackage's launcher resolver
// so serve and CLI callers keep using driver.ResolveSandboxLauncher unchanged
// (Go has no function aliases, hence the thin wrapper).
func ResolveSandboxLauncher() (SandboxLauncher, error) {
	return sandbox.ResolveSandboxLauncher()
}

type Executor struct {
	Store             store.Store
	WorkspaceKey      string
	RunID             string
	WorkDir           string
	NodeID            string
	LeaseID           string
	Runner            Runner
	HeartbeatInterval time.Duration
	// APIBaseURL/APIToken configure the driver-op HTTP API exported to driver
	// runtimes via the default NodeRunner (LOOM_DRIVER_API_URL/_TOKEN).
	APIBaseURL string
	APIToken   string //nolint:gosec // G117: driver API bearer token intentionally stored in runtime config.
	// RunTokenKey, when set, is the HS256 signing key used to mint the
	// run-scoped bearer token injected into the workflow runtime as
	// LOOM_RUN_TOKEN at claim time. Nil disables minting and keeps the
	// legacy LOOM_DRIVER_API_TOKEN + identity-quad env authenticating (no
	// flag-day). Token-carrying runs drop that legacy env once the
	// deprecated LegacyDriverAuthEnvVar fallback is switched off. Must be
	// the same key the serve driver-op API verifies with
	// (ResolveRunTokenSigningKey: one key per process).
	RunTokenKey []byte
	// SandboxLauncher, when set, launches workflow runtimes through the SB1
	// sandbox seam — e.g. the rootless container launcher selected by
	// LOOM_DRIVER_SANDBOX=container in serve (ResolveSandboxLauncher). Nil
	// keeps the default local node-process launcher. Ignored when Runner is
	// set explicitly.
	SandboxLauncher SandboxLauncher
	// RunOutcomes is Execution's narrow outbound outcome port. Terminal run
	// state is authoritative even when publication fails: the publisher is
	// retried by lifecycle recovery/replay and must be idempotent by EventID.
	// Nil disables cross-capability fan-out while retaining composition-await
	// notification, which is owned by Execution and remains store-backed.
	RunOutcomes RunOutcomePublisher
	// Execution is the Phase 4 owner of live DriverRun lifecycle mutations.
	// Serve always supplies this API and its typed authority resolvers. Nil
	// retains the legacy direct-store compatibility path for standalone CLI
	// callers and existing isolated tests until their command family migrates.
	Execution            execution.DriverRunAPI
	RunOutcomeQueue      execution.DriverRunOutcomeAPI
	ExecutionWorkers     execution.TaskRunWorkerAPI
	ExecutionAuthorities execution.DriverRunAuthorityResolver
	SystemAuthorities    execution.SystemAuthorityResolver
}

type ExecutionResult struct {
	Run     *domain.DriverRun
	Claimed *domain.DriverRun
	Final   *domain.DriverRun
	Skipped bool
}

func (e *Executor) RunOnce(ctx context.Context) (*ExecutionResult, error) {
	if e == nil || e.Store == nil {
		return nil, fmt.Errorf("store required: %w", domain.ErrInvalid)
	}
	if e.Execution == nil || e.RunOutcomeQueue == nil || e.ExecutionWorkers == nil || e.ExecutionAuthorities == nil || e.SystemAuthorities == nil {
		return nil, fmt.Errorf("execution DriverRun, outcome queue, and worker-node APIs are required: %w", execution.ErrUnavailable)
	}
	workDir, err := e.resolveWorkDir()
	if err != nil {
		return nil, err
	}
	run, err := e.nextQueuedRun(ctx)
	if err != nil {
		return nil, err
	}
	nodeID := e.nodeID()
	leaseID := e.leaseID(run.RunID)
	leaseToken := generatedTaskRunLeaseToken()
	if len(e.RunTokenKey) > 0 {
		leaseToken, err = DeriveDriverRunLeaseToken(e.RunTokenKey, run.WorkspaceKey, run.RunID, nodeID, leaseID)
		if err != nil {
			return nil, err
		}
	}
	if err := e.ensureNode(ctx, run.WorkspaceKey, nodeID); err != nil {
		return nil, err
	}
	claimed, err := e.claimDriverRun(ctx, run, nodeID, leaseID, leaseToken)
	if err != nil {
		if errors.Is(err, domain.ErrAlreadyClaimed) || errors.Is(err, domain.ErrInvalidTransition) {
			return &ExecutionResult{Run: run, Skipped: true}, nil
		}
		return nil, fmt.Errorf("claim driver run: %w", err)
	}
	result := &ExecutionResult{Run: run, Claimed: claimed}
	hbCtx, stopHeartbeat := context.WithCancel(ctx)
	defer stopHeartbeat()
	// runCtx is the runner's context: the heartbeat cancels it when it
	// observes a cooperative cancel request (composition cascade, AW10),
	// and the runner then reports cancelled through the normal finish.
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	if interval := e.heartbeatInterval(); interval > 0 {
		go heartbeatDriverRun(hbCtx, e, claimed, leaseToken, interval, cancelRun)
		go heartbeatExecutorNode(hbCtx, e, claimed.WorkspaceKey, nodeID, e.nodeTTL())
	}
	result.Final, err = e.settleClaimed(ctx, claimed, leaseToken, e.runClaimed(runCtx, workDir, claimed))
	if err != nil {
		return result, err
	}
	return result, nil
}

func (e *Executor) runClaimed(ctx context.Context, workDir string, claimed *domain.DriverRun) RunResult {
	runner := e.Runner
	if runner == nil {
		runner = NodeRunner{APIBaseURL: e.APIBaseURL, APIToken: e.APIToken, Launcher: e.SandboxLauncher}
	}
	req, err := loadRunRequest(ctx, workDir, claimed, e.Store)
	if err != nil {
		return RunResult{Status: domain.DriverRunFailed, Summary: err.Error(), ErrorClass: "bundle_verification"}
	}
	req.RunToken = e.mintRunToken(ctx, claimed)
	runResult, runErr := runner.Run(ctx, req)
	if runErr != nil && ctx.Err() != nil {
		// The runner context was cancelled under it (cooperative cancel
		// request or executor shutdown): the run is cancelled, not failed —
		// mirroring NodeRunner's own ctx-cancelled mapping.
		runResult = RunResult{Status: domain.DriverRunCancelled, Summary: "driver run cancelled: " + runErr.Error(), ErrorClass: "driver_cancelled"}
	} else if runErr != nil {
		runResult = RunResult{Status: domain.DriverRunFailed, Summary: runErr.Error(), ErrorClass: "driver_runtime"}
	} else {
		runResult = requireExplicitTerminalRunResult(runResult)
	}
	return runResult
}

// mintRunToken mints the run-scoped bearer token for a freshly claimed run
// (§4: minted at claim time), binding the claim's lease + fencing token into
// the claims so fenced run verification doubles as revocation: a superseded
// lease rejects the token regardless of expiry. TTL = maximum run duration
// (LOOM_RUN_TOKEN_TTL, default 24h) per the locked step-9 decision — expiry
// is the hard run-duration cap, not the lease window. Failures degrade to ""
// with a warning rather than failing the claimed run: a token-less run keeps
// the legacy LOOM_DRIVER_API_TOKEN + identity-quad env, which still
// authenticates.
func (e *Executor) mintRunToken(ctx context.Context, claimed *domain.DriverRun) string {
	if len(e.RunTokenKey) == 0 || claimed == nil {
		return ""
	}
	ttl, err := RunTokenTTL()
	if err != nil {
		slog.WarnContext(ctx, "driver run token TTL env invalid; using default",
			"default", DefaultRunTokenTTL, "err", err)
		ttl = DefaultRunTokenTTL
	}
	token, err := MintRunToken(RunTokenClaims{
		WorkspaceKey: claimed.WorkspaceKey,
		RunID:        claimed.RunID,
		NodeID:       claimed.NodeID,
		LeaseID:      claimed.LeaseID,
		FencingToken: claimed.FencingToken,
	}, e.RunTokenKey, ttl)
	if err != nil {
		slog.WarnContext(ctx, "mint driver run token failed; runtime falls back to legacy driver API auth",
			"runID", claimed.RunID, "err", err)
		return ""
	}
	return token
}

func (e *Executor) RecoverStaleOnce(ctx context.Context) (*store.StaleDriverRunRecoveryResult, error) {
	if e == nil || e.Store == nil {
		return nil, fmt.Errorf("store required: %w", domain.ErrInvalid)
	}
	maxAge := 5 * time.Minute
	recover := store.StaleDriverRunRecovery{
		MaxAgeSeconds: int64(maxAge / time.Second),
		Summary:       "driver executor heartbeat expired",
	}
	if e.WorkspaceKey != "" {
		return e.recoverStaleWorkspace(ctx, e.WorkspaceKey, recover)
	}
	workspaces, err := e.Store.Workspaces().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list workspaces for stale driver recovery: %w", err)
	}
	out := &store.StaleDriverRunRecoveryResult{}
	for _, ws := range workspaces {
		if ws == nil {
			continue
		}
		result, err := e.recoverStaleWorkspace(ctx, ws.Key, recover)
		if err != nil {
			return nil, err
		}
		out.WorkspaceKey = ""
		if out.StaleBefore.IsZero() {
			out.StaleBefore = result.StaleBefore
		}
		if out.RecoveredAt.IsZero() || result.RecoveredAt.After(out.RecoveredAt) {
			out.RecoveredAt = result.RecoveredAt
		}
		out.Recovered += result.Recovered
		out.SkippedFresh += result.SkippedFresh
		out.RecoveredRunIDs = append(out.RecoveredRunIDs, result.RecoveredRunIDs...)
		out.SkippedFreshRunIDs = append(out.SkippedFreshRunIDs, result.SkippedFreshRunIDs...)
	}
	return out, nil
}

// recoverStaleWorkspace runs one workspace's stale-run sweep and publishes
// run.finished for every recovered run (AW6): a stale-failed run is a
// terminal transition like any other, so a parent awaiting the child must
// learn about it. The store stamps error class stale_driver_run by default.
func (e *Executor) recoverStaleWorkspace(ctx context.Context, ws string, recover store.StaleDriverRunRecovery) (*store.StaleDriverRunRecoveryResult, error) {
	result, err := e.recoverDriverRuns(ctx, ws, recover)
	if err != nil {
		return nil, err
	}
	for _, runID := range result.RecoveredRunIDs {
		run, getErr := e.Store.DriverRuns().Get(ctx, ws, runID)
		if getErr != nil {
			slog.WarnContext(ctx, "load recovered driver run for run.finished emission failed",
				"workspace", ws, "runID", runID, "error", getErr)
			continue
		}
		e.publishRunFinished(ctx, run)
	}
	return result, nil
}

func (e *Executor) nextQueuedRun(ctx context.Context) (*domain.DriverRun, error) {
	if strings.TrimSpace(e.RunID) != "" {
		return queuedRunByID(ctx, e.Store, e.WorkspaceKey, e.RunID)
	}
	if e.WorkspaceKey != "" {
		return nextQueuedRunInWorkspace(ctx, e.Store, e.WorkspaceKey)
	}
	workspaces, err := e.Store.Workspaces().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}
	for _, ws := range workspaces {
		if ws == nil {
			continue
		}
		run, err := nextQueuedRunInWorkspace(ctx, e.Store, ws.Key)
		if err == nil {
			return run, nil
		}
		if !errors.Is(err, ErrNoQueuedRun) {
			return nil, err
		}
	}
	return nil, ErrNoQueuedRun
}

func queuedRunByID(ctx context.Context, s store.Store, ws, runID string) (*domain.DriverRun, error) {
	if strings.TrimSpace(ws) == "" {
		return nil, fmt.Errorf("workspace key required for run %q: %w", runID, domain.ErrInvalid)
	}
	run, err := s.DriverRuns().Get(ctx, ws, runID)
	if err != nil {
		return nil, fmt.Errorf("get queued driver run: %w", err)
	}
	if run.Status != domain.DriverRunQueued {
		return nil, ErrNoQueuedRun
	}
	return run, nil
}

func nextQueuedRunInWorkspace(ctx context.Context, s store.Store, ws string) (*domain.DriverRun, error) {
	runs, err := s.DriverRuns().List(ctx, ws, store.DriverRunFilter{Status: domain.DriverRunQueued, Limit: 1})
	if err != nil {
		return nil, fmt.Errorf("list queued driver runs: %w", err)
	}
	if len(runs) == 0 {
		return nil, ErrNoQueuedRun
	}
	return runs[0], nil
}

// settleClaimed finishes a terminal runner result, or acknowledges a
// suspended one (AW11): an await op already suspended the run server-side and
// released the slot, so the executor must NOT Finish — it re-reads the run,
// which may legitimately already be queued or running again (pending->suspend
// window tolerance: the resolver can resume before the runner exits). A
// runner that reports suspended while the run is still running under OUR
// lease lied (no await actually suspended it); it is finished failed so the
// slot does not leak to the stale sweeper.
func (e *Executor) settleClaimed(ctx context.Context, claimed *domain.DriverRun, leaseToken string, result RunResult) (*domain.DriverRun, error) {
	if result.Status != domain.DriverRunSuspendedAwaitingEvent {
		return e.finish(ctx, claimed, leaseToken, result)
	}
	run, err := e.Store.DriverRuns().Get(ctx, claimed.WorkspaceKey, claimed.RunID)
	if err != nil {
		return nil, fmt.Errorf("read suspended driver run: %w", err)
	}
	if run.Status == domain.DriverRunRunning && run.NodeID == claimed.NodeID && run.LeaseID == claimed.LeaseID {
		return e.finish(ctx, claimed, leaseToken, RunResult{
			Status:     domain.DriverRunFailed,
			Summary:    "driver reported suspended but no await suspended the run",
			ErrorClass: "invalid_driver_result",
			Output:     result.Output,
		})
	}
	return run, nil
}

func (e *Executor) finish(ctx context.Context, claimed *domain.DriverRun, leaseToken string, result RunResult) (*domain.DriverRun, error) {
	if strings.TrimSpace(result.Summary) == "" {
		result.Summary = string(result.Status)
	}
	final, err := e.finalizeDriverRun(ctx, claimed, leaseToken, result)
	if err != nil {
		if settled, ok := e.settleDisownedFinish(ctx, claimed, err); ok {
			return settled, nil
		}
		return nil, fmt.Errorf("finish driver run: %w", err)
	}
	// Finalize commits the parent before the recursive child cascade. The live
	// owner lane gives normal execution low latency; the durable run-outcome
	// reconciler replays the same request ID through its system lane after a
	// crash or response loss.
	if cascadeErr := e.cascadeChildDriverRuns(ctx, claimed, final, leaseToken); cascadeErr != nil {
		slog.WarnContext(ctx, "cascade terminal parent DriverRun children failed; durable outcome recovery will retry",
			"workspace", final.WorkspaceKey, "runID", final.RunID, "status", final.Status, "error", cascadeErr)
	}
	// Every server-side terminal transition publishes run.finished (AW6):
	// completed, failed, needs_review and cancelled all land here (a
	// cancelled runner reports DriverRunCancelled through the same finish).
	e.publishRunFinished(ctx, final)
	return final, nil
}

func (e *Executor) publishRunFinished(ctx context.Context, run *domain.DriverRun) {
	awaitNotifier := e.executionRunOutcomeAwaitNotifier()
	emitRunFinishedEventWithExecution(
		ctx, e.Store, e.RunOutcomes, run, e.RunOutcomeQueue, e.Execution,
		e.SystemAuthorities, awaitNotifier,
	)
}

func (e *Executor) cascadeChildDriverRuns(
	ctx context.Context,
	claimed, final *domain.DriverRun,
	leaseToken string,
) error {
	if e == nil || e.Execution == nil || e.ExecutionAuthorities == nil || claimed == nil || final == nil {
		return execution.ErrUnavailable
	}
	if !final.Status.IsTerminal() || claimed.RunID != final.RunID || claimed.WorkspaceKey != final.WorkspaceKey {
		return execution.ErrInvalid
	}
	owner := execution.Owner{
		ResourceKind: execution.ResourceDriverRun,
		ResourceID:   claimed.RunID,
		NodeID:       claimed.NodeID,
		LeaseID:      claimed.LeaseID,
		LeaseToken:   leaseToken,
		FencingToken: claimed.FencingToken,
	}
	auth, err := e.ExecutionAuthorities.ResolveDriverRunAuthority(
		ctx,
		claimed.WorkspaceKey,
		execution.ActionCascadeChildDriverRuns,
		owner,
	)
	if err != nil {
		return fmt.Errorf("resolve child DriverRun cascade authority: %w", err)
	}
	cascadedAt := time.Now().UTC()
	if final.FinishedAt != nil && !final.FinishedAt.IsZero() {
		cascadedAt = final.FinishedAt.UTC()
	}
	status := execution.DriverRunStatus(final.Status)
	_, err = e.Execution.CascadeChildDriverRuns(ctx, auth, execution.CascadeChildDriverRunsCommand{
		WorkspaceKey: final.WorkspaceKey,
		RequestID:    execution.CascadeChildDriverRunsRequestID(final.RunID, status),
		Owner:        owner,
		ParentRunID:  final.RunID,
		ParentStatus: status,
		Reason:       childDriverRunCascadeReason(final.Status),
		ErrorClass:   childDriverRunCascadeErrorClass,
		CascadedAt:   cascadedAt,
		MaxDepth:     DefaultCompositionMaxDepth,
	})
	if err != nil {
		return fmt.Errorf("cascade child DriverRuns: %w", err)
	}
	return nil
}

func (e *Executor) executionRunOutcomeAwaitNotifier() RunOutcomeAwaitNotifier {
	if e == nil || e.Execution == nil {
		return nil
	}
	if e.SystemAuthorities == nil {
		return nil
	}
	notifier, err := NewRunOutcomeAwaitNotifierWithResolver(e.Store.Awaits(), &ExecutionAwaitResolver{
		API: e.Execution, Authorities: e.SystemAuthorities, ComponentID: RunOutcomeAwaitComponentID,
	})
	if err != nil {
		slog.Warn("compose Execution run outcome await notifier failed", "error", err)
		return nil
	}
	return notifier
}

// settleDisownedFinish tolerates the one legitimate way a finish can lose
// ownership mid-run: an events/await op suspended the run server-side
// (releasing the slot and clearing the owner triple) but the workflow
// runtime distorted the suspension signal into a terminal-looking result —
// e.g. a framework that serializes the SDK's WorkflowSuspended sentinel into
// a generic internal error, so the launcher reports failed instead of
// suspended (observed with the real Flue runtime, AW12). The server-side
// suspension is authoritative: acknowledge it like settleClaimed does for a
// clean suspended report. A run already resolved and re-queued is the same
// accepted register->suspend window. Anything else (zombie lease, stale
// recovery) stays an error.
func (e *Executor) settleDisownedFinish(ctx context.Context, claimed *domain.DriverRun, finishErr error) (*domain.DriverRun, bool) {
	if !errors.Is(finishErr, domain.ErrNotOwner) {
		return nil, false
	}
	run, err := e.Store.DriverRuns().Get(ctx, claimed.WorkspaceKey, claimed.RunID)
	if err != nil {
		return nil, false
	}
	suspended := run.Status == domain.DriverRunSuspendedAwaitingEvent
	requeued := run.Status == domain.DriverRunQueued && run.ResumeSourceEventID != ""
	if !suspended && !requeued {
		return nil, false
	}
	slog.WarnContext(ctx, "driver run finish superseded by server-side await suspension; acknowledging suspended run",
		"runID", claimed.RunID, "status", string(run.Status))
	return run, true
}

func (e *Executor) resolveWorkDir() (string, error) {
	workDir := e.WorkDir
	if workDir == "" {
		workDir = "."
	}
	abs, err := filepath.Abs(workDir)
	if err != nil {
		return "", fmt.Errorf("resolve executor work dir: %w", err)
	}
	return abs, nil
}

func (e *Executor) nodeID() string {
	if e.NodeID != "" {
		return e.NodeID
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "local"
	}
	return fmt.Sprintf("loom-driver-executor-%s-%d", host, os.Getpid())
}

func (e *Executor) leaseID(runID string) string {
	if e.LeaseID != "" {
		return e.LeaseID
	}
	return fmt.Sprintf("%s-%d", runID, time.Now().UTC().UnixNano())
}

func (e *Executor) ensureNode(ctx context.Context, ws, nodeID string) error {
	if e.ExecutionWorkers == nil || e.SystemAuthorities == nil {
		return fmt.Errorf("execution system authority and worker-node API required: %w", execution.ErrUnavailable)
	}
	ttl := e.nodeTTL()
	now := time.Now().UTC()
	registerAuth, err := e.SystemAuthorities.ResolveExecutionSystemAuthority(
		ctx, ws, execution.ActionRegisterWorkerNode, string(execution.DriverExecutorComponentID),
	)
	if err != nil {
		return err
	}
	_, err = e.ExecutionWorkers.RegisterWorkerNode(ctx, registerAuth, execution.RegisterWorkerNodeCommand{
		WorkspaceKey: ws, RequestID: "register-driver-executor-node:" + nodeID, NodeID: nodeID,
		OwnerActor: executorOwnerActor(), RuntimeProvider: string(domain.RuntimeProviderLocal),
		Labels:        []string{"loom-driver-executor"},
		Capabilities:  []string{"driver-runner", "task-runner", "flue-local"},
		ToolInventory: []string{"loom-driver"}, Version: "loom-serve", Capacity: 1, TTL: ttl, RegisteredAt: now,
	})
	if err != nil {
		return fmt.Errorf("register executor node through Execution: %w", err)
	}
	heartbeatAuth, err := e.SystemAuthorities.ResolveExecutionSystemAuthority(
		ctx, ws, execution.ActionHeartbeatWorkerNode, string(execution.DriverExecutorComponentID),
	)
	if err != nil {
		return err
	}
	if _, err := e.ExecutionWorkers.HeartbeatWorkerNode(ctx, heartbeatAuth, execution.HeartbeatWorkerNodeCommand{
		WorkspaceKey: ws, RequestID: fmt.Sprintf("heartbeat-driver-executor-node:%s:%d", nodeID, now.UnixNano()),
		NodeID: nodeID, TTL: ttl, HeartbeatAt: now,
	}); err != nil {
		return fmt.Errorf("heartbeat executor node through Execution: %w", err)
	}
	drainAuth, err := e.SystemAuthorities.ResolveExecutionSystemAuthority(
		ctx, ws, execution.ActionSetWorkerNodeDrain, string(execution.DriverExecutorComponentID),
	)
	if err != nil {
		return err
	}
	if _, err := e.ExecutionWorkers.SetWorkerNodeDrain(ctx, drainAuth, execution.SetWorkerNodeDrainCommand{
		WorkspaceKey: ws, RequestID: "activate-driver-executor-node:" + nodeID,
		NodeID: nodeID, DrainState: execution.WorkerNodeActive, ChangedAt: now,
	}); err != nil {
		return fmt.Errorf("activate executor node through Execution: %w", err)
	}
	return nil
}

func mergeNodeStringSet(existing, desired []string) []string {
	if len(existing) == 0 {
		return append([]string(nil), desired...)
	}
	out := make([]string, 0, len(existing)+len(desired))
	seen := map[string]struct{}{}
	for _, values := range [][]string{existing, desired} {
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			out = append(out, value)
		}
	}
	return out
}

func executorOwnerActor() string {
	for _, key := range []string{"LOOM_FLEET_DB_ACTOR", "LOOM_DRIVER_FLEET_DB_ACTOR", "USER"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return "loom-driver-executor"
}

func (e *Executor) heartbeatInterval() time.Duration {
	if e.HeartbeatInterval == 0 {
		return 30 * time.Second
	}
	if e.HeartbeatInterval < 0 {
		return 0
	}
	return e.HeartbeatInterval
}

func (e *Executor) nodeTTL() time.Duration {
	ttl := 5 * time.Minute
	if interval := e.heartbeatInterval(); interval > 0 && interval*3 > ttl {
		ttl = interval * 3
	}
	return ttl
}

// heartbeatDriverRun renews the run lease and observes cooperative cancel
// requests (composition cascade, AW10): a heartbeat that comes back with
// CancelRequestedAt set fires onCancelRequested, canceling the runner's
// context so the run terminalizes as cancelled through the normal finish.
func heartbeatDriverRun(ctx context.Context, executor *Executor, claimed *domain.DriverRun, leaseToken string, interval time.Duration, onCancelRequested func()) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run, err := executor.heartbeatDriverRun(ctx, claimed, leaseToken)
			if err == nil && run != nil && run.CancelRequestedAt != nil && onCancelRequested != nil {
				onCancelRequested()
			}
		}
	}
}

func (e *Executor) claimDriverRun(ctx context.Context, queued *domain.DriverRun, nodeID, leaseID, leaseToken string) (*domain.DriverRun, error) {
	if e.Execution == nil {
		return nil, execution.ErrUnavailable
	}
	if e.SystemAuthorities == nil {
		return nil, fmt.Errorf("execution system authority resolver required: %w", domain.ErrInvalid)
	}
	auth, err := e.SystemAuthorities.ResolveExecutionSystemAuthority(
		ctx, queued.WorkspaceKey, execution.ActionClaimDriverRun, string(execution.DriverExecutorComponentID),
	)
	if err != nil {
		return nil, err
	}
	run, err := e.Execution.ClaimDriverRun(ctx, auth, execution.ClaimDriverRunCommand{
		WorkspaceKey: queued.WorkspaceKey, RequestID: "driver-run-claim:" + queued.RunID + ":" + leaseID,
		RunID: queued.RunID, NodeID: nodeID, LeaseID: leaseID, LeaseToken: leaseToken,
	})
	if err != nil {
		return nil, err
	}
	return LegacyDriverRunSnapshot(run)
}

func (e *Executor) heartbeatDriverRun(ctx context.Context, claimed *domain.DriverRun, leaseToken string) (*domain.DriverRun, error) {
	if e.Execution == nil {
		return nil, execution.ErrUnavailable
	}
	if e.ExecutionAuthorities == nil {
		return nil, fmt.Errorf("execution DriverRun authority resolver required: %w", domain.ErrInvalid)
	}
	owner := executionOwnerFromLegacyDriverRun(claimed, leaseToken)
	auth, err := e.ExecutionAuthorities.ResolveDriverRunAuthority(ctx, claimed.WorkspaceKey, execution.ActionHeartbeatDriverRun, owner)
	if err != nil {
		return nil, err
	}
	run, err := e.Execution.HeartbeatDriverRun(ctx, auth, execution.DriverRunHeartbeatCommand{
		WorkspaceKey: claimed.WorkspaceKey, Owner: owner, At: time.Now().UTC(),
	})
	if err != nil {
		return nil, err
	}
	return LegacyDriverRunSnapshot(run)
}

func (e *Executor) finalizeDriverRun(ctx context.Context, claimed *domain.DriverRun, leaseToken string, result RunResult) (*domain.DriverRun, error) {
	if e.Execution == nil {
		return nil, execution.ErrUnavailable
	}
	if e.ExecutionAuthorities == nil {
		return nil, fmt.Errorf("execution DriverRun authority resolver required: %w", domain.ErrInvalid)
	}
	owner := executionOwnerFromLegacyDriverRun(claimed, leaseToken)
	auth, err := e.ExecutionAuthorities.ResolveDriverRunAuthority(ctx, claimed.WorkspaceKey, execution.ActionFinalizeDriverRun, owner)
	if err != nil {
		return nil, err
	}
	run, err := e.Execution.FinalizeDriverRun(ctx, auth, execution.FinalizeDriverRunCommand{
		WorkspaceKey: claimed.WorkspaceKey,
		RequestID:    fmt.Sprintf("driver-run-finalize:%s:%d", claimed.RunID, claimed.FencingToken),
		Owner:        owner, Status: execution.DriverRunStatus(result.Status), Summary: result.Summary,
		ErrorClass: result.ErrorClass, Output: cloneStringMap(result.Output), FinishedAt: time.Now().UTC(),
	})
	if err != nil {
		return nil, err
	}
	return LegacyDriverRunSnapshot(run)
}

func (e *Executor) recoverDriverRuns(ctx context.Context, workspace string, recover store.StaleDriverRunRecovery) (*store.StaleDriverRunRecoveryResult, error) {
	if e.Execution == nil {
		return nil, execution.ErrUnavailable
	}
	if e.SystemAuthorities == nil {
		return nil, fmt.Errorf("execution system authority resolver required: %w", domain.ErrInvalid)
	}
	observedAt := time.Now().UTC()
	maxAge := time.Duration(recover.MaxAgeSeconds) * time.Second
	if !recover.StaleBefore.IsZero() {
		// Execution expresses the cutoff as observed_at - max_age and requires a
		// positive duration. Anchor an explicit legacy cutoff one nanosecond after
		// itself so even a future cutoff remains exact and valid.
		maxAge = time.Nanosecond
		observedAt = recover.StaleBefore.Add(maxAge)
	} else if maxAge <= 0 {
		maxAge = 5 * time.Minute
	}
	auth, err := e.SystemAuthorities.ResolveExecutionSystemAuthority(
		ctx, workspace, execution.ActionRecoverDriverRuns, string(execution.DriverExecutorComponentID),
	)
	if err != nil {
		return nil, err
	}
	result, err := e.Execution.RecoverDriverRuns(ctx, auth, execution.RecoverDriverRunsCommand{
		WorkspaceKey: workspace, RequestID: fmt.Sprintf("driver-run-recover:%s:%d", workspace, observedAt.UnixNano()),
		ObservedAt: observedAt, MaxAge: maxAge, ErrorClass: recover.ErrorClass, Summary: recover.Summary, Limit: recover.Limit,
	})
	if err != nil {
		return nil, err
	}
	return &store.StaleDriverRunRecoveryResult{
		WorkspaceKey: result.WorkspaceKey, StaleBefore: result.StaleBefore, RecoveredAt: result.RecoveredAt,
		Recovered: result.Recovered, SkippedFresh: result.SkippedFresh,
		RecoveredRunIDs:    append([]string(nil), result.RecoveredRunIDs...),
		SkippedFreshRunIDs: append([]string(nil), result.SkippedFreshRunIDs...),
	}, nil
}

func executionOwnerFromLegacyDriverRun(run *domain.DriverRun, leaseToken string) execution.Owner {
	if run == nil {
		return execution.Owner{}
	}
	return execution.Owner{
		ResourceKind: execution.ResourceDriverRun, ResourceID: run.RunID,
		NodeID: run.NodeID, LeaseID: run.LeaseID, LeaseToken: leaseToken, FencingToken: run.FencingToken,
	}
}

// LegacyDriverRunSnapshot projects Execution's public snapshot onto the
// shipped DriverRun transport/read model while legacy handlers migrate.
func LegacyDriverRunSnapshot(run *execution.DriverRun) (*domain.DriverRun, error) {
	if run == nil {
		return nil, fmt.Errorf("execution returned no DriverRun: %w", domain.ErrInvalid)
	}
	return &domain.DriverRun{
		WorkspaceKey: run.WorkspaceKey, RunID: run.RunID, DriverID: run.DriverID, DriverVersionID: run.DriverVersionID,
		Entrypoint: run.Entrypoint, SourceKind: run.SourceKind, SourceRef: run.SourceRef, EpicID: run.EpicID,
		ParentRunID: run.ParentRunID, TriggerBindingID: run.TriggerBindingID, AgentServiceID: run.AgentServiceID,
		SubjectKey: run.SubjectKey, Status: domain.DriverRunStatus(run.Status), NodeID: run.Owner.NodeID,
		LeaseID: run.Owner.LeaseID, FencingToken: run.Owner.FencingToken, IdempotencyKey: run.IdempotencyKey,
		Payload: append([]byte(nil), run.Payload...), Output: cloneStringMap(run.Output), Summary: run.Summary,
		ErrorClass: run.ErrorClass, StartedAt: run.StartedAt, LastHeartbeat: run.LastHeartbeat, FinishedAt: run.FinishedAt,
		AwaitInstanceKey: run.AwaitInstanceKey, SuspendedAt: run.SuspendedAt,
		CancelRequestedAt: run.CancelRequestedAt, CancelRequestedReason: run.CancelRequestedReason,
		ResumeSourceEventID: run.ResumeSourceEventID, CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt,
	}, nil
}

func heartbeatExecutorNode(ctx context.Context, executor *Executor, ws, nodeID string, ttl time.Duration) {
	ticker := time.NewTicker(ttl / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if executor == nil {
				continue
			}
			if executor.ExecutionWorkers == nil {
				continue
			}
			if executor.SystemAuthorities == nil {
				continue
			}
			auth, err := executor.SystemAuthorities.ResolveExecutionSystemAuthority(
				ctx, ws, execution.ActionHeartbeatWorkerNode, string(execution.DriverExecutorComponentID),
			)
			if err != nil {
				continue
			}
			_, _ = executor.ExecutionWorkers.HeartbeatWorkerNode(ctx, auth, execution.HeartbeatWorkerNodeCommand{
				WorkspaceKey: ws, RequestID: "heartbeat-driver-executor-node:" + nodeID,
				NodeID: nodeID, TTL: ttl, HeartbeatAt: time.Now().UTC(),
			})
		}
	}
}

func loadRunRequest(ctx context.Context, workDir string, run *domain.DriverRun, s store.Store) (RunRequest, error) {
	version, err := s.DriverVersions().Get(ctx, run.WorkspaceKey, run.DriverVersionID)
	if err != nil {
		return RunRequest{}, fmt.Errorf("load pinned driver version: %w", err)
	}
	if version.DriverID != run.DriverID {
		return RunRequest{}, fmt.Errorf("pinned version %q belongs to driver %q, run wants %q: %w", version.VersionID, version.DriverID, run.DriverID, domain.ErrInvalid)
	}
	if version.ValidationStatus != domain.DriverVersionValidationPassed {
		return RunRequest{}, fmt.Errorf("pinned version %q is not passed: %w", version.VersionID, domain.ErrInvalid)
	}
	if version.BundleRef == "" {
		return RunRequest{}, fmt.Errorf("pinned version %q has no bundle_ref: %w", version.VersionID, domain.ErrInvalid)
	}
	bundleRoot, err := safeBundleRoot(workDir, version.BundleRef)
	if err != nil {
		return RunRequest{}, err
	}
	manifest, serverPath, err := verifyBundleManifest(bundleRoot, version.BundleDigest)
	if err != nil {
		return RunRequest{}, err
	}
	trust, err := driverTrustLevel(ctx, s.Drivers(), run, version)
	if err != nil {
		return RunRequest{}, err
	}
	return RunRequest{
		Run:        run,
		Version:    version,
		BundleRoot: bundleRoot,
		ServerPath: serverPath,
		Manifest:   manifest,
		TrustLevel: trust,
	}, nil
}

// verifyBundleManifest reads the staged bundle's manifest, resolves the built
// Flue server path inside the bundle, and checks the bundle tree digest
// against the pinned version digest.
func verifyBundleManifest(bundleRoot, wantDigest string) (map[string]string, string, error) {
	manifestPath := filepath.Join(bundleRoot, "manifest.json")
	manifestBytes, err := os.ReadFile(manifestPath) //nolint:gosec // path is constrained under workDir by safeBundleRoot.
	if err != nil {
		return nil, "", fmt.Errorf("read bundle manifest: %w", err)
	}
	manifest := map[string]string{}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, "", fmt.Errorf("decode bundle manifest: %w", err)
	}
	serverRef := manifest["server_ref"]
	if serverRef == "" {
		return nil, "", fmt.Errorf("native Flue bundle manifest missing server_ref: %w", domain.ErrInvalid)
	}
	serverPath, err := safeBundleFile(bundleRoot, serverRef)
	if err != nil {
		return nil, "", err
	}
	if info, err := os.Stat(serverPath); err != nil {
		return nil, "", fmt.Errorf("stat built Flue server: %w", err)
	} else if info.IsDir() {
		return nil, "", fmt.Errorf("built Flue server %q is a directory: %w", serverRef, domain.ErrInvalid)
	}
	if got, err := digestBundleTree(bundleRoot, manifestBytes); err != nil {
		return nil, "", err
	} else if got != wantDigest {
		return nil, "", fmt.Errorf("bundle digest mismatch: got %s want %s: %w", got, wantDigest, domain.ErrInvalid)
	}
	return manifest, serverPath, nil
}

func safeBundleRoot(workDir, bundleRef string) (string, error) {
	if filepath.IsAbs(bundleRef) {
		return "", fmt.Errorf("bundle_ref must be relative: %w", domain.ErrInvalid)
	}
	root := filepath.Clean(filepath.Join(workDir, filepath.FromSlash(bundleRef)))
	rel, err := filepath.Rel(workDir, root)
	if err != nil {
		return "", fmt.Errorf("resolve bundle_ref: %w", err)
	}
	if rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return "", fmt.Errorf("bundle_ref escapes work dir: %w", domain.ErrInvalid)
	}
	return root, nil
}

func safeBundleFile(bundleRoot, ref string) (string, error) {
	if filepath.IsAbs(ref) {
		return "", fmt.Errorf("bundle file ref must be relative: %w", domain.ErrInvalid)
	}
	path := filepath.Clean(filepath.Join(bundleRoot, filepath.FromSlash(ref)))
	rel, err := filepath.Rel(bundleRoot, path)
	if err != nil {
		return "", fmt.Errorf("resolve bundle file ref: %w", err)
	}
	if rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return "", fmt.Errorf("bundle file ref escapes bundle root: %w", domain.ErrInvalid)
	}
	return path, nil
}
