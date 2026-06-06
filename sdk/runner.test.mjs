import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { LoomAPIError, TaskRunClient } from "./runner.js";

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
    }, { fetch: async () => new Response("{}") });

    assert.equal(client.baseUrl, "http://fleet-db:8080");
    assert.equal(client.workspace, "TEST");
    assert.equal(client.taskRunId, "task-run-1");
    assert.equal(client.taskId, "TEST-1");
    assert.equal(client.nodeId, "node-1");
    assert.equal(client.leaseId, "lease-1");
    assert.equal(client.leaseToken, "lease-token");
    assert.equal(client.fencingToken, 42);
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

function json(body, init = {}) {
  return new Response(JSON.stringify(body), {
    status: init.status || 200,
    headers: { "Content-Type": "application/json" },
  });
}
