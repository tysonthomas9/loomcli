//nolint:revive // Tests use the established driver package name to exercise unexported helpers.
package driver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestExecutorRunOnceClaimsVerifiesAndFinishes(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	writeFlueDist(t, root, "epic-runner", "done")
	registered, err := RegisterFlueDriver(ctx, st, RegisterFlueOptions{WorkspaceKey: "TEST", WorkDir: root, DistPath: "dist", DriverName: "epic-runner", CreatedBy: "tester", Activate: true})
	if err != nil {
		t.Fatalf("RegisterFlueDriver: %v", err)
	}
	run, err := CreateDriverRun(ctx, st, RunOptions{
		WorkspaceKey:   "TEST",
		DriverID:       registered.Driver.DriverID,
		EpicID:         "TEST-1",
		RunID:          "run-1",
		IdempotencyKey: "idem-1",
	})
	if err != nil {
		t.Fatalf("CreateDriverRun: %v", err)
	}

	runner := &recordingRunner{result: RunResult{Status: domain.DriverRunCompleted, Summary: "driver completed"}}
	result, err := (&Executor{
		Store:             st,
		WorkspaceKey:      "TEST",
		WorkDir:           root,
		NodeID:            "node-1",
		LeaseID:           "lease-1",
		Runner:            runner,
		HeartbeatInterval: -1,
	}).RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Claimed == nil || result.Claimed.Status != domain.DriverRunRunning || result.Claimed.NodeID != "node-1" {
		t.Fatalf("claimed = %+v, want running node-1", result.Claimed)
	}
	if result.Final == nil || result.Final.Status != domain.DriverRunCompleted || result.Final.Summary != "driver completed" {
		t.Fatalf("final = %+v, want completed summary", result.Final)
	}
	if runner.calls != 1 {
		t.Fatalf("runner calls = %d, want 1", runner.calls)
	}
	if runner.req.Run.RunID != run.RunID || runner.req.Version.VersionID != registered.Version.VersionID {
		t.Fatalf("runner req = %+v version=%+v, want pinned run/version", runner.req.Run, runner.req.Version)
	}
	if runner.req.WorkflowPath != "" {
		t.Fatalf("runner workflow path = %q, want native Flue server only", runner.req.WorkflowPath)
	}
	if _, err := os.Stat(runner.req.ServerPath); err != nil {
		t.Fatalf("runner server path missing: %v", err)
	}
	stored, err := st.DriverRuns().Get(ctx, "TEST", "run-1")
	if err != nil {
		t.Fatalf("Get stored run: %v", err)
	}
	if stored.Status != domain.DriverRunCompleted || stored.FencingToken == 0 {
		t.Fatalf("stored run = %+v, want completed with fencing token", stored)
	}
	node, err := st.Nodes().Get(ctx, "TEST", "node-1")
	if err != nil {
		t.Fatalf("Get executor node: %v", err)
	}
	if node.DrainState != domain.NodeDrainActive || node.RuntimeProvider != domain.RuntimeProviderLocal {
		t.Fatalf("executor node = %+v, want active local node", node)
	}
}

func TestExecutorRunOnceTargetsSpecificQueuedRunID(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	writeFlueDist(t, root, "epic-runner", "done")
	registered, err := RegisterFlueDriver(ctx, st, RegisterFlueOptions{WorkspaceKey: "TEST", WorkDir: root, DistPath: "dist", DriverName: "epic-runner", CreatedBy: "tester", Activate: true})
	if err != nil {
		t.Fatalf("RegisterFlueDriver: %v", err)
	}
	if _, err := CreateDriverRun(ctx, st, RunOptions{WorkspaceKey: "TEST", DriverID: registered.Driver.DriverID, EpicID: "TEST-1", RunID: "run-1"}); err != nil {
		t.Fatalf("Create first DriverRun: %v", err)
	}
	if _, err := CreateDriverRun(ctx, st, RunOptions{WorkspaceKey: "TEST", DriverID: registered.Driver.DriverID, EpicID: "TEST-2", RunID: "run-2"}); err != nil {
		t.Fatalf("Create second DriverRun: %v", err)
	}

	runner := &recordingRunner{result: RunResult{Status: domain.DriverRunCompleted, Summary: "targeted"}}
	result, err := (&Executor{
		Store:             st,
		WorkspaceKey:      "TEST",
		RunID:             "run-2",
		WorkDir:           root,
		NodeID:            "node-1",
		LeaseID:           "lease-2",
		Runner:            runner,
		HeartbeatInterval: -1,
	}).RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Final == nil || result.Final.RunID != "run-2" || result.Final.Status != domain.DriverRunCompleted {
		t.Fatalf("final = %+v, want completed run-2", result.Final)
	}
	if runner.req.Run == nil || runner.req.Run.RunID != "run-2" {
		t.Fatalf("runner saw run %+v, want run-2", runner.req.Run)
	}
	untouched, err := st.DriverRuns().Get(ctx, "TEST", "run-1")
	if err != nil {
		t.Fatalf("Get run-1: %v", err)
	}
	if untouched.Status != domain.DriverRunQueued {
		t.Fatalf("run-1 status = %s, want queued", untouched.Status)
	}
}

