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
  const result = await client.taskRuns.request({ taskId: "TASK-1", providerProfile: "local", parentSessionId: "lead-session-1", nodeId: "target-node" });
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
  assert.equal(calls[1].args.includes("--session"), false);
  assert.equal(calls[1].args.includes("--force"), false);
  assert.ok(calls[0].args.includes("--node-id"));
  assert.equal(calls[0].args[calls[0].args.indexOf("--node-id") + 1], "target-node");
  assert.ok(calls[0].args.includes("--parent-session-id"));
  assert.equal(calls[0].args[calls[0].args.indexOf("--parent-session-id") + 1], "lead-session-1");
});

test("FlueDriverClient exposes epic snapshot, active runs, and stale recovery helpers", async () => {
  const dir = mkdtempSync(path.join(tmpdir(), "loom-sdk-flue-helpers-"));
  const recorder = path.join(dir, "calls.jsonl");
  const fakeLoom = path.join(dir, "fake-loom.mjs");
  writeFileSync(fakeLoom, `#!/usr/bin/env node
import fs from 'node:fs';
const recorder = ${JSON.stringify(recorder)};
const args = process.argv.slice(2);
fs.appendFileSync(recorder, JSON.stringify({ args }) + '\\n');
if (args[1] === 'epic-get') {
  console.log(JSON.stringify({ id: 'EPIC-1', issue_type: 'epic', title: 'Epic' }));
} else if (args[1] === 'epic-snapshot') {
  console.log(JSON.stringify({ epicId: 'EPIC-1', readyCount: 1, blockedCount: 0, openChildrenCount: 1 }));
} else if (args[1] === 'list-agents') {
  console.log(JSON.stringify([{ name: 'nova', role_name: 'lead', parent: '' }]));
} else if (args[1] === 'agent-orchestration-session') {
  console.log(JSON.stringify({ agentName: 'nova', orchestratorSessionId: 'session-1' }));
} else if (args[1] === 'update-agent-parent') {
  console.log(JSON.stringify({ name: 'nova', role_name: 'lead', parent: 'EPIC-1' }));
} else if (args[1] === 'deliver-lead-assignment') {
  console.log(JSON.stringify({ agentName: 'nova', state: 'delivered', sessionId: 'session-1' }));
} else if (args[1] === 'deliver-agent-message') {
  console.log(JSON.stringify({ agentName: 'nova', state: 'delivered', sessionId: 'session-1' }));
} else if (args[1] === 'active-task-runs') {
  console.log(JSON.stringify({ driverRunId: 'run-1', activeCount: 2 }));
} else if (args[1] === 'recover-stale-tasks') {
  console.log(JSON.stringify({ driverRunId: 'run-1', recovered: 1, released: 1 }));
} else {
  console.log(JSON.stringify({ ok: true }));
}
`);

  const client = createLoomDriverClient({
    input: { epicId: "EPIC-1" },
    command: [process.execPath, fakeLoom],
    env: {
      LOOM_DRIVER_WORKSPACE: "WS",
      LOOM_DRIVER_RUN_ID: "run-1",
    },
  });
  assert.equal(client.messageLeadAgent, undefined);

  assert.equal((await client.epics.get()).issue_type, "epic");
  assert.equal((await client.epics.snapshot()).readyCount, 1);
  assert.equal((await client.agents.list())[0].name, "nova");
  assert.equal((await client.agents.orchestrationSession({ agent: "nova" })).orchestratorSessionId, "session-1");
  assert.equal((await client.agents.updateParent({ agent: "nova", parent: "EPIC-1", expectParent: "" })).parent, "EPIC-1");
  assert.equal((await client.agents.deliverAssignment({ agent: "nova" })).state, "delivered");
  assert.equal((await client.agents.message({ agent: "nova", message: "Task TASK-1 completed" })).state, "delivered");
  assert.equal((await client.taskRuns.active({ limit: 10 })).activeCount, 2);
  assert.equal((await client.taskRuns.recoverStale({ maxAgeSeconds: 30 })).recovered, 1);

  const calls = readJSONL(recorder);
  assert.deepEqual(calls.map((call) => call.args[1]), [
    "epic-get",
    "epic-snapshot",
    "list-agents",
    "agent-orchestration-session",
    "update-agent-parent",
    "deliver-lead-assignment",
    "deliver-agent-message",
    "active-task-runs",
    "recover-stale-tasks",
  ]);
  for (const call of calls) {
    assert.ok(call.args.includes("--driver-run-id"));
    assert.equal(call.args[call.args.indexOf("--driver-run-id") + 1], "run-1");
    assert.ok(call.args.includes("--workspace-key"));
    assert.equal(call.args[call.args.indexOf("--workspace-key") + 1], "WS");
  }
  assert.ok(calls[0].args.includes("--epic-id"));
  assert.equal(calls[0].args[calls[0].args.indexOf("--epic-id") + 1], "EPIC-1");
  assert.ok(calls[1].args.includes("--epic-id"));
  assert.equal(calls[1].args[calls[1].args.indexOf("--epic-id") + 1], "EPIC-1");
  assert.ok(calls[3].args.includes("--agent"));
  assert.equal(calls[3].args[calls[3].args.indexOf("--agent") + 1], "nova");
  assert.ok(calls[4].args.includes("--parent"));
  assert.equal(calls[4].args[calls[4].args.indexOf("--parent") + 1], "EPIC-1");
  assert.ok(calls[5].args.includes("--agent"));
  assert.equal(calls[5].args[calls[5].args.indexOf("--agent") + 1], "nova");
  assert.ok(calls[6].args.includes("--agent"));
  assert.equal(calls[6].args[calls[6].args.indexOf("--agent") + 1], "nova");
  assert.ok(calls[6].args.includes("--message"));
  assert.equal(calls[6].args[calls[6].args.indexOf("--message") + 1], "Task TASK-1 completed");
  assert.ok(calls[7].args.includes("--limit"));
  assert.equal(calls[7].args[calls[7].args.indexOf("--limit") + 1], "10");
  assert.ok(calls[8].args.includes("--max-age-seconds"));
  assert.equal(calls[8].args[calls[8].args.indexOf("--max-age-seconds") + 1], "30");
});

