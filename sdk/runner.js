import { pickEnv, trim } from "./internal.js";
import { spawn } from "node:child_process";

export const RunnerEnv = Object.freeze({
  apiUrl: "LOOM_TASK_RUN_API_URL",
  baseUrl: "LOOM_FLEET_DB_URL",
  apiKey: "LOOM_FLEET_DB_API_KEY",
  actor: "LOOM_FLEET_DB_ACTOR",
  agentName: "LOOM_AGENT_NAME",
  workspace: "LOOM_WORKSPACE",
  taskRunId: "LOOM_TASK_RUN_ID",
  taskId: "LOOM_TASK_ID",
  nodeId: "LOOM_TASK_RUN_NODE_ID",
  leaseId: "LOOM_TASK_RUN_LEASE_ID",
  leaseToken: "LOOM_TASK_RUN_LEASE_TOKEN",
  runnerLeaseToken: "LOOM_RUNNER_LEASE_TOKEN",
  fencingToken: "LOOM_TASK_RUN_FENCING_TOKEN",
  requestJson: "LOOM_TASK_RUN_REQUEST_JSON",
});

export class LoomAPIError extends Error {
  constructor(message, options = {}) {
    super(message);
    this.name = "LoomAPIError";
    this.status = options.status || 0;
    this.code = options.code || "";
    this.retryable = options.retryable === true;
    this.details = options.details;
    this.responseBody = options.responseBody || "";
  }
}

// AgentExecSpecError is reserved for caller mistakes in an agent-exec form.
// Process/spawn/prompt/transport failures are returned by agent.exec instead,
// so task leaves can map them to their task outcome.
export class AgentExecSpecError extends Error {
  constructor(message) {
    super(message);
    this.name = "AgentExecSpecError";
  }
}

export class TaskRunClient {
  #requestPayload;

  static fromEnv(env = process.env, options = {}) {
    return new TaskRunClient({
      apiUrl: options.apiUrl || pickEnv(env, RunnerEnv.apiUrl),
      baseUrl: options.baseUrl || pickEnv(env, RunnerEnv.baseUrl),
      apiKey: options.apiKey || pickEnv(env, RunnerEnv.apiKey),
      actor: options.actor || pickEnv(env, RunnerEnv.actor, RunnerEnv.agentName, "USER"),
      authToken: options.authToken,
      fetch: options.fetch,
      workspace: options.workspace || pickEnv(env, RunnerEnv.workspace, "LOOM_DRIVER_WORKSPACE"),
      taskRunId: options.taskRunId || pickEnv(env, RunnerEnv.taskRunId),
      taskId: options.taskId || pickEnv(env, RunnerEnv.taskId),
      nodeId: options.nodeId || pickEnv(env, RunnerEnv.nodeId),
      leaseId: options.leaseId || pickEnv(env, RunnerEnv.leaseId),
      leaseToken: options.leaseToken || pickEnv(env, RunnerEnv.leaseToken, RunnerEnv.runnerLeaseToken),
      fencingToken: options.fencingToken || pickEnv(env, RunnerEnv.fencingToken),
      requestJson: options.requestJson ?? pickEnv(env, RunnerEnv.requestJson),
    });
  }

  constructor(options = {}) {
    // Serve transport: when LOOM_TASK_RUN_API_URL (or the apiUrl option) is
    // set, all operations target the loom serve task-run API authenticated
    // by the per-task-run lease token alone — no LOOM_FLEET_DB_URL, no
    // LOOM_FLEET_DB_API_KEY. Otherwise the legacy direct-fleet-db transport
    // is used unchanged.
    this.apiUrl = trim(options.apiUrl);
    this.serveMode = this.apiUrl !== "";
    this.baseUrl = this.serveMode ? this.apiUrl.replace(/\/+$/, "") : normalizeBaseUrl(options.baseUrl);
    this.workspace = required("workspace", options.workspace);
    this.taskRunId = required("taskRunId", options.taskRunId);
    this.taskId = trim(options.taskId);
    this.nodeId = required("nodeId", options.nodeId);
    this.leaseId = required("leaseId", options.leaseId);
    this.leaseToken = required("leaseToken", options.leaseToken);
    this.fencingToken = parseFencingToken(required("fencingToken", options.fencingToken), {
      preserveString: this.serveMode,
    });
    this.requestJson = trim(options.requestJson);
    this.#requestPayload = parseRequestJson(this.requestJson);
    this.apiKey = trim(options.apiKey);
    this.actor = trim(options.actor);
    this.authToken = trim(options.authToken);
    this.fetch = options.fetch || globalThis.fetch;
    if (typeof this.fetch !== "function") {
      throw new TypeError("fetch is required; use Node.js 18+ or pass a fetch implementation");
    }

    this.logs = Object.freeze({
      append: (entry, requestOptions) => this.appendLog(entry, requestOptions),
    });
    this.artifacts = Object.freeze({
      declare: (input, requestOptions) => this.declareArtifact(input, requestOptions),
      get: (artifactId, requestOptions) => this.getArtifact(artifactId, requestOptions),
      list: (input, requestOptions) => this.listArtifacts(input, requestOptions),
    });
    this.runtimeCredentials = Object.freeze({
      get: (input, requestOptions) => this.getRuntimeCredential(input, requestOptions),
    });
    // agent.exec has two deliberately disjoint invocation forms. The callable
    // process form owns child-process capture; exec.invoke owns the lifecycle
    // around a leaf-owned in-process harness prompt. Never turn this into an
    // optional-argv overload: rejecting each form's keys in the other form is
    // part of the public agent-exec contract.
    const exec = (spec) => executeAgentProcess(this, spec);
    exec.invoke = (spec) => executeAgentInvoke(this, spec);
    this.agent = Object.freeze({
      exec,
    });
  }

