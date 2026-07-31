import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { LoomAPIError, TaskRunClient } from "./runner.js";

describe("TaskRunClient.fromEnv", () => {
  it("uses the Loom task-run facade and scoped runner env vars", () => {
    const client = TaskRunClient.fromEnv({
      LOOM_TASK_RUN_API_URL: "http://loom:8080/",
      LOOM_WORKSPACE: "TEST",
      LOOM_TASK_RUN_ID: "task-run-1",
      LOOM_TASK_ID: "TEST-1",
      LOOM_TASK_RUN_NODE_ID: "node-1",
      LOOM_TASK_RUN_LEASE_ID: "lease-1",
      LOOM_TASK_RUN_LEASE_TOKEN: "lease-token",
      LOOM_TASK_RUN_FENCING_TOKEN: "42",
      LOOM_TASK_RUN_REQUEST_JSON: JSON.stringify({ task_id: "TEST-1", runner: "local-task-runner", input: { command: "true" } }),
    }, { fetch: async () => new Response("{}") });

    assert.equal(client.apiUrl, "http://loom:8080");
    assert.equal(client.workspace, "TEST");
    assert.equal(client.taskRunId, "task-run-1");
    assert.equal(client.taskId, "TEST-1");
    assert.equal(client.nodeId, "node-1");
    assert.equal(client.leaseId, "lease-1");
    assert.equal(client.leaseToken, "lease-token");
    assert.equal(client.fencingToken, "42");
    assert.deepEqual(client.request(), { task_id: "TEST-1", runner: "local-task-runner", input: { command: "true" } });
    assert.deepEqual(client.input(), { command: "true" });
  });

  it("rejects malformed task-run request JSON", () => {
    assert.throws(
      () => TaskRunClient.fromEnv({
        LOOM_TASK_RUN_API_URL: "http://loom:8080/",
        LOOM_WORKSPACE: "TEST",
        LOOM_TASK_RUN_ID: "task-run-1",
        LOOM_TASK_RUN_NODE_ID: "node-1",
        LOOM_TASK_RUN_LEASE_ID: "lease-1",
        LOOM_TASK_RUN_LEASE_TOKEN: "lease-token",
        LOOM_TASK_RUN_FENCING_TOKEN: "42",
        LOOM_TASK_RUN_REQUEST_JSON: "{",
      }, { fetch: async () => new Response("{}") }),
      /LOOM_TASK_RUN_REQUEST_JSON is invalid JSON/,
    );
  });

  it("fails closed without the serve facade or scoped credentials", () => {
    assert.throws(
      () => TaskRunClient.fromEnv({}, { fetch: async () => new Response("{}") }),
      /apiUrl is required \(set LOOM_TASK_RUN_API_URL\)/,
    );
    assert.throws(
      () => new TaskRunClient({
        apiUrl: "http://loom:8080",
        workspace: "TEST",
        taskRunId: "task-run-1",
        nodeId: "node-1",
        leaseId: "lease-1",
        leaseToken: "token",
        fencingToken: "0",
        fetch: async () => new Response("{}"),
      }),
      /fencingToken must be a positive integer/,
    );
  });

  it("does not expose broad task mutation helpers", () => {
    const client = new TaskRunClient({
      apiUrl: "http://loom:8080",
      workspace: "TEST",
      taskRunId: "task-run-1",
      nodeId: "node-1",
      leaseId: "lease-1",
      leaseToken: "lease-token",
      fencingToken: 42,
      fetch: async () => new Response("{}"),
    });

    assert.equal("updateTask" in client, false);
    assert.equal("closeTask" in client, false);
    assert.equal("createTask" in client, false);
    assert.equal("runtimeCredentials" in client, false);
    assert.equal("getRuntimeCredential" in client, false);
    assert.equal(typeof client.daytona.execute, "function");
  });
});

