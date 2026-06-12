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

test("FlueDriverClient epics.watch yields snapshot then taskRun frames and stops on closed", async () => {
  const snapshotData = {
    epic: { epicId: "EPIC-1", readyCount: 1, blockedCount: 0 },
    active: { driverRunId: "run-1", activeCount: 0 },
  };
  await withSSEServer((call, res) => {
    res.writeHead(200, { "Content-Type": "text/event-stream" });
    res.write(": heartbeat\n\n");
    writeSSEFrame(res, "0", "snapshot", snapshotData);
    writeSSEFrame(res, "1", "taskRun", { seq: 1, type: "task_run_started", taskRunId: "task-run-1" });
    writeSSEFrame(res, "2", "taskRun", { seq: 2, type: "task_run_completed", taskRunId: "task-run-1" });
    writeSSEFrame(res, "2", "closed", { code: "parent_not_running" });
    res.end();
  }, async ({ apiUrl, calls }) => {
    const client = watchTestClient(apiUrl);
    const events = await collectWatchEvents(client.epics.watch({ epicId: "EPIC-1" }));

    assert.deepEqual(
      events.map((event) => [event.type, event.id]),
      [["snapshot", "0"], ["taskRun", "1"], ["taskRun", "2"], ["closed", "2"]]
    );
    assert.deepEqual(events[0].data, snapshotData);
    assert.equal(events[1].data.taskRunId, "task-run-1");
    assert.deepEqual(events[3].data, { code: "parent_not_running" });

    assert.equal(calls.length, 1);
    assert.equal(calls[0].method, "GET");
    assert.equal(calls[0].url, "/api/workspaces/WS/driver/watch/epic?epicId=EPIC-1");
    assert.equal(calls[0].headers["x-loom-driver-run-id"], "run-1");
    assert.equal(calls[0].headers["x-loom-driver-node-id"], "node-1");
    assert.equal(calls[0].headers["x-loom-driver-lease-id"], "lease-1");
    assert.equal(calls[0].headers["x-loom-driver-fencing-token"], "7");
    assert.equal(calls[0].headers.authorization, "Bearer api-token");
    assert.equal(calls[0].headers["last-event-id"], undefined);
  });
});

test("FlueDriverClient epics.watch reconnects with Last-Event-ID after disconnect", async () => {
  await withSSEServer((call, res, calls) => {
    res.writeHead(200, { "Content-Type": "text/event-stream" });
    if (calls.length === 1) {
      writeSSEFrame(res, "3", "snapshot", { epic: { epicId: "EPIC-1" }, active: null });
      writeSSEFrame(res, "5", "taskRun", { seq: 5, type: "task_run_started" });
      res.end(); // Drop without a closed frame so the client reconnects.
      return;
    }
    writeSSEFrame(res, "6", "taskRun", { seq: 6, type: "task_run_completed" });
    writeSSEFrame(res, "6", "closed", { code: "parent_not_running" });
    res.end();
  }, async ({ apiUrl, calls }) => {
    const client = watchTestClient(apiUrl);
    const events = await collectWatchEvents(
      client.epics.watch({ epicId: "EPIC-1", afterSeq: 3, reconnectMs: 10 })
    );

    assert.deepEqual(
      events.map((event) => [event.type, event.id]),
      [["snapshot", "3"], ["taskRun", "5"], ["taskRun", "6"], ["closed", "6"]]
    );
    assert.equal(calls.length, 2);
    assert.equal(calls[0].headers["last-event-id"], "3");
    assert.equal(calls[1].headers["last-event-id"], "5");
  });
});

