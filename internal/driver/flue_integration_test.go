package driver

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestRealFlueBuildAndBuiltServerSmoke(t *testing.T) {
	if os.Getenv("LOOM_REAL_FLUE_TEST") != "1" {
		t.Skip("set LOOM_REAL_FLUE_TEST=1 to run the real Flue CLI smoke")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not available: %v", err)
	}
	flueCommand := realFlueCommandForTest(t)

	ctx := context.Background()
	root := t.TempDir()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	writeRealFlueProject(t, root, "real-flue-smoke", `export async function run(ctx) {
  const input = ctx.payload || {};
  return { status: "completed", summary: input.message || "real flue smoke" };
}
`)
	buildRealFlueProject(t, root, flueCommand)

	registered, err := RegisterFlueDriver(ctx, st, RegisterFlueOptions{
		WorkspaceKey: "TEST",
		WorkDir:      root,
		DistPath:     "dist",
		DriverName:   "real-flue-smoke",
		WorkflowName: "real-flue-smoke",
		SourceRef:    "workflows/real-flue-smoke.ts",
		CreatedBy:    "tester",
		Activate:     true,
	})
	if err != nil {
		t.Fatalf("RegisterFlueDriver after real Flue build %q: %v", strings.Join(flueCommand, " "), err)
	}
	if got := registered.Version.Manifest["server_ref"]; got != "dist/server.mjs" {
		t.Fatalf("server_ref = %q, want dist/server.mjs", got)
	}
	run, err := CreateDriverRun(ctx, st, RunOptions{
		WorkspaceKey: "TEST",
		DriverID:     registered.Driver.DriverID,
		EpicID:       "TEST-1",
		RunID:        "run-1",
		Payload:      json.RawMessage(`{"message":"real flue ok"}`),
	})
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
	if result.Status != domain.DriverRunCompleted || result.Summary != "real flue ok" {
		t.Fatalf("result = %+v, want completed real flue ok", result)
	}
}

func TestRealFlueEpicLoopDrainsDAGSmoke(t *testing.T) {
	if os.Getenv("LOOM_REAL_FLUE_TEST") != "1" {
		t.Skip("set LOOM_REAL_FLUE_TEST=1 to run the real Flue CLI smoke")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not available: %v", err)
	}
	flueCommand := realFlueCommandForTest(t)

	ctx := context.Background()
	root := t.TempDir()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	writeRealFlueProject(t, root, "real-flue-epic-loop", `import { createLoomDriverClient } from "@loom/sdk/flue";

export async function run(ctx) {
  const input = ctx.payload || {};
  const loom = createLoomDriverClient({ input });
  const completed = [];
  while (true) {
    const task = await loom.tasks.claimReady({ epicId: input.epicId });
    if (!task) {
      return loom.completed({ summary: "drained:" + completed.join(",") });
    }
    const result = await loom.taskRuns.request({
      taskId: task.id,
      providerProfile: "local-noop",
    });
    if (result.status !== "completed") {
      await loom.tasks.release(task.id);
      return loom.needsReview({ summary: "failed:" + task.id, taskRunId: result.id });
    }
    await loom.tasks.complete(task.id);
    completed.push(task.id);
  }
}
`)
	buildRealFlueProject(t, root, flueCommand)

	registered, err := RegisterFlueDriver(ctx, st, RegisterFlueOptions{
		WorkspaceKey: "TEST",
		WorkDir:      root,
		DistPath:     "dist",
		DriverName:   "real-flue-epic-loop",
		WorkflowName: "real-flue-epic-loop",
		SourceRef:    "workflows/real-flue-epic-loop.ts",
		CreatedBy:    "tester",
		Activate:     true,
	})
	if err != nil {
		t.Fatalf("RegisterFlueDriver after real Flue build %q: %v", strings.Join(flueCommand, " "), err)
	}
	run, err := CreateDriverRun(ctx, st, RunOptions{
		WorkspaceKey: "TEST",
		DriverID:     registered.Driver.DriverID,
		EpicID:       "TEST-1",
		RunID:        "run-epic-loop-1",
		Payload:      json.RawMessage(`{"epicId":"TEST-1"}`),
	})
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

	statePath := filepath.Join(root, "fake-loom-state.json")
	fakeLoom := writeRealFlueEpicLoopFakeLoom(t, root)
	result, err := (NodeRunner{ExecTaskCommand: []string{nodePathForTest(t), fakeLoom, statePath}}).Run(ctx, req)
	if err != nil {
		t.Fatalf("NodeRunner.Run: %v", err)
	}
	wantSummary := "drained:TEST-A,TEST-B,TEST-C,TEST-D"
	if result.Status != domain.DriverRunCompleted || result.Summary != wantSummary {
		if data, readErr := os.ReadFile(statePath); readErr == nil {
			t.Logf("fake loom state: %s", strings.TrimSpace(string(data)))
		} else {
			t.Logf("fake loom state unavailable: %v", readErr)
		}
		t.Fatalf("result = %+v, want completed %q", result, wantSummary)
	}
	state := readRealFlueEpicLoopState(t, statePath)
	if strings.Join(state.Completed, ",") != "TEST-A,TEST-B,TEST-C,TEST-D" {
		t.Fatalf("completed order = %+v, want TEST-A,TEST-B,TEST-C,TEST-D", state.Completed)
	}
	if strings.Join(state.Executed, ",") != "TEST-A,TEST-B,TEST-C,TEST-D" {
		t.Fatalf("executed order = %+v, want TEST-A,TEST-B,TEST-C,TEST-D", state.Executed)
	}
}

type realFlueEpicLoopState struct {
	Completed []string `json:"completed"`
	Executed  []string `json:"executed"`
}