  request() {
    return cloneRequestPayload(this.#requestPayload);
  }

  input() {
    const request = this.#requestPayload;
    if (!request || request.input === undefined || request.input === null) {
      return undefined;
    }
    return cloneRequestPayload(request.input);
  }

  async getTaskRun(options = {}) {
    if (this.serveMode) {
      return this.#op("get", {}, options);
    }
    return this.#json("GET", this.#taskRunPath(), undefined, options);
  }

  async getTask(options = {}) {
    if (this.serveMode) {
      const out = await this.#op("task-get", {}, options);
      const taskRun = out?.taskRun;
      if (!out?.task) {
        return { taskRun };
      }
      return { ...out.task, task_run: taskRun, taskRun };
    }
    const taskRun = await this.getTaskRun(options);
    const taskId = trim(taskRun.task_id || taskRun.taskId || this.taskId);
    if (!taskId) {
      return { taskRun };
    }
    const task = await this.#json("GET", this.#workspacePath(`/issues/${escapePath(taskId)}`), undefined, options);
    return { ...task, task_run: taskRun, taskRun };
  }

  async heartbeat(input = {}, options = {}) {
    if (this.serveMode) {
      return this.#op("heartbeat", compact({
        runtimeMetadata: metadata(input.runtime_metadata || input.runtimeMetadata),
        logsRef: input.logs_ref || input.logsRef,
        artifactsRef: input.artifacts_ref || input.artifactsRef,
      }), options);
    }
    const body = this.#ownerBody({
      runtime_metadata: metadata(input.runtime_metadata || input.runtimeMetadata),
      logs_ref: input.logs_ref || input.logsRef,
      artifacts_ref: input.artifacts_ref || input.artifactsRef,
    });
    return this.#json("POST", this.#taskRunPath("/heartbeat"), body, withLease(options));
  }

  async appendLog(input = {}, options = {}) {
    if (input.text === undefined || input.text === null) {
      throw new TypeError("logs.append requires text");
    }
    if (this.serveMode) {
      return this.#op("log-append", compact({
        stream: input.stream || "stdout",
        text: String(input.text),
        timestamp: input.timestamp,
      }), options);
    }
    const body = this.#ownerBody({
      stream: input.stream || "stdout",
      text: String(input.text),
      timestamp: input.timestamp,
    });
    return this.#json("POST", this.#taskRunPath("/logs"), body, withLease(options));
  }

  async declareArtifact(input = {}, options = {}) {
    const type = required("artifact type", input.type);
    const artifactId = trim(input.artifact_id || input.artifactId || input.id);
    const idempotencyKey = trim(input.idempotency_key || input.idempotencyKey);
    const artifactMetadata = metadata(input.metadata) || {};
    if (idempotencyKey && artifactMetadata.idempotency_key === undefined) {
      artifactMetadata.idempotency_key = idempotencyKey;
    }
    if (this.serveMode) {
      const artifact = await this.#op("artifact-declare", compact({
        artifactId,
        taskId: input.task_id || input.taskId || this.taskId,
        type,
        uri: input.uri,
        summary: input.summary,
        mimeType: input.mime_type || input.mimeType,
        sizeBytes: input.size_bytes ?? input.sizeBytes,
        checksum: input.checksum,
        contentHash: input.content_hash || input.contentHash,
        visibility: input.visibility,
        redactionStatus: input.redaction_status || input.redactionStatus,
        durableStatus: input.durable_status || input.durableStatus || "declared",
        metadata: Object.keys(artifactMetadata).length > 0 ? artifactMetadata : undefined,
      }), options);
      return new ArtifactHandle(this, artifact);
    }
    const body = compact({
      artifact_id: artifactId,
      owner_type: "task_run",
      owner_id: this.taskRunId,
      task_id: input.task_id || input.taskId || this.taskId,
      type,
      uri: input.uri,
      summary: input.summary,
      mime_type: input.mime_type || input.mimeType,
      size_bytes: input.size_bytes ?? input.sizeBytes,
      checksum: input.checksum,
      content_hash: input.content_hash || input.contentHash,
      visibility: input.visibility,
      redaction_status: input.redaction_status || input.redactionStatus,
      durable_status: input.durable_status || input.durableStatus || "declared",
      metadata: Object.keys(artifactMetadata).length > 0 ? artifactMetadata : undefined,
    });
    const artifact = await this.#json("POST", this.#workspacePath("/artifacts"), body, options);
    return new ArtifactHandle(this, artifact);
  }

  async getArtifact(artifactId, options = {}) {
    if (this.serveMode) {
      const artifact = await this.#op("artifact-get", { artifactId: required("artifactId", artifactId) }, options);
      return new ArtifactHandle(this, artifact);
    }
    const artifact = await this.#json("GET", this.#artifactPath(artifactId), undefined, options);
    return new ArtifactHandle(this, artifact);
  }

  async listArtifacts(input = {}, options = {}) {
    if (this.serveMode) {
      const out = await this.#op("artifact-list", compact({
        type: input.type,
        durableStatus: input.durable_status || input.durableStatus || input.status,
        limit: input.limit,
      }), options);
      return {
        ...out,
        artifacts: (out.artifacts || []).map((artifact) => new ArtifactHandle(this, artifact)),
      };
    }
    const params = new URLSearchParams();
    params.set("owner_type", "task_run");
    params.set("owner_id", this.taskRunId);
    for (const [key, value] of Object.entries({
      type: input.type,
      durable_status: input.durable_status || input.durableStatus || input.status,
      limit: input.limit,
    })) {
      if (value !== undefined && value !== null && String(value).trim() !== "") {
        params.set(key, String(value));
      }
    }
    const out = await this.#json("GET", `${this.#workspacePath("/artifacts")}?${params}`, undefined, options);
    return {
      ...out,
      artifacts: (out.artifacts || []).map((artifact) => new ArtifactHandle(this, artifact)),
    };
  }

  async uploadArtifactContent(artifactId, content, options = {}) {
    if (content === undefined || content === null) {
      throw new TypeError("artifact upload content is required");
    }
    const headers = {};
    const contentType = trim(options.mimeType || options.contentType);
    if (contentType) {
      headers["Content-Type"] = contentType;
    }
    if (this.serveMode) {
      const path = `${this.#workspacePath("/task-run/artifacts")}/${escapePath(required("artifactId", artifactId))}/content`;
      return this.#raw("PUT", path, content, { ...options, headers });
    }
    return this.#raw("PUT", `${this.#artifactPath(artifactId)}/content`, content, { ...options, headers });
  }

  async finalizeArtifact(artifactId, input = {}, options = {}) {
    if (this.serveMode) {
      return this.#op("artifact-finalize", compact({
        artifactId: required("artifactId", artifactId),
        uri: input.uri,
        summary: input.summary,
        mimeType: input.mime_type || input.mimeType,
        sizeBytes: input.size_bytes ?? input.sizeBytes,
        checksum: input.checksum,
        contentHash: input.content_hash || input.contentHash,
        visibility: input.visibility,
        redactionStatus: input.redaction_status || input.redactionStatus,
        metadata: input.metadata,
      }), options);
    }
    const body = compact({
      uri: input.uri,
      summary: input.summary,
      mime_type: input.mime_type || input.mimeType,
      size_bytes: input.size_bytes ?? input.sizeBytes,
      checksum: input.checksum,
      content_hash: input.content_hash || input.contentHash,
      visibility: input.visibility,
      redaction_status: input.redaction_status || input.redactionStatus,
      metadata: input.metadata,
    });
    return this.#json("POST", `${this.#artifactPath(artifactId)}/finalize`, body, options);
  }

  async getRuntimeCredential(input = {}, options = {}) {
    if (!this.serveMode) {
      throw new LoomAPIError("runtime credentials require the loom serve task-run API", {
        code: "serve_transport_required",
      });
    }
    return this.#op("runtime-credential", {
      provider: required("credential provider", input.provider),
    }, options);
  }

  async sessionOpen(input = {}, options = {}) {
    this.#requireServeTransport("session-open");
    return this.#op("session-open", compact({
      invocationKey: input.invocationKey,
      backend: input.backend,
      // The lifecycle store requires an explicit model label. Process callers
      // may omit it only because the SDK truthfully labels it unknown.
      model: trim(input.model) || "unknown",
      parentSessionId: input.parentSessionId,
      kind: input.kind,
      tags: input.tags,
      metadata: metadata(input.metadata),
    }), options);
  }

  async sessionClose(input = {}, options = {}) {
    this.#requireServeTransport("session-close");
    const usage = input.usage && typeof input.usage === "object"
      ? compact({
        tokens: input.usage.tokens,
        inputTokens: input.usage.inputTokens,
        outputTokens: input.usage.outputTokens,
        cacheReadTokens: input.usage.cacheReadTokens,
        cacheWriteTokens: input.usage.cacheWriteTokens,
        cost: input.usage.cost,
      })
      : undefined;
    return this.#op("session-close", compact({
      sessionId: input.sessionId,
      status: input.status,
      exitCode: input.exitCode,
      summary: input.summary,
      usage: usage && Object.keys(usage).length > 0 ? usage : undefined,
      transcriptRef: input.transcriptRef,
      metadata: metadata(input.metadata),
    }), options);
  }

  // Reserved for non-bridge ownership topologies. A bridge-run task-plane
  // leaf must return its IPC result; serve rejects self-completion there.
  async completeRun(input = {}, options = {}) {
    const artifactIds = normalizeStringList(input.required_artifact_ids || input.requiredArtifactIDs || input.artifact_ids || input.artifactIds);
    const policy = input.taskStatusPolicy || input.task_status_policy || {};
    const closeTask = input.close_task ?? input.closeTask ?? policy.action === "close";
    const reason = input.close_reason || input.closeReason || policy.reason;
    if (this.serveMode) {
      return this.#op("complete", compact({
        completionId: input.completion_id || input.completionId || `complete-${this.taskRunId}`,
        status: input.status || "completed",
        exitCode: input.exit_code ?? input.exitCode,
        logsRef: input.logs_ref || input.logsRef,
        artifactsRef: input.artifacts_ref || input.artifactsRef,
        requiredArtifactIds: artifactIds.length > 0 ? artifactIds : undefined,
        requireArtifacts: input.require_artifacts ?? input.requireArtifacts ?? (artifactIds.length > 0 ? true : undefined),
        inputTokens: input.input_tokens ?? input.inputTokens,
        outputTokens: input.output_tokens ?? input.outputTokens,
        cacheReadTokens: input.cache_read_tokens ?? input.cacheReadTokens,
        cacheWriteTokens: input.cache_write_tokens ?? input.cacheWriteTokens,
        estimatedCostUsd: input.estimated_cost_usd ?? input.estimatedCostUsd,
        runtimeMetadata: metadata(input.runtime_metadata || input.runtimeMetadata),
        errorClass: input.error_class || input.errorClass,
        errorMessage: input.error_message || input.errorMessage,
        closeTask: closeTask || undefined,
        closeReason: reason,
      }), options);
    }
    const body = this.#ownerBody({
      completion_id: input.completion_id || input.completionId || `complete-${this.taskRunId}`,
      status: input.status || "completed",
      exit_code: input.exit_code ?? input.exitCode,
      logs_ref: input.logs_ref || input.logsRef,
      artifacts_ref: input.artifacts_ref || input.artifactsRef,
      required_artifact_ids: artifactIds.length > 0 ? artifactIds : undefined,
      require_artifacts: input.require_artifacts ?? input.requireArtifacts ?? (artifactIds.length > 0 ? true : undefined),
      input_tokens: input.input_tokens ?? input.inputTokens,
      output_tokens: input.output_tokens ?? input.outputTokens,
      cache_read_tokens: input.cache_read_tokens ?? input.cacheReadTokens,
      cache_write_tokens: input.cache_write_tokens ?? input.cacheWriteTokens,
      estimated_cost_usd: input.estimated_cost_usd ?? input.estimatedCostUsd,
      runtime_metadata: metadata(input.runtime_metadata || input.runtimeMetadata),
      error_class: input.error_class || input.errorClass,
      error_message: input.error_message || input.errorMessage,
      close_task: closeTask || undefined,
      close_reason: reason,
    });
    return this.#json("POST", this.#taskRunPath("/complete"), body, withLease(options));
  }

  #ownerBody(fields) {
    return compact({
      node_id: this.nodeId,
      lease_id: this.leaseId,
      fencing_token: this.fencingToken,
      ...fields,
    });
  }

  #requireServeTransport(operation) {
    if (!this.serveMode) {
      throw new LoomAPIError(`${operation} requires the loom serve task-run API`, {
        code: "serve_transport_required",
      });
    }
  }

  #taskRunPath(suffix = "") {
    return this.#workspacePath(`/task-runs/${escapePath(this.taskRunId)}${suffix}`);
  }

  #artifactPath(artifactId) {
    return this.#workspacePath(`/artifacts/${escapePath(required("artifactId", artifactId))}`);
  }

  #workspacePath(suffix) {
    if (this.serveMode) {
      return `/api/workspaces/${escapePath(this.workspace)}${suffix}`;
    }
    return `/api/v1/${escapePath(this.workspace)}${suffix}`;
  }

  // #op posts a serve-transport task-run operation (camelCase wire,
  // lease-token auth carried in headers by #request).
  async #op(op, body, options = {}) {
    return this.#json("POST", this.#workspacePath(`/task-run/${op}`), body, options);
  }

  async #json(method, path, body, options = {}) {
    return this.#request(method, path, {
      ...options,
      body: body === undefined ? undefined : JSON.stringify(body),
      headers: {
        "Content-Type": "application/json",
        ...(options.headers || {}),
      },
    });
  }

  async #raw(method, path, body, options = {}) {
    return this.#request(method, path, {
      ...options,
      body,
      headers: options.headers || {},
    });
  }

  async #request(method, path, options = {}) {
    const headers = {
      Accept: "application/json",
      ...(options.headers || {}),
    };
    if (this.serveMode) {
      // Serve transport: the lease token is the ONLY credential. No fleet-db
      // API key, no actor header — the server resolves identity from the
      // fenced lease tuple.
      headers.Authorization = `Bearer ${this.leaseToken}`;
      headers["X-Loom-Task-Run-Id"] = this.taskRunId;
      headers["X-Loom-Task-Run-Node-Id"] = this.nodeId;
      headers["X-Loom-Task-Run-Lease-Id"] = this.leaseId;
      headers["X-Loom-Task-Run-Fencing-Token"] = String(this.fencingToken);
    } else {
      if (this.apiKey) {
        headers["X-API-Key"] = this.apiKey;
        headers["X-Fleet-API-Key"] = this.apiKey;
      }
      if (this.actor) {
        headers["X-Actor"] = this.actor;
      }
      if (this.authToken) {
        headers.Authorization = `Bearer ${this.authToken}`;
      }
      if (options.useLeaseToken !== false && this.leaseToken) {
        headers["X-Lease-Token"] = this.leaseToken;
      }
    }

    const response = await this.fetch(this.baseUrl + path, {
      method,
      headers,
      body: options.body,
      signal: options.signal,
    });
    const text = await response.text();
    if (!response.ok) {
      throw errorFromResponse(response, text);
    }
    if (response.status === 204 || trim(text) === "") {
      return undefined;
    }
    try {
      return JSON.parse(text);
    } catch (error) {
      throw new LoomAPIError(`invalid JSON response from FleetDB: ${error.message}`, {
        status: response.status,
        responseBody: text,
      });
    }
  }
}

