import { createServer } from "node:http";
import test from "node:test";
import assert from "node:assert/strict";

import { DriverApiError, createLoomDriverClient } from "./flue.js";

test("FlueDriverClient sends camelCase task run requests and remembers child task lease over HTTP", async () => {
  await withDriverServer(async (call) => {
    if (call.url === "/api/workspaces/WS/driver/exec-task") {
      return {
        id: "task-run-1",
        taskRunId: "task-run-1",
        taskId: "TASK-1",
        leaseToken: "lease-token-1",
        status: "completed",
        logsRef: "logs://task-run-1",
        artifactsRef: "artifacts://task-run-1",
        artifactIds: ["artifact-1"],
      };
    }
    if (call.url === "/api/workspaces/WS/driver/complete-task") {
      return { id: "TASK-1", status: "completed" };
    }
    return notFound();
  }, async ({ apiUrl, calls }) => {
    const client = createLoomDriverClient({
      input: { epicId: "EPIC-1" },
      apiToken: "api-token",
      env: {
        LOOM_DRIVER_WORKSPACE: "WS",
        LOOM_DRIVER_RUN_ID: "run-1",
        LOOM_DRIVER_NODE_ID: "node-1",
        LOOM_DRIVER_LEASE_ID: "lease-1",
        LOOM_DRIVER_FENCING_TOKEN: "7",
        LOOM_DRIVER_API_URL: apiUrl,
      },
    });

    const result = await client.taskRuns.request({
      taskId: "TASK-1",
      providerProfile: "flue-local",
      parentSessionId: "lead-session-1",
      nodeId: "target-node",
      supportedProviders: ["flue"],
      capabilities: ["repo"],
      sandboxPlacement: {
        provider: "local",
        sandboxId: "sandbox-1",
        cwd: "/repo",
        repoRef: "main",
      },
    });
    assert.equal(result.status, "completed");
    await client.tasks.complete("TASK-1");

    assert.equal(calls.length, 2);
    assert.equal(calls[0].url, "/api/workspaces/WS/driver/exec-task");
    assert.equal(calls[0].headers["x-loom-driver-run-id"], "run-1");
    assert.equal(calls[0].headers["x-loom-driver-node-id"], "node-1");
    assert.equal(calls[0].headers["x-loom-driver-lease-id"], "lease-1");
    assert.equal(calls[0].headers["x-loom-driver-fencing-token"], "7");
    assert.equal(calls[0].headers.authorization, "Bearer api-token");
    assert.deepEqual(calls[0].body, {
      taskId: "TASK-1",
      providerProfile: "flue-local",
      parentSessionId: "lead-session-1",
      nodeId: "target-node",
      supportedProviders: ["flue"],
      capabilities: ["repo"],
      sandboxPlacement: {
        provider: "local",
        sandboxId: "sandbox-1",
        cwd: "/repo",
        repoRef: "main",
      },
      deferCompletion: true,
      enqueueOnly: true,
    });
    assertNoSnakeCaseKeys(calls[0].body);

    assert.equal(calls[1].url, "/api/workspaces/WS/driver/complete-task");
    assert.deepEqual(calls[1].body, {
      taskId: "TASK-1",
      taskRunId: "task-run-1",
      leaseToken: "lease-token-1",
      logsRef: "logs://task-run-1",
      artifactsRef: "artifacts://task-run-1",
      artifactIds: ["artifact-1"],
    });
    assertNoSnakeCaseKeys(calls[1].body);
  });
});

