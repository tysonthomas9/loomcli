package driver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver/runtypes"
	"github.com/tysonthomas9/loomcli/internal/driver/sandbox"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/trigger"
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
	// InternalEvents, when set, is the shared C14 loopback the run.finished
	// lifecycle emission rides (AW6) — sharing keeps the hop-depth ledger
	// warm across emissions. Nil is fine: emission falls back to a
	// zero-config loopback over Store.
	InternalEvents *trigger.InternalSource
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
	if err := e.ensureNode(ctx, run.WorkspaceKey, nodeID); err != nil {
		return nil, err
	}
	claimed, err := e.Store.DriverRuns().Claim(ctx, run.WorkspaceKey, run.RunID, nodeID, leaseID)
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
		go heartbeatDriverRun(hbCtx, e.Store, claimed, interval, cancelRun)
		go heartbeatExecutorNode(hbCtx, e.Store, claimed.WorkspaceKey, nodeID, e.nodeTTL())
	}
	result.Final, err = e.settleClaimed(ctx, claimed, e.runClaimed(runCtx, workDir, claimed))
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
	result, err := e.Store.DriverRuns().RecoverStale(ctx, ws, recover)
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
		emitRunFinishedEvent(ctx, e.Store, e.InternalEvents, run)
		cascadeCancelChildren(ctx, e.Store, e.InternalEvents, run, 0)
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
func (e *Executor) settleClaimed(ctx context.Context, claimed *domain.DriverRun, result RunResult) (*domain.DriverRun, error) {
	if result.Status != domain.DriverRunSuspendedAwaitingEvent {
		return e.finish(ctx, claimed, result)
	}
	run, err := e.Store.DriverRuns().Get(ctx, claimed.WorkspaceKey, claimed.RunID)
	if err != nil {
		return nil, fmt.Errorf("read suspended driver run: %w", err)
	}
	if run.Status == domain.DriverRunRunning && run.NodeID == claimed.NodeID && run.LeaseID == claimed.LeaseID {
		return e.finish(ctx, claimed, RunResult{
			Status:     domain.DriverRunFailed,
			Summary:    "driver reported suspended but no await suspended the run",
			ErrorClass: "invalid_driver_result",
			Output:     result.Output,
		})
	}
	return run, nil
}