export class ArtifactHandle {
  constructor(client, artifact) {
    this.client = client;
    this.artifact = artifact || {};
    this.id = this.artifact.artifact_id || this.artifact.artifactId || "";
  }

  async upload(content, options = {}) {
    this.artifact = await this.client.uploadArtifactContent(this.id, content, options);
    return this;
  }

  async finalize(input = {}, options = {}) {
    this.artifact = await this.client.finalizeArtifact(this.id, input, options);
    return this;
  }

  toJSON() {
    return this.artifact;
  }
}

// executeAgentProcess is the SDK's process-form agent-exec helper. It opens
// sessions only for actual Agent Invocations. It must never be used as a
// generic deterministic-command capture wrapper (checkout, diff, etc. open
// no AgentSession by contract).
async function executeAgentProcess(client, spec) {
  validateAgentExecSpec(spec);
  const session = {
    id: null,
    attempt: null,
    transcriptRef: null,
    opened: false,
    closed: false,
    degraded: false,
    degradedReason: null,
  };
  const runtimeMetadata = {};

  await openAgentSession(client, spec, session, runtimeMetadata);
  const processResult = await runAgentProcess(spec);
  let entries = captureAgentEntries(spec, processResult);
  entries = redactAgentEntries(entries, spec.redactSecrets);
  const usage = extractAgentUsage(entries);
  const streamError = agentStreamError(spec, processResult.stdout);

  if (session.opened && spec.transcript !== "none") {
    await uploadAgentTranscript(client, spec, session, entries, runtimeMetadata);
  }

  const sendClose = async (input = {}) => {
    if (!session.opened) {
      return { ok: false };
    }
    try {
      const closeMetadata = agentCloseMetadata(processResult, input.metadata);
      await client.sessionClose({
        sessionId: session.id,
        status: input.status || agentProcessStatus(processResult),
        exitCode: processResult.exitCode,
        summary: input.summary || agentProcessSummary(spec, processResult),
        usage,
        transcriptRef: session.transcriptRef,
        metadata: closeMetadata,
      });
      session.closed = true;
      return { ok: true };
    } catch (error) {
      await markObservabilityDegraded(client, session, runtimeMetadata, observabilityErrorCode(error));
      return { ok: false };
    }
  };

  if (spec.close !== "deferred") {
    await sendClose();
  }
  const result = {
    ...processResult,
    entries,
    usage,
    streamError,
    session,
    // Merge this into the leaf's TaskRun completion/runtimeMetadata. The
    // helper also best-effort heartbeats it while the lease remains live.
    runtimeMetadata,
  };
  if (spec.close === "deferred") {
    result.finalize = async (input = {}) => sendClose(input);
  }
  return result;
}

