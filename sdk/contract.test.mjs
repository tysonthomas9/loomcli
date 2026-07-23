// SDK v1 contract freeze (SP1): these tests verify the RUNTIME client against
// the frozen surface manifest (api-surface.v1.json) — op names, wire field
// names, error envelope, watch/await semantics, connector action mapping and
// strict camelCase inputs. A failure here means a BREAKING CHANGE to the
// published contract: update the manifest in the same change and bump the
// SDK major version, never rename silently. The server half of the freeze
// lives in internal/webui/handlers/driverapi/contract_test.go.

import { createServer } from "node:http";
import { readFileSync } from "node:fs";
import test from "node:test";
import assert from "node:assert/strict";

import {
  DriverApiError,
  LoomDriverClient,
  WorkflowSuspended,
  isWorkflowSuspended,
} from "./driver.js";

const manifest = JSON.parse(readFileSync(new URL("./api-surface.v1.json", import.meta.url), "utf8"));

function newClient(apiUrl, input = { epicId: "EPIC-1" }) {
  return LoomDriverClient.fromEnv({
    apiUrl,
    input,
    env: {
      LOOM_DRIVER_WORKSPACE: "WS",
      LOOM_DRIVER_RUN_ID: "run-1",
    },
  });
}

test("contract: client namespace surface matches the manifest exactly", () => {
  const client = newClient("http://127.0.0.1:1");
  for (const [namespace, methods] of Object.entries(manifest.client.namespaces)) {
    const surface = client[namespace];
    assert.ok(surface, `client.${namespace} missing`);
    assert.deepEqual(
      Object.keys(surface).sort(),
      [...methods].sort(),
      `client.${namespace} surface drifted from the frozen manifest`,
    );
  }
  for (const [source, methods] of Object.entries(manifest.client.connectorMethods)) {
    assert.deepEqual(
      Object.keys(client.connectors[source]).sort(),
      Object.keys(methods).sort(),
      `client.connectors.${source} surface drifted from the frozen manifest`,
    );
  }
  for (const helper of manifest.client.resultHelpers) {
    assert.equal(typeof client[helper], "function", `result helper ${helper} missing`);
  }
});

