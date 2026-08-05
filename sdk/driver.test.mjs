import { createServer } from "node:http";
import test from "node:test";
import assert from "node:assert/strict";

import { DriverApiError, WorkflowSuspended, createLoomDriverClient, isWorkflowSuspended } from "./driver.js";

test("LoomDriverClient sends camelCase task run requests without exposing a worker lease", async () => {
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
        LOOM_TASK_RUN_LEASE_TOKEN: "same-run-token",
      },
    });

    const result = await client.taskRuns.request({
      taskId: "TASK-1",
      runner: "local-task-runner",
      parentSessionId: "lead-session-1",
      nodeId: "target-node",
      capabilities: ["repo"],
    });
    assert.equal(result.status, "completed");
	assert.equal(result.leaseToken, undefined);
    await client.tasks.complete({ taskId: "TASK-1", leaseToken: "caller-controlled-token" });

    assert.equal(calls.length, 2);
    assert.equal(calls[0].url, "/api/workspaces/WS/driver/exec-task");
    assert.equal(calls[0].headers["x-loom-driver-run-id"], "run-1");
    assert.equal(calls[0].headers["x-loom-driver-node-id"], "node-1");
    assert.equal(calls[0].headers["x-loom-driver-lease-id"], "lease-1");
    assert.equal(calls[0].headers["x-loom-driver-fencing-token"], "7");
    assert.equal(calls[0].headers.authorization, "Bearer api-token");
    assert.deepEqual(calls[0].body, {
      taskId: "TASK-1",
      runner: "local-task-runner",
      parentSessionId: "lead-session-1",
      nodeId: "target-node",
      capabilities: ["repo"],
      deferCompletion: true,
      enqueueOnly: true,
    });
    assertNoSnakeCaseKeys(calls[0].body);

    assert.equal(calls[1].url, "/api/workspaces/WS/driver/complete-task");
    assert.deepEqual(calls[1].body, {
      taskId: "TASK-1",
      taskRunId: "task-run-1",
      leaseToken: "same-run-token",
      logsRef: "logs://task-run-1",
      artifactsRef: "artifacts://task-run-1",
      artifactIds: ["artifact-1"],
    });
    assertNoSnakeCaseKeys(calls[1].body);
  });
});

test("LoomDriverClient.taskRuns.request puts the optional input payload (diff+rubric) on the exec-task wire verbatim", async () => {
  // Closes the dropped-payload gap: input.input must reach exec-task body.input
  // unmodified (the review TaskRun runner reads the diff+rubric from there).
  const reviewInput = {
    kind: "github-review",
    repo: "octo/hello",
    prNumber: 7,
    headSha: "sha-head",
    baseRef: "main",
    diff: "--- a/x.go\n+++ b/x.go\n@@ -1 +1 @@\n-old\n+new\n",
    rubric: { mustPass: ["builds", "tests"], notes: "" },
  };
  await withDriverServer(async (call) => {
    if (call.url === "/api/workspaces/WS/driver/exec-task") {
      return { id: "task-run-1", taskRunId: "task-run-1", taskId: "TASK-1", status: "queued" };
    }
    return notFound();
  }, async ({ apiUrl, calls }) => {
    const client = createLoomDriverClient({
      input: { epicId: "EPIC-1" },
      env: { LOOM_DRIVER_WORKSPACE: "WS", LOOM_DRIVER_RUN_ID: "run-1", LOOM_DRIVER_API_URL: apiUrl },
    });

    await client.taskRuns.request({
      taskId: "TASK-1",
      runner: "local-task-runner",
      input: reviewInput,
    });

    assert.equal(calls.length, 1);
    assert.equal(calls[0].url, "/api/workspaces/WS/driver/exec-task");
    // The field name the server handler reads (driverapi execTaskParams.Input,
    // json:"input") — a raw JSON object, never stringified.
    assert.equal(typeof calls[0].body.input, "object");
    // Verbatim: rawKeys bypasses compactParams, so nested empties (rubric.notes:
    // "") and falsy-but-meaningful values survive intact.
    assert.deepEqual(calls[0].body.input, reviewInput);
    assert.deepEqual(calls[0].body.deferCompletion, true);
    assert.deepEqual(calls[0].body.enqueueOnly, true);
  });
});

test("LoomDriverClient.taskRuns.request omits input from the wire when none is given", async () => {
  await withDriverServer(async (call) => {
    if (call.url === "/api/workspaces/WS/driver/exec-task") {
      return { id: "task-run-1", taskRunId: "task-run-1", taskId: "TASK-1", status: "queued" };
    }
    return notFound();
  }, async ({ apiUrl, calls }) => {
    const client = createLoomDriverClient({
      input: { epicId: "EPIC-1" },
      env: { LOOM_DRIVER_WORKSPACE: "WS", LOOM_DRIVER_RUN_ID: "run-1", LOOM_DRIVER_API_URL: apiUrl },
    });

    await client.taskRuns.request({ taskId: "TASK-1", runner: "local-task-runner" });

    assert.equal(calls.length, 1);
    assert.equal(Object.hasOwn(calls[0].body, "input"), false, "no input key when caller omits it");
    assert.equal(Object.hasOwn(calls[0].body, "closeTask"), false, "no closeTask key when caller omits it");
  });
});