test("FlueDriverClient exposes epic, agent, active run, and stale recovery helpers over HTTP", async () => {
  await withDriverServer(async (call) => {
    switch (call.url) {
      case "/api/workspaces/WS/driver/epic-get":
        return { id: "EPIC-1", issueType: "epic", title: "Epic" };
      case "/api/workspaces/WS/driver/epic-snapshot":
        return { epicId: "EPIC-1", readyCount: 1, blockedCount: 0, openChildrenCount: 1 };
      case "/api/workspaces/WS/driver/list-agents":
        return [{ name: "nova", roleName: "lead", parent: "" }];
      case "/api/workspaces/WS/driver/agent-orchestration-session":
        return { agentName: "nova", orchestratorSessionId: "session-1" };
      case "/api/workspaces/WS/driver/update-agent-parent":
        return { name: "nova", roleName: "lead", parent: "EPIC-1" };
      case "/api/workspaces/WS/driver/deliver-lead-assignment":
        return { agentName: "nova", state: "delivered", sessionId: "session-1" };
      case "/api/workspaces/WS/driver/deliver-agent-message":
        return { agentName: "nova", state: "delivered", sessionId: "session-1" };
      case "/api/workspaces/WS/driver/claim-ready":
        return { id: "TASK-1", title: "Task" };
      case "/api/workspaces/WS/driver/active-task-runs":
        return { driverRunId: "run-1", activeCount: 2 };
      case "/api/workspaces/WS/driver/recover-stale-tasks":
        return { driverRunId: "run-1", recovered: 1, released: 1 };
      default:
        return notFound();
    }
  }, async ({ apiUrl, calls }) => {
    const client = createLoomDriverClient({
      input: { epicId: "EPIC-1" },
      env: {
        LOOM_DRIVER_WORKSPACE: "WS",
        LOOM_DRIVER_RUN_ID: "run-1",
        LOOM_DRIVER_API_URL: apiUrl,
      },
    });
    assert.equal(client.messageLeadAgent, undefined);

    assert.equal((await client.epics.get()).issueType, "epic");
    assert.equal((await client.epics.snapshot()).readyCount, 1);
    assert.equal((await client.agents.list())[0].name, "nova");
    assert.equal((await client.agents.orchestrationSession({ agent: "nova" })).orchestratorSessionId, "session-1");
    assert.equal((await client.agents.updateParent({ agent: "nova", parent: "EPIC-1", expectParent: "" })).parent, "EPIC-1");
    assert.equal((await client.agents.deliverAssignment({ agent: "nova" })).state, "delivered");
    assert.equal((await client.agents.message({ agent: "nova", message: "Task TASK-1 completed" })).state, "delivered");
    assert.equal((await client.tasks.claimReady()).id, "TASK-1");
    assert.equal((await client.taskRuns.active({ limit: 10 })).activeCount, 2);
    assert.equal((await client.taskRuns.recoverStale({ maxAgeSeconds: 30 })).recovered, 1);

    assert.deepEqual(calls.map((call) => call.url), [
      "/api/workspaces/WS/driver/epic-get",
      "/api/workspaces/WS/driver/epic-snapshot",
      "/api/workspaces/WS/driver/list-agents",
      "/api/workspaces/WS/driver/agent-orchestration-session",
      "/api/workspaces/WS/driver/update-agent-parent",
      "/api/workspaces/WS/driver/deliver-lead-assignment",
      "/api/workspaces/WS/driver/deliver-agent-message",
      "/api/workspaces/WS/driver/claim-ready",
      "/api/workspaces/WS/driver/active-task-runs",
      "/api/workspaces/WS/driver/recover-stale-tasks",
    ]);
    assert.deepEqual(calls[0].body, { epicId: "EPIC-1" });
    assert.deepEqual(calls[1].body, { epicId: "EPIC-1" });
    assert.deepEqual(calls[3].body, { agent: "nova" });
    assert.deepEqual(calls[4].body, { agent: "nova", parent: "EPIC-1" });
    assert.deepEqual(calls[5].body, { agent: "nova" });
    assert.deepEqual(calls[6].body, { agent: "nova", message: "Task TASK-1 completed" });
    assert.deepEqual(calls[7].body, { epicId: "EPIC-1" });
    assert.deepEqual(calls[8].body, { epicId: "EPIC-1", limit: 10 });
    assert.deepEqual(calls[9].body, { maxAgeSeconds: 30 });
    for (const call of calls) {
      assertNoSnakeCaseKeys(call.body);
    }
  });
});

test("FlueDriverClient task request enqueues and await polls terminal result", async () => {
  let getCount = 0;
  await withDriverServer(async (call) => {
    if (call.url === "/api/workspaces/WS/driver/exec-task") {
      return { id: call.body.taskRunId, taskRunId: call.body.taskRunId, taskId: call.body.taskId, status: "queued" };
    }
    if (call.url === "/api/workspaces/WS/driver/task-run-get") {
      getCount += 1;
      const status = getCount < 2 ? "running" : "completed";
      return {
        id: call.body.taskRunId,
        taskRunId: call.body.taskRunId,
        taskId: "TASK-1",
        status,
        logsRef: status === "completed" ? "logs://task-run-1" : "",
      };
    }
    return notFound();
  }, async ({ apiUrl, calls }) => {
    const client = createLoomDriverClient({
      input: { epicId: "EPIC-1" },
      env: {
        LOOM_DRIVER_WORKSPACE: "WS",
        LOOM_DRIVER_RUN_ID: "run-1",
        LOOM_DRIVER_API_URL: apiUrl,
      },
    });

    const queued = await client.taskRuns.request({ taskId: "TASK-1", taskRunId: "task-run-1", providerProfile: "flue-local" });
    assert.equal(queued.status, "queued");
    const completed = await client.taskRuns.await({ taskRunId: queued.taskRunId, pollMs: 10, timeoutMs: 1000 });
    assert.equal(completed.status, "completed");
    assert.equal(completed.logsRef, "logs://task-run-1");

    assert.deepEqual(calls.map((call) => call.url), [
      "/api/workspaces/WS/driver/exec-task",
      "/api/workspaces/WS/driver/task-run-get",
      "/api/workspaces/WS/driver/task-run-get",
    ]);
    assert.equal(calls[0].body.enqueueOnly, true);
    assert.equal(calls[0].body.taskId, "TASK-1");
    assert.equal(calls[0].body.taskRunId, "task-run-1");
  });
});

