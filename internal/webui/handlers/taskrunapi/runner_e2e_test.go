package taskrunapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	appserve "github.com/tysonthomas9/loomcli/internal/app/serve"
	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// e2eRunnerScript is the bridge-spawned task runner for the end-to-end test:
// the real sdk/runner.js in serve mode, driving the full task-run lifecycle
// (read, heartbeat, log, artifact declare/upload/finalize, complete) against
// the serve-hosted surface. It hard-fails if any LOOM_FLEET_DB_* credential
// is visible — the vet A4 condition this chunk closes.
const e2eRunnerScript = `
import { TaskRunClient } from %q;

for (const key of Object.keys(process.env)) {
  if (key.startsWith("LOOM_FLEET_DB") || key.startsWith("LOOM_FLEETDB")) {
    console.error(key + " leaked into task runner env");
    process.exit(2);
  }
}
if (!process.env.LOOM_TASK_RUN_API_URL) {
  console.error("LOOM_TASK_RUN_API_URL missing");
  process.exit(2);
}

const upstreamFetch = globalThis.fetch;
let loseNextLogResponse = true;
const client = TaskRunClient.fromEnv(process.env, {
  fetch: async (url, init) => {
    const response = await upstreamFetch(url, init);
    if (loseNextLogResponse && new URL(url).pathname.endsWith("/task-run/log-append")) {
      loseNextLogResponse = false;
      await response.arrayBuffer();
      throw new Error("simulated lost log-append response");
    }
    return response;
  },
});
if (!client.serveMode) {
  console.error("client did not select the serve transport");
  process.exit(2);
}

const run = await client.getTaskRun();
if (run.taskRunId !== client.taskRunId) {
  console.error("getTaskRun mismatch: " + JSON.stringify(run));
  process.exit(2);
}
await client.heartbeat({ runtimeMetadata: { phase: "working" } });
const logAppend = {
  requestId: "working-log-" + client.taskRunId,
  stream: "stdout",
  text: "working\n",
  timestamp: new Date().toISOString(),
};
let responseLost = false;
try {
  await client.logs.append(logAppend);
} catch (error) {
  responseLost = error?.message === "simulated lost log-append response";
}
if (!responseLost) {
  console.error("log append response-loss injection did not fire");
  process.exit(2);
}
const replayedLog = await client.logs.append(logAppend);
if (replayedLog.sequence !== 1) {
  console.error("log append replay mismatch: " + JSON.stringify(replayedLog));
  process.exit(2);
}
const artifact = await client.artifacts.declare({
  id: "artifact-" + client.taskRunId,
  type: "patch",
  summary: "e2e patch",
});
await artifact.upload("patch body", { mimeType: "text/x-diff" });
await artifact.finalize({ summary: "e2e patch finalized" });
await client.completeRun({
  completionId: "completion-" + client.taskRunId,
  artifactIds: [artifact.id],
  exitCode: 0,
  runtimeMetadata: { phase: "done" },
});
console.log(JSON.stringify({ status: "completed", exit_code: 0, artifact_ids: [artifact.id] }));
`