test("LoomDriverClient carries the retained-review claim from child request to fenced handoff", async () => {
  await withDriverServer(async (call) => {
    if (call.url === "/api/workspaces/WS/driver/exec-task") {
      return { taskRunId: "review-child-1", taskId: "TASK-1", status: "queued" };
    }
    if (call.url === "/api/workspaces/WS/driver/handoff-review") {
      return { id: "TASK-1", status: "open", released: true };
    }
    return notFound();
  }, async ({ apiUrl, calls }) => {
    const client = createLoomDriverClient({
      env: { LOOM_DRIVER_WORKSPACE: "WS", LOOM_DRIVER_RUN_ID: "review-parent-1", LOOM_DRIVER_API_URL: apiUrl },
    });

    await client.taskRuns.request({
      taskId: "TASK-1",
      taskRunId: "review-child-1",
      runner: "github-review-task-runner",
      closeTask: false,
      retainWorkItemClaim: true,
    });
    await client.tasks.handoffReview({
      taskId: "TASK-1",
      taskRunId: "review-child-1",
      status: "open",
      reason: "review findings require changes",
    });

    assert.equal(calls.length, 2);
    assert.deepEqual(calls[0].body, {
      taskId: "TASK-1",
      taskRunId: "review-child-1",
      runner: "github-review-task-runner",
      deferCompletion: true,
      closeTask: false,
      retainWorkItemClaim: true,
      enqueueOnly: true,
    });
    assert.deepEqual(calls[1].body, {
      taskId: "TASK-1",
      taskRunId: "review-child-1",
      status: "open",
      reason: "review findings require changes",
    });
  });
});

test("LoomDriverClient rejects incomplete review handoffs before HTTP", async () => {
  await withDriverServer(async () => notFound(), async ({ apiUrl, calls }) => {
    const client = createLoomDriverClient({
      env: { LOOM_DRIVER_WORKSPACE: "WS", LOOM_DRIVER_RUN_ID: "review-parent-1", LOOM_DRIVER_API_URL: apiUrl },
    });

    await assert.rejects(client.tasks.handoffReview({ taskId: "TASK-1", status: "open" }), /requires taskRunId/);
    await assert.rejects(
      client.tasks.handoffReview({ taskId: "TASK-1", taskRunId: "review-child-1", status: "needs_review" }),
      /status must be "open", "review", or "closed"/,
    );
    await assert.rejects(
      client.tasks.handoffReview({ taskId: "TASK-1", taskRunId: "review-child-1", status: "review" }),
      /requires priority/,
    );
    await assert.rejects(
      client.tasks.handoffReview({
        taskId: "TASK-1", taskRunId: "review-child-1", status: "review", priority: 2,
      }),
      /requires nonblank commentBody/,
    );
    await assert.rejects(
      client.tasks.handoffReview({
        taskId: "TASK-1", taskRunId: "review-child-1", status: "review",
        priority: 2.5, commentBody: "triaged",
      }),
      /priority as an integer/,
    );
    await assert.rejects(
      client.tasks.handoffReview({
        taskId: "TASK-1", taskRunId: "review-child-1", status: "review",
        priority: 2, labels: "bug", commentBody: "triaged",
      }),
      /labels must be an array of strings/,
    );
    await assert.rejects(
      client.tasks.handoffReview({
        taskId: "TASK-1", taskRunId: "review-child-1", status: "open", labels: [],
      }),
      /only valid for review status/,
    );
    await assert.rejects(
      client.tasks.handoffReview({
        taskId: "TASK-1", taskRunId: "review-child-1", status: "review",
        priority: 2, commentBody: "documented", externalRef: "local-branch:loom/docs@bad",
      }),
      /externalRef must be a canonical local-branch reference/,
    );
    await assert.rejects(
      client.tasks.handoffReview({
        taskId: "TASK-1", taskRunId: "review-child-1", status: "review",
        priority: 2, commentBody: "documented",
        externalRef: `local-branch:bad branch@${"a".repeat(40)}`,
      }),
      /externalRef must be a canonical local-branch reference/,
    );
    assert.equal(calls.length, 0);
  });
});