// executeAgentInvoke is the SDK's in-process harness form. The leaf owns the
// prompt callback; this helper owns the session lifecycle and transcript
// artifact. The collector lives in host memory until invoke returns, so a leaf
// crash/OOM mid-prompt loses its partial entries and leaves reconciliation to
// stamp agent_session_unclosed. This is intentionally crash-lossy.
//
// This form is for Agent Invocations only. Deterministic sandbox commands
// (clone, checkout, diff, tests, etc.) must never call it or create a session.
async function executeAgentInvoke(client, spec) {
  validateAgentInvokeSpec(spec);
  const session = {
    id: null,
    attempt: null,
    transcriptRef: null,
    opened: false,
    closed: false,
    degraded: false,
    degradedReason: null,
  };
  const runtimeMetadata = {};

  // Opening immediately precedes the leaf-owned prompt call. If the lifecycle
  // plane is degraded, the agent still runs and its result remains useful.
  await openAgentSession(client, spec, session, runtimeMetadata);
  const started = Date.now();
  let response = null;
  let invokeError = null;
  try {
    response = await spec.invoke();
  } catch (error) {
    // A prompt rejection is an invocation outcome, not an SDK exception.
    invokeError = errorMessage(error);
  }
  const durationMs = Date.now() - started;
  const entries = redactAgentEntries(spec.transcriptCollector.entries, spec.redactSecrets);
  const usage = extractInvokeUsage(response);

  if (session.opened) {
    // Upload failure deliberately does not skip close. Match the process form:
    // mark observability degraded and close without transcriptRef so the
    // reconciler never sees a silently open successful invocation.
    await uploadAgentTranscript(client, spec, session, entries, runtimeMetadata);
    try {
      await client.sessionClose({
        sessionId: session.id,
        status: invokeError ? "failed" : "completed",
        summary: agentInvokeSummary(spec, invokeError, durationMs),
        usage,
        transcriptRef: session.transcriptRef,
        metadata: agentInvokeCloseMetadata(invokeError),
      });
      session.closed = true;
    } catch (error) {
      await markObservabilityDegraded(client, session, runtimeMetadata, observabilityErrorCode(error));
    }
  }

  return {
    response,
    invokeError,
    durationMs,
    entries,
    usage,
    session,
    // Merge this into the leaf's TaskRun completion/runtimeMetadata. The
    // helper also best-effort heartbeats it while the lease remains live.
    runtimeMetadata,
  };
}

