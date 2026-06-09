#!/usr/bin/env node
import fs from "node:fs";
import path from "node:path";

const logPath = process.env.LEAD_EPIC_RUNNER_TASK_LOG;
if (!logPath) {
  console.error("LEAD_EPIC_RUNNER_TASK_LOG is required");
  process.exit(2);
}

const request = JSON.parse(process.env.LOOM_TASK_RUN_REQUEST_JSON || "{}");
if (!request.task_id || !request.task_run_id) {
  console.error("task runner request is missing task_id or task_run_id");
  process.exit(3);
}
if (request.provider_profile !== "flue-local") {
  console.error(`unexpected provider profile ${request.provider_profile}`);
  process.exit(4);
}
if (process.env.LOOM_TASK_RUN_LEASE_TOKEN !== request.lease_token) {
  console.error("task-run lease token did not reach the task runner");
  process.exit(5);
}

fs.mkdirSync(path.dirname(logPath), { recursive: true });
fs.appendFileSync(logPath, `${request.task_id}\n`);

console.log(
  JSON.stringify({
    status: "completed",
    exit_code: 0,
    logs_ref: `task-run://${request.task_run_id}/logs`,
    runtime_metadata: {
      task_runner: "lead-epic-runner-playwright",
      provider_profile: request.provider_profile,
    },
  }),
);
