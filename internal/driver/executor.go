package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

var ErrNoQueuedRun = errors.New("driver executor: no queued run")

type Runner interface {
	Run(ctx context.Context, req RunRequest) (RunResult, error)
}

type RunRequest struct {
	Run          *domain.DriverRun
	Version      *domain.DriverVersion
	BundleRoot   string
	WorkflowPath string
	ServerPath   string
	Manifest     map[string]string
}

type RunResult struct {
	Status     domain.DriverRunStatus
	Summary    string
	ErrorClass string
	Output     map[string]string
}

type Executor struct {
	Store             store.Store
	WorkspaceKey      string
	RunID             string
	WorkDir           string
	NodeID            string
	LeaseID           string
	Runner            Runner
	PollInterval      time.Duration
	HeartbeatInterval time.Duration
	StaleRunMaxAge    time.Duration
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
	runner := e.Runner
	if runner == nil {
		runner = NodeRunner{}
	}
	hbCtx, stopHeartbeat := context.WithCancel(ctx)
	defer stopHeartbeat()
	if interval := e.heartbeatInterval(); interval > 0 {
		go heartbeatDriverRun(hbCtx, e.Store, claimed, interval)
		go heartbeatExecutorNode(hbCtx, e.Store, claimed.WorkspaceKey, nodeID, e.nodeTTL())
	}
	req, err := loadRunRequest(ctx, workDir, claimed, e.Store)
	if err != nil {
		result.Final, err = e.finish(ctx, claimed, RunResult{Status: domain.DriverRunFailed, Summary: err.Error(), ErrorClass: "bundle_verification"})
		if err != nil {
			return result, err
		}
		return result, nil
	}
	runResult, runErr := runner.Run(ctx, req)
	if runErr != nil {
		runResult = RunResult{Status: domain.DriverRunFailed, Summary: runErr.Error(), ErrorClass: "driver_runtime"}
	}
	if !runResult.Status.IsTerminal() {
		runResult.Status = domain.DriverRunCompleted
	}
	result.Final, err = e.finish(ctx, claimed, runResult)
	if err != nil {
		return result, err
	}
	return result, nil
}

func (e *Executor) RunLoop(ctx context.Context) error {
	interval := e.PollInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			if _, err := e.RecoverStaleOnce(ctx); err != nil {
				return err
			}
			_, err := e.RunOnce(ctx)
			if err != nil && !errors.Is(err, ErrNoQueuedRun) {
				return err
			}
			timer.Reset(interval)
		}
	}
}