test("FlueDriverClient epics.watch ends iteration when the AbortSignal fires", async () => {
  await withSSEServer((call, res) => {
    res.writeHead(200, { "Content-Type": "text/event-stream" });
    writeSSEFrame(res, "1", "snapshot", { epic: { epicId: "EPIC-1" }, active: null });
    // Hold the stream open; the client aborts mid-iteration.
  }, async ({ apiUrl }) => {
    const controller = new AbortController();
    const client = watchTestClient(apiUrl);
    const events = [];
    for await (const event of client.epics.watch({ epicId: "EPIC-1", signal: controller.signal })) {
      events.push(event);
      controller.abort();
    }
    assert.equal(events.length, 1);
    assert.equal(events[0].type, "snapshot");
  });
});

test("FlueDriverClient epics.watch surfaces HTTP 401 as DriverApiError", async () => {
  await withSSEServer((call, res) => {
    res.writeHead(401, { "Content-Type": "application/json" });
    res.end(JSON.stringify({
      error: { code: "unauthenticated", message: "driver API token required", retryable: false },
    }));
  }, async ({ apiUrl }) => {
    const client = watchTestClient(apiUrl);
    await assert.rejects(
      () => collectWatchEvents(client.epics.watch({ epicId: "EPIC-1" })),
      (err) => {
        assert.ok(err instanceof DriverApiError);
        assert.equal(err.code, "unauthenticated");
        assert.equal(err.message, "driver API token required");
        assert.equal(err.retryable, false);
        assert.equal(err.status, 401);
        return true;
      }
    );
  });
});

test("FlueDriverClient connectors send camelCase connector-dispatch wire with run headers", async () => {
  await withDriverServer(async (call) => {
    if (call.url === "/api/workspaces/WS/driver/connector-dispatch") {
      // withDriverServer treats a top-level "body" key as the response
      // wrapper, so the connector result rides inside it.
      return {
        statusCode: 200,
        body: { callId: "cc:run-1:" + call.body.action + ":" + call.body.callSeq, decision: "granted", status: 200, body: { merged: true } },
      };
    }
    return notFound();
  }, async ({ apiUrl, calls }) => {
    const client = watchTestClient(apiUrl);

    const result = await client.connectors.github.merge({
      connectorId: "gh-main",
      resource: "repo:octo/hello",
      owner: "octo",
      repo: "hello",
      number: 7,
      mergeMethod: "squash",
      expectedHeadSha: "abc123",
    });
    assert.equal(result.decision, "granted");
    assert.equal(result.callId, "cc:run-1:github.merge:1");
    assert.deepEqual(result.body, { merged: true });

    assert.equal(calls.length, 1);
    assert.equal(calls[0].method, "POST");
    assert.equal(calls[0].url, "/api/workspaces/WS/driver/connector-dispatch");
    assert.equal(calls[0].headers["x-loom-driver-run-id"], "run-1");
    assert.equal(calls[0].headers["x-loom-driver-node-id"], "node-1");
    assert.equal(calls[0].headers["x-loom-driver-lease-id"], "lease-1");
    assert.equal(calls[0].headers["x-loom-driver-fencing-token"], "7");
    assert.equal(calls[0].headers.authorization, "Bearer api-token");
    assert.deepEqual(calls[0].body, {
      connectorId: "gh-main",
      action: "github.merge",
      resource: "repo:octo/hello",
      args: { owner: "octo", repo: "hello", number: 7, mergeMethod: "squash" },
      preconditions: { expectedHeadSha: "abc123" },
      callSeq: 1,
    });
    assertNoSnakeCaseKeys(calls[0].body);
  });
});