func TestExecutorRunOnceFailsNonTerminalRunnerResult(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	writeFlueDist(t, root, "epic-runner", "done")
	registered, err := RegisterFlueDriver(ctx, st, RegisterFlueOptions{WorkspaceKey: "TEST", WorkDir: root, DistPath: "dist", DriverName: "epic-runner", CreatedBy: "tester", Activate: true})
	if err != nil {
		t.Fatalf("RegisterFlueDriver: %v", err)
	}
	if _, err := CreateDriverRun(ctx, st, RunOptions{
		WorkspaceKey: "TEST",
		DriverID:     registered.Driver.DriverID,
		RunID:        "run-1",
	}); err != nil {
		t.Fatalf("CreateDriverRun: %v", err)
	}

	result, err := (&Executor{
		Store:             st,
		WorkspaceKey:      "TEST",
		WorkDir:           root,
		NodeID:            "node-1",
		LeaseID:           "lease-1",
		Runner:            &recordingRunner{result: RunResult{Status: domain.DriverRunRunning, Summary: "still working"}},
		HeartbeatInterval: -1,
	}).RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Final == nil || result.Final.Status != domain.DriverRunFailed || result.Final.ErrorClass != "invalid_driver_result" {
		t.Fatalf("final = %+v, want failed invalid_driver_result", result.Final)
	}
	if result.Final.Summary != `driver result status "running" is not terminal: still working` {
		t.Fatalf("summary = %q, want non-terminal status detail", result.Final.Summary)
	}
}

func TestExecutorRunOnceFailsTamperedBundleBeforeRunner(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	writeFlueDist(t, root, "epic-runner", "done")
	registered, err := RegisterFlueDriver(ctx, st, RegisterFlueOptions{WorkspaceKey: "TEST", WorkDir: root, DistPath: "dist", DriverName: "epic-runner", CreatedBy: "tester", Activate: true})
	if err != nil {
		t.Fatalf("RegisterFlueDriver: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(registered.Version.BundleRef), "dist", "server.mjs"), []byte("tampered\n"), 0o644); err != nil {
		t.Fatalf("tamper bundle: %v", err)
	}
	if _, err := CreateDriverRun(ctx, st, RunOptions{WorkspaceKey: "TEST", DriverID: registered.Driver.DriverID, EpicID: "TEST-1", RunID: "run-1"}); err != nil {
		t.Fatalf("CreateDriverRun: %v", err)
	}

	runner := &recordingRunner{result: RunResult{Status: domain.DriverRunCompleted}}
	result, err := (&Executor{
		Store:             st,
		WorkspaceKey:      "TEST",
		WorkDir:           root,
		NodeID:            "node-1",
		LeaseID:           "lease-1",
		Runner:            runner,
		HeartbeatInterval: -1,
	}).RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if runner.calls != 0 {
		t.Fatalf("runner calls = %d, want 0 for tampered bundle", runner.calls)
	}
	if result.Final == nil || result.Final.Status != domain.DriverRunFailed || result.Final.ErrorClass != "bundle_verification" {
		t.Fatalf("final = %+v, want failed bundle_verification", result.Final)
	}
}

func TestNodeRunnerRunsBuiltFlueServer(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not available: %v", err)
	}
	t.Setenv("LOOM_FLEET_DB_URL", "https://fleet.invalid")
	t.Setenv("LOOM_FLEET_DB_API_KEY", "broad-secret")
	t.Setenv("LOOM_TASK_RUN_LEASE_TOKEN", "task-run-token")
	t.Setenv("OPENAI_API_KEY", "model-secret")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "aws-secret")
	t.Setenv("GITHUB_TOKEN", "github-secret")

	ctx := context.Background()
	root := t.TempDir()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	writeFlueDist(t, root, "epic-runner", "fake flue")
	registered, err := RegisterFlueDriver(ctx, st, RegisterFlueOptions{WorkspaceKey: "TEST", WorkDir: root, DistPath: "dist", DriverName: "epic-runner", CreatedBy: "tester", Activate: true})
	if err != nil {
		t.Fatalf("RegisterFlueDriver: %v", err)
	}
	run, err := CreateDriverRun(ctx, st, RunOptions{WorkspaceKey: "TEST", DriverID: registered.Driver.DriverID, EpicID: "TEST-1", RunID: "run-1"})
	if err != nil {
		t.Fatalf("CreateDriverRun: %v", err)
	}
	claimed, err := st.DriverRuns().Claim(ctx, "TEST", run.RunID, "node-1", "lease-1")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	req, err := loadRunRequest(ctx, root, claimed, st)
	if err != nil {
		t.Fatalf("loadRunRequest: %v", err)
	}
	result, err := (NodeRunner{}).Run(ctx, req)
	if err != nil {
		t.Fatalf("NodeRunner.Run: %v", err)
	}
	if result.Status != domain.DriverRunCompleted || result.Summary != "fake flue" {
		t.Fatalf("result = %+v, want completed fake flue", result)
	}
}