async function uploadAgentTranscript(client, spec, session, entries, runtimeMetadata) {
  try {
    const artifactId = transcriptArtifactID(client.taskRunId, session.attempt, spec.invocationKey);
    const artifact = await client.artifacts.declare({
      id: artifactId,
      type: "agent-transcript",
      mimeType: "application/x-ndjson",
    });
    await artifact.upload(`${entries.map((entry) => JSON.stringify(entry)).join("\n")}${entries.length > 0 ? "\n" : ""}`, {
      mimeType: "application/x-ndjson",
    });
    await artifact.finalize({ summary: `Agent transcript for ${spec.invocationKey}` });
    session.transcriptRef = `artifact://${artifactId}`;
  } catch (error) {
    await markObservabilityDegraded(client, session, runtimeMetadata, observabilityErrorCode(error));
  }
}

async function openAgentSession(client, spec, session, runtimeMetadata) {
  const retries = spec.openRetries ?? 2;
  for (let attempt = 0; attempt <= retries; attempt += 1) {
    try {
      const opened = await client.sessionOpen({
        invocationKey: spec.invocationKey,
        backend: spec.backend,
        model: spec.model || "unknown",
        parentSessionId: spec.parentSessionId,
        kind: spec.kind,
        tags: spec.tags,
        metadata: spec.metadata,
      });
      session.id = opened.sessionId;
      session.attempt = opened.attempt;
      session.opened = true;
      return;
    } catch (error) {
      const reason = observabilityErrorCode(error);
      if (!agentSessionOpenRetryable(error) || attempt >= retries) {
        await markObservabilityDegraded(client, session, runtimeMetadata, reason);
        return;
      }
      await delay(25 * (attempt + 1));
    }
  }
}

async function markObservabilityDegraded(client, session, runtimeMetadata, reason) {
  session.degraded = true;
  session.degradedReason ||= reason;
  runtimeMetadata.observability_degraded = "true";
  runtimeMetadata.observability_degraded_code ||= reason;
  try {
    await client.heartbeat({ runtimeMetadata });
  } catch {
    // The result still carries the flag for the leaf to merge into its final
    // TaskRun result when taskrunapi was unavailable at degradation time.
  }
}

function agentSessionOpenRetryable(error) {
  // A fetch/transport failure has no wire classification and is retryable.
  // HTTP errors must opt in through the taskrunapi retryable envelope field.
  return !(error instanceof LoomAPIError) || error.retryable;
}

function observabilityErrorCode(error) {
  if (error && typeof error.code === "string" && error.code !== "") return error.code;
  if (error instanceof LoomAPIError && error.status) return `http_${error.status}`;
  return "transport_failure";
}