func (e *Executor) finish(ctx context.Context, claimed *domain.DriverRun, result RunResult) (*domain.DriverRun, error) {
	if strings.TrimSpace(result.Summary) == "" {
		result.Summary = string(result.Status)
	}
	final, err := e.Store.DriverRuns().Finish(ctx, claimed.WorkspaceKey, claimed.RunID, store.DriverRunFinish{
		NodeID:       claimed.NodeID,
		LeaseID:      claimed.LeaseID,
		FencingToken: claimed.FencingToken,
		Status:       result.Status,
		Summary:      result.Summary,
		ErrorClass:   result.ErrorClass,
		Output:       result.Output,
	})
	if err != nil {
		if settled, ok := e.settleDisownedFinish(ctx, claimed, err); ok {
			return settled, nil
		}
		return nil, fmt.Errorf("finish driver run: %w", err)
	}
	// Every server-side terminal transition publishes run.finished (AW6):
	// completed, failed, needs_review and cancelled all land here (a
	// cancelled runner reports DriverRunCancelled through the same finish).
	emitRunFinishedEvent(ctx, e.Store, e.InternalEvents, final)
	// Composition cascade (AW10): a terminal parent cancels its queued
	// children and cancel-requests its running ones.
	cascadeCancelChildren(ctx, e.Store, e.InternalEvents, final, 0)
	return final, nil
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
	ttl := e.nodeTTL()
	ownerActor := executorOwnerActor()
	runtimeProvider := domain.RuntimeProviderLocal
	labels := []string{"loom-driver-executor"}
	capabilities := []string{"driver-runner", "task-runner", "flue-local"}
	toolInventory := []string{"loom-driver"}
	drainState := domain.NodeDrainActive
	_, err := e.Store.Nodes().Create(ctx, store.NodeCreate{
		WorkspaceKey:    ws,
		NodeID:          nodeID,
		OwnerActor:      ownerActor,
		RuntimeProvider: runtimeProvider,
		Labels:          labels,
		Capabilities:    capabilities,
		ToolInventory:   toolInventory,
		DrainState:      drainState,
		TTL:             ttl,
	})
	if err == nil {
		return nil
	}
	if errors.Is(err, domain.ErrAlreadyExists) {
		if _, hbErr := e.Store.Nodes().Heartbeat(ctx, ws, nodeID, ttl); hbErr != nil {
			return fmt.Errorf("heartbeat executor node: %w", hbErr)
		}
		if existing, getErr := e.Store.Nodes().Get(ctx, ws, nodeID); getErr == nil {
			labels = mergeNodeStringSet(existing.Labels, labels)
			capabilities = mergeNodeStringSet(existing.Capabilities, capabilities)
			toolInventory = mergeNodeStringSet(existing.ToolInventory, toolInventory)
		}
		if _, updateErr := e.Store.Nodes().Update(ctx, ws, nodeID, store.NodeUpdate{
			OwnerActor:      &ownerActor,
			RuntimeProvider: &runtimeProvider,
			Labels:          &labels,
			Capabilities:    &capabilities,
			ToolInventory:   &toolInventory,
			DrainState:      &drainState,
		}); updateErr != nil {
			return fmt.Errorf("refresh executor node: %w", updateErr)
		}
		return nil
	}
	return fmt.Errorf("register executor node: %w", err)
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
func heartbeatDriverRun(ctx context.Context, s store.Store, claimed *domain.DriverRun, interval time.Duration, onCancelRequested func()) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run, err := s.DriverRuns().Heartbeat(ctx, claimed.WorkspaceKey, claimed.RunID, claimed.NodeID, claimed.LeaseID, claimed.FencingToken)
			if err == nil && run != nil && run.CancelRequestedAt != nil && onCancelRequested != nil {
				onCancelRequested()
			}
		}
	}
}