func TestNodeRunnerRunsRegisteredNativeFlueArtifact(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not available: %v", err)
	}

	ctx := context.Background()
	root := t.TempDir()
	writeFlueDist(t, root, "epic-runner", "native flue")
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	registered, err := RegisterFlueDriver(ctx, st, RegisterFlueOptions{
		WorkspaceKey: "TEST",
		WorkDir:      root,
		DistPath:     "dist",
		DriverName:   "epic-runner",
		Activate:     true,
		CreatedBy:    "tester",
	})
	if err != nil {
		t.Fatalf("RegisterFlueDriver: %v", err)
	}
	run, err := CreateDriverRun(ctx, st, RunOptions{WorkspaceKey: "TEST", DriverID: registered.Driver.DriverID, EpicID: "TEST-1", RunID: "run-1"})
	if err != nil {
		t.Fatalf("CreateDriverRun: %v", err)
	}
	claimed, err := st.DriverRuns().Claim(ctx, "TEST", run.RunID, "node-1", "lease-1")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	req, err := loadRunRequest(ctx, root, claimed, st)
	if err != nil {
		t.Fatalf("loadRunRequest: %v", err)
	}
	if req.WorkflowPath != "" {
		t.Fatalf("WorkflowPath = %q, want no generated workflow path", req.WorkflowPath)
	}
	result, err := (NodeRunner{}).Run(ctx, req)
	if err != nil {
		t.Fatalf("NodeRunner.Run: %v", err)
	}
	if result.Status != domain.DriverRunCompleted || result.Summary != "native flue" {
		t.Fatalf("result = %+v, want completed native flue", result)
	}
	if result.Output["logs_ref"] != "driver-run://run-1/flue-local" || result.Output["runtime"] != RuntimeFlueNode {
		t.Fatalf("output = %+v, want driver-run logs ref and flue-node runtime", result.Output)
	}
}

func TestNodeRunnerFailsWhenRuntimeReturnsNoResult(t *testing.T) {
	root := t.TempDir()
	nodePath := writeExecutable(t, root, "fake-node", "#!/bin/sh\nexit 0\n")
	result, err := (NodeRunner{NodePath: nodePath}).Run(context.Background(), RunRequest{
		Run: &domain.DriverRun{
			WorkspaceKey:    "TEST",
			RunID:           "run-empty",
			Payload:         json.RawMessage(`{}`),
			DriverID:        "driver-1",
			DriverVersionID: "version-1",
		},
		BundleRoot: root,
		ServerPath: filepath.Join(root, "dist", "server.mjs"),
		Manifest:   map[string]string{"workflow_name": "epic-runner"},
	})
	if err != nil {
		t.Fatalf("NodeRunner.Run: %v", err)
	}
	if result.Status != domain.DriverRunFailed || result.ErrorClass != "invalid_driver_result" || result.Summary != "Flue workflow returned no result" {
		t.Fatalf("result = %+v, want failed invalid_driver_result for empty output", result)
	}
}