function validateAgentExecSpec(spec) {
  if (!spec || typeof spec !== "object" || Array.isArray(spec)) {
    throw new AgentExecSpecError("agent.exec requires a process-form spec object");
  }
  rejectAgentExecKeys(spec, ["invoke", "transcriptCollector", "run", "prompt"], "agent.exec accepts only the process form; use agent.exec.invoke for an in-process prompt");
  validateAgentExecDescriptor(spec, "agent.exec");
  if (!Array.isArray(spec.argv) || spec.argv.length === 0 || spec.argv.some((part) => typeof part !== "string" || part === "")) {
    throw new AgentExecSpecError("agent.exec argv must be a non-empty string array");
  }
  if (spec.cwd !== undefined && typeof spec.cwd !== "string") {
    throw new AgentExecSpecError("agent.exec cwd must be a string when supplied");
  }
  if (spec.env !== undefined && (!spec.env || typeof spec.env !== "object" || Array.isArray(spec.env) || Object.values(spec.env).some((value) => value !== undefined && typeof value !== "string"))) {
    throw new AgentExecSpecError("agent.exec env must map names to strings when supplied");
  }
  if (spec.stdin !== undefined && typeof spec.stdin !== "string" && !(spec.stdin instanceof Uint8Array)) {
    throw new AgentExecSpecError("agent.exec stdin must be a string or Uint8Array when supplied");
  }
  if (spec.timeoutMs !== undefined && (!Number.isFinite(spec.timeoutMs) || spec.timeoutMs < 0)) {
    throw new AgentExecSpecError("agent.exec timeoutMs must be a non-negative number when supplied");
  }
  if (spec.live !== undefined && typeof spec.live !== "boolean") {
    throw new AgentExecSpecError("agent.exec live must be a boolean when supplied");
  }
  if (spec.close !== undefined && spec.close !== "auto" && spec.close !== "deferred") {
    throw new AgentExecSpecError("agent.exec close must be auto or deferred");
  }
  if (spec.transcript !== undefined && !["stream-json", "minimal", "none"].includes(spec.transcript)) {
    throw new AgentExecSpecError("agent.exec transcript must be stream-json, minimal, or none");
  }
  if (spec.openRetries !== undefined && (!Number.isInteger(spec.openRetries) || spec.openRetries < 0 || spec.openRetries > 10)) {
    throw new AgentExecSpecError("agent.exec openRetries must be an integer from 0 to 10");
  }
  if (spec.redactSecrets !== undefined && !isDeclaredSecretList(spec.redactSecrets)) {
    throw new AgentExecSpecError("agent.exec redactSecrets must declare secret values as a string array or object");
  }
}

function validateAgentInvokeSpec(spec) {
  if (!spec || typeof spec !== "object" || Array.isArray(spec)) {
    throw new AgentExecSpecError("agent.exec.invoke requires an invoke-form spec object");
  }
  rejectAgentExecKeys(
    spec,
    ["argv", "cwd", "env", "stdin", "timeoutMs", "live", "transcript", "close"],
    "agent.exec.invoke accepts only the invoke form; use agent.exec for a child process",
  );
  validateAgentExecDescriptor(spec, "agent.exec.invoke");
  if (typeof spec.invoke !== "function") {
    throw new AgentExecSpecError("agent.exec.invoke invoke must be a function");
  }
  if (!spec.transcriptCollector || typeof spec.transcriptCollector !== "object" || Array.isArray(spec.transcriptCollector) ||
    !Array.isArray(spec.transcriptCollector.entries) || spec.transcriptCollector.entries.some((entry) => !entry || typeof entry !== "object" || Array.isArray(entry))) {
    throw new AgentExecSpecError("agent.exec.invoke transcriptCollector must expose canonical entry objects from the in-process collector");
  }
  if (spec.openRetries !== undefined && (!Number.isInteger(spec.openRetries) || spec.openRetries < 0 || spec.openRetries > 10)) {
    throw new AgentExecSpecError("agent.exec.invoke openRetries must be an integer from 0 to 10");
  }
  if (spec.redactSecrets !== undefined && !isDeclaredSecretList(spec.redactSecrets)) {
    throw new AgentExecSpecError("agent.exec.invoke redactSecrets must declare secret values as a string array or object");
  }
}

function validateAgentExecDescriptor(spec, operation) {
  if (typeof spec.invocationKey !== "string" || !/^[a-z0-9][a-z0-9-]{0,63}$/.test(spec.invocationKey)) {
    throw new AgentExecSpecError(`${operation} invocationKey must be a strict slug`);
  }
  if (typeof spec.backend !== "string" || trim(spec.backend) === "") {
    throw new AgentExecSpecError(`${operation} backend is required`);
  }
  if (spec.model !== undefined && typeof spec.model !== "string") {
    throw new AgentExecSpecError(`${operation} model must be a string when supplied`);
  }
  if (spec.parentSessionId !== undefined && typeof spec.parentSessionId !== "string") {
    throw new AgentExecSpecError(`${operation} parentSessionId must be a string when supplied`);
  }
  if (spec.kind !== undefined && typeof spec.kind !== "string") {
    throw new AgentExecSpecError(`${operation} kind must be a string when supplied`);
  }
  if (spec.tags !== undefined && (!Array.isArray(spec.tags) || spec.tags.some((tag) => typeof tag !== "string"))) {
    throw new AgentExecSpecError(`${operation} tags must be a string array when supplied`);
  }
  if (spec.metadata !== undefined && (!spec.metadata || typeof spec.metadata !== "object" || Array.isArray(spec.metadata))) {
    throw new AgentExecSpecError(`${operation} metadata must be an object when supplied`);
  }
}

function rejectAgentExecKeys(spec, keys, message) {
  if (keys.some((key) => key in spec)) {
    throw new AgentExecSpecError(message);
  }
}

function runAgentProcess(spec) {
  const started = Date.now();
  return new Promise((resolve) => {
    let child;
    try {
      child = spawn(spec.argv[0], spec.argv.slice(1), {
        cwd: spec.cwd,
        env: { ...process.env, ...(spec.env || {}) },
        stdio: ["pipe", "pipe", "pipe"],
      });
    } catch (error) {
      resolve(processResult(null, false, errorMessage(error), "", "", started));
      return;
    }
    let stdout = "";
    let stderr = "";
    let timedOut = false;
    let settled = false;
    const timer = spec.timeoutMs > 0 ? setTimeout(() => {
      timedOut = true;
      child.kill("SIGKILL");
    }, spec.timeoutMs) : null;
    const settle = (exitCode, spawnError) => {
      if (settled) return;
      settled = true;
      if (timer) clearTimeout(timer);
      resolve(processResult(exitCode, timedOut, spawnError, stdout, stderr, started));
    };
    child.stdout.on("data", (chunk) => {
      stdout += chunk;
      if (spec.live) process.stderr.write(chunk);
    });
    child.stderr.on("data", (chunk) => {
      stderr += chunk;
      if (spec.live) process.stderr.write(chunk);
    });
    child.on("error", (error) => settle(null, errorMessage(error)));
    child.on("close", (code) => settle(code, null));
    if (spec.stdin !== undefined) child.stdin.end(spec.stdin);
    else child.stdin.end();
  });
}