test("LoomDriverClient serializes atomic Review annotations in camelCase", async () => {
  await withDriverServer(async (call) => {
    if (call.url === "/api/workspaces/WS/driver/handoff-review") {
      return { id: "TASK-1", status: "review", released: true };
    }
    return notFound();
  }, async ({ apiUrl, calls }) => {
    const client = createLoomDriverClient({
      env: { LOOM_DRIVER_WORKSPACE: "WS", LOOM_DRIVER_RUN_ID: "review-parent-1", LOOM_DRIVER_API_URL: apiUrl },
    });

    await client.tasks.handoffReview({
      taskId: "TASK-1",
      taskRunId: "review-child-1",
      status: "review",
      priority: 0,
      labels: ["bug", "triaged"],
      commentBody: "Automated triage completed.",
      externalRef: `local-branch:loom/TASK-1@${"a".repeat(40)}`,
    });

    assert.deepEqual(calls[0].body, {
      taskId: "TASK-1",
      taskRunId: "review-child-1",
      status: "review",
      priority: 0,
      labels: ["bug", "triaged"],
      commentBody: "Automated triage completed.",
      externalRef: `local-branch:loom/TASK-1@${"a".repeat(40)}`,
    });
  });
});

test("LoomDriverClient.taskRuns.request serializes repoRef at both outgoing placement boundaries", async () => {
  await withDriverServer(async (call) => {
    if (call.url === "/api/workspaces/WS/driver/exec-task") {
      return { id: "task-run-1", taskRunId: "task-run-1", taskId: "TASK-1", status: "queued" };
    }
    return notFound();
  }, async ({ apiUrl, calls }) => {
    const client = createLoomDriverClient({
      input: { epicId: "EPIC-1" },
      env: { LOOM_DRIVER_WORKSPACE: "WS", LOOM_DRIVER_RUN_ID: "run-1", LOOM_DRIVER_API_URL: apiUrl },
    });

    await client.taskRuns.request({
      taskId: "TASK-1",
      runner: "local-task-runner",
      repoRef: "phase4-terra-ui-repo-fixed",
    });

    assert.equal(calls.length, 1);
    assert.equal(calls[0].url, "/api/workspaces/WS/driver/exec-task");
    assert.equal(calls[0].body.repoRef, "phase4-terra-ui-repo-fixed");
    assert.deepEqual(calls[0].body.sandboxPlacement, {
      repoRef: "phase4-terra-ui-repo-fixed",
    });
  });
});

test("LoomDriverClient.taskRuns.request replays the exact command after a committed response disconnect", async () => {
  let requestNumber = 0;
  await withDriverServer(async (call) => {
    if (call.url !== "/api/workspaces/WS/driver/exec-task") {
      return notFound();
    }
    requestNumber += 1;
    if (requestNumber === 1) {
      // Model: Fleet committed its immutable request receipt, then the public
      // response connection disappeared before the SDK could read it.
      return { destroySocket: true };
    }
    return { id: "task-run-1", taskRunId: "task-run-1", taskId: "TASK-1", status: "queued", replayed: true };
  }, async ({ apiUrl, calls }) => {
    const client = createLoomDriverClient({
      input: { epicId: "EPIC-1" },
      env: { LOOM_DRIVER_WORKSPACE: "WS", LOOM_DRIVER_RUN_ID: "run-1", LOOM_DRIVER_API_URL: apiUrl },
    });

    const result = await client.taskRuns.request({ taskId: "TASK-1", taskRunId: "task-run-1", runner: "local-task-runner" });
    assert.equal(result.taskRunId, "task-run-1");
    assert.equal(result.replayed, true);
    assert.equal(calls.length, 2);
    assert.deepEqual(calls[1].body, calls[0].body, "lost-response retry must preserve exact request identity");
  });
});

test("LoomDriverClient.taskRuns.request forwards closeTask=false verbatim (planner close-suppression)", async () => {
  await withDriverServer(async (call) => {
    if (call.url === "/api/workspaces/WS/driver/exec-task") {
      return { id: "task-run-1", taskRunId: "task-run-1", taskId: "TASK-1", status: "queued" };
    }
    return notFound();
  }, async ({ apiUrl, calls }) => {
    const client = createLoomDriverClient({
      input: { epicId: "EPIC-1" },
      env: { LOOM_DRIVER_WORKSPACE: "WS", LOOM_DRIVER_RUN_ID: "run-1", LOOM_DRIVER_API_URL: apiUrl },
    });

    await client.taskRuns.request({ taskId: "TASK-1", runner: "local-task-runner", closeTask: false });

    assert.equal(calls.length, 1);
    // A boolean false must survive to the wire (not dropped by compaction) so
    // the server suppresses the worker's close-on-success for a planner run.
    assert.equal(calls[0].body.closeTask, false);
  });
});