func TestNodeRunnerFailsWhenWorkflowResultIsMissingTerminalStatus(t *testing.T) {
	root := t.TempDir()
	nodePath := writeExecutable(t, root, "fake-node", "#!/bin/sh\nprintf '%s\\n' '{\"summary\":\"done\"}'\n")
	result, err := (NodeRunner{NodePath: nodePath}).Run(context.Background(), RunRequest{
		Run: &domain.DriverRun{
			WorkspaceKey:    "TEST",
			RunID:           "run-missing-status",
			Payload:         json.RawMessage(`{}`),
			DriverID:        "driver-1",
			DriverVersionID: "version-1",
		},
		BundleRoot: root,
		ServerPath: filepath.Join(root, "dist", "server.mjs"),
		Manifest:   map[string]string{"workflow_name": "epic-runner"},
	})
	if err != nil {
		t.Fatalf("NodeRunner.Run: %v", err)
	}
	if result.Status != domain.DriverRunFailed || result.ErrorClass != "invalid_driver_result" {
		t.Fatalf("result = %+v, want failed invalid_driver_result", result)
	}
	if result.Summary != "driver result missing terminal status: done" {
		t.Fatalf("summary = %q, want missing status detail", result.Summary)
	}
}

func TestNodeRunnerCancellationPropagatesToBuiltFlueServer(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not available: %v", err)
	}

	root := t.TempDir()
	dist := filepath.Join(root, "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatalf("mkdir dist: %v", err)
	}
	startedPath := filepath.Join(root, "started")
	cancelledPath := filepath.Join(root, "cancelled")
	if err := os.WriteFile(filepath.Join(dist, "server.mjs"), []byte(`
import fs from 'node:fs';

const startedPath = `+strconv.Quote(startedPath)+`;
const cancelledPath = `+strconv.Quote(cancelledPath)+`;

function recordCancelled(signal) {
  fs.writeFileSync(cancelledPath, signal);
  process.exit(0);
}

process.once('SIGINT', () => recordCancelled('SIGINT'));
process.once('SIGTERM', () => recordCancelled('SIGTERM'));

if (process.send) {
  fs.writeFileSync(startedPath, 'started');
  process.send({ version: 1, type: 'ready', target: 'workflow', name: process.env.FLUE_CLI_NAME || 'epic-runner' });
  process.on('message', () => {});
  setInterval(() => {}, 1000);
}
`), 0o644); err != nil {
		t.Fatalf("write server: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct {
		result RunResult
		err    error
	}, 1)
	go func() {
		result, err := (NodeRunner{}).Run(ctx, RunRequest{
			Run: &domain.DriverRun{
				WorkspaceKey:    "TEST",
				RunID:           "run-cancel",
				NodeID:          "node-1",
				LeaseID:         "lease-1",
				FencingToken:    42,
				Payload:         json.RawMessage(`{}`),
				DriverID:        "epic-runner",
				DriverVersionID: "version-1",
			},
			BundleRoot: root,
			ServerPath: filepath.Join(dist, "server.mjs"),
			Manifest:   map[string]string{"workflow_name": "epic-runner"},
		})
		done <- struct {
			result RunResult
			err    error
		}{result: result, err: err}
	}()
	waitForFile(t, startedPath)
	cancel()
	select {
	case out := <-done:
		if out.err != nil {
			t.Fatalf("NodeRunner.Run err = %v", out.err)
		}
		if out.result.Status != domain.DriverRunCancelled || out.result.ErrorClass != "driver_cancelled" {
			t.Fatalf("result = %+v, want cancelled driver_cancelled", out.result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("NodeRunner.Run did not return after cancellation")
	}
	waitForFile(t, cancelledPath)
}

func TestNodeRunnerBuiltFlueServerReceivesOnlyScopedFleetDBHandoff(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not available: %v", err)
	}
	t.Setenv("LOOM_FLEET_DB_URL", "https://fleet.invalid")
	t.Setenv("LOOM_FLEET_DB_API_KEY", "broad-secret")
	t.Setenv("LOOM_FLEET_DB_ACTOR", "broad-actor")
	t.Setenv("LOOM_DRIVER_FLEET_DB_API_KEY", "scoped-secret")
	t.Setenv("LOOM_DRIVER_FLEET_DB_ACTOR", "driver-run:run-1")
	t.Setenv("LOOM_CONFIG_DIR", "/tmp/loom-config")
	t.Setenv("OPENAI_API_KEY", "model-secret")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "aws-secret")
	t.Setenv("GITHUB_TOKEN", "github-secret")

	root := t.TempDir()
	dist := filepath.Join(root, "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatalf("mkdir dist: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dist, "server.mjs"), []byte(`
const leaked = [
  'LOOM_FLEET_DB_URL',
  'LOOM_FLEET_DB_API_KEY',
  'LOOM_TASK_RUN_LEASE_TOKEN',
  'OPENAI_API_KEY',
  'AWS_SECRET_ACCESS_KEY',
  'GITHUB_TOKEN',
].filter((key) => process.env[key]);
const handoffOk = process.env.LOOM_DRIVER_FLEET_DB_URL === 'https://fleet.invalid'
  && process.env.LOOM_DRIVER_FLEET_DB_API_KEY === 'scoped-secret'
  && process.env.LOOM_DRIVER_FLEET_DB_ACTOR === 'driver-run:run-1'
  && process.env.LOOM_CONFIG_DIR === '/tmp/loom-config';
if (process.send) {
  process.send({ version: 1, type: 'ready', target: 'workflow', name: process.env.FLUE_CLI_NAME || 'epic-runner' });
  process.on('message', (message) => {
    const result = leaked.length || !handoffOk
      ? { status: 'failed', summary: 'bad handoff leaked=' + leaked.join(',') + ' handoffOk=' + handoffOk, errorClass: 'env_leak' }
      : { status: 'completed', summary: 'handoff ok' };
    process.send({ version: 1, type: 'result', requestId: message.requestId, result }, () => process.exit(0));
  });
}
`), 0o644); err != nil {
		t.Fatalf("write server: %v", err)
	}

	result, err := (NodeRunner{}).Run(context.Background(), RunRequest{
		Run: &domain.DriverRun{
			WorkspaceKey:    "TEST",
			RunID:           "run-1",
			NodeID:          "node-1",
			LeaseID:         "lease-1",
			FencingToken:    42,
			Payload:         json.RawMessage(`{}`),
			Entrypoint:      EntrypointRun,
			DriverID:        "driver-1",
			DriverVersionID: "version-1",
		},
		BundleRoot: root,
		ServerPath: filepath.Join(dist, "server.mjs"),
		Manifest:   map[string]string{"workflow_name": "epic-runner"},
	})
	if err != nil {
		t.Fatalf("NodeRunner.Run: %v", err)
	}
	if result.Status != domain.DriverRunCompleted || result.Summary != "handoff ok" {
		t.Fatalf("result = %+v, want completed handoff ok", result)
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func writeExecutable(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}
	return path
}

