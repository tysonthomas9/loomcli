#!/usr/bin/env node
// Deterministic TaskRun runner stub for the podman-stack acceptance suite
// (scripts/test-podman-stack.sh). Mounted read-only into the loom-serve
// container at /opt/e2e by compose.e2e.yaml and wired through
// LOOM_DRIVER_TASK_RUNNER_CMD_JSON, mirroring the host harness stub in
// scripts/test-real-flue-epic-runner.sh:
//
//   - verifies the per-task-run lease token reached the runner env,
//   - appends the executed task_id to LOOM_E2E_TASK_LOG (on the loom-work
//     volume) so the driver script can assert execution count + DAG order,
//   - optionally fails one task on every attempt when its task_id is written
//     to LOOM_E2E_FAIL_TASK_FILE (retry-then-park scenarios),
//   - reports a terminal completed/failed result on stdout.
//
// No secrets are read or printed here.
import fs from "node:fs";
import path from "node:path";

const request = JSON.parse(process.env.LOOM_TASK_RUN_REQUEST_JSON || "{}");
const logPath = process.env.LOOM_E2E_TASK_LOG || "/work/e2e/task-runner.log";
const failTaskPath = process.env.LOOM_E2E_FAIL_TASK_FILE || "/work/e2e/fail-task-id";

if (process.env.LOOM_TASK_RUN_LEASE_TOKEN !== request.lease_token) {
  console.error("task-run lease token did not reach the task runner");
  process.exit(3);
}

fs.mkdirSync(path.dirname(logPath), { recursive: true });
fs.appendFileSync(logPath, request.task_id + "\n");

let failTaskId = "";
try {
  failTaskId = fs.readFileSync(failTaskPath, "utf8").trim();
} catch {
  // no failure injection configured
}
if (failTaskId && request.task_id === failTaskId) {
  console.log(
    JSON.stringify({
      status: "failed",
      exitCode: 1,
      errorClass: "injected_task_failure",
      errorMessage: "deliberate failure injected by podman-stack e2e",
      logsRef: "logs://" + request.task_run_id,
    }),
  );
  process.exit(0);
}

console.log(
  JSON.stringify({
    status: "completed",
    exitCode: 0,
    logsRef: "logs://" + request.task_run_id,
    runtimeMetadata: {
      task_runner: "podman-stack-e2e",
      sandbox_provider: (request.sandbox_placement && request.sandbox_placement.provider) || "",
    },
  }),
);