describe("TaskRunClient serve transport", () => {
  const serveEnv = {
    LOOM_TASK_RUN_API_URL: "http://127.0.0.1:8080/",
    LOOM_DRIVER_WORKSPACE: "TEST",
    LOOM_TASK_RUN_ID: "task-run-1",
    LOOM_TASK_ID: "TEST-1",
    LOOM_TASK_RUN_NODE_ID: "node-1",
    LOOM_TASK_RUN_LEASE_ID: "lease-1",
    LOOM_TASK_RUN_LEASE_TOKEN: "lease-token",
    LOOM_TASK_RUN_FENCING_TOKEN: "42",
  };

  it("fromEnv works without any LOOM_FLEET_DB_* env", () => {
    const client = TaskRunClient.fromEnv(serveEnv, { fetch: async () => json({}) });
    assert.equal(client.serveMode, true);
    assert.equal(client.baseUrl, "http://127.0.0.1:8080");
    assert.equal(client.workspace, "TEST");
    assert.equal(client.leaseToken, "lease-token");
  });

  it("submits a fixed secret-free Daytona intent and ignores capability-shaped extras", async () => {
    let call;
    const client = TaskRunClient.fromEnv(serveEnv, {
      fetch: async (url, init = {}) => {
        call = { url, headers: init.headers, body: JSON.parse(init.body) };
        return json({
          schemaVersion: "daytona-task-run-execution.v1",
          status: "completed",
          exitCode: 0,
          usage: {},
          sandbox: { provider: "daytona", id: "sandbox-opaque" },
        });
      },
    });
    const result = await client.daytona.execute({
      repositoryUrl: "https://github.com/octocat/Hello-World.git",
      taskPrompt: "Make a focused change.",
      backend: "codex",
      delivery: { openPullRequest: false, credentials: "credential-sentinel" },
      credentials: "credential-sentinel",
      env: { DAYTONA_API_KEY: "credential-sentinel" },
    });

    assert.equal(new URL(call.url).pathname, "/api/workspaces/TEST/task-run/daytona-execute");
    assert.deepEqual(call.body, {
      schemaVersion: "daytona-task-run-execution.v1",
      repositoryUrl: "https://github.com/octocat/Hello-World.git",
      taskPrompt: "Make a focused change.",
      backend: "codex",
      delivery: { openPullRequest: false },
    });
    assert.equal(JSON.stringify(call).includes("credential-sentinel"), false);
    assert.equal(result.sandbox.id, "sandbox-opaque");
  });

  it("preserves int64 fencing tokens exactly in serve transport", async () => {
    const fencingToken = "1781415319333983288";
    let headerValue = "";
    const client = TaskRunClient.fromEnv({
      ...serveEnv,
      LOOM_TASK_RUN_FENCING_TOKEN: fencingToken,
    }, {
      fetch: async (_url, init = {}) => {
        headerValue = init.headers["X-Loom-Task-Run-Fencing-Token"];
        return json({ taskRunId: "task-run-1", status: "running" });
      },
    });

    assert.equal(client.fencingToken, fencingToken);
    await client.heartbeat();
    assert.equal(headerValue, fencingToken);
  });

  it("targets serve task-run ops with lease-token auth and no fleet-db credentials", async () => {
    const calls = [];
    const fetch = async (url, init = {}) => {
      const call = { url, method: init.method, headers: init.headers, body: init.body };
      calls.push(call);
      const path = new URL(url).pathname;
      if (call.method !== "PUT" && init.body !== undefined) {
        call.json = JSON.parse(init.body);
      }
      if (path.endsWith("/task-run/get")) {
        return json({ taskRunId: "task-run-1", taskId: "TEST-1", status: "running" });
      }
      if (path.endsWith("/task-run/task-get")) {
        return json({
          task: { id: "TEST-1", title: "Do the work" },
          taskRun: { taskRunId: "task-run-1", taskId: "TEST-1", status: "running" },
        });
      }
      if (path.endsWith("/task-run/heartbeat")) {
        return json({ taskRunId: "task-run-1", status: "running" });
      }
      if (path.endsWith("/task-run/log-append")) {
        return json({ taskRunId: "task-run-1", sequence: 1, stream: call.json.stream, text: call.json.text });
      }
      if (path.endsWith("/task-run/artifact-declare")) {
        return json({ artifactId: call.json.artifactId, ownerType: "task_run", ownerId: "task-run-1", type: call.json.type, durableStatus: "declared" });
      }
      if (path.endsWith("/task-run/artifacts/artifact-1/content")) {
        assert.equal(call.body, "patch body");
        return json({ artifactId: "artifact-1", type: "patch", durableStatus: "uploading" });
      }
      if (path.endsWith("/task-run/artifact-finalize")) {
        return json({ artifactId: "artifact-1", type: "patch", durableStatus: "finalized", contentHash: call.json.contentHash });
      }
      if (path.endsWith("/task-run/artifact-list")) {
        return json({ artifacts: [{ artifactId: "artifact-1", type: "patch", durableStatus: "finalized" }] });
      }
      if (path.endsWith("/task-run/complete")) {
        return json({
          completion: { completionId: call.json.completionId, artifactIds: call.json.requiredArtifactIds },
          taskRun: { taskRunId: "task-run-1", taskId: "TEST-1", status: call.json.status },
        });
      }
      return json({ error: { code: "unknown_op", message: `unexpected ${call.method} ${path}`, retryable: false } }, { status: 404 });
    };
    const client = TaskRunClient.fromEnv(serveEnv, { fetch });

    const task = await client.getTask();
    await client.heartbeat({ runtimeMetadata: { phase: "starting" } });
    const logTimestamp = "2026-07-16T20:30:00.000Z";
    await client.logs.append({ requestId: "task-run-log-1", stream: "stdout", text: "starting\n", timestamp: logTimestamp });
    const artifact = await client.artifacts.declare({
      id: "artifact-1",
      type: "patch",
      contentHash: "sha256:declared",
      sizeBytes: 10,
      idempotencyKey: "artifact-key",
    });
    await artifact.upload("patch body", { mimeType: "text/x-diff" });
    await artifact.finalize({ contentHash: "sha256:uploaded" });
    const listed = await client.artifacts.list({ type: "patch" });
    const completed = await client.completeRun({
      completionId: "completion-1",
      artifactIds: [artifact.id],
      taskStatusPolicy: { action: "close", reason: "done" },
    });

    assert.equal(task.id, "TEST-1");
    assert.equal(task.taskRun.taskRunId, "task-run-1");
    assert.equal(artifact.id, "artifact-1");
    assert.equal(artifact.artifact.durableStatus, "finalized");
    assert.equal(listed.artifacts[0].id, "artifact-1");
    assert.equal(completed.completion.completionId, "completion-1");

    assert.deepEqual(calls.map((call) => `${call.method} ${new URL(call.url).pathname}`), [
      "POST /api/workspaces/TEST/task-run/task-get",
      "POST /api/workspaces/TEST/task-run/heartbeat",
      "POST /api/workspaces/TEST/task-run/log-append",
      "POST /api/workspaces/TEST/task-run/artifact-declare",
      "PUT /api/workspaces/TEST/task-run/artifacts/artifact-1/content",
      "POST /api/workspaces/TEST/task-run/artifact-finalize",
      "POST /api/workspaces/TEST/task-run/artifact-list",
      "POST /api/workspaces/TEST/task-run/complete",
    ]);

    for (const call of calls) {
      // Lease token is the only credential; the fleet-db key/actor headers
      // must never appear on the serve transport.
      assert.equal(call.headers.Authorization, "Bearer lease-token");
      assert.equal(call.headers["X-Loom-Task-Run-Id"], "task-run-1");
      assert.equal(call.headers["X-Loom-Task-Run-Node-Id"], "node-1");
      assert.equal(call.headers["X-Loom-Task-Run-Lease-Id"], "lease-1");
      assert.equal(call.headers["X-Loom-Task-Run-Fencing-Token"], "42");
      assert.equal("X-API-Key" in call.headers, false);
      assert.equal("X-Fleet-API-Key" in call.headers, false);
      assert.equal("X-Lease-Token" in call.headers, false);
      assert.equal("X-Actor" in call.headers, false);
    }
    // Fenced identity travels in headers, not bodies, on the serve wire.
    assert.equal(calls[1].json.node_id, undefined);
    assert.equal(calls[1].json.lease_id, undefined);
    assert.equal(calls[1].json.fencing_token, undefined);
    assert.equal(calls[2].json.requestId, "task-run-log-1");
    assert.equal(calls[2].json.timestamp, logTimestamp);
    assert.equal(calls[3].json.metadata.idempotency_key, "artifact-key");
    assert.equal(calls[4].headers["Content-Type"], "text/x-diff");
    assert.deepEqual(calls[7].json.requiredArtifactIds, ["artifact-1"]);
    assert.equal(calls[7].json.requireArtifacts, true);
    assert.equal(calls[7].json.closeTask, true);
    assert.equal(calls[7].json.closeReason, "done");
  });

  it("surfaces serve structured error envelopes", async () => {
    const client = TaskRunClient.fromEnv(serveEnv, {
      fetch: async () => json({ error: { code: "lease_denied", message: "task-run lease verification failed", retryable: false } }, { status: 401 }),
    });
    await assert.rejects(
      () => client.heartbeat(),
      (error) => error instanceof LoomAPIError && error.status === 401 && error.code === "lease_denied",
    );
  });

  it("preserves one log request identity and timestamp across replay attempts", async () => {
    const bodies = [];
    const client = TaskRunClient.fromEnv(serveEnv, {
      fetch: async (_url, init = {}) => {
        bodies.push(JSON.parse(init.body));
        return json({ taskRunId: "task-run-1", sequence: 1, stream: "stdout", text: "line\n" });
      },
    });
    const append = {
      request_id: " task-run-log-replay ",
      text: "line\n",
      timestamp: new Date("2026-07-16T20:31:00Z"),
    };

    await client.logs.append(append);
    await client.logs.append(append);

    assert.deepEqual(bodies, [
      { requestId: "task-run-log-replay", stream: "stdout", text: "line\n", timestamp: "2026-07-16T20:31:00.000Z" },
      { requestId: "task-run-log-replay", stream: "stdout", text: "line\n", timestamp: "2026-07-16T20:31:00.000Z" },
    ]);
  });

  it("fails closed when log replay identity or timestamp is ambiguous", async () => {
    const client = TaskRunClient.fromEnv(serveEnv, { fetch: async () => json({}) });
    await assert.rejects(() => client.logs.append({ text: "line\n", timestamp: "2026-07-16T20:31:00Z" }), /requestId is required/);
    await assert.rejects(() => client.logs.append({ requestId: "log-1", text: "line\n" }), /timestamp must be a valid date-time/);
    await assert.rejects(
      () => client.logs.append({ requestId: "log-1", request_id: "log-2", text: "line\n", timestamp: "2026-07-16T20:31:00Z" }),
      /requestId and request_id must match/,
    );
    await assert.rejects(() => client.logs.append({ requestId: "log-1", text: "line\n", timestamp: "not-a-date" }), /valid date-time/);
    await assert.rejects(() => client.logs.append({ requestId: "log-1", text: "line\n", timestamp: "2026-07-16" }), /valid date-time/);
  });

  it("rejects legacy fleet-db env when the serve URL is absent", () => {
    assert.throws(() => TaskRunClient.fromEnv({
      ...serveEnv,
      LOOM_TASK_RUN_API_URL: "",
      LOOM_FLEET_DB_URL: "http://fleet-db:8080",
      LOOM_FLEET_DB_API_KEY: "api-key",
    }, { fetch: async () => json({}) }), /LOOM_TASK_RUN_API_URL/);
  });
});

function json(body, init = {}) {
  return new Response(JSON.stringify(body), {
    status: init.status || 200,
    headers: { "Content-Type": "application/json" },
  });
}