// TestBridgeRunnerCompletesTaskRunViaServeSurface is the credential-removal
// end-to-end: a HostBridgeTaskExecutor-spawned node runner using the real
// SDK completes a task run entirely through the serve task-run API,
// authenticated by its lease tuple, with zero fleet-db env. The bridge's
// allowlisted base env plus taskRunnerEnv is exactly what production runners
// receive.
func TestBridgeRunnerCompletesTaskRunViaServeSurface(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found in PATH; skipping runner SDK e2e")
	}
	runnerJS, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "sdk", "runner.js"))
	if err != nil {
		t.Fatalf("resolve sdk/runner.js: %v", err)
	}
	if _, err := os.Stat(runnerJS); err != nil {
		t.Fatalf("sdk/runner.js not found at %s: %v", runnerJS, err)
	}

	ctx := context.Background()
	st := memstore.New()
	if _, err := st.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "WS",
		TaskRunID:    "task-run-e2e",
		TaskID:       "TASK-1",
		Status:       domain.TaskRunRunning,
		NodeID:       "node-1",
		LeaseID:      "lease-1",
		LeaseToken:   "lease-token-e2e",
		FencingToken: 42,
	}); err != nil {
		t.Fatalf("create task run: %v", err)
	}
	mux := http.NewServeMux()
	executionCapability, err := appserve.NewExecutionCapability(executionDependenciesForTaskRunAPITest(t, st))
	if err != nil {
		t.Fatalf("compose Execution capability: %v", err)
	}
	module := NewModule(Config{
		Store: st, Execution: executionCapability.TaskRunAPI(),
		Authorities: executionCapability.TaskRunAuthorityResolver(),
	})
	artifactsAPI := newTaskRunArtifactAPIForTest(module)
	module.artifacts = artifactsAPI
	module.Register(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	script := filepath.Join(t.TempDir(), "e2e-runner.mjs")
	if err := os.WriteFile(script, []byte(formatScript(runnerJS)), 0o600); err != nil {
		t.Fatalf("write runner script: %v", err)
	}

	executor := driverpkg.HostBridgeTaskExecutor{
		Store:               st,
		Artifacts:           artifactsAPI,
		ArtifactAuthorities: executionCapability.TaskRunAuthorityResolver(),
		WorktreePath:        t.TempDir(),
		Command:             []string{node, script},
		APIBaseURL:          server.URL,
	}
	result, err := executor.ExecuteTask(ctx, driverpkg.TaskExecRequest{
		WorkspaceKey:    "WS",
		TaskRunID:       "task-run-e2e",
		TaskID:          "TASK-1",
		ProviderProfile: "custom-e2e",
		NodeID:          "node-1",
		LeaseID:         "lease-1",
		LeaseToken:      "lease-token-e2e",
		FencingToken:    42,
	})
	if err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}
	if result.Status != domain.TaskRunCompleted {
		t.Fatalf("runner result status = %q, want completed", result.Status)
	}

	assertServeSurfaceEffects(t, st)
}

// formatScript injects the absolute runner.js path into the e2e script.
func formatScript(runnerJS string) string {
	return "// generated by runner_e2e_test.go\n" + fmt.Sprintf(e2eRunnerScript, "file://"+filepath.ToSlash(runnerJS))
}

func assertServeSurfaceEffects(t *testing.T, st store.Store) {
	t.Helper()
	ctx := context.Background()
	run, err := st.TaskRuns().Get(ctx, "WS", "task-run-e2e")
	if err != nil {
		t.Fatalf("get task run: %v", err)
	}
	if run.Status != domain.TaskRunCompleted {
		t.Fatalf("task run status = %q, want completed via serve surface", run.Status)
	}
	// Completion replaces runtime metadata wholesale (store semantics), so
	// the completion-time value is what survives; the heartbeat's metadata
	// merge was observable while the run was live.
	if run.RuntimeMetadata["phase"] != "done" {
		t.Fatalf("completion metadata missing: %v", run.RuntimeMetadata)
	}
	logs, err := st.TaskRuns().ListLogs(ctx, "WS", "task-run-e2e", store.TaskRunLogFilter{})
	if err != nil || len(logs) != 1 || logs[0].Text != "working\n" {
		t.Fatalf("logs = %v err=%v, want the runner's appended line", logs, err)
	}
	artifact, err := st.Artifacts().Get(ctx, "WS", "artifact-task-run-e2e")
	if err != nil {
		t.Fatalf("get artifact: %v", err)
	}
	if artifact.OwnerType != taskRunOwnerType || artifact.OwnerID != "task-run-e2e" {
		t.Fatalf("artifact owner = %s/%s, want task_run/task-run-e2e", artifact.OwnerType, artifact.OwnerID)
	}
	if artifact.DurableStatus != "finalized" || artifact.Summary != "e2e patch finalized" {
		t.Fatalf("artifact = %+v, want finalized with updated summary", artifact)
	}
}