test("FlueDriverClient connectors map every surface method onto its dispatch action", async () => {
  const table = [
    { source: "github", method: "merge", action: "github.merge", input: { expectedHeadSha: "sha-1" } },
    { source: "github", method: "postReview", action: "github.review.post", input: { expectedHeadSha: "sha-1", event: "APPROVE" } },
    { source: "github", method: "readPullRequest", action: "github.pull_request.read", input: { owner: "o", repo: "r", number: 1 } },
    { source: "github", method: "listPulls", action: "github.pulls.list", input: { owner: "o", repo: "r", state: "open" } },
    { source: "github", method: "compare", action: "github.compare.read", input: { owner: "o", repo: "r", base: "main", head: "dev" } },
    { source: "github", method: "postIssueComment", action: "github.issue_comment.post", input: { owner: "o", repo: "r", number: 1, body: "hi" } },
    { source: "slack", method: "post", action: "slack.chat.post", input: { channel: "C1", text: "hi" } },
    { source: "slack", method: "readConversations", action: "slack.conversations.read", input: { channel: "C1" } },
    { source: "datadog", method: "readMonitors", action: "datadog.monitors.read", input: { name: "api" } },
    { source: "datadog", method: "readAlert", action: "datadog.alert.read", input: { monitorId: 42 } },
    { source: "datadog", method: "declareIncident", action: "datadog.incidents.write", input: { monitorId: 42, title: "down" } },
  ];
  await withDriverServer(async (call) => {
    if (call.url === "/api/workspaces/WS/driver/connector-dispatch") {
      return { callId: "cc", decision: "granted" };
    }
    return notFound();
  }, async ({ apiUrl, calls }) => {
    const client = watchTestClient(apiUrl);
    for (const row of table) {
      await client.connectors[row.source][row.method]({ connectorId: "conn-1", resource: "res:1", ...row.input });
    }
    assert.equal(calls.length, table.length);
    for (const [i, row] of table.entries()) {
      assert.equal(calls[i].body.action, row.action, `${row.source}.${row.method}`);
      assert.equal(calls[i].body.connectorId, "conn-1");
      assert.equal(calls[i].body.callSeq, 1, `${row.source}.${row.method} starts its own per-action sequence`);
      assertNoSnakeCaseKeys(calls[i].body);
    }
  });
});

test("FlueDriverClient connectors refuse precondition-gated ops synchronously before any network call", async () => {
  const table = [
    { name: "github.merge flat", call: (c) => c.connectors.github.merge({ owner: "o", repo: "r", number: 1 }), want: /expectedHeadSha/ },
    { name: "github.merge empty sha", call: (c) => c.connectors.github.merge({ owner: "o", repo: "r", number: 1, expectedHeadSha: "  " }), want: /expectedHeadSha/ },
    { name: "github.postReview", call: (c) => c.connectors.github.postReview({ owner: "o", repo: "r", number: 1, event: "APPROVE" }), want: /expectedHeadSha/ },
    { name: "registry-only action via dispatch", call: (c) => c.connectors.dispatch({ action: "slack.chat.delete", channel: "C1" }), want: /expectedMessageTs/ },
  ];
  await withDriverServer(async () => notFound(), async ({ apiUrl, calls }) => {
    const client = watchTestClient(apiUrl);
    for (const row of table) {
      assert.throws(() => row.call(client), (err) => {
        assert.ok(err instanceof DriverApiError, row.name);
        assert.equal(err.code, "precondition_required", row.name);
        assert.equal(err.retryable, false, row.name);
        assert.match(err.message, row.want, row.name);
        return true;
      }, row.name);
    }
    assert.equal(calls.length, 0, "client-side refusal must not reach the wire");

    // The refusals above consumed no sequence numbers: the first granted
    // merge still gets callSeq 1.
    await assert.rejects(() => client.connectors.github.merge({ owner: "o", repo: "r", number: 1, expectedHeadSha: "sha" }));
    assert.equal(calls.length, 1);
    assert.equal(calls[0].body.callSeq, 1);
  });
});

test("FlueDriverClient connectors accept preconditions via the nested object", async () => {
  await withDriverServer(async () => ({ callId: "cc", decision: "granted" }), async ({ apiUrl, calls }) => {
    const client = watchTestClient(apiUrl);
    await client.connectors.github.merge({
      owner: "o",
      repo: "r",
      number: 1,
      preconditions: { expectedHeadSha: "nested-sha" },
    });
    assert.deepEqual(calls[0].body.preconditions, { expectedHeadSha: "nested-sha" });
    assert.deepEqual(calls[0].body.args, { owner: "o", repo: "r", number: 1 });
  });
});