function processResult(exitCode, timedOut, spawnError, stdout, stderr, started) {
  return { exitCode, timedOut, spawnError, stdout, stderr, durationMs: Date.now() - started };
}

function captureAgentEntries(spec, result) {
  if (spec.transcript === "none") return [];
  const lead = {
    seq: 0,
    role: "system",
    type: "session_meta",
    backend: spec.backend,
    model: spec.model || "unknown",
    invocation_key: spec.invocationKey,
  };
  if (spec.transcript === "stream-json") {
    const entries = [lead];
    for (const line of result.stdout.split("\n")) {
      try {
        const event = JSON.parse(line);
        if (event && typeof event === "object" && event.type !== "session_meta") {
          for (const entry of streamEventEntries(spec.backend, event)) {
            entries.push({ ...entry, seq: entries.length });
          }
        }
      } catch {
        // Non-JSON backend chatter is intentionally not promoted to an event.
      }
    }
    if (entries.length > 1) return entries;
  }
  const entries = [lead];
  if (spec.stdin !== undefined && String(spec.stdin) !== "") {
    entries.push({ seq: entries.length, role: "user", type: "text", text: String(spec.stdin) });
  }
  if (result.stdout !== "") {
    entries.push({ seq: entries.length, role: "assistant", type: "text", text: result.stdout });
  }
  if (result.stderr !== "") {
    entries.push({ seq: entries.length, role: "tool", type: "stderr", text: result.stderr });
  }
  return entries;
}

function streamEventEntries(backend, event) {
  if (backend === "codex") {
    return codexStreamEventEntries(event);
  }
  return [event];
}

function codexStreamEventEntries(event) {
  if (!isModernCodexStreamEvent(event)) {
    return [event];
  }
  if (event.type === "item.completed") {
    return codexItemEntries(event.item);
  }
  if (event.type === "turn.completed") {
    const usage = codexUsage(event.usage);
    return usage ? [{ role: "system", type: "result", usage }] : [];
  }
  return [];
}

function isModernCodexStreamEvent(event) {
  return ["thread.started", "turn.started", "item.completed", "turn.completed"].includes(event.type);
}

function codexItemEntries(item) {
  if (!item || typeof item !== "object") return [];
  const text = textValue(item.text);
  if (item.type === "agent_message") {
    return text ? [{ role: "assistant", type: "text", text }] : [];
  }
  if (item.type === "reasoning") {
    return text ? [{ role: "assistant", type: "reasoning", text }] : [];
  }
  if (item.type === "command_execution") {
    return codexCommandEntries(item);
  }
  return [];
}

function codexCommandEntries(item) {
  const toolUseId = textValue(item.id);
  const output = textValue(item.aggregated_output ?? item.output) || codexCommandExit(item.exit_code);
  return [
    {
      role: "assistant",
      type: "tool_use",
      tool_name: "command_execution",
      tool_use_id: toolUseId,
      tool_input: { command: textValue(item.command) },
    },
    { role: "tool", type: "tool_result", tool_use_id: toolUseId, output },
  ];
}

function codexCommandExit(exitCode) {
  return Number.isFinite(exitCode) ? `exit code: ${exitCode}` : "";
}

function codexUsage(value) {
  if (!value || typeof value !== "object") return null;
  const usage = compact({
    input_tokens: finiteNumber(value.input_tokens ?? value.inputTokens),
    cached_input_tokens: finiteNumber(value.cached_input_tokens ?? value.cachedInputTokens),
    output_tokens: finiteNumber(value.output_tokens ?? value.outputTokens),
    cache_write_tokens: finiteNumber(value.cache_write_tokens ?? value.cacheWriteTokens),
    cost_usd: finiteNumber(value.cost_usd ?? value.costUsd ?? value.total_cost_usd),
  });
  return Object.keys(usage).length > 0 ? usage : null;
}

function redactAgentEntries(entries, declaredSecrets) {
  const secrets = declaredSecretValues(declaredSecrets);
  if (secrets.length === 0) return entries;
  return entries.map((entry) => {
    let encoded = JSON.stringify(entry);
    for (const secret of secrets) {
      encoded = encoded.split(secret).join("[REDACTED]");
    }
    return JSON.parse(encoded);
  });
}

function isDeclaredSecretList(value) {
  if (Array.isArray(value)) return value.every((item) => typeof item === "string");
  return value && typeof value === "object" && !Array.isArray(value) && Object.values(value).every((item) => typeof item === "string");
}

function declaredSecretValues(value) {
  const candidates = Array.isArray(value) ? value : value && typeof value === "object" ? Object.values(value) : [];
  return candidates.filter((item) => typeof item === "string" && item !== "");
}

function extractAgentUsage(entries) {
  for (let index = entries.length - 1; index >= 0; index -= 1) {
    const usage = normalizeAgentUsage(entries[index]?.usage);
    if (usage) return usage;
  }
  return null;
}

function extractInvokeUsage(response) {
  return response && typeof response === "object" ? normalizeAgentUsage(response.usage) : null;
}