test("LoomDriverClient.tasks.diff calls task-diff with taskId", async () => {
  await withDriverServer(async (call) => {
    if (call.url === "/api/workspaces/WS/driver/task-diff") {
      return {
        taskId: call.body.taskId,
        externalRef: "local-branch:loom/TASK-1@abcdef1",
        branch: "loom/TASK-1",
        headSha: "abcdef1",
        resolvedHead: "abcdef1234567890",
        baseRef: "main",
        baseSha: "1234567890abcdef",
        diff: "diff --git a/x b/x\n",
        sizeBytes: 19,
        limitBytes: 524288,
        egressMechanism: "filesystem-origin",
      };
    }
    return notFound();
  }, async ({ apiUrl, calls }) => {
    const client = createLoomDriverClient({
      input: { epicId: "EPIC-1" },
      env: { LOOM_DRIVER_WORKSPACE: "WS", LOOM_DRIVER_RUN_ID: "run-1", LOOM_DRIVER_API_URL: apiUrl },
    });

    const result = await client.tasks.diff("TASK-1");

    assert.equal(result.taskId, "TASK-1");
    assert.equal(result.diff, "diff --git a/x b/x\n");
    assert.equal(calls.length, 1);
    assert.equal(calls[0].url, "/api/workspaces/WS/driver/task-diff");
    assert.deepEqual(calls[0].body, { taskId: "TASK-1" });
  });
});

test("LoomDriverClient.taskRuns.request ignores legacy provider routing inputs", async () => {
  await withDriverServer(async (call) => {
    if (call.url === "/api/workspaces/WS/driver/exec-task") {
      return { id: "task-run-1", taskRunId: "task-run-1", taskId: "TASK-1", status: "queued" };
    }
    return notFound();
  }, async ({ apiUrl, calls }) => {
    const client = createLoomDriverClient({
      input: { epicId: "EPIC-1" },
      env: { LOOM_DRIVER_WORKSPACE: "WS", LOOM_DRIVER_RUN_ID: "run-1", LOOM_DRIVER_API_URL: apiUrl },
    });

    await client.taskRuns.request({
      taskId: "TASK-1",
      runner: "local-task-runner",
      providerProfile: "flue-local",
      supportedProviders: ["flue-local"],
      sandboxPlacement: { provider: "flue-local" },
    });

    assert.equal(calls.length, 1);
    assert.equal(calls[0].body.runner, "local-task-runner");
    assert.equal(Object.hasOwn(calls[0].body, "providerProfile"), false);
    assert.equal(Object.hasOwn(calls[0].body, "supportedProviders"), false);
    assert.equal(Object.hasOwn(calls[0].body, "sandboxPlacement"), false);
    assertNoSnakeCaseKeys(calls[0].body);
  });
});

