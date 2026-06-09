import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import assert from "node:assert/strict";

import { createLoomDriverClient } from "./flue.js";

test("FlueDriverClient maps scoped handoff env and remembers child task lease", async () => {
  const dir = mkdtempSync(path.join(tmpdir(), "loom-sdk-flue-"));
  const recorder = path.join(dir, "calls.jsonl");
  const fakeLoom = path.join(dir, "fake-loom.mjs");
  writeFileSync(fakeLoom, `#!/usr/bin/env node
import fs from 'node:fs';
const recorder = ${JSON.stringify(recorder)};
const args = process.argv.slice(2);
fs.appendFileSync(recorder, JSON.stringify({
  args,
  env: {
    LOOM_FLEET_DB_URL: process.env.LOOM_FLEET_DB_URL || '',
    LOOM_FLEET_DB_API_KEY: process.env.LOOM_FLEET_DB_API_KEY || '',
    LOOM_FLEET_DB_ACTOR: process.env.LOOM_FLEET_DB_ACTOR || '',
    LOOM_DRIVER_FLEET_DB_URL: process.env.LOOM_DRIVER_FLEET_DB_URL || '',
    LOOM_DRIVER_FLEET_DB_API_KEY: process.env.LOOM_DRIVER_FLEET_DB_API_KEY || '',
    LOOM_DRIVER_FLEET_DB_ACTOR: process.env.LOOM_DRIVER_FLEET_DB_ACTOR || '',
  },
}) + '\\n');
if (args[1] === 'exec-task') {
  console.log(JSON.stringify({ id: 'task-run-1', taskRunId: 'task-run-1', taskId: 'TASK-1', leaseToken: 'lease-token-1', status: 'completed', summary: 'ok' }));
} else if (args[1] === 'complete-task') {
  console.log(JSON.stringify({ id: 'TASK-1', status: 'completed' }));
} else {
  console.log(JSON.stringify({ id: 'TASK-1' }));
}
`);

  const client = createLoomDriverClient({
    input: { epicId: "EPIC-1" },
    command: [process.execPath, fakeLoom],
    env: {
      LOOM_DRIVER_WORKSPACE: "WS",
      LOOM_DRIVER_RUN_ID: "run-1",
      LOOM_DRIVER_FLEET_DB_URL: "https://fleet.invalid",
      LOOM_DRIVER_FLEET_DB_API_KEY: "scoped-secret",
      LOOM_DRIVER_FLEET_DB_ACTOR: "driver-run:run-1",
    },
  });
  const result = await client.taskRuns.request({ taskId: "TASK-1", providerProfile: "local" });
  assert.equal(result.status, "completed");
  await client.tasks.complete("TASK-1");

  const calls = readJSONL(recorder);
  assert.equal(calls.length, 2);
  for (const call of calls) {
    assert.equal(call.env.LOOM_FLEET_DB_URL, "https://fleet.invalid");
    assert.equal(call.env.LOOM_FLEET_DB_API_KEY, "scoped-secret");
    assert.equal(call.env.LOOM_FLEET_DB_ACTOR, "driver-run:run-1");
    assert.equal(call.env.LOOM_DRIVER_FLEET_DB_URL, "");
    assert.equal(call.env.LOOM_DRIVER_FLEET_DB_API_KEY, "");
    assert.equal(call.env.LOOM_DRIVER_FLEET_DB_ACTOR, "");
  }
  assert.ok(calls[1].args.includes("--task-run-id"));
  assert.equal(calls[1].args[calls[1].args.indexOf("--task-run-id") + 1], "task-run-1");
  assert.ok(calls[1].args.includes("--lease-token"));
  assert.equal(calls[1].args[calls[1].args.indexOf("--lease-token") + 1], "lease-token-1");
});

function readJSONL(file) {
  return String(readFileSync(file, "utf8"))
    .trim()
    .split("\n")
    .filter(Boolean)
    .map((line) => JSON.parse(line));
}