func heartbeatExecutorNode(ctx context.Context, s store.Store, ws, nodeID string, ttl time.Duration) {
	ticker := time.NewTicker(ttl / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = s.Nodes().Heartbeat(ctx, ws, nodeID, ttl)
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

// LegacyDriverAuthEnvVar names the env switch that keeps the deprecated
// static-bearer auth surface flowing to workflow runtimes that already
// receive a run-scoped token: LOOM_DRIVER_API_TOKEN (node-wide shared bearer,
// cross-run authority) plus the LOOM_DRIVER_LEASE_ID/LOOM_DRIVER_FENCING_TOKEN
// identity vars used by header-quad auth. While enabled, workflow bundles
// built against the pre-token SDK keep authenticating; bundles on the
// token-aware SDK ignore the legacy vars and go token-only.
//
// Deprecated — removal path (§9.5 workflow env lockdown):
//  1. This release: default ON, so loom-dev keeps working at deploy. Rebuild
//     workflow bundles against the token-aware SDK, then set
//     LOOM_DRIVER_LEGACY_AUTH_ENV=0 on serve to lock the env down.
//  2. Next release: the default flips OFF (=1 stays as break-glass).
//  3. The release after: the switch and the legacy export are removed.
//
// Runs without a minted token (no RunTokenKey, e.g. CLI/ops executors) always
// get the legacy env regardless of this switch — no flag-day.
const LegacyDriverAuthEnvVar = "LOOM_DRIVER_LEGACY_AUTH_ENV"

// legacyDriverAuthEnv reports whether this run's workflow env carries the
// legacy static-bearer auth surface. Token-less runs always do (it is their
// only auth); token-carrying runs only while the deprecated
// LegacyDriverAuthEnvVar fallback stays enabled.
func legacyDriverAuthEnv(req RunRequest) bool {
	if strings.TrimSpace(req.RunToken) == "" {
		return true
	}
	return legacyDriverAuthEnvEnabled(os.Getenv(LegacyDriverAuthEnvVar))
}

// legacyDriverAuthEnvEnabled parses the switch value: default ON for one
// release; only an explicit false-y value locks the env down.
func legacyDriverAuthEnvEnabled(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "0", "false", "off", "no":
		return false
	default:
		return true
	}
}

type NodeRunner struct {
	NodePath        string
	ExecTaskCommand []string
	// APIBaseURL, when set, is exported to the driver runtime as
	// LOOM_DRIVER_API_URL so the workflow SDK uses the driver-op HTTP API on
	// loom serve instead of spawning CLI subprocesses.
	APIBaseURL string
	// APIToken is the shared driver API bearer token forwarded as
	// LOOM_DRIVER_API_TOKEN when APIBaseURL is set — but only to runs on the
	// legacy auth surface (no run token, or the deprecated
	// LegacyDriverAuthEnvVar fallback still on). Token-carrying runs are
	// token-only: the static bearer never reaches their workflow env.
	APIToken string //nolint:gosec // G117: driver API bearer token intentionally forwarded to legacy runtimes.
	// Launcher launches the workflow-bundle runtime (SB1 sandbox seam).
	// Nil means the default local node-process launcher — today's
	// flue-local behavior, unchanged.
	Launcher SandboxLauncher
}

func (r NodeRunner) Run(ctx context.Context, req RunRequest) (RunResult, error) {
	node := r.NodePath
	if node == "" {
		node = "node"
	}
	payload := req.Run.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	if !json.Valid(payload) {
		return RunResult{}, fmt.Errorf("driver payload is invalid JSON: %w", domain.ErrInvalid)
	}
	if req.ServerPath == "" {
		return RunResult{}, fmt.Errorf("native Flue server path required: %w", domain.ErrInvalid)
	}
	return r.runBuiltFlueServer(ctx, req, node, payload)
}

func (r NodeRunner) runBuiltFlueServer(ctx context.Context, req RunRequest, node string, input []byte) (RunResult, error) {
	env, err := r.runtimeEnv(req, input)
	if err != nil {
		return RunResult{}, err
	}
	launcher := r.Launcher
	if launcher == nil {
		launcher = processLauncher{NodePath: node}
	}
	// SB3 trust placement policy: an untrusted bundle never launches outside
	// an isolating sandbox — the run fails sandbox_required with no process
	// spawned, never a silent fallback.
	if refusal, refused := sandbox.RefuseUntrustedPlacement(req, launcher); refused {
		return refusal, nil
	}
	process, err := launcher.Launch(ctx, LaunchSpec{
		BundleRoot: req.BundleRoot,
		ServerPath: req.ServerPath,
		WorkDir:    req.BundleRoot,
		Env:        env,
		Manifest:   req.Manifest,
		TrustLevel: req.TrustLevel,
	})
	if err != nil {
		return RunResult{}, err
	}
	exit, waitErr := process.Wait()
	result := flueRuntimeResult(ctx, req, exit.Stdout, exit.Stderr, waitErr)
	sandbox.RecordSandboxPlacement(&result, process.Placement())
	sandbox.RecordTrustPlacementDecision(&result, req.TrustLevel, sandbox.LauncherPlacementProvider(launcher))
	return result, nil
}

// runtimeEnv assembles the complete workflow runtime environment: the
// identity/auth env from flueRuntimeEnv plus the driver-op API endpoint. The
// node-wide static bearer is cross-run authority, so token-carrying runs drop
// it (workflow calls are token-only per the step-9 locked decision) unless
// the deprecated LegacyDriverAuthEnvVar fallback is still on.
func (r NodeRunner) runtimeEnv(req RunRequest, input []byte) ([]string, error) {
	execTaskCommand, err := r.execTaskCommand()
	if err != nil {
		return nil, err
	}
	env, err := flueRuntimeEnv(req, input, execTaskCommand)
	if err != nil {
		return nil, err
	}
	apiBaseURL := strings.TrimSpace(r.APIBaseURL)
	if apiBaseURL == "" {
		return env, nil
	}
	env = append(env, "LOOM_DRIVER_API_URL="+apiBaseURL)
	if apiToken := strings.TrimSpace(r.APIToken); apiToken != "" && legacyDriverAuthEnv(req) {
		env = append(env, "LOOM_DRIVER_API_TOKEN="+apiToken)
	}
	return env, nil
}

func flueRuntimeEnv(req RunRequest, input []byte, execTaskCommand []string) ([]string, error) {
	env := driverRuntimeBaseEnv(os.Environ())
	env = append(env,
		"LOOM_DRIVER_WORKSPACE="+req.Run.WorkspaceKey,
		"LOOM_DRIVER_RUN_ID="+req.Run.RunID,
		"LOOM_DRIVER_NODE_ID="+req.Run.NodeID,
	)
	// Lease identity doubles as auth material under header-quad auth, so a
	// token-carrying run keeps it out of the workflow env (§9.5 lockdown:
	// blast radius = one run x one lease TTL) unless the deprecated
	// LegacyDriverAuthEnvVar fallback is still on.
	if legacyDriverAuthEnv(req) {
		env = append(env,
			"LOOM_DRIVER_LEASE_ID="+req.Run.LeaseID,
			fmt.Sprintf("LOOM_DRIVER_FENCING_TOKEN=%d", req.Run.FencingToken),
		)
	}
	env = append(env,
		"LOOM_FLUE_SERVER_PATH="+req.ServerPath,
		"LOOM_FLUE_BUNDLE_ROOT="+req.BundleRoot,
		"LOOM_FLUE_WORKFLOW_NAME="+workflowName(req),
		"LOOM_FLUE_INVOKE_PAYLOAD="+string(input),
	)
	// Run-scoped bearer token (LOOM_RUN_TOKEN), minted at claim time. The
	// parent-env filter strips any inherited *TOKEN* variable, so the only
	// token a workflow process ever sees is the one minted for its own run.
	if token := strings.TrimSpace(req.RunToken); token != "" {
		env = append(env, "LOOM_RUN_TOKEN="+token)
	}
	if len(execTaskCommand) > 0 {
		encoded, err := json.Marshal(execTaskCommand)
		if err != nil {
			return nil, fmt.Errorf("encode exec-task command: %w", err)
		}
		env = append(env, "LOOM_DRIVER_EXEC_TASK_CMD_JSON="+string(encoded))
	}
	return env, nil
}

func flueRuntimeResult(ctx context.Context, req RunRequest, stdout, stderr string, runErr error) RunResult {
	if runErr != nil {
		return failedFlueRuntimeResult(ctx, req, stdout, stderr, runErr)
	}
	out := strings.TrimSpace(stdout)
	if out == "" {
		return invalidDriverResult(req, "Flue workflow returned no result", stdout, stderr)
	}
	lines := strings.Split(out, "\n")
	var payload struct {
		Status     domain.DriverRunStatus `json:"status"`
		Summary    string                 `json:"summary"`
		ErrorClass string                 `json:"errorClass"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &payload); err != nil {
		return invalidDriverResult(req, fmt.Sprintf("decode Flue runtime result: %v", err), stdout, stderr)
	}
	result := RunResult{Status: payload.Status, Summary: payload.Summary, ErrorClass: payload.ErrorClass, Output: flueRunOutput(req, stdout, stderr)}
	if result.Status == domain.DriverRunFailed {
		result.Summary = flueFailedSummary(result.Summary, stderr)
	}
	return requireExplicitTerminalRunResult(result)
}

func failedFlueRuntimeResult(ctx context.Context, req RunRequest, stdout, stderr string, runErr error) RunResult {
	if ctx.Err() != nil {
		return RunResult{
			Status:     domain.DriverRunCancelled,
			Summary:    "Flue local runner cancelled",
			ErrorClass: "driver_cancelled",
			Output:     flueRunOutput(req, stdout, stderr),
		}
	}
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		msg = runErr.Error()
	}
	return RunResult{Status: domain.DriverRunFailed, Summary: msg, ErrorClass: "driver_runtime", Output: flueRunOutput(req, stdout, stderr)}
}

func flueFailedSummary(summary, stderr string) string {
	detail := strings.TrimSpace(stderr)
	if detail == "" {
		return summary
	}
	if summary != "" {
		return summary + ": " + detail
	}
	return detail
}

func requireExplicitTerminalRunResult(result RunResult) RunResult {
	if result.Status.IsTerminal() {
		return result
	}
	if result.Status == domain.DriverRunSuspendedAwaitingEvent {
		// A suspended report is the runner's clean exit after an await op
		// suspended the run server-side (AW9/AW11): not a terminal result and
		// not an error — settleClaimed acknowledges it without a Finish.
		return result
	}
	detail := "driver result missing terminal status"
	if result.Status != "" {
		detail = fmt.Sprintf("driver result status %q is not terminal", result.Status)
	}
	summary := detail
	if existing := strings.TrimSpace(result.Summary); existing != "" {
		summary += ": " + existing
	}
	result.Status = domain.DriverRunFailed
	result.Summary = summary
	result.ErrorClass = "invalid_driver_result"
	return result
}

func invalidDriverResult(req RunRequest, summary, stdout, stderr string) RunResult {
	return RunResult{
		Status:     domain.DriverRunFailed,
		Summary:    summary,
		ErrorClass: "invalid_driver_result",
		Output:     flueRunOutput(req, stdout, stderr),
	}
}

func flueRunOutput(req RunRequest, stdout, stderr string) map[string]string {
	output := map[string]string{
		"runtime":  RuntimeFlueNode,
		"logs_ref": "driver-run://" + req.Run.RunID + "/flue-local",
	}
	if req.Manifest["artifact_kind"] != "" {
		output["artifact_kind"] = req.Manifest["artifact_kind"]
	}
	if req.Manifest["workflow_name"] != "" {
		output["workflow_name"] = req.Manifest["workflow_name"]
	}
	if tail := textTail(stderr, 4096); tail != "" {
		output["flue_stderr_tail"] = tail
	}
	if tail := textTail(stdout, 4096); tail != "" {
		output["flue_stdout_tail"] = tail
	}
	if count := nonEmptyLineCount(stdout) + nonEmptyLineCount(stderr); count > 0 {
		output["flue_event_count"] = strconv.Itoa(count)
	}
	return output
}

func textTail(value string, maxBytes int) string {
	value = strings.TrimSpace(value)
	if value == "" || maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	return value[len(value)-maxBytes:]
}

func nonEmptyLineCount(value string) int {
	count := 0
	for _, line := range strings.Split(value, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

func (r NodeRunner) execTaskCommand() ([]string, error) {
	if len(r.ExecTaskCommand) > 0 {
		return append([]string(nil), r.ExecTaskCommand...), nil
	}
	if os.Getenv("LOOM_DRIVER_EXEC_TASK_CMD_JSON") != "" || os.Getenv("LOOM_DRIVER_EXEC_TASK_CMD") != "" {
		return nil, nil
	}
	executable, err := os.Executable()
	if err == nil && executable != "" {
		return []string{executable}, nil
	}
	if os.Args[0] != "" {
		return []string{os.Args[0]}, nil
	}
	return nil, fmt.Errorf("resolve exec-task command: %w", err)
}

func workflowName(req RunRequest) string {
	if req.Manifest["workflow_name"] != "" {
		return req.Manifest["workflow_name"]
	}
	if req.Run != nil && req.Run.DriverID != "" {
		return req.Run.DriverID
	}
	return EntrypointRun
}