test("LoomDriverClient exposes epic, agent, active run, and stale recovery helpers over HTTP", async () => {
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

test("LoomDriverClient task request enqueues and await polls terminal result", async () => {
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

    const queued = await client.taskRuns.request({ taskId: "TASK-1", taskRunId: "task-run-1", runner: "local-task-runner" });
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

test("LoomDriverClient can observe one child while another child is still polling", async () => {
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
        status: "completed",
      };
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

    async function requestAndAwait(taskId) {
      const queued = await client.taskRuns.request({ taskId, runner: "local-task-runner" });
      return client.taskRuns.await({ taskRunId: queued.taskRunId, pollMs: 10, timeoutMs: 1000 });
    }

    await Promise.all([
      requestAndAwait("FAST").then(() => events.push("observed-FAST")),
      requestAndAwait("SLOW").then(() => events.push("observed-SLOW")),
    ]);

    assert.ok(events.includes("poll-start-FAST"), "FAST task should poll");
    assert.ok(events.includes("poll-start-SLOW"), "SLOW task should poll");
    assert.ok(events.includes("observed-FAST"), "FAST task should be observed terminal");
    assert.ok(events.includes("poll-end-SLOW"), "SLOW task should finish polling");
    assert.ok(
      events.indexOf("observed-FAST") < events.indexOf("poll-end-SLOW"),
      "FAST observation should not wait for SLOW polling; events: " + events.join(",")
    );
  });
});

test("LoomDriverClient requires the driver HTTP API URL", async () => {
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

test("LoomDriverClient returns needs_review results", () => {
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

test("LoomDriverClient maps structured driver API errors", async () => {
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

test("LoomDriverClient epics.watch yields snapshot then taskRun frames and stops on closed", async () => {
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

test("LoomDriverClient epics.watch reconnects with Last-Event-ID after disconnect", async () => {
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

test("LoomDriverClient epics.watch ends iteration when the AbortSignal fires", async () => {
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

test("LoomDriverClient epics.watch surfaces HTTP 401 as DriverApiError", async () => {
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

test("LoomDriverClient connectors send camelCase connector-dispatch wire with run headers", async () => {
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

test("LoomDriverClient connectors map every surface method onto its dispatch action", async () => {
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

test("LoomDriverClient connectors refuse precondition-gated ops synchronously before any network call", async () => {
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

test("LoomDriverClient connectors accept preconditions via the nested object", async () => {
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

test("LoomDriverClient connectors surface structured connector refusals as DriverApiError", async () => {
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

test("LoomDriverClient connectors auto-increment callSeq per action and replay deterministically", async () => {
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

test("LoomDriverClient events.await sends camelCase wire and derives awaitIndex from call order", async () => {
  await withDriverServer(async (call) => {
    if (call.url === "/api/workspaces/WS/driver/events/await") {
      return {
        status: "satisfied",
        instanceKey: "run-1#await-" + call.body.awaitIndex,
        pattern: call.body.pattern,
        deadline: "2026-06-13T00:00:00Z",
        event: { id: "evt-" + call.body.awaitIndex, payload: { turn: call.body.awaitIndex }, actor: "alice", occurredAt: "2026-06-12T00:00:00Z" },
      };
    }
    return notFound();
  }, async ({ apiUrl, calls }) => {
    const client = watchTestClient(apiUrl);

    const first = await client.events.await({
      pattern: "approval:octo/hello#123@sha-1",
      actor: ["alice", "bob"],
      timeoutMs: 60_000,
    });
    assert.equal(first.status, "satisfied");
    assert.equal(first.instanceKey, "run-1#await-1");
    assert.deepEqual(first.event.payload, { turn: 1 });

    const second = await client.events.await({
      pattern: "slack.thread_reply:C123/1718012345.0001",
      actor: "requester",
      timeout: 30_000,
    });
    assert.equal(second.instanceKey, "run-1#await-2");

    assert.equal(calls.length, 2);
    assert.equal(calls[0].method, "POST");
    assert.equal(calls[0].url, "/api/workspaces/WS/driver/events/await");
    assert.equal(calls[0].headers["x-loom-driver-run-id"], "run-1");
    assert.equal(calls[0].headers["x-loom-driver-node-id"], "node-1");
    assert.equal(calls[0].headers["x-loom-driver-lease-id"], "lease-1");
    assert.equal(calls[0].headers["x-loom-driver-fencing-token"], "7");
    assert.equal(calls[0].headers.authorization, "Bearer api-token");
    assert.deepEqual(calls[0].body, {
      pattern: "approval:octo/hello#123@sha-1",
      actor: ["alice", "bob"],
      timeoutMs: 60_000,
      awaitIndex: 1,
    });
    assert.deepEqual(calls[1].body, {
      pattern: "slack.thread_reply:C123/1718012345.0001",
      actor: ["requester"],
      timeoutMs: 30_000,
      awaitIndex: 2,
    });
    for (const call of calls) {
      assertNoSnakeCaseKeys(call.body);
    }
  });
});

test("LoomDriverClient events.await replays satisfied awaits then throws the suspend sentinel", async () => {
  // Re-entry fast-forward: awaits 1 and 2 were satisfied before the resume,
  // await 3 suspends the run again.
  await withDriverServer(async (call) => {
    if (call.url !== "/api/workspaces/WS/driver/events/await") {
      return notFound();
    }
    if (call.body.awaitIndex <= 2) {
      return {
        status: "satisfied",
        instanceKey: "run-1#await-" + call.body.awaitIndex,
        pattern: call.body.pattern,
        deadline: "2026-06-13T00:00:00Z",
        event: { id: "evt-" + call.body.awaitIndex, payload: { turn: call.body.awaitIndex }, occurredAt: "2026-06-12T00:00:00Z" },
      };
    }
    return { status: "suspended" };
  }, async ({ apiUrl, calls }) => {
    const client = watchTestClient(apiUrl);
    const turns = [];
    let suspendErr;
    try {
      for (;;) {
        const { event } = await client.events.await({ pattern: "slack.thread_reply:C1/1.0", timeoutMs: 1000 });
        turns.push(event.payload.turn);
      }
    } catch (err) {
      suspendErr = err;
    }

    assert.deepEqual(turns, [1, 2]);
    assert.ok(suspendErr instanceof WorkflowSuspended);
    assert.ok(isWorkflowSuspended(suspendErr));
    assert.equal(suspendErr.type, "workflow_suspended");
    assert.equal(suspendErr.awaitIndex, 3);
    assert.ok(suspendErr.message.startsWith("workflow_suspended:"), suspendErr.message);
    assert.deepEqual(suspendErr.result, {
      status: "suspended_awaiting_event",
      summary: "workflow suspended awaiting event",
    });
    assert.deepEqual(calls.map((call) => call.body.awaitIndex), [1, 2, 3]);
  });
});

test("LoomDriverClient awaits refuse missing pattern/timeout synchronously without consuming an index", async () => {
  await withDriverServer(async () => ({
    status: "satisfied",
    instanceKey: "run-1#await-1",
    pattern: "p:1",
    deadline: "2026-06-13T00:00:00Z",
    event: { id: "evt-1", occurredAt: "2026-06-12T00:00:00Z" },
  }), async ({ apiUrl, calls }) => {
    const client = watchTestClient(apiUrl);
    const table = [
      { name: "events.await no timeout", call: () => client.events.await({ pattern: "p:1" }), code: "await_timeout_required" },
      { name: "events.await zero timeout", call: () => client.events.await({ pattern: "p:1", timeoutMs: 0 }), code: "await_timeout_required" },
      { name: "events.await NaN timeout", call: () => client.events.await({ pattern: "p:1", timeout: "soon" }), code: "await_timeout_required" },
      { name: "workflows.await no timeout", call: () => client.workflows.await({ childRunId: "run-c" }), code: "await_timeout_required" },
    ];
    for (const row of table) {
      assert.throws(row.call, (err) => {
        assert.ok(err instanceof DriverApiError, row.name);
        assert.equal(err.code, row.code, row.name);
        assert.equal(err.retryable, false, row.name);
        return true;
      }, row.name);
    }
    assert.throws(() => client.events.await({ timeoutMs: 1000 }), /events\.await requires pattern/);
    assert.equal(calls.length, 0, "synchronous refusals must not reach the wire");

    // None of the refusals consumed an await slot: the first valid await is #1.
    const ok = await client.events.await({ pattern: "p:1", timeoutMs: 1000 });
    assert.equal(ok.instanceKey, "run-1#await-1");
    assert.equal(calls[0].body.awaitIndex, 1);
  });
});

test("LoomDriverClient workflows.start derives startIndex from call order and is re-entry deterministic", async () => {
  const handler = async (call) => {
    if (call.url !== "/api/workspaces/WS/driver/workflows/start") {
      return notFound();
    }
    return {
      childRunId: "run-child-" + (call.body.idempotencyKey || "start-" + call.body.startIndex),
      workflowName: call.body.workflowName,
      status: "queued",
      parentRunId: "run-1",
    };
  };
  let firstRun;
  const sequence = async (client) => {
    const out = [];
    out.push(await client.workflows.start({ workflow: "deploy", input: { env: "prod", note: "" } }));
    out.push(await client.workflows.start({ workflow: "deploy" }));
    out.push(await client.workflows.start({ workflow: "notify", idempotencyKey: "notify-1" }));
    return out;
  };
  await withDriverServer(handler, async ({ apiUrl, calls }) => {
    const children = await sequence(watchTestClient(apiUrl));
    assert.deepEqual(children.map((child) => child.childRunId), [
      "run-child-start-1",
      "run-child-start-2",
      "run-child-notify-1",
    ]);
    assert.deepEqual(calls[0].body, {
      workflowName: "deploy",
      startIndex: 1,
      input: { env: "prod", note: "" },
    });
    assert.equal(calls[0].body.input.note, "", "child input crosses the wire verbatim, not compacted");
    assert.deepEqual(calls[1].body, { workflowName: "deploy", startIndex: 2 });
    assert.deepEqual(calls[2].body, { workflowName: "notify", idempotencyKey: "notify-1" });
    assert.equal(calls[2].body.startIndex, undefined, "an explicit idempotencyKey replaces the counter");
    for (const call of calls) {
      assertNoSnakeCaseKeys(call.body);
    }
    firstRun = calls.map((call) => [call.body.startIndex, call.body.idempotencyKey]);
  });

  // Re-entry: a fresh client issuing the same starts in the same order
  // derives the same identities, so the server replays the same children.
  await withDriverServer(handler, async ({ apiUrl, calls }) => {
    await sequence(watchTestClient(apiUrl));
    assert.deepEqual(calls.map((call) => [call.body.startIndex, call.body.idempotencyKey]), firstRun);
  });
});

test("LoomDriverClient workflows.await shares the await counter and returns the child outcome", async () => {
  await withDriverServer(async (call) => {
    if (call.url === "/api/workspaces/WS/driver/events/await") {
      return {
        status: "satisfied",
        instanceKey: "run-1#await-" + call.body.awaitIndex,
        pattern: call.body.pattern,
        deadline: "2026-06-13T00:00:00Z",
        event: { id: "evt-1", occurredAt: "2026-06-12T00:00:00Z" },
      };
    }
    if (call.url === "/api/workspaces/WS/driver/workflows/await") {
      return {
        status: "satisfied",
        instanceKey: "run-1#await-" + call.body.awaitIndex,
        pattern: "run.finished:" + call.body.childRunId,
        deadline: "2026-06-13T00:00:00Z",
        event: { id: "evt-finish", payload: { runId: call.body.childRunId, status: "completed" }, occurredAt: "2026-06-12T00:00:00Z" },
        child: { runId: call.body.childRunId, status: "completed", summary: "child done" },
      };
    }
    return notFound();
  }, async ({ apiUrl, calls }) => {
    const client = watchTestClient(apiUrl);
    await client.events.await({ pattern: "p:1", timeoutMs: 1000 });
    const finished = await client.workflows.await({ childRunId: "run-child-1", timeoutMs: 5000 });

    assert.equal(finished.status, "satisfied");
    assert.equal(finished.child.status, "completed");
    assert.equal(finished.child.summary, "child done");
    assert.equal(finished.event.payload.runId, "run-child-1");

    assert.equal(calls[1].url, "/api/workspaces/WS/driver/workflows/await");
    assert.deepEqual(calls[1].body, { childRunId: "run-child-1", timeoutMs: 5000, awaitIndex: 2 });
    assertNoSnakeCaseKeys(calls[1].body);
  });
});

test("LoomDriverClient workflows.await throws the suspend sentinel on suspended", async () => {
  await withDriverServer(async (call) => {
    if (call.url === "/api/workspaces/WS/driver/workflows/await") {
      return { status: "suspended" };
    }
    return notFound();
  }, async ({ apiUrl }) => {
    const client = watchTestClient(apiUrl);
    await assert.rejects(
      () => client.workflows.await({ childRunId: "run-child-1", timeoutMs: 5000 }),
      (err) => {
        assert.ok(err instanceof WorkflowSuspended);
        assert.ok(isWorkflowSuspended(err));
        assert.equal(err.awaitIndex, 1);
        return true;
      }
    );
  });
});

test("LoomDriverClient events.await surfaces structured await errors as DriverApiError", async () => {
  const table = [
    { name: "unscoped pattern", statusCode: 400, code: "await_pattern_unscoped" },
    { name: "timeout above cap", statusCode: 400, code: "await_timeout_required" },
    { name: "actor forbidden", statusCode: 403, code: "await_actor_forbidden" },
    { name: "composition depth", statusCode: 400, code: "composition_depth_exceeded" },
  ];
  for (const row of table) {
    await withDriverServer(async () => ({
      statusCode: row.statusCode,
      body: { error: { code: row.code, message: row.name, retryable: false } },
    }), async ({ apiUrl }) => {
      const client = watchTestClient(apiUrl);
      await assert.rejects(
        () => client.events.await({ pattern: "p:1", timeoutMs: 1000 }),
        (err) => {
          assert.ok(err instanceof DriverApiError, row.name);
          assert.equal(err.code, row.code, row.name);
          assert.equal(err.status, row.statusCode, row.name);
          assert.equal(isWorkflowSuspended(err), false, row.name);
          return true;
        },
        row.name
      );
    });
  }
});

test("LoomDriverClient events.list fetches the run's awaits without consuming a slot", async () => {
  await withDriverServer(async (call) => {
    if (call.url === "/api/workspaces/WS/driver/events/awaits") {
      return {
        runId: "run-1",
        awaits: [
          { instanceKey: "run-1#await-1", status: "satisfied", satisfiedByEventId: "evt-1" },
          { instanceKey: "run-1#await-2", status: "pending" },
        ],
      };
    }
    if (call.url === "/api/workspaces/WS/driver/events/await") {
      return {
        status: "satisfied",
        instanceKey: "run-1#await-" + call.body.awaitIndex,
        pattern: call.body.pattern,
        deadline: "2026-06-13T00:00:00Z",
        event: { id: "evt-1", occurredAt: "2026-06-12T00:00:00Z" },
      };
    }
    return notFound();
  }, async ({ apiUrl, calls }) => {
    const client = watchTestClient(apiUrl);
    const listing = await client.events.list();
    assert.equal(listing.runId, "run-1");
    assert.equal(listing.awaits.length, 2);
    assert.equal(calls[0].method, "GET");
    assert.equal(calls[0].url, "/api/workspaces/WS/driver/events/awaits");
    assert.equal(calls[0].headers["x-loom-driver-run-id"], "run-1");

    // Listing consumed no await slot: the next await is still #1.
    const next = await client.events.await({ pattern: "p:1", timeoutMs: 1000 });
    assert.equal(next.instanceKey, "run-1#await-1");
  });
});

test("LoomDriverClient sends token-only auth when LOOM_RUN_TOKEN is set", async () => {
  await withDriverServer(async (call) => {
    if (call.url === "/api/workspaces/WS/driver/epic-get") {
      return { id: "EPIC-1", issueType: "epic" };
    }
    if (call.url === "/api/workspaces/WS/driver/events/await") {
      return {
        status: "satisfied",
        instanceKey: "run-1#await-" + call.body.awaitIndex,
        pattern: call.body.pattern,
        deadline: "2026-06-13T00:00:00Z",
        event: { id: "evt-1", occurredAt: "2026-06-12T00:00:00Z" },
      };
    }
    return notFound();
  }, async ({ apiUrl, calls }) => {
    // The legacy header-quad env AND the static apiToken are deliberately
    // present: the run token must win and the quad must NOT leak onto the
    // wire (a conflicting X-Loom-Driver-Run-Id is a server-side 401
    // identity_mismatch).
    const client = tokenTestClient(apiUrl);
    assert.equal(client.runToken, "run-token-jwt");

    await client.epics.get();
    await client.events.await({ pattern: "p:1", timeoutMs: 1000 });

    assert.equal(calls.length, 2);
    for (const call of calls) {
      assert.equal(call.headers.authorization, "Bearer run-token-jwt", call.url);
      assertNoDriverIdentityHeaders(call.headers, call.url);
    }
    assert.deepEqual(calls[1].body, { pattern: "p:1", timeoutMs: 1000, awaitIndex: 1 });
  });
});

test("LoomDriverClient token-only auth needs no LOOM_DRIVER_RUN_ID (identity rides the claims)", async () => {
  await withDriverServer(async (call) => {
    if (call.url === "/api/workspaces/WS/driver/epic-get") {
      return { id: "EPIC-1", issueType: "epic" };
    }
    return notFound();
  }, async ({ apiUrl, calls }) => {
    const client = createLoomDriverClient({
      input: { epicId: "EPIC-1" },
      env: {
        LOOM_DRIVER_WORKSPACE: "WS",
        LOOM_DRIVER_API_URL: apiUrl,
        LOOM_RUN_TOKEN: "run-token-jwt",
      },
    });
    assert.equal((await client.epics.get()).issueType, "epic");
    assert.equal(calls[0].headers.authorization, "Bearer run-token-jwt");
    assertNoDriverIdentityHeaders(calls[0].headers, calls[0].url);

    // Without either credential source the run id is still required.
    const legacy = createLoomDriverClient({
      input: {},
      env: { LOOM_DRIVER_WORKSPACE: "WS", LOOM_DRIVER_API_URL: apiUrl },
    });
    await assert.rejects(() => legacy.epics.get(), /LOOM_DRIVER_RUN_ID is required/);
  });
});

test("LoomDriverClient maps 401 token_expired to a non-retryable DriverApiError", async () => {
  // retryable:true row guards the normalization: an expired run token can
  // never be retried (the TTL is the max-run-duration cap), whatever a future
  // server envelope claims.
  for (const envelopeRetryable of [false, true]) {
    await withDriverServer(async () => ({
      statusCode: 401,
      body: {
        error: {
          code: "token_expired",
          message: "run token expired (max run duration reached)",
          retryable: envelopeRetryable,
        },
      },
    }), async ({ apiUrl }) => {
      const client = tokenTestClient(apiUrl);
      await assert.rejects(() => client.epics.get(), (err) => {
        assert.ok(err instanceof DriverApiError);
        assert.equal(err.code, "token_expired");
        assert.equal(err.retryable, false, `envelope retryable=${envelopeRetryable} must normalize to false`);
        assert.equal(err.status, 401);
        assert.equal(err.message, "run token expired (max run duration reached)");
        return true;
      });
    });
  }
});

test("LoomDriverClient epics.watch is token-only with LOOM_RUN_TOKEN and treats token_expired as fatal", async () => {
  await withSSEServer((call, res) => {
    res.writeHead(200, { "Content-Type": "text/event-stream" });
    writeSSEFrame(res, "1", "snapshot", { epic: { epicId: "EPIC-1" }, active: null });
    writeSSEFrame(res, "1", "closed", { code: "parent_not_running" });
    res.end();
  }, async ({ apiUrl, calls }) => {
    const client = tokenTestClient(apiUrl);
    const events = await collectWatchEvents(client.epics.watch({ epicId: "EPIC-1" }));
    assert.equal(events.length, 2);
    assert.equal(calls.length, 1);
    assert.equal(calls[0].headers.authorization, "Bearer run-token-jwt");
    assertNoDriverIdentityHeaders(calls[0].headers, calls[0].url);
  });

  // token_expired must throw on the FIRST response — never reconnect-loop —
  // even when the envelope (wrongly) says retryable.
  await withSSEServer((call, res) => {
    res.writeHead(401, { "Content-Type": "application/json" });
    res.end(JSON.stringify({
      error: { code: "token_expired", message: "run token expired (max run duration reached)", retryable: true },
    }));
  }, async ({ apiUrl, calls }) => {
    const client = tokenTestClient(apiUrl);
    await assert.rejects(
      () => collectWatchEvents(client.epics.watch({ epicId: "EPIC-1", reconnectMs: 1 })),
      (err) => {
        assert.ok(err instanceof DriverApiError);
        assert.equal(err.code, "token_expired");
        assert.equal(err.retryable, false);
        assert.equal(err.status, 401);
        return true;
      }
    );
    assert.equal(calls.length, 1, "expired token must not reconnect");
  });
});

// tokenTestClient layers LOOM_RUN_TOKEN over the full legacy env (header quad
// + static apiToken) so token-only tests prove precedence, not just presence.
function tokenTestClient(apiUrl) {
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
      LOOM_RUN_TOKEN: "run-token-jwt",
    },
  });
}

// assertNoDriverIdentityHeaders asserts the ABSENCE of every X-Loom-Driver-*
// header (token-only transport): node:http lowercases incoming header names,
// so scanning the keys catches any current or future identity header.
function assertNoDriverIdentityHeaders(headers, context) {
  for (const name of Object.keys(headers)) {
    assert.equal(
      name.startsWith("x-loom-driver-"),
      false,
      `token-only request must not carry ${name} (${context})`
    );
  }
}

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
    if (result && result.destroySocket === true) {
      req.socket.destroy();
      return;
    }
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