func (e *Executor) RecoverStaleOnce(ctx context.Context) (*store.StaleDriverRunRecoveryResult, error) {
	if e == nil || e.Store == nil {
		return nil, fmt.Errorf("store required: %w", domain.ErrInvalid)
	}
	maxAge := e.staleRunMaxAge()
	if maxAge < 0 {
		return &store.StaleDriverRunRecoveryResult{}, nil
	}
	recover := store.StaleDriverRunRecovery{
		MaxAgeSeconds: int64(maxAge / time.Second),
		Summary:       "driver executor heartbeat expired",
	}
	if e.WorkspaceKey != "" {
		return e.Store.DriverRuns().RecoverStale(ctx, e.WorkspaceKey, recover)
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
		result, err := e.Store.DriverRuns().RecoverStale(ctx, ws.Key, recover)
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
		return nil, fmt.Errorf("finish driver run: %w", err)
	}
	return final, nil
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
	_, err := e.Store.Nodes().Create(ctx, store.NodeCreate{
		WorkspaceKey:    ws,
		NodeID:          nodeID,
		OwnerActor:      executorOwnerActor(),
		RuntimeProvider: domain.RuntimeProviderLocal,
		Labels:          []string{"loom-driver-executor"},
		Capabilities:    []string{"driver-runner", "task-runner", "flue-local"},
		ToolInventory:   []string{"loom-driver"},
		DrainState:      domain.NodeDrainActive,
		TTL:             ttl,
	})
	if err == nil {
		return nil
	}
	if errors.Is(err, domain.ErrAlreadyExists) {
		if _, hbErr := e.Store.Nodes().Heartbeat(ctx, ws, nodeID, ttl); hbErr != nil {
			return fmt.Errorf("heartbeat executor node: %w", hbErr)
		}
		return nil
	}
	return fmt.Errorf("register executor node: %w", err)
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

func (e *Executor) staleRunMaxAge() time.Duration {
	if e.StaleRunMaxAge == 0 {
		return 5 * time.Minute
	}
	return e.StaleRunMaxAge
}

func heartbeatDriverRun(ctx context.Context, s store.Store, claimed *domain.DriverRun, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = s.DriverRuns().Heartbeat(ctx, claimed.WorkspaceKey, claimed.RunID, claimed.NodeID, claimed.LeaseID, claimed.FencingToken)
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
	manifestPath := filepath.Join(bundleRoot, "manifest.json")
	manifestBytes, err := os.ReadFile(manifestPath) //nolint:gosec // path is constrained under workDir by safeBundleRoot.
	if err != nil {
		return RunRequest{}, fmt.Errorf("read bundle manifest: %w", err)
	}
	manifest := map[string]string{}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return RunRequest{}, fmt.Errorf("decode bundle manifest: %w", err)
	}
	serverRef := manifest["server_ref"]
	if serverRef == "" {
		return RunRequest{}, fmt.Errorf("native Flue bundle manifest missing server_ref: %w", domain.ErrInvalid)
	}
	serverPath, err := safeBundleFile(bundleRoot, serverRef)
	if err != nil {
		return RunRequest{}, err
	}
	if info, err := os.Stat(serverPath); err != nil {
		return RunRequest{}, fmt.Errorf("stat built Flue server: %w", err)
	} else if info.IsDir() {
		return RunRequest{}, fmt.Errorf("built Flue server %q is a directory: %w", serverRef, domain.ErrInvalid)
	}
	if got, err := digestBundleTree(bundleRoot, manifestBytes); err != nil {
		return RunRequest{}, err
	} else if got != version.BundleDigest {
		return RunRequest{}, fmt.Errorf("bundle digest mismatch: got %s want %s: %w", got, version.BundleDigest, domain.ErrInvalid)
	}
	return RunRequest{
		Run:        run,
		Version:    version,
		BundleRoot: bundleRoot,
		ServerPath: serverPath,
		Manifest:   manifest,
	}, nil
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

type NodeRunner struct {
	NodePath        string
	ExecTaskCommand []string
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
	launcher, err := os.CreateTemp("", "loom-flue-runtime-*.mjs")
	if err != nil {
		return RunResult{}, fmt.Errorf("create Flue runtime launcher: %w", err)
	}
	launcherPath := launcher.Name()
	defer func() { _ = os.Remove(launcherPath) }()
	if _, err := launcher.WriteString(flueLocalLauncher); err != nil {
		_ = launcher.Close()
		return RunResult{}, fmt.Errorf("write Flue runtime launcher: %w", err)
	}
	if err := launcher.Close(); err != nil {
		return RunResult{}, fmt.Errorf("close Flue runtime launcher: %w", err)
	}
	execTaskCommand, err := r.execTaskCommand()
	if err != nil {
		return RunResult{}, err
	}
	parentEnv := os.Environ()
	env := append(driverRuntimeBaseEnv(parentEnv), driverRuntimeFleetDBHandoffEnv(parentEnv)...)
	env = append(env,
		"LOOM_DRIVER_WORKSPACE="+req.Run.WorkspaceKey,
		"LOOM_DRIVER_RUN_ID="+req.Run.RunID,
		"LOOM_DRIVER_NODE_ID="+req.Run.NodeID,
		"LOOM_DRIVER_LEASE_ID="+req.Run.LeaseID,
		fmt.Sprintf("LOOM_DRIVER_FENCING_TOKEN=%d", req.Run.FencingToken),
		"LOOM_FLUE_SERVER_PATH="+req.ServerPath,
		"LOOM_FLUE_BUNDLE_ROOT="+req.BundleRoot,
		"LOOM_FLUE_WORKFLOW_NAME="+workflowName(req),
		"LOOM_FLUE_INVOKE_PAYLOAD="+string(input),
	)
	if len(execTaskCommand) > 0 {
		encoded, err := json.Marshal(execTaskCommand)
		if err != nil {
			return RunResult{}, fmt.Errorf("encode exec-task command: %w", err)
		}
		env = append(env, "LOOM_DRIVER_EXEC_TASK_CMD_JSON="+string(encoded))
	}
	cmd := exec.CommandContext(ctx, node, launcherPath)
	cmd.Dir = req.BundleRoot
	cmd.Env = env
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Signal(os.Interrupt)
	}
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return RunResult{
				Status:     domain.DriverRunCancelled,
				Summary:    "Flue local runner cancelled",
				ErrorClass: "driver_cancelled",
				Output:     flueRunOutput(req, stdout.String(), stderr.String()),
			}, nil
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return RunResult{Status: domain.DriverRunFailed, Summary: msg, ErrorClass: "driver_runtime", Output: flueRunOutput(req, stdout.String(), stderr.String())}, nil
	}
	out := strings.TrimSpace(stdout.String())
	if out == "" {
		return RunResult{Status: domain.DriverRunCompleted, Summary: "completed", Output: flueRunOutput(req, stdout.String(), stderr.String())}, nil
	}
	lines := strings.Split(out, "\n")
	var payload struct {
		Status     domain.DriverRunStatus `json:"status"`
		Summary    string                 `json:"summary"`
		ErrorClass string                 `json:"errorClass"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &payload); err != nil {
		return RunResult{}, fmt.Errorf("decode Flue runtime result: %w", err)
	}
	result := RunResult{Status: payload.Status, Summary: payload.Summary, ErrorClass: payload.ErrorClass, Output: flueRunOutput(req, stdout.String(), stderr.String())}
	if result.Status == domain.DriverRunFailed {
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			if result.Summary != "" {
				result.Summary += ": " + detail
			} else {
				result.Summary = detail
			}
		}
	}
	return result, nil
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

const flueLocalLauncher = `
import { fork } from 'node:child_process';

const serverPath = process.env.LOOM_FLUE_SERVER_PATH;
const bundleRoot = process.env.LOOM_FLUE_BUNDLE_ROOT || process.cwd();
const workflowName = process.env.LOOM_FLUE_WORKFLOW_NAME;
const payload = JSON.parse(process.env.LOOM_FLUE_INVOKE_PAYLOAD || '{}');
const requestId = process.env.LOOM_DRIVER_RUN_ID || 'loom-driver-run';

if (!serverPath || !workflowName) {
  console.log(JSON.stringify({ status: 'failed', summary: 'missing Flue server path or workflow name', errorClass: 'driver_runtime' }));
  process.exit(0);
}

let completed = false;
let invoked = false;
const child = fork(serverPath, [], {
  cwd: bundleRoot,
  env: {
    ...process.env,
    FLUE_MODE: 'local',
    FLUE_CLI_TARGET: 'workflow',
    FLUE_CLI_NAME: workflowName,
  },
  stdio: ['ignore', 'pipe', 'pipe', 'ipc'],
});

child.stdout?.on('data', (data) => process.stderr.write(data));
child.stderr?.on('data', (data) => process.stderr.write(data));

function finish(result) {
  if (completed) return;
  completed = true;
  console.log(JSON.stringify(result || {}));
  try { child.disconnect(); } catch {}
}

child.on('message', (message) => {
  if (!message || typeof message !== 'object') return;
  if (message.type === 'ready' && !invoked) {
    invoked = true;
    child.send({ version: 1, type: 'invoke', requestId, payload });
    return;
  }
  if (message.type === 'result') {
    finish(message.result || {});
    return;
  }
  if (message.type === 'error') {
    const error = message.error || {};
    finish({
      status: 'failed',
      summary: error.message || error.details || 'Flue workflow failed',
      errorClass: error.type || 'driver_runtime',
    });
  }
});

child.on('error', (error) => {
  finish({ status: 'failed', summary: error?.message || String(error), errorClass: 'driver_runtime' });
});

function shutdown(signal) {
  if (completed) return;
  completed = true;
  try { child.kill(signal); } catch {}
  setTimeout(() => {
    try { child.kill('SIGKILL'); } catch {}
  }, 1000).unref?.();
  console.log(JSON.stringify({
    status: 'cancelled',
    summary: 'Flue local runner cancelled',
    errorClass: 'driver_cancelled',
  }));
  process.exit(signal === 'SIGINT' ? 130 : 143);
}

process.once('SIGINT', () => shutdown('SIGINT'));
process.once('SIGTERM', () => shutdown('SIGTERM'));

child.on('exit', (code, signal) => {
  if (completed) return;
  finish({
    status: 'failed',
    summary: 'Flue local runner exited before result' + (signal ? ' signal=' + signal : ' code=' + code),
    errorClass: 'driver_runtime',
  });
});
`