test("FlueDriverClient can complete one child while another child is still polling", async () => {
  const events = [];
  await withDriverServer(async (call) => {
    if (call.url === "/api/workspaces/WS/driver/exec-task") {
      events.push("request-" + call.body.taskId);
      return {
        id: "task-run-" + call.body.taskId.toLowerCase(),
        taskRunId: "task-run-" + call.body.taskId.toLowerCase(),
        taskId: call.body.taskId,
        status: "queued",
      };
    }
    if (call.url === "/api/workspaces/WS/driver/task-run-get") {
      const task = String(call.body.taskRunId).replace("task-run-", "").toUpperCase();
      events.push("poll-start-" + task);
      await sleep(task === "SLOW" ? 250 : 20);
      events.push("poll-end-" + task);
      return {
        id: call.body.taskRunId,
        taskRunId: call.body.taskRunId,
        taskId: task,
        leaseToken: "token-" + task,
        status: "completed",
      };
    }
    if (call.url === "/api/workspaces/WS/driver/complete-task") {
      events.push("complete-" + call.body.taskId);
      return { id: call.body.taskId, status: "completed" };
    }
    return notFound();
  }, async ({ apiUrl }) => {
    const client = createLoomDriverClient({
      input: { epicId: "EPIC-1" },
      env: {
        LOOM_DRIVER_WORKSPACE: "WS",
        LOOM_DRIVER_RUN_ID: "run-1",
        LOOM_DRIVER_API_URL: apiUrl,
      },
    });

    async function requestAwaitAndComplete(taskId) {
      const queued = await client.taskRuns.request({ taskId, providerProfile: "flue-local" });
      const result = await client.taskRuns.await({ taskRunId: queued.taskRunId, pollMs: 10, timeoutMs: 1000 });
      await client.tasks.complete({
        taskId,
        taskRunId: result.taskRunId,
        leaseToken: result.leaseToken,
      });
    }

    await Promise.all([
      requestAwaitAndComplete("FAST"),
      requestAwaitAndComplete("SLOW"),
    ]);

    assert.ok(events.includes("poll-start-FAST"), "FAST task should poll");
    assert.ok(events.includes("poll-start-SLOW"), "SLOW task should poll");
    assert.ok(events.includes("complete-FAST"), "FAST task should complete");
    assert.ok(events.includes("poll-end-SLOW"), "SLOW task should finish polling");
    assert.ok(
      events.indexOf("complete-FAST") < events.indexOf("poll-end-SLOW"),
      "FAST completion should not wait for SLOW polling; events: " + events.join(",")
    );
  });
});

test("FlueDriverClient requires the driver HTTP API URL", async () => {
  const client = createLoomDriverClient({
    input: { epicId: "EPIC-1" },
    env: {
      LOOM_DRIVER_WORKSPACE: "WS",
      LOOM_DRIVER_RUN_ID: "run-1",
    },
  });

  await assert.rejects(
    () => client.epics.get(),
    /LOOM_DRIVER_API_URL is required for the driver HTTP API/
  );
});

test("FlueDriverClient returns needs_review results", () => {
  const client = createLoomDriverClient({
    input: {},
    env: {
      LOOM_DRIVER_WORKSPACE: "WS",
      LOOM_DRIVER_RUN_ID: "run-1",
      LOOM_DRIVER_API_URL: "http://127.0.0.1:1",
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

test("FlueDriverClient maps structured driver API errors", async () => {
  await withDriverServer(async () => ({
    statusCode: 409,
    body: {
      error: {
        code: "lease_conflict",
        message: "lease conflict",
        retryable: true,
        details: { leaseId: "other" },
      },
    },
  }), async ({ apiUrl }) => {
    const client = createLoomDriverClient({
      input: { epicId: "EPIC-1" },
      env: {
        LOOM_DRIVER_WORKSPACE: "WS",
        LOOM_DRIVER_RUN_ID: "run-1",
        LOOM_DRIVER_API_URL: apiUrl,
      },
    });

    await assert.rejects(async () => client.epics.get(), (err) => {
      assert.ok(err instanceof DriverApiError);
      assert.equal(err.message, "lease conflict");
      assert.equal(err.code, "lease_conflict");
      assert.equal(err.retryable, true);
      assert.equal(err.status, 409);
      assert.deepEqual(err.details, { leaseId: "other" });
      return true;
    });
  });
});

async function withDriverServer(handler, fn) {
  const calls = [];
  const server = createServer(async (req, res) => {
    const chunks = [];
    for await (const chunk of req) {
      chunks.push(chunk);
    }
    const bodyText = Buffer.concat(chunks).toString("utf8");
    const body = bodyText ? JSON.parse(bodyText) : {};
    const call = {
      method: req.method,
      url: req.url,
      headers: req.headers,
      body,
    };
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

function notFound() {
  return {
    statusCode: 404,
    body: {
      error: {
        code: "unknown_op",
        message: "unknown op",
        retryable: false,
      },
    },
  };
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
    assert.equal(key.includes("_"), false, "snake_case key found: " + key);
    assertNoSnakeCaseKeys(nested);
  }
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