func writeRealFlueEpicLoopFakeLoom(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "fake-loom.mjs")
	script := `#!/usr/bin/env node
import fs from 'node:fs';

const statePath = process.argv[2];
if (!statePath) {
  console.error('missing fake loom state path argument');
  process.exit(2);
}

function loadState() {
  try {
    return JSON.parse(fs.readFileSync(statePath, 'utf8'));
  } catch {
    return { completed: [], executed: [] };
  }
}

function saveState(state) {
  fs.writeFileSync(statePath, JSON.stringify(state));
}

function argValue(args, flag) {
  const idx = args.indexOf(flag);
  return idx >= 0 && idx + 1 < args.length ? args[idx + 1] : '';
}

function nextReady(completed) {
  const done = new Set(completed);
  if (!done.has('TEST-A')) return 'TEST-A';
  if (!done.has('TEST-B')) return 'TEST-B';
  if (!done.has('TEST-C')) return 'TEST-C';
  if (done.has('TEST-B') && done.has('TEST-C') && !done.has('TEST-D')) return 'TEST-D';
  return '';
}

const args = process.argv.slice(3);
if (args[0] !== 'driver') {
  console.error('expected driver subcommand');
  process.exit(3);
}

const state = loadState();
switch (args[1]) {
  case 'claim-ready': {
    const task = nextReady(state.completed || []);
    if (task) {
      console.log(JSON.stringify({ id: task, title: task }));
    }
    break;
  }
  case 'exec-task': {
    const task = argValue(args, '--task-id');
    if (!task) {
      console.error('exec-task missing --task-id');
      process.exit(4);
    }
    state.executed = state.executed || [];
    state.executed.push(task);
    saveState(state);
    const taskRunId = 'task-run-' + task.toLowerCase();
    console.log(JSON.stringify({
      id: taskRunId,
      taskRunId,
      taskId: task,
      leaseToken: 'token-' + task,
      status: 'completed',
      exitCode: 0,
      logsRef: 'logs://' + taskRunId,
      summary: 'ran ' + task
    }));
    break;
  }
  case 'complete-task': {
    const task = argValue(args, '--task-id');
    const taskRunId = argValue(args, '--task-run-id');
    const token = argValue(args, '--lease-token');
    if (!task || taskRunId !== 'task-run-' + task.toLowerCase() || token !== 'token-' + task) {
      console.error('complete-task missing remembered task-run id or lease token');
      process.exit(5);
    }
    state.completed = state.completed || [];
    if (!state.completed.includes(task)) state.completed.push(task);
    saveState(state);
    console.log(JSON.stringify({ id: task, status: 'completed' }));
    break;
  }
  case 'release-task':
    console.error('unexpected release-task for happy path');
    process.exit(6);
    break;
  default:
    console.error('unexpected driver command: ' + args[1]);
    process.exit(7);
}
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake loom command: %v", err)
	}
	return path
}

func readRealFlueEpicLoopState(t *testing.T, path string) realFlueEpicLoopState {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fake loom state: %v", err)
	}
	var state realFlueEpicLoopState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("decode fake loom state: %v", err)
	}
	return state
}

func writeRealFlueProject(t *testing.T, root, workflowName, source string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "workflows"), 0o755); err != nil {
		t.Fatalf("mkdir workflows: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"type":"module","dependencies":{"@loom/sdk":"file:./node_modules/@loom/sdk"}}`+"\n"), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	sdkRoot, err := filepath.Abs("../../sdk")
	if err != nil {
		t.Fatalf("resolve sdk root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sdkRoot, "package.json")); err != nil {
		t.Fatalf("local @loom/sdk package not found at %s: %v", sdkRoot, err)
	}
	loomScope := filepath.Join(root, "node_modules", "@loom")
	if err := os.MkdirAll(loomScope, 0o755); err != nil {
		t.Fatalf("mkdir node_modules/@loom: %v", err)
	}
	if err := os.Symlink(sdkRoot, filepath.Join(loomScope, "sdk")); err != nil {
		t.Fatalf("link @loom/sdk: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflows", workflowName+".ts"), []byte(source), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
}

func buildRealFlueProject(t *testing.T, root string, flueCommand []string) {
	t.Helper()
	outputDir := filepath.Join(root, "dist")
	args := append(append([]string{}, flueCommand[1:]...), "build", "--target", "node", "--root", root, "--output", outputDir)
	cmd := exec.Command(flueCommand[0], args...) //nolint:gosec // test command is operator-provided.
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("real Flue build %q failed: %v\n%s", strings.Join(flueCommand, " "), err, strings.TrimSpace(string(output)))
	}
	if _, err := os.Stat(filepath.Join(outputDir, "server.mjs")); err != nil {
		t.Fatalf("real Flue build missing dist/server.mjs: %v", err)
	}
}

func nodePathForTest(t *testing.T) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node not available: %v", err)
	}
	return node
}

func realFlueCommandForTest(t *testing.T) []string {
	t.Helper()
	if encoded := strings.TrimSpace(os.Getenv("LOOM_REAL_FLUE_CMD_JSON")); encoded != "" {
		var parsed []string
		if err := json.Unmarshal([]byte(encoded), &parsed); err != nil {
			t.Fatalf("decode LOOM_REAL_FLUE_CMD_JSON: %v", err)
		}
		if len(parsed) == 0 {
			t.Fatal("LOOM_REAL_FLUE_CMD_JSON must contain at least one command element")
		}
		return parsed
	}
	if raw := strings.TrimSpace(os.Getenv("LOOM_REAL_FLUE_CMD")); raw != "" {
		return []string{raw}
	}
	path, err := exec.LookPath("flue")
	if err != nil {
		t.Skip("flue not found on PATH; set LOOM_REAL_FLUE_CMD_JSON or LOOM_REAL_FLUE_CMD")
	}
	return []string{path}
}