function normalizeAgentUsage(usage) {
  if (!usage || typeof usage !== "object") return null;
  const input = finiteNumber(usage.input_tokens ?? usage.inputTokens ?? usage.input);
  const output = finiteNumber(usage.output_tokens ?? usage.outputTokens ?? usage.output);
  const cacheRead = finiteNumber(usage.cache_read_tokens ?? usage.cacheReadTokens ?? usage.cached_input_tokens ?? usage.cacheRead);
  const cacheWrite = finiteNumber(usage.cache_write_tokens ?? usage.cacheWriteTokens ?? usage.cacheWrite);
  const tokens = finiteNumber(usage.tokens ?? usage.total_tokens ?? usage.totalTokens) ??
    (input !== null || output !== null ? (input || 0) + (output || 0) : null);
  const structuredCost = usage.cost && typeof usage.cost === "object" ? usage.cost.total : usage.cost;
  const cost = finiteNumber(structuredCost ?? usage.cost_usd ?? usage.costUsd ?? usage.total_cost_usd);
  if (tokens === null && cost === null && input === null && output === null && cacheRead === null && cacheWrite === null) {
    return null;
  }
  const result = { tokens, cost };
  if (input !== null) result.inputTokens = input;
  if (output !== null) result.outputTokens = output;
  if (cacheRead !== null) result.cacheReadTokens = cacheRead;
  if (cacheWrite !== null) result.cacheWriteTokens = cacheWrite;
  return result;
}

function agentStreamError(spec, stdout) {
  if (spec.backend !== "opencode") return "";
  for (const line of stdout.split("\n")) {
    try {
      const event = JSON.parse(line);
      if (event?.type !== "error") continue;
      return String(event.error?.message || event.error?.data?.message || event.message || "opencode reported an error");
    } catch {
      // Stream-json capture deliberately ignores backend chatter.
    }
  }
  return "";
}

function finiteNumber(value) {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function textValue(value) {
  return typeof value === "string" ? value : "";
}

function transcriptArtifactID(taskRunId, attempt, invocationKey) {
  return `transcript-${taskRunId}-a${attempt}-${invocationKey}`;
}

function agentProcessStatus(result) {
  return result.spawnError || result.timedOut || result.exitCode !== 0 ? "failed" : "completed";
}

function agentProcessErrorClass(result) {
  if (result.spawnError) return "agent_spawn_failed";
  if (result.timedOut) return "agent_timeout";
  if (result.exitCode !== 0) return "agent_nonzero_exit";
  return "";
}

function agentCloseMetadata(result, supplied) {
  const closeMetadata = { ...(supplied || {}) };
  const errorClass = agentProcessErrorClass(result);
  if (errorClass && closeMetadata.error_class === undefined) {
    closeMetadata.error_class = errorClass;
  }
  return Object.keys(closeMetadata).length > 0 ? closeMetadata : undefined;
}

function agentProcessSummary(spec, result) {
  if (result.spawnError) return `${spec.backend} spawn failed: ${result.spawnError}`;
  if (result.timedOut) return `${spec.backend} timed out after ${spec.timeoutMs}ms`;
  return `${spec.backend} exited ${result.exitCode} in ${result.durationMs}ms`;
}

function agentInvokeSummary(spec, invokeError, durationMs) {
  if (invokeError) return `${spec.backend} prompt failed: ${invokeError}`;
  return `${spec.backend} prompt completed in ${durationMs}ms`;
}

function agentInvokeCloseMetadata(invokeError) {
  return invokeError ? { error_class: "agent_invoke_failed" } : undefined;
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function errorMessage(error) {
  return error && error.message ? error.message : String(error);
}

function withLease(options) {
  return { ...options, useLeaseToken: true };
}

function normalizeBaseUrl(value) {
  const out = trim(value);
  if (!out) {
    throw new TypeError("baseUrl is required");
  }
  return out.replace(/\/+$/, "");
}

function required(name, value) {
  const out = trim(value);
  if (!out) {
    throw new TypeError(`${name} is required`);
  }
  return out;
}

function parseFencingToken(value, options = {}) {
  const raw = trim(value);
  if (!/^[1-9]\d*$/.test(raw)) {
    throw new TypeError("fencingToken must be a positive integer");
  }
  if (options.preserveString) {
    return raw;
  }
  const token = Number(raw);
  if (!Number.isSafeInteger(token)) {
    throw new TypeError("fencingToken must be a positive integer");
  }
  return token;
}

function parseRequestJson(raw) {
  raw = trim(raw);
  if (!raw) {
    return {};
  }
  try {
    const parsed = JSON.parse(raw);
    return parsed && typeof parsed === "object" && !Array.isArray(parsed) ? parsed : {};
  } catch (err) {
    throw new TypeError(`LOOM_TASK_RUN_REQUEST_JSON is invalid JSON: ${err.message}`);
  }
}

function cloneRequestPayload(value) {
  if (value === undefined || value === null) {
    return value;
  }
  return JSON.parse(JSON.stringify(value));
}

function escapePath(value) {
  return encodeURIComponent(String(value));
}

function metadata(value) {
  if (value === undefined || value === null) {
    return undefined;
  }
  if (typeof value !== "object" || Array.isArray(value)) {
    throw new TypeError("metadata must be an object");
  }
  const out = {};
  for (const [key, item] of Object.entries(value)) {
    if (item !== undefined && item !== null) {
      out[key] = String(item);
    }
  }
  return out;
}

function compact(value) {
  const out = {};
  for (const [key, item] of Object.entries(value)) {
    if (item === undefined || item === null) {
      continue;
    }
    if (typeof item === "string" && item.trim() === "") {
      continue;
    }
    out[key] = item;
  }
  return out;
}

function normalizeStringList(value) {
  if (value === undefined || value === null) {
    return [];
  }
  if (!Array.isArray(value)) {
    throw new TypeError("artifactIds must be an array");
  }
  return value.map((item) => trim(item)).filter(Boolean);
}

function errorFromResponse(response, text) {
  let message = `FleetDB request failed with HTTP ${response.status}`;
  let code = "";
  let details;
  let retryable = false;
  if (trim(text)) {
    try {
      const body = JSON.parse(text);
      if (body?.error) {
        message = body.error.message || message;
        code = body.error.code || "";
        details = body.error.details;
        retryable = body.error.retryable === true;
      } else if (body?.message) {
        message = body.message;
        code = body.code || "";
        details = body.details;
        retryable = body.retryable === true;
      }
    } catch {
      message = text;
    }
  }
  return new LoomAPIError(message, {
    status: response.status,
    code,
    retryable,
    details,
    responseBody: text,
  });
}