test("contract: every op sends only frozen camelCase wire fields to its frozen path", async () => {
  await withDriverServer((call) => {
    if (call.url.endsWith("/driver/events/await")) {
      return satisfiedAwait(call.body.awaitIndex);
    }
    if (call.url.endsWith("/driver/workflows/await")) {
      return { ...satisfiedAwait(call.body.awaitIndex), child: { runId: call.body.childRunId, status: "completed" } };
    }
    if (call.url.endsWith("/driver/workflows/start")) {
      return { childRunId: "child-1", workflowName: call.body.workflowName, status: "queued", parentRunId: "run-1" };
    }
    if (call.url.endsWith("/driver/events/awaits")) {
      return { runId: "run-1", awaits: [] };
    }
    return {};
  }, async ({ apiUrl, calls }) => {
    const client = newClient(apiUrl);
    // `type` narrows the ready queue server-side; `actor` is accepted for
    // wire-compat but ignored server-side (the lock is keyed by the run's
    // derived actor — see the driverapi actor-lock security fix).
    await client.tasks.claimReady({ actor: "lead", limit: 5, type: "bug", sourceRepo: "alpha" });
    await client.tasks.claimReview({ taskId: "TASK-1" });
    await client.tasks.handoffReview({
      taskId: "TASK-1",
      taskRunId: "task-run-1",
      status: "open",
      reason: "review findings require changes",
    });
    await client.epics.get({});
    await client.epics.snapshot({});
    await client.agents.list();
    await client.agents.orchestrationSession({ agent: "lead" });
    await client.agents.updateParent({ agent: "lead", parent: "EPIC-2", expectParent: "EPIC-1" });
    await client.agents.deliverAssignment({ agent: "lead" });
    await client.agents.message({ agent: "lead", message: "hello" });
    await client.taskRuns.request({
      taskId: "TASK-1",
      runner: "local-task-runner",
      taskRunId: "task-run-1",
      workerProfileId: "wp-1",
      parentSessionId: "sess-1",
      nodeId: "node-1",
      runnerId: "runner-1",
      driverStepId: "step-1",
      capabilities: ["git"],
      leaseToken: "lt-1",
      closeTask: false,
      retainWorkItemClaim: true,
    });
    await client.taskRuns.get({ taskRunId: "task-run-1" });
    await client.taskRuns.active({ limit: 2 });
    await client.taskRuns.recoverStale({
      staleBefore: "2026-01-01T00:00:00Z",
      maxAgeSeconds: 60,
      errorClass: "stale",
      errorMessage: "lost",
    });
    await client.tasks.complete({
      taskId: "TASK-1",
      taskRunId: "task-run-1",
      reason: "done",
      completionId: "comp-1",
      leaseToken: "lt-1",
      logsRef: "logs",
      artifactsRef: "artifacts",
      artifactIds: ["art-1"],
    });
    await client.tasks.release({ taskId: "TASK-1", actor: "lead" });
    await client.tasks.releaseReview({ taskId: "TASK-1" });
    await client.tasks.claim({ taskId: "TASK-1", actor: "lead", epicId: "EPIC-1", limit: 5 });
    await client.tasks.diff({ taskId: "TASK-1" });
    await client.connectors.dispatch({
      action: "github.pull_request.read",
      connectorId: "conn-1",
      resource: "octo/hello#1",
      args: { owner: "octo" },
    });
    await client.events.await({ pattern: "approval:octo/hello#1@sha", actor: "reviewer", timeoutMs: 1000 });
    await client.events.list();
    await client.workflows.start({ workflow: "child-flow", idempotencyKey: "key-1", input: { a: 1 } });
    await client.workflows.await({ childRunId: "child-1", timeoutMs: 1000 });
    await client.issues.get({ issueId: "ISSUE-1" });
    await client.issues.list({ externalRef: "octo/hello#1", type: "bug", status: "open", limit: 10 });
    await client.issues.listComments({ issueId: "ISSUE-1" });
    await client.issues.comment({ issueId: "ISSUE-1", body: "looks good" });
    await client.issues.update({ issueId: "ISSUE-1", status: "open", priority: 1, labels: ["review-cycle:1"], assignee: "agent", externalRef: "octo/hello#1" });
    await client.issues.blockRepositoryRequired({ issueId: "ISSUE-1" });
    await client.issues.addLabel({ issueId: "ISSUE-1", label: "review-cycle:1" });
    await client.issues.removeLabel({ issueId: "ISSUE-1", label: "review-cycle:1" });
    await client.roles.get({ name: "docs-assistant" });
    // binding.config takes NO input: the binding is resolved server-side from
    // the calling run's provenance (a body binding id would be ignored).
    await client.binding.config();

    const exercised = new Set();
    for (const call of calls) {
      const op = call.url.replace(/^.*\/driver\//, "").replace(/\?.*$/, "");
      const spec = manifest.ops[op];
      assert.ok(spec, `op ${op} hit the wire but is not in the frozen manifest`);
      assert.equal(call.method, spec.method, `op ${op} method drifted`);
      exercised.add(op);
      if (spec.method === "GET") {
        continue;
      }
      assertNoSnakeCaseKeys(call.body);
      const allowed = new Set(spec.fields);
      for (const key of Object.keys(call.body)) {
        assert.ok(allowed.has(key), `op ${op} sent wire field ${key} not in the frozen manifest`);
      }
      for (const [parent, nestedFields] of Object.entries(spec.nested || {})) {
        const nestedAllowed = new Set(nestedFields);
        for (const key of Object.keys(call.body[parent] || {})) {
          assert.ok(nestedAllowed.has(key), `op ${op} sent nested field ${parent}.${key} not in the frozen manifest`);
        }
      }
    }
    assert.deepEqual(
      [...exercised].sort(),
      Object.keys(manifest.ops).sort(),
      "contract test no longer exercises every frozen op",
    );
  });
});

test("contract: connector methods map onto their frozen dispatch actions", async () => {
  await withDriverServer(() => ({ callId: "call-1", decision: "granted" }), async ({ apiUrl, calls }) => {
    const client = newClient(apiUrl);
    const expected = [];
    for (const [source, methods] of Object.entries(manifest.client.connectorMethods)) {
      for (const [method, action] of Object.entries(methods)) {
        await client.connectors[source][method]({ expectedHeadSha: "sha-1" });
        expected.push(action);
      }
    }
    assert.deepEqual(calls.map((call) => call.body.action), expected, "connector action mapping drifted");
  });
});

test("contract: error envelope round-trips code, retryable and additive details", async () => {
  const details = { expected: "sha-a", observed: "sha-b" };
  await withDriverServer(() => ({
    statusCode: 409,
    body: { error: { code: "stale_subject", message: "subject moved", retryable: false, details } },
  }), async ({ apiUrl }) => {
    const client = newClient(apiUrl);
    try {
      await client.epics.snapshot({});
      assert.fail("expected DriverApiError");
    } catch (apiErr) {
      assert.ok(apiErr instanceof DriverApiError);
      assert.equal(apiErr.code, "stale_subject");
      assert.equal(apiErr.retryable, false);
      assert.equal(apiErr.status, 409);
      assert.deepEqual(apiErr.details, details);
      assert.ok(manifest.errorEnvelope.codes.includes(apiErr.code));
    }
  });
});

test("contract: token_expired is forced non-retryable even when the envelope lies", async () => {
  assert.ok(manifest.errorEnvelope.neverRetryable.includes("token_expired"));
  await withDriverServer(() => ({
    statusCode: 401,
    body: { error: { code: "token_expired", message: "expired", retryable: true } },
  }), async ({ apiUrl }) => {
    const client = newClient(apiUrl);
    try {
      await client.epics.get({});
      assert.fail("expected DriverApiError");
    } catch (err) {
      assert.equal(err.code, "token_expired");
      assert.equal(err.retryable, false, "token TTL is the max-run-duration cap: never retryable");
    }
  });
});

test("contract: client-side error codes are all in the frozen manifest", () => {
  for (const code of ["timeout", "unavailable", "internal", "precondition_required", "await_timeout_required"]) {
    assert.ok(manifest.errorEnvelope.codes.includes(code), `client-side code ${code} missing from manifest`);
  }
});

test("contract: strict camelCase — snake_case inputs are not honored", async () => {
  await withDriverServer(() => ({}), async ({ apiUrl, calls }) => {
    const client = newClient(apiUrl, {});
    await client.epics.get({ epic_id: "EPIC-9" });
    assert.equal(calls.length, 1);
    assert.deepEqual(calls[0].body, {}, "snake_case epic_id must be ignored, not forwarded");

    assert.throws(
      () => client.events.await({ pattern: "approval:x@sha", timeout_ms: 500 }),
      (err) => err instanceof DriverApiError && err.code === "await_timeout_required",
      "snake_case timeout_ms must not satisfy the required timeoutMs",
    );
    await assert.rejects(
      client.tasks.release({ task_id: "TASK-1" }),
      /requires taskId/,
      "snake_case task_id must not satisfy taskId",
    );
    assert.equal(calls.length, 1, "rejected snake_case inputs must never reach the wire");
  });
});

test("contract: suspended wire status throws the WorkflowSuspended sentinel", async () => {
  await withDriverServer(() => ({ status: "suspended" }), async ({ apiUrl }) => {
    const client = newClient(apiUrl);
    try {
      await client.events.await({ pattern: "approval:x@sha", timeoutMs: 1000 });
      assert.fail("expected WorkflowSuspended");
    } catch (err) {
      assert.ok(err instanceof WorkflowSuspended);
      assert.ok(isWorkflowSuspended(err));
      assert.equal(err.result.status, manifest.suspendedResultStatus);
    }
  });
});

test("contract: await responses surface only the frozen returned statuses", async () => {
  await withDriverServer((call) => satisfiedAwait(call.body.awaitIndex, "timed_out"), async ({ apiUrl }) => {
    const client = newClient(apiUrl);
    const result = await client.events.await({ pattern: "approval:x@sha", timeoutMs: 1 });
    assert.ok(
      manifest.await.returnedStatuses.includes(result.status),
      `await status ${result.status} outside the frozen contract`,
    );
  });
});

test("contract: result helpers emit only frozen statuses", () => {
  const client = newClient("http://127.0.0.1:1");
  const statuses = [client.completed(), client.failed(), client.needsReview()].map((r) => r.status);
  assert.deepEqual(statuses, ["completed", "failed", "needs_review"]);
  for (const status of statuses) {
    assert.ok(manifest.resultStatuses.includes(status));
  }
  assert.ok(manifest.resultStatuses.includes("cancelled"));
});

test("contract: watch stream yields only frozen event types and ends on closed", async () => {
  await withSSEServer((call, res) => {
    assert.equal(call.headers.accept, "text/event-stream");
    res.writeHead(200, { "Content-Type": "text/event-stream" });
    writeSSEFrame(res, "1", "snapshot", { epicId: "EPIC-1" });
    writeSSEFrame(res, "2", "taskRun", { taskRunId: "task-run-1" });
    writeSSEFrame(res, "3", "closed", { code: "parent_not_running" });
    res.end();
  }, async ({ apiUrl }) => {
    const client = newClient(apiUrl);
    const events = [];
    for await (const event of client.epics.watch({})) {
      events.push(event);
    }
    assert.deepEqual(events.map((e) => e.type), ["snapshot", "taskRun", "closed"]);
    for (const event of events) {
      assert.ok(manifest.watch.eventTypes.includes(event.type), `watch event type ${event.type} outside the contract`);
    }
    assert.equal(events.at(-1).type, manifest.watch.terminalEventType);
  });
});

function satisfiedAwait(awaitIndex, status = "satisfied") {
  return {
    status,
    instanceKey: `run-1#await-${awaitIndex}`,
    pattern: "approval:octo/hello#1@sha",
    deadline: "2026-06-12T00:00:00Z",
    event: { id: "evt-1", payload: { ok: true }, actor: "reviewer", occurredAt: "2026-06-11T00:00:00Z" },
  };
}

function writeSSEFrame(res, id, event, data) {
  res.write(`id: ${id}\nevent: ${event}\ndata: ${JSON.stringify(data)}\n\n`);
}

function assertNoSnakeCaseKeys(value) {
  if (!value || typeof value !== "object") {
    return;
  }
  if (Array.isArray(value)) {
    for (const item of value) {
      assertNoSnakeCaseKeys(item);
    }
    return;
  }
  for (const [key, nested] of Object.entries(value)) {
    assert.equal(key.includes("_"), false, "snake_case wire key found: " + key);
    assertNoSnakeCaseKeys(nested);
  }
}

async function withDriverServer(handler, fn) {
  const calls = [];
  const server = createServer(async (req, res) => {
    const chunks = [];
    for await (const chunk of req) {
      chunks.push(chunk);
    }
    const bodyText = Buffer.concat(chunks).toString("utf8");
    const body = bodyText ? JSON.parse(bodyText) : {};
    const call = { method: req.method, url: req.url, headers: req.headers, body };
    calls.push(call);
    const result = await handler(call, calls);
    const statusCode = result && typeof result.statusCode === "number" ? result.statusCode : 200;
    const responseBody = result && Object.hasOwn(result, "body") ? result.body : result;
    res.statusCode = statusCode;
    res.setHeader("Content-Type", "application/json");
    res.end(JSON.stringify(responseBody ?? null));
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  const address = server.address();
  try {
    await fn({ apiUrl: `http://127.0.0.1:${address.port}`, calls });
  } finally {
    await new Promise((resolve) => server.close(resolve));
  }
}

async function withSSEServer(handler, fn) {
  const calls = [];
  const sockets = new Set();
  const server = createServer((req, res) => {
    const call = { method: req.method, url: req.url, headers: req.headers };
    calls.push(call);
    handler(call, res, calls);
  });
  server.on("connection", (socket) => {
    sockets.add(socket);
    socket.on("close", () => sockets.delete(socket));
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  const address = server.address();
  try {
    await fn({ apiUrl: `http://127.0.0.1:${address.port}`, calls });
  } finally {
    for (const socket of sockets) {
      socket.destroy();
    }
    await new Promise((resolve) => server.close(resolve));
  }
}