func TestExecutorScansWorkspacesAndReportsNoQueuedRun(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "EMPTY", Name: "empty"}); err != nil {
		t.Fatalf("Create EMPTY workspace: %v", err)
	}
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create TEST workspace: %v", err)
	}
	writeFlueDist(t, root, "epic-runner", "done")
	registered, err := RegisterFlueDriver(ctx, st, RegisterFlueOptions{WorkspaceKey: "TEST", WorkDir: root, DistPath: "dist", DriverName: "epic-runner", CreatedBy: "tester", Activate: true})
	if err != nil {
		t.Fatalf("RegisterFlueDriver: %v", err)
	}
	if _, err := CreateDriverRun(ctx, st, RunOptions{WorkspaceKey: "TEST", DriverID: registered.Driver.DriverID, EpicID: "TEST-1", RunID: "run-1"}); err != nil {
		t.Fatalf("CreateDriverRun: %v", err)
	}
	result, err := (&Executor{
		Store:             st,
		WorkDir:           root,
		NodeID:            "node-1",
		LeaseID:           "lease-1",
		Runner:            &recordingRunner{result: RunResult{Status: domain.DriverRunCompleted, Summary: "done"}},
		HeartbeatInterval: -1,
	}).RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce scan workspaces: %v", err)
	}
	if result.Final == nil || result.Final.WorkspaceKey != "TEST" {
		t.Fatalf("final = %+v, want TEST workspace run", result.Final)
	}
	if _, err := (&Executor{Store: st, WorkDir: root, HeartbeatInterval: -1}).RunOnce(ctx); !errors.Is(err, ErrNoQueuedRun) {
		t.Fatalf("RunOnce after drain err = %v, want ErrNoQueuedRun", err)
	}
}

type recordingRunner struct {
	calls  int
	req    RunRequest
	result RunResult
	err    error
}

func (r *recordingRunner) Run(_ context.Context, req RunRequest) (RunResult, error) {
	r.calls++
	r.req = req
	return r.result, r.err
}
