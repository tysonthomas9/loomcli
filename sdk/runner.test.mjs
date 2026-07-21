import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { describe, it } from "node:test";

import { AgentExecSpecError, LoomAPIError, TaskRunClient } from "./runner.js";

const surface = JSON.parse(readFileSync(new URL("./api-surface.v1.json", import.meta.url), "utf8"));
const modernCodexFixture = readFileSync(new URL("../docs/design/fixtures/agent-observability/modern-codex-event-stream.jsonl", import.meta.url), "utf8");

describe("TaskRunClient.fromEnv", () => {
  it("uses Loom runner env vars", () => {
    const client = TaskRunClient.fromEnv({
      LOOM_FLEET_DB_URL: "http://fleet-db:8080/",
      LOOM_FLEET_DB_API_KEY: "api-key",
      LOOM_FLEET_DB_ACTOR: "runner",
      LOOM_WORKSPACE: "TEST",
      LOOM_TASK_RUN_ID: "task-run-1",
      LOOM_TASK_ID: "TEST-1",
      LOOM_TASK_RUN_NODE_ID: "node-1",
      LOOM_TASK_RUN_LEASE_ID: "lease-1",
      LOOM_TASK_RUN_LEASE_TOKEN: "lease-token",
      LOOM_TASK_RUN_FENCING_TOKEN: "42",
      LOOM_TASK_RUN_REQUEST_JSON: JSON.stringify({ task_id: "TEST-1", runner: "local-task-runner", input: { command: "true" } }),
    }, { fetch: async () => new Response("{}") });

    assert.equal(client.baseUrl, "http://fleet-db:8080");
    assert.equal(client.workspace, "TEST");
    assert.equal(client.taskRunId, "task-run-1");
    assert.equal(client.taskId, "TEST-1");
    assert.equal(client.nodeId, "node-1");
    assert.equal(client.leaseId, "lease-1");
    assert.equal(client.leaseToken, "lease-token");
    assert.equal(client.fencingToken, 42);
    assert.deepEqual(client.request(), { task_id: "TEST-1", runner: "local-task-runner", input: { command: "true" } });
    assert.deepEqual(client.input(), { command: "true" });
  });

  it("rejects malformed task-run request JSON", () => {
    assert.throws(
      () => TaskRunClient.fromEnv({
        LOOM_FLEET_DB_URL: "http://fleet-db:8080/",
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

  it("requires scoped task-run credentials", () => {
    assert.throws(
      () => TaskRunClient.fromEnv({}, { fetch: async () => new Response("{}") }),
      /baseUrl is required/,
    );
    assert.throws(
      () => new TaskRunClient({
        baseUrl: "http://fleet-db:8080",
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
});

describe("TaskRunClient", () => {
  it("performs the scoped runner workflow against FleetDB routes", async () => {
    const calls = [];
    const fetch = async (url, init = {}) => {
      const call = {
        url,
        method: init.method,
        headers: init.headers,
        body: init.body,
      };
      calls.push(call);

      const path = new URL(url).pathname;
      if (call.method !== "PUT" && init.body !== undefined) {
        call.json = JSON.parse(init.body);
      }
      if (call.method === "GET" && path === "/api/v1/TEST/task-runs/task-run-1") {
        return json({
          workspace_key: "TEST",
          task_run_id: "task-run-1",
          task_id: "TEST-1",
          status: "running",
          node_id: "node-1",
          lease_id: "lease-1",
          fencing_token: 42,
        });
      }
      if (call.method === "GET" && path === "/api/v1/TEST/issues/TEST-1") {
        return json({ id: "TEST-1", title: "Do the work", status: "in_progress" });
      }
      if (call.method === "POST" && path === "/api/v1/TEST/task-runs/task-run-1/heartbeat") {
        return json({ task_run_id: "task-run-1", status: "running" });
      }
      if (call.method === "POST" && path === "/api/v1/TEST/task-runs/task-run-1/logs") {
        return json({ task_run_id: "task-run-1", sequence: 1, stream: call.json.stream, text: call.json.text });
      }
      if (call.method === "POST" && path === "/api/v1/TEST/artifacts") {
        return json({
          workspace_key: "TEST",
          artifact_id: call.json.artifact_id,
          owner_type: "task_run",
          owner_id: "task-run-1",
          type: call.json.type,
          durable_status: "declared",
        }, { status: 201 });
      }
      if (call.method === "PUT" && path === "/api/v1/TEST/artifacts/artifact-1/content") {
        assert.equal(call.body, "patch body");
        return json({
          workspace_key: "TEST",
          artifact_id: "artifact-1",
          type: "patch",
          durable_status: "uploading",
          content_hash: "sha256:uploaded",
          mime_type: "text/x-diff",
        });
      }
      if (call.method === "POST" && path === "/api/v1/TEST/artifacts/artifact-1/finalize") {
        return json({
          workspace_key: "TEST",
          artifact_id: "artifact-1",
          type: "patch",
          durable_status: "finalized",
          content_hash: call.json.content_hash,
        });
      }
      if (call.method === "POST" && path === "/api/v1/TEST/task-runs/task-run-1/complete") {
        return json({
          completion: {
            completion_id: call.json.completion_id,
            artifact_ids: call.json.required_artifact_ids,
          },
          task_run: {
            task_run_id: "task-run-1",
            task_id: "TEST-1",
            status: call.json.status,
          },
        });
      }
      return json({ error: { code: "not_found", message: `unexpected ${call.method} ${path}` } }, { status: 404 });
    };
    const client = new TaskRunClient({
      baseUrl: "http://fleet-db:8080",
      apiKey: "api-key",
      actor: "runner",
      workspace: "TEST",
      taskRunId: "task-run-1",
      taskId: "TEST-1",
      nodeId: "node-1",
      leaseId: "lease-1",
      leaseToken: "lease-token",
      fencingToken: 42,
      fetch,
    });

    const task = await client.getTask();
    await client.heartbeat({ runtimeMetadata: { phase: "starting" } });
    await client.logs.append({ stream: "stdout", text: "starting\n" });
    const artifact = await client.artifacts.declare({
      id: "artifact-1",
      type: "patch",
      contentHash: "sha256:declared",
      sizeBytes: 10,
      idempotencyKey: "artifact-key",
    });
    await artifact.upload("patch body", { mimeType: "text/x-diff" });
    await artifact.finalize({ contentHash: "sha256:uploaded" });
    const completed = await client.completeRun({
      completionId: "completion-1",
      artifactIds: [artifact.id],
      taskStatusPolicy: { action: "close", reason: "done" },
      inputTokens: 11,
      outputTokens: 7,
    });

    assert.equal(task.id, "TEST-1");
    assert.equal(task.task_run.task_run_id, "task-run-1");
    assert.equal(artifact.artifact.durable_status, "finalized");
    assert.equal(completed.completion.completion_id, "completion-1");
    assert.deepEqual(calls.map((call) => `${call.method} ${new URL(call.url).pathname}`), [
      "GET /api/v1/TEST/task-runs/task-run-1",
      "GET /api/v1/TEST/issues/TEST-1",
      "POST /api/v1/TEST/task-runs/task-run-1/heartbeat",
      "POST /api/v1/TEST/task-runs/task-run-1/logs",
      "POST /api/v1/TEST/artifacts",
      "PUT /api/v1/TEST/artifacts/artifact-1/content",
      "POST /api/v1/TEST/artifacts/artifact-1/finalize",
      "POST /api/v1/TEST/task-runs/task-run-1/complete",
    ]);

    for (const call of calls) {
      assert.equal(call.headers["X-API-Key"], "api-key");
      assert.equal(call.headers["X-Fleet-API-Key"], "api-key");
      assert.equal(call.headers["X-Actor"], "runner");
    }
    for (const call of calls.slice(2)) {
      assert.equal(call.headers["X-Lease-Token"], "lease-token");
    }
    assert.equal(calls[2].json.node_id, "node-1");
    assert.equal(calls[2].json.lease_id, "lease-1");
    assert.equal(calls[2].json.fencing_token, 42);
    assert.equal(calls[4].json.owner_type, "task_run");
    assert.equal(calls[4].json.owner_id, "task-run-1");
    assert.equal(calls[4].json.metadata.idempotency_key, "artifact-key");
    assert.equal(calls[5].headers["Content-Type"], "text/x-diff");
    assert.deepEqual(calls[7].json.required_artifact_ids, ["artifact-1"]);
    assert.equal(calls[7].json.require_artifacts, true);
    assert.equal(calls[7].json.close_task, true);
    assert.equal(calls[7].json.close_reason, "done");
  });

  it("surfaces FleetDB error envelopes", async () => {
    const client = new TaskRunClient({
      baseUrl: "http://fleet-db:8080",
      workspace: "TEST",
      taskRunId: "task-run-1",
      nodeId: "node-1",
      leaseId: "lease-1",
      leaseToken: "lease-token",
      fencingToken: 42,
      fetch: async () => json({ error: { code: "invalid_transition", message: "not owner" } }, { status: 409 }),
    });

    await assert.rejects(
      () => client.heartbeat(),
      (error) => error instanceof LoomAPIError && error.status === 409 && error.code === "invalid_transition" && error.message === "not owner",
    );
  });

  it("does not expose broad task mutation helpers", () => {
    const client = new TaskRunClient({
      baseUrl: "http://fleet-db:8080",
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
    assert.equal(client.apiKey, "");
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
      if (path.endsWith("/task-run/runtime-credential")) {
        return json({ provider: call.json.provider, value: `${call.json.provider}-secret` });
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
    const credential = await client.runtimeCredentials.get({ provider: "daytona" });
    await client.heartbeat({ runtimeMetadata: { phase: "starting" } });
    await client.logs.append({ stream: "stdout", text: "starting\n" });
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
    assert.equal(credential.value, "daytona-secret");
    assert.equal(artifact.id, "artifact-1");
    assert.equal(artifact.artifact.durableStatus, "finalized");
    assert.equal(listed.artifacts[0].id, "artifact-1");
    assert.equal(completed.completion.completionId, "completion-1");

    assert.deepEqual(calls.map((call) => `${call.method} ${new URL(call.url).pathname}`), [
      "POST /api/workspaces/TEST/task-run/task-get",
      "POST /api/workspaces/TEST/task-run/runtime-credential",
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
    assert.equal(calls[2].json.node_id, undefined);
    assert.equal(calls[2].json.lease_id, undefined);
    assert.equal(calls[2].json.fencing_token, undefined);
    assert.equal(calls[4].json.metadata.idempotency_key, "artifact-key");
    assert.equal(calls[5].headers["Content-Type"], "text/x-diff");
    assert.deepEqual(calls[8].json.requiredArtifactIds, ["artifact-1"]);
    assert.equal(calls[8].json.requireArtifacts, true);
    assert.equal(calls[8].json.closeTask, true);
    assert.equal(calls[8].json.closeReason, "done");
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

  it("legacy fleet-db env takes effect only when the serve URL is absent", () => {
    const client = TaskRunClient.fromEnv({
      ...serveEnv,
      LOOM_TASK_RUN_API_URL: "",
      LOOM_FLEET_DB_URL: "http://fleet-db:8080",
      LOOM_FLEET_DB_API_KEY: "api-key",
    }, { fetch: async () => json({}) });
    assert.equal(client.serveMode, false);
    assert.equal(client.baseUrl, "http://fleet-db:8080");
    assert.equal(client.apiKey, "api-key");
  });
});

describe("TaskRunClient.agent.exec", () => {
  it("maps modern Codex command execution to canonical tool entries", async () => {
    const client = taskRunClientForAgent((url, init = {}) => {
      const path = new URL(url).pathname;
      const body = init.method === "PUT" ? init.body : JSON.parse(init.body);
      if (path.endsWith("/session-open")) return json({ sessionId: "task-run-1-a1-agent", attempt: 1 });
      if (path.endsWith("/artifact-declare")) return json({ artifactId: body.artifactId, type: body.type, durableStatus: "declared" });
      if (path.endsWith("/content")) return json({ artifactId: "transcript-task-run-1-a1-agent", durableStatus: "uploaded" });
      if (path.endsWith("/artifact-finalize")) return json({ artifactId: body.artifactId, durableStatus: "finalized" });
      if (path.endsWith("/session-close")) return json({ sessionId: body.sessionId, status: body.status });
      throw new Error(`unexpected ${init.method} ${path}`);
    });
    const stream = `${JSON.stringify({ type: "thread.started" })}\n${JSON.stringify({
      type: "item.completed",
      item: { id: "command-1", type: "command_execution", command: "pwd", aggregated_output: "/repo" },
    })}\n`;

    const result = await client.agent.exec({
      invocationKey: "agent",
      backend: "codex",
      argv: [process.execPath, "-e", "process.stdout.write(process.argv[1])", stream],
      transcript: "stream-json",
    });

    assert.deepEqual(result.entries.slice(1), [
      { seq: 1, role: "assistant", type: "tool_use", tool_name: "command_execution", tool_use_id: "command-1", tool_input: { command: "pwd" } },
      { seq: 2, role: "tool", type: "tool_result", tool_use_id: "command-1", output: "/repo" },
    ]);
  });

  it("normalizes the modern Codex event stream and sends its split usage on close", async () => {
    const calls = [];
    const client = taskRunClientForAgent((url, init = {}) => {
      const path = new URL(url).pathname;
      const body = init.method === "PUT" ? init.body : JSON.parse(init.body);
      calls.push({ path, method: init.method, body });
      if (path.endsWith("/session-open")) return json({ sessionId: "task-run-1-a1-judge", attempt: 1 });
      if (path.endsWith("/artifact-declare")) return json({ artifactId: body.artifactId, type: body.type, durableStatus: "declared" });
      if (path.endsWith("/content")) return json({ artifactId: "transcript-task-run-1-a1-judge", durableStatus: "uploaded" });
      if (path.endsWith("/artifact-finalize")) return json({ artifactId: body.artifactId, durableStatus: "finalized" });
      if (path.endsWith("/session-close")) return json({ sessionId: body.sessionId, status: body.status });
      throw new Error(`unexpected ${init.method} ${path}`);
    });

    const result = await client.agent.exec({
      invocationKey: "judge",
      backend: "codex",
      model: "gpt-5.6-sol",
      argv: [process.execPath, "-e", "process.stdout.write(process.argv[1])", modernCodexFixture],
      transcript: "stream-json",
    });

    assert.deepEqual(result.usage, {
      tokens: 16949,
      inputTokens: 15755,
      cacheReadTokens: 0,
      outputTokens: 1194,
      cost: null,
    });
    const uploaded = calls.find((call) => call.path.endsWith("/content"));
    const entries = String(uploaded.body).trim().split("\n").map((line) => JSON.parse(line));
    const message = entries.find((entry) => entry.type === "text" && entry.role === "assistant");
    assert.match(message.text, /false_success_claim/);
    const close = calls.find((call) => call.path.endsWith("/session-close"));
    assert.deepEqual(close.body.usage, {
      tokens: 16949,
      inputTokens: 15755,
      cacheReadTokens: 0,
      outputTokens: 1194,
    });
  });

  it("uses the frozen session wire contract, uploads the composed transcript artifact, and preserves unknown usage", async () => {
    const calls = [];
    const client = taskRunClientForAgent((url, init = {}) => {
      const path = new URL(url).pathname;
      const body = init.method === "PUT" ? init.body : JSON.parse(init.body);
      calls.push({ path, method: init.method, body });
      if (path.endsWith("/session-open")) return json({ sessionId: "task-run-1-a1-agent", attempt: 1 });
      if (path.endsWith("/artifact-declare")) return json({ artifactId: body.artifactId, type: body.type, durableStatus: "declared" });
      if (path.endsWith("/content")) return json({ artifactId: "transcript-task-run-1-a1-agent", durableStatus: "uploaded" });
      if (path.endsWith("/artifact-finalize")) return json({ artifactId: body.artifactId, durableStatus: "finalized" });
      if (path.endsWith("/session-close")) return json({ sessionId: body.sessionId, status: body.status });
      throw new Error(`unexpected ${init.method} ${path}`);
    });

    const result = await client.agent.exec({
      invocationKey: "agent",
      backend: "codex",
      model: "gpt-5",
      argv: [process.execPath, "-e", "console.log(JSON.stringify({type: 'message', text: 'hello'}))"],
      transcript: "stream-json",
    });

    assert.equal(result.exitCode, 0);
    assert.equal(result.session.id, "task-run-1-a1-agent");
    assert.equal(result.session.transcriptRef, "artifact://transcript-task-run-1-a1-agent");
    assert.equal(result.usage, null, "absence stays unknown instead of zero");
    const open = calls.find((call) => call.path.endsWith("/session-open"));
    const close = calls.find((call) => call.path.endsWith("/session-close"));
    const declare = calls.find((call) => call.path.endsWith("/artifact-declare"));
    assert.deepEqual(Object.keys(open.body).sort(), [...surface.taskRunApi.ops["session-open"].fields].filter((key) => ["invocationKey", "backend", "model"].includes(key)).sort());
    assert.equal(declare.body.artifactId, "transcript-task-run-1-a1-agent");
    assert.equal(close.body.usage, undefined, "missing usage must not be serialized as zero");
    assert.deepEqual(Object.keys(close.body).sort(), ["exitCode", "sessionId", "status", "summary", "transcriptRef"]);
  });

  it("degrades only observability after the default open retries and leaves the process result intact", async () => {
    let opens = 0;
    const client = taskRunClientForAgent((url, init = {}) => {
      const path = new URL(url).pathname;
      if (path.endsWith("/session-open")) {
        opens += 1;
        return json({ error: { code: "session_lifecycle_contention", message: "down", retryable: true } }, { status: 503 });
      }
      if (path.endsWith("/heartbeat")) return json({ taskRunId: "task-run-1", status: "running" });
      throw new Error(`unexpected ${init.method} ${path}`);
    });

    const result = await client.agent.exec({
      invocationKey: "agent",
      backend: "codex",
      argv: [process.execPath, "-e", "process.stdout.write('intact\\n')"],
      transcript: "minimal",
    });

    assert.equal(opens, surface.taskRunApi.agentExec.openRetriesDefault + 1);
    assert.equal(result.exitCode, 0);
    assert.equal(result.stdout, "intact\n");
    assert.equal(result.session.opened, false);
    assert.equal(result.session.degraded, true);
    assert.equal(result.session.degradedReason, "session_lifecycle_contention");
    assert.deepEqual(result.runtimeMetadata, {
      observability_degraded: "true",
      observability_degraded_code: "session_lifecycle_contention",
    });
  });

  it("degrades immediately on a non-retryable descriptor conflict and exposes its code", async () => {
    let opens = 0;
    const client = taskRunClientForAgent((url, init = {}) => {
      const path = new URL(url).pathname;
      if (path.endsWith("/session-open")) {
        opens += 1;
        return json({ error: { code: "session_descriptor_conflict", message: "different descriptor", retryable: false } }, { status: 409 });
      }
      if (path.endsWith("/heartbeat")) return json({ taskRunId: "task-run-1", status: "running" });
      throw new Error(`unexpected ${init.method} ${path}`);
    });

    const result = await client.agent.exec({
      invocationKey: "agent",
      backend: "codex",
      argv: [process.execPath, "-e", "process.stdout.write('intact')"],
      transcript: "none",
    });

    assert.equal(opens, 1);
    assert.equal(result.stdout, "intact");
    assert.equal(result.session.degradedReason, "session_descriptor_conflict");
    assert.equal(result.runtimeMetadata.observability_degraded_code, "session_descriptor_conflict");
  });

  it("retries retryable lifecycle contention and then succeeds", async () => {
    let opens = 0;
    const client = taskRunClientForAgent((url, init = {}) => {
      const path = new URL(url).pathname;
      const body = JSON.parse(init.body);
      if (path.endsWith("/session-open")) {
        opens += 1;
        if (opens === 1) {
          return json({ error: { code: "session_lifecycle_contention", message: "retry", retryable: true } }, { status: 503 });
        }
        return json({ sessionId: "task-run-1-a1-agent", attempt: 1 });
      }
      if (path.endsWith("/session-close")) return json({ sessionId: body.sessionId, status: body.status });
      throw new Error(`unexpected ${init.method} ${path}`);
    });

    const result = await client.agent.exec({
      invocationKey: "agent",
      backend: "codex",
      argv: [process.execPath, "-e", "process.exit(0)"],
      transcript: "none",
    });

    assert.equal(opens, 2);
    assert.equal(result.session.opened, true);
    assert.equal(result.session.closed, true);
    assert.equal(result.session.degraded, false);
    assert.equal(result.session.degradedReason, null);
  });

  it("defers close until finalize and intentionally leaves a crash path open", async () => {
    const closed = [];
    const client = taskRunClientForAgent((url, init = {}) => {
      const path = new URL(url).pathname;
      const body = init.method === "PUT" ? init.body : JSON.parse(init.body);
      if (path.endsWith("/session-open")) return json({ sessionId: `task-run-1-a1-${body.invocationKey}`, attempt: 1 });
      if (path.endsWith("/artifact-declare")) return json({ artifactId: body.artifactId, type: body.type, durableStatus: "declared" });
      if (path.endsWith("/content")) return json({ artifactId: "any", durableStatus: "uploaded" });
      if (path.endsWith("/artifact-finalize")) return json({ artifactId: body.artifactId, durableStatus: "finalized" });
      if (path.endsWith("/session-close")) {
        closed.push(body);
        return json({ sessionId: body.sessionId, status: body.status });
      }
      throw new Error(`unexpected ${init.method} ${path}`);
    });
    const base = {
      backend: "codex",
      argv: [process.execPath, "-e", "process.stdout.write('ok')"],
      transcript: "minimal",
      close: "deferred",
    };
    const deferred = await client.agent.exec({ ...base, invocationKey: "agent" });
    assert.equal(closed.length, 0);
    assert.equal(typeof deferred.finalize, "function");
    assert.deepEqual(await deferred.finalize({ status: "completed", summary: "leaf outcome" }), { ok: true });
    assert.equal(closed.length, 1);
    assert.equal(closed[0].summary, "leaf outcome");

    // No finalize call models a leaf crash. Slice 4's reconciler owns this
    // registered-but-open session; this helper must not quietly close it.
    const crashPath = await client.agent.exec({ ...base, invocationKey: "crash" });
    assert.equal(crashPath.session.opened, true);
    assert.equal(crashPath.session.closed, false);
    assert.equal(closed.length, 1);
  });

  it("throws AgentExecSpecError only for invalid process-form caller input", async () => {
    const client = taskRunClientForAgent(() => json({}));
    await assert.rejects(
      () => client.agent.exec({ invocationKey: "agent", backend: "codex", invoke: () => {} }),
      AgentExecSpecError,
    );
  });
});

function taskRunClientForAgent(fetch) {
  return new TaskRunClient({
    apiUrl: "http://127.0.0.1:8080",
    workspace: "TEST",
    taskRunId: "task-run-1",
    taskId: "TEST-1",
    nodeId: "node-1",
    leaseId: "lease-1",
    leaseToken: "lease-token",
    fencingToken: "42",
    fetch,
  });
}

function json(body, init = {}) {
  return new Response(JSON.stringify(body), {
    status: init.status || 200,
    headers: { "Content-Type": "application/json" },
  });
}