test("FlueDriverClient connectors surface structured connector refusals as DriverApiError", async () => {
  const table = [
    { name: "grant denied", statusCode: 403, code: "grant_denied", retryable: false },
    { name: "precondition required (server)", statusCode: 400, code: "precondition_required", retryable: false },
    { name: "stale subject is not auto-retried", statusCode: 409, code: "stale_subject", retryable: false },
    { name: "rate limited", statusCode: 429, code: "rate_limited", retryable: true },
    { name: "upstream error", statusCode: 502, code: "upstream_error", retryable: true },
    { name: "egress unavailable (no vault key)", statusCode: 503, code: "unavailable", retryable: false },
  ];
  for (const row of table) {
    await withDriverServer(async () => ({
      statusCode: row.statusCode,
      body: { error: { code: row.code, message: row.name, retryable: row.retryable } },
    }), async ({ apiUrl }) => {
      const client = watchTestClient(apiUrl);
      await assert.rejects(
        () => client.connectors.slack.post({ connectorId: "slack-1", resource: "channel:C1", channel: "C1", text: "hi" }),
        (err) => {
          assert.ok(err instanceof DriverApiError, row.name);
          assert.equal(err.code, row.code, row.name);
          assert.equal(err.retryable, row.retryable, row.name);
          assert.equal(err.status, row.statusCode, row.name);
          assert.equal(err.message, row.name);
          return true;
        },
        row.name
      );
    });
  }
});

test("FlueDriverClient connectors auto-increment callSeq per action and replay deterministically", async () => {
  const handler = async () => ({ callId: "cc", decision: "granted" });
  const sequence = async (client) => {
    await client.connectors.github.merge({ owner: "o", repo: "r", number: 1, expectedHeadSha: "s1" });
    await client.connectors.slack.post({ channel: "C1", text: "one" });
    await client.connectors.github.merge({ owner: "o", repo: "r", number: 2, expectedHeadSha: "s2" });
    await client.connectors.slack.post({ channel: "C1", text: "two" });
  };

  let firstRun;
  await withDriverServer(handler, async ({ apiUrl, calls }) => {
    await sequence(watchTestClient(apiUrl));
    firstRun = calls.map((call) => [call.body.action, call.body.callSeq]);
  });
  assert.deepEqual(firstRun, [
    ["github.merge", 1],
    ["slack.chat.post", 1],
    ["github.merge", 2],
    ["slack.chat.post", 2],
  ]);

  // Re-entry: a fresh client issuing the same calls in the same order derives
  // the same (action, callSeq) pairs, hence the same idempotency keys.
  await withDriverServer(handler, async ({ apiUrl, calls }) => {
    await sequence(watchTestClient(apiUrl));
    assert.deepEqual(calls.map((call) => [call.body.action, call.body.callSeq]), firstRun);
  });

  // An explicit callSeq overrides the counter without advancing it.
  await withDriverServer(handler, async ({ apiUrl, calls }) => {
    const client = watchTestClient(apiUrl);
    await client.connectors.github.merge({ owner: "o", repo: "r", number: 1, expectedHeadSha: "s1", callSeq: 9 });
    await client.connectors.github.merge({ owner: "o", repo: "r", number: 1, expectedHeadSha: "s1" });
    assert.deepEqual(calls.map((call) => call.body.callSeq), [9, 1]);
  });
});

function watchTestClient(apiUrl) {
  return createLoomDriverClient({
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
}

async function collectWatchEvents(iterator) {
  const events = [];
  for await (const event of iterator) {
    events.push(event);
  }
  return events;
}

function writeSSEFrame(res, id, event, data) {
  res.write(`id: ${id}\nevent: ${event}\ndata: ${JSON.stringify(data)}\n\n`);
}

// withSSEServer hands the handler the raw response so tests control the SSE
// wire bytes directly; open sockets are destroyed on teardown so a stream the
// client abandoned cannot hang server.close.
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