test("FlueDriverClient lets completed child requests be completed while slower siblings still run", async () => {
  const dir = mkdtempSync(path.join(tmpdir(), "loom-sdk-flue-concurrent-"));
  const recorder = path.join(dir, "calls.jsonl");
  const fakeLoom = path.join(dir, "fake-loom.mjs");
  writeFileSync(fakeLoom, `#!/usr/bin/env node
import fs from 'node:fs';
import { setTimeout as sleep } from 'node:timers/promises';

const recorder = ${JSON.stringify(recorder)};
const args = process.argv.slice(2);

function argValue(flag) {
  const idx = args.indexOf(flag);
  return idx >= 0 && idx + 1 < args.length ? args[idx + 1] : '';
}

function record(event) {
  fs.appendFileSync(recorder, JSON.stringify({ event, args }) + '\\n');
}

if (args[1] === 'exec-task') {
  const task = argValue('--task-id');
  record('exec-start-' + task);
  await sleep(task === 'SLOW' ? 250 : 20);
  record('exec-end-' + task);
  const taskRunId = 'task-run-' + task.toLowerCase();
  console.log(JSON.stringify({
    id: taskRunId,
    taskRunId,
    taskId: task,
    leaseToken: 'token-' + task,
    status: 'completed',
    logsRef: 'logs://' + taskRunId,
    artifactsRef: 'artifacts://' + taskRunId
  }));
} else if (args[1] === 'complete-task') {
  const task = argValue('--task-id');
  record('complete-' + task);
  console.log(JSON.stringify({ id: task, status: 'completed' }));
} else {
  record(args[1] || 'unknown');
  console.log(JSON.stringify({ ok: true }));
}
`);

  const client = createLoomDriverClient({
    input: { epicId: "EPIC-1" },
    command: [process.execPath, fakeLoom],
    env: {
      LOOM_DRIVER_WORKSPACE: "WS",
      LOOM_DRIVER_RUN_ID: "run-1",
    },
  });

  async function requestAndComplete(taskId) {
    const result = await client.taskRuns.request({ taskId, providerProfile: "local" });
    await client.tasks.complete({
      taskId,
      taskRunId: result.taskRunId,
      leaseToken: result.leaseToken,
      logsRef: result.logsRef,
      artifactsRef: result.artifactsRef,
    });
  }

  await Promise.all([
    requestAndComplete("FAST"),
    requestAndComplete("SLOW"),
  ]);

  const events = readJSONL(recorder).map((call) => call.event);
  assert.ok(events.indexOf("exec-start-FAST") >= 0, "FAST task should start");
  assert.ok(events.indexOf("exec-start-SLOW") >= 0, "SLOW task should start");
  assert.ok(events.indexOf("complete-FAST") >= 0, "FAST task should complete");
  assert.ok(events.indexOf("exec-end-SLOW") >= 0, "SLOW task should finish");
  assert.ok(
    events.indexOf("complete-FAST") < events.indexOf("exec-end-SLOW"),
    "FAST complete-task should not wait for SLOW exec-task to finish; events: " + events.join(",")
  );
});

test("FlueDriverClient returns needs_review results", () => {
  const client = createLoomDriverClient({
    input: {},
    command: [process.execPath, "-e", "console.log('{}')"],
    env: {
      LOOM_DRIVER_WORKSPACE: "WS",
      LOOM_DRIVER_RUN_ID: "run-1",
    },
  });

  assert.deepEqual(client.needsReview({ summary: "blocked", errorClass: "epic_blocked" }), {
    status: "needs_review",
    summary: "blocked",
    errorClass: "epic_blocked",
    taskRunId: undefined,
    logsRef: undefined,
    artifactsRef: undefined,
  });
});

function readJSONL(file) {
  return String(readFileSync(file, "utf8"))
    .trim()
    .split("\n")
    .filter(Boolean)
    .map((line) => JSON.parse(line));
}
