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
}

type Executor struct {
	Store             store.Store
	WorkspaceKey      string
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

func (e *Executor) heartbeatInterval() time.Duration {
	if e.HeartbeatInterval == 0 {
		return 30 * time.Second
	}
	if e.HeartbeatInterval < 0 {
		return 0
	}
	return e.HeartbeatInterval
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
	if serverRef != "" {
		serverPath, err := safeBundleFile(bundleRoot, serverRef)
		if err != nil {
			return RunRequest{}, err
		}
		if info, err := os.Stat(serverPath); err != nil {
			return RunRequest{}, fmt.Errorf("stat built Flue server: %w", err)
		} else if info.IsDir() {
			return RunRequest{}, fmt.Errorf("built Flue server %q is a directory: %w", serverRef, domain.ErrInvalid)
		}
		sourceRef := manifest["source_bundle_ref"]
		if sourceRef == "" {
			return RunRequest{}, fmt.Errorf("bundle manifest missing source_bundle_ref: %w", domain.ErrInvalid)
		}
		sourcePath, err := safeBundleFile(bundleRoot, sourceRef)
		if err != nil {
			return RunRequest{}, err
		}
		sourceBytes, err := os.ReadFile(sourcePath) //nolint:gosec // path is constrained under bundleRoot by safeBundleFile.
		if err != nil {
			return RunRequest{}, fmt.Errorf("read bundled workflow source: %w", err)
		}
		if got := digestBytes(sourceBytes); got != version.SourceDigest {
			return RunRequest{}, fmt.Errorf("source digest mismatch: got %s want %s: %w", got, version.SourceDigest, domain.ErrInvalid)
		}
		if got, err := digestBundleTree(bundleRoot, manifestBytes); err != nil {
			return RunRequest{}, err
		} else if got != version.BundleDigest {
			return RunRequest{}, fmt.Errorf("bundle digest mismatch: got %s want %s: %w", got, version.BundleDigest, domain.ErrInvalid)
		}
		workflowPath := ""
		if workflowRef := manifest["workflow_ref"]; workflowRef != "" {
			workflowPath, _ = safeBundleFile(bundleRoot, workflowRef)
		}
		return RunRequest{
			Run:          run,
			Version:      version,
			BundleRoot:   bundleRoot,
			WorkflowPath: workflowPath,
			ServerPath:   serverPath,
			Manifest:     manifest,
		}, nil
	}

	workflowRef := manifest["workflow_ref"]
	if workflowRef == "" {
		return RunRequest{}, fmt.Errorf("bundle manifest missing server_ref or workflow_ref: %w", domain.ErrInvalid)
	}
	workflowPath, err := safeBundleFile(bundleRoot, workflowRef)
	if err != nil {
		return RunRequest{}, err
	}
	sourceBytes, err := os.ReadFile(workflowPath) //nolint:gosec // path is constrained under bundleRoot by safeBundleFile.
	if err != nil {
		return RunRequest{}, fmt.Errorf("read bundled workflow: %w", err)
	}
	if got := digestBytes(sourceBytes); got != version.SourceDigest {
		return RunRequest{}, fmt.Errorf("source digest mismatch: got %s want %s: %w", got, version.SourceDigest, domain.ErrInvalid)
	}
	if got := digestBundleLegacy(manifestBytes, sourceBytes); got != version.BundleDigest {
		return RunRequest{}, fmt.Errorf("bundle digest mismatch: got %s want %s: %w", got, version.BundleDigest, domain.ErrInvalid)
	}
	return RunRequest{
		Run:          run,
		Version:      version,
		BundleRoot:   bundleRoot,
		WorkflowPath: workflowPath,
		Manifest:     manifest,
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
	input, err := json.Marshal(req.Run.Input)
	if err != nil {
		return RunResult{}, fmt.Errorf("encode driver input: %w", err)
	}
	if req.ServerPath != "" {
		return r.runBuiltFlueServer(ctx, req, node, input)
	}
	bootstrap, err := os.CreateTemp("", "loom-driver-runtime-*.mjs")
	if err != nil {
		return RunResult{}, fmt.Errorf("create driver runtime bootstrap: %w", err)
	}
	bootstrapPath := bootstrap.Name()
	defer func() { _ = os.Remove(bootstrapPath) }()
	if _, err := bootstrap.WriteString(nodeRuntimeBootstrap); err != nil {
		_ = bootstrap.Close()
		return RunResult{}, fmt.Errorf("write driver runtime bootstrap: %w", err)
	}
	if err := bootstrap.Close(); err != nil {
		return RunResult{}, fmt.Errorf("close driver runtime bootstrap: %w", err)
	}
	execTaskCommand, err := r.execTaskCommand()
	if err != nil {
		return RunResult{}, err
	}
	env := append(driverRuntimeBaseEnv(os.Environ()),
		"LOOM_DRIVER_WORKSPACE="+req.Run.WorkspaceKey,
		"LOOM_DRIVER_WORKFLOW="+req.WorkflowPath,
		"LOOM_DRIVER_ENTRYPOINT="+entrypoint(req),
		"LOOM_DRIVER_INPUT="+string(input),
		"LOOM_DRIVER_RUN_ID="+req.Run.RunID,
		"LOOM_DRIVER_NODE_ID="+req.Run.NodeID,
		"LOOM_DRIVER_LEASE_ID="+req.Run.LeaseID,
		fmt.Sprintf("LOOM_DRIVER_FENCING_TOKEN=%d", req.Run.FencingToken),
	)
	if len(execTaskCommand) > 0 {
		encoded, err := json.Marshal(execTaskCommand)
		if err != nil {
			return RunResult{}, fmt.Errorf("encode exec-task command: %w", err)
		}
		env = append(env, "LOOM_DRIVER_EXEC_TASK_CMD_JSON="+string(encoded))
	}
	cmd := exec.CommandContext(ctx, node, "--experimental-strip-types", bootstrapPath)
	cmd.Dir = req.BundleRoot
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return RunResult{Status: domain.DriverRunFailed, Summary: msg, ErrorClass: "driver_runtime"}, nil
	}
	out := strings.TrimSpace(stdout.String())
	if out == "" {
		return RunResult{Status: domain.DriverRunCompleted, Summary: "completed"}, nil
	}
	lines := strings.Split(out, "\n")
	var payload struct {
		Status     domain.DriverRunStatus `json:"status"`
		Summary    string                 `json:"summary"`
		ErrorClass string                 `json:"errorClass"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &payload); err != nil {
		return RunResult{}, fmt.Errorf("decode driver runtime result: %w", err)
	}
	return RunResult{Status: payload.Status, Summary: payload.Summary, ErrorClass: payload.ErrorClass}, nil
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
	env := append(driverRuntimeBaseEnv(os.Environ()),
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
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return RunResult{Status: domain.DriverRunFailed, Summary: msg, ErrorClass: "driver_runtime"}, nil
	}
	out := strings.TrimSpace(stdout.String())
	if out == "" {
		return RunResult{Status: domain.DriverRunCompleted, Summary: "completed"}, nil
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
	return RunResult{Status: payload.Status, Summary: payload.Summary, ErrorClass: payload.ErrorClass}, nil
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

func entrypoint(req RunRequest) string {
	if req.Run.Entrypoint != "" {
		return req.Run.Entrypoint
	}
	if req.Manifest["entrypoint"] != "" {
		return req.Manifest["entrypoint"]
	}
	return EntrypointRun
}

func workflowName(req RunRequest) string {
	if req.Manifest["workflow_name"] != "" {
		return req.Manifest["workflow_name"]
	}
	return strings.TrimSuffix(filepath.Base(req.WorkflowPath), filepath.Ext(req.WorkflowPath))
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

child.on('exit', (code, signal) => {
  if (completed) return;
  finish({
    status: 'failed',
    summary: 'Flue local runner exited before result' + (signal ? ' signal=' + signal : ' code=' + code),
    errorClass: 'driver_runtime',
  });
});
`

const nodeRuntimeBootstrap = `
globalThis.defineDriver = (definition) => definition;

const workflowPath = process.env.LOOM_DRIVER_WORKFLOW;
const entrypoint = process.env.LOOM_DRIVER_ENTRYPOINT || 'run';
const input = JSON.parse(process.env.LOOM_DRIVER_INPUT || '{}');
const workspace = process.env.LOOM_DRIVER_WORKSPACE || '';
const driverRunId = process.env.LOOM_DRIVER_RUN_ID || '';
const taskRunResultsByTaskId = new Map();
const taskRunResultsByRunId = new Map();

function complete(payload = {}) {
  return { status: 'completed', summary: payload.summary || 'completed' };
}

function failed(payload = {}) {
  return { status: 'failed', summary: payload.summary || 'failed', errorClass: payload.errorClass || 'driver_failed' };
}

function needsHuman(payload = {}) {
  return { status: 'needs_human', summary: payload.summary || 'needs human', errorClass: payload.errorClass || 'needs_human' };
}

function execTaskCommand() {
  const jsonCommand = process.env.LOOM_DRIVER_EXEC_TASK_CMD_JSON || '';
  if (jsonCommand) {
    const parsed = JSON.parse(jsonCommand);
    if (!Array.isArray(parsed) || parsed.length === 0) {
      throw new Error('LOOM_DRIVER_EXEC_TASK_CMD_JSON must be a non-empty string array');
    }
    return parsed.map(String);
  }
  const command = process.env.LOOM_DRIVER_EXEC_TASK_CMD || 'loom';
  return [command];
}

async function requestTaskRun(payload = {}) {
  const taskId = payload.taskId || payload.task_id;
  if (!taskId) {
    throw new Error('ctx.taskRuns.request requires taskId');
  }
  if (!driverRunId) {
    throw new Error('ctx.taskRuns.request requires LOOM_DRIVER_RUN_ID');
  }
  const providerProfile = payload.providerProfile || payload.provider_profile || '';
  const taskRunId = payload.taskRunId || payload.task_run_id || '';
  const workerProfileId = payload.workerProfileId || payload.worker_profile_id || '';
  const runnerId = payload.runnerId || payload.runner_id || '';
  const supportedProviders = payload.supportedProviders || payload.supported_providers || [];
  const capabilities = payload.capabilities || [];
  const sandboxPlacement = payload.sandboxPlacement || payload.sandbox_placement || {};
  const command = execTaskCommand();
  const args = command.slice(1).concat([
    'driver',
    'exec-task',
    '--driver-run-id',
    driverRunId,
    '--task-id',
    String(taskId),
    '--provider-profile',
    String(providerProfile),
    '--json',
  ]);
  if (workspace) {
    args.push('--workspace-key', workspace);
  }
  if (taskRunId) {
    args.push('--task-run-id', String(taskRunId));
  }
  if (workerProfileId) {
    args.push('--worker-profile-id', String(workerProfileId));
  }
  if (runnerId) {
    args.push('--runner-id', String(runnerId));
  }
  appendRepeatedFlag(args, '--supported-provider', supportedProviders);
  appendRepeatedFlag(args, '--capability', capabilities);
  appendStringFlag(args, '--sandbox-provider', sandboxPlacement.provider || payload.sandboxProvider || payload.sandbox_provider || '');
  appendStringFlag(args, '--sandbox-id', sandboxPlacement.sandbox_id || sandboxPlacement.sandboxId || payload.sandboxId || payload.sandbox_id || '');
  appendStringFlag(args, '--sandbox-cwd', sandboxPlacement.cwd || payload.sandboxCwd || payload.sandbox_cwd || '');
  appendStringFlag(args, '--sandbox-repo-ref', sandboxPlacement.repo_ref || sandboxPlacement.repoRef || payload.sandboxRepoRef || payload.sandbox_repo_ref || '');
  args.push('--defer-completion');
  const { spawnSync } = await import('node:child_process');
  const proc = spawnSync(command[0], args, { encoding: 'utf8', env: process.env });
  if (proc.error) {
    throw proc.error;
  }
  if (proc.status !== 0) {
    const detail = (proc.stderr || proc.stdout || '').trim();
    throw new Error('loom driver exec-task failed' + (detail ? ': ' + detail : ''));
  }
  const stdout = (proc.stdout || '').trim();
  if (!stdout) {
    throw new Error('loom driver exec-task returned empty output');
  }
  const result = JSON.parse(stdout);
  rememberTaskRunResult(result);
  return result;
}

function appendStringFlag(args, flag, value) {
  if (value !== undefined && value !== null && String(value).trim() !== '') {
    args.push(flag, String(value));
  }
}

function appendRepeatedFlag(args, flag, values) {
  const list = Array.isArray(values) ? values : (values ? [values] : []);
  for (const value of list) appendStringFlag(args, flag, value);
}

function rememberTaskRunResult(result = {}) {
  const runId = result.taskRunId || result.task_run_id || result.id || '';
  const taskId = result.taskId || result.task_id || '';
  if (runId) taskRunResultsByRunId.set(String(runId), result);
  if (taskId) taskRunResultsByTaskId.set(String(taskId), result);
}

async function claimReadyTask(payload = {}) {
  if (!driverRunId) {
    throw new Error('ctx.tasks.claimReady requires LOOM_DRIVER_RUN_ID');
  }
  const epicId = payload.epicId || payload.epic_id || input.epicId || '';
  const command = execTaskCommand();
  const args = command.slice(1).concat([
    'driver',
    'claim-ready',
    '--driver-run-id',
    driverRunId,
    '--json',
  ]);
  if (workspace) {
    args.push('--workspace-key', workspace);
  }
  if (epicId) {
    args.push('--epic-id', String(epicId));
  }
  const { spawnSync } = await import('node:child_process');
  const proc = spawnSync(command[0], args, { encoding: 'utf8', env: process.env });
  if (proc.error) {
    throw proc.error;
  }
  if (proc.status !== 0) {
    const detail = (proc.stderr || proc.stdout || '').trim();
    throw new Error('loom driver claim-ready failed' + (detail ? ': ' + detail : ''));
  }
  const stdout = (proc.stdout || '').trim();
  if (!stdout) {
    return null;
  }
  return JSON.parse(stdout);
}

function taskPayloadID(payload) {
  if (typeof payload === 'string') return payload;
  return payload.taskId || payload.task_id || payload.id || '';
}

async function completeTask(payload = {}) {
  const taskId = taskPayloadID(payload);
  const requestedTaskRunId = payload.taskRunId || payload.task_run_id || '';
  const remembered = requestedTaskRunId
    ? taskRunResultsByRunId.get(String(requestedTaskRunId))
    : taskRunResultsByTaskId.get(String(taskId));
  const taskRunId = requestedTaskRunId || remembered?.taskRunId || remembered?.task_run_id || remembered?.id || '';
  if (!taskId && !taskRunId) {
    throw new Error('ctx.tasks.complete requires taskId or taskRunId');
  }
  if (!driverRunId) {
    throw new Error('ctx.tasks.complete requires LOOM_DRIVER_RUN_ID');
  }
  const command = execTaskCommand();
  const args = command.slice(1).concat([
    'driver',
    'complete-task',
    '--driver-run-id',
    driverRunId,
    '--json',
  ]);
  if (taskId) {
    args.push('--task-id', String(taskId));
  }
  if (workspace) {
    args.push('--workspace-key', workspace);
  }
  if (payload.reason) {
    args.push('--reason', String(payload.reason));
  }
  if (taskRunId) {
    args.push('--task-run-id', String(taskRunId));
  }
  const completionId = payload.completionId || payload.completion_id || '';
  if (completionId) {
    args.push('--completion-id', String(completionId));
  }
  const leaseToken = payload.leaseToken || payload.lease_token || process.env.LOOM_TASK_RUN_LEASE_TOKEN || process.env.LOOM_RUNNER_LEASE_TOKEN || '';
  if (leaseToken) {
    args.push('--lease-token', String(leaseToken));
  }
  const logsRef = payload.logsRef || payload.logs_ref || remembered?.logsRef || remembered?.logs_ref || '';
  if (logsRef) {
    args.push('--logs-ref', String(logsRef));
  }
  const artifactsRef = payload.artifactsRef || payload.artifacts_ref || remembered?.artifactsRef || remembered?.artifacts_ref || '';
  if (artifactsRef) {
    args.push('--artifacts-ref', String(artifactsRef));
  }
  const artifactIds = payload.artifactIds || payload.artifact_ids || remembered?.artifactIds || remembered?.artifact_ids || [];
  if (Array.isArray(artifactIds)) {
    for (const artifactId of artifactIds) {
      if (artifactId) args.push('--artifact-id', String(artifactId));
    }
  }
  if (payload.session) {
    args.push('--session', String(payload.session));
  }
  if (payload.force) {
    args.push('--force');
  }
  const { spawnSync } = await import('node:child_process');
  const proc = spawnSync(command[0], args, { encoding: 'utf8', env: process.env });
  if (proc.error) {
    throw proc.error;
  }
  if (proc.status !== 0) {
    const detail = (proc.stderr || proc.stdout || '').trim();
    throw new Error('loom driver complete-task failed' + (detail ? ': ' + detail : ''));
  }
  const stdout = (proc.stdout || '').trim();
  return stdout ? JSON.parse(stdout) : { id: String(taskId) };
}

async function releaseTask(payload = {}) {
  const taskId = taskPayloadID(payload);
  if (!taskId) {
    throw new Error('ctx.tasks.release requires taskId');
  }
  if (!driverRunId) {
    throw new Error('ctx.tasks.release requires LOOM_DRIVER_RUN_ID');
  }
  const command = execTaskCommand();
  const args = command.slice(1).concat([
    'driver',
    'release-task',
    '--driver-run-id',
    driverRunId,
    '--task-id',
    String(taskId),
    '--json',
  ]);
  if (workspace) {
    args.push('--workspace-key', workspace);
  }
  const { spawnSync } = await import('node:child_process');
  const proc = spawnSync(command[0], args, { encoding: 'utf8', env: process.env });
  if (proc.error) {
    throw proc.error;
  }
  if (proc.status !== 0) {
    const detail = (proc.stderr || proc.stdout || '').trim();
    throw new Error('loom driver release-task failed' + (detail ? ': ' + detail : ''));
  }
  const stdout = (proc.stdout || '').trim();
  return stdout ? JSON.parse(stdout) : { id: String(taskId), released: true };
}

const ctx = {
  input,
  run: { complete, failed, needsHuman },
  tasks: {
    claimReady: claimReadyTask,
    complete: completeTask,
    release: releaseTask,
  },
  taskRuns: {
    request: requestTaskRun,
  },
};

try {
  const { pathToFileURL } = await import('node:url');
  const mod = await import(pathToFileURL(workflowPath).href);
  const entry = mod[entrypoint];
  let result;
  if (typeof entry === 'function') {
    result = await entry(ctx);
  } else if (entry && typeof entry.run === 'function') {
    result = await entry.run(ctx);
  } else {
    throw new Error('driver entrypoint ' + entrypoint + ' is not a function or defineDriver object');
  }
  if (!result) result = complete();
  if (!result.status) result.status = 'completed';
  console.log(JSON.stringify(result));
} catch (err) {
  console.log(JSON.stringify({ status: 'failed', summary: err && err.stack ? err.stack : String(err), errorClass: 'driver_exception' }));
}
`
