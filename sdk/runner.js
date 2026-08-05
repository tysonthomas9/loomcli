import { pickEnv, trim } from "./internal.js";

export const RunnerEnv = Object.freeze({
  apiUrl: "LOOM_TASK_RUN_API_URL",
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
    this.details = options.details;
    this.responseBody = options.responseBody || "";
  }
}

export class TaskRunClient {
  #requestPayload;

  static fromEnv(env = process.env, options = {}) {
    return new TaskRunClient({
      apiUrl: options.apiUrl || pickEnv(env, RunnerEnv.apiUrl),
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
    // Phase 4 intentionally has one mutation path. Runner code always calls
    // the loom serve facade, which derives identity from the fenced TaskRun
    // headers. Direct FleetDB credentials and routes are not accepted.
    this.apiUrl = normalizeApiUrl(options.apiUrl);
    // Read-only compatibility indicators for callers that previously selected
    // between transports. They can now only describe the serve facade.
    this.serveMode = true;
    this.baseUrl = this.apiUrl;
    this.workspace = required("workspace", options.workspace);
    this.taskRunId = required("taskRunId", options.taskRunId);
    this.taskId = trim(options.taskId);
    this.nodeId = required("nodeId", options.nodeId);
    this.leaseId = required("leaseId", options.leaseId);
    this.leaseToken = required("leaseToken", options.leaseToken);
    this.fencingToken = parseFencingToken(required("fencingToken", options.fencingToken), { preserveString: true });
    this.requestJson = trim(options.requestJson);
    this.#requestPayload = parseRequestJson(this.requestJson);
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
    return this.#op("get", {}, options);
  }

  async getTask(options = {}) {
    const out = await this.#op("task-get", {}, options);
    const taskRun = out?.taskRun;
    if (!out?.task) {
      return { taskRun };
    }
    return { ...out.task, task_run: taskRun, taskRun };
  }

  async heartbeat(input = {}, options = {}) {
    return this.#op("heartbeat", compact({
      runtimeMetadata: metadata(input.runtime_metadata || input.runtimeMetadata),
      logsRef: input.logs_ref || input.logsRef,
      artifactsRef: input.artifacts_ref || input.artifactsRef,
    }), options);
  }

  async appendLog(input = {}, options = {}) {
    if (input.text === undefined || input.text === null) {
      throw new TypeError("logs.append requires text");
    }
    const requestId = logAppendRequestId(input);
    const timestamp = logAppendTimestamp(input.timestamp);
    return this.#op("log-append", compact({
      requestId,
      stream: input.stream || "stdout",
      text: String(input.text),
      timestamp,
    }), options);
  }

  async declareArtifact(input = {}, options = {}) {
    const type = required("artifact type", input.type);
    const artifactId = trim(input.artifact_id || input.artifactId || input.id);
    const idempotencyKey = trim(input.idempotency_key || input.idempotencyKey);
    const artifactMetadata = metadata(input.metadata) || {};
    if (idempotencyKey && artifactMetadata.idempotency_key === undefined) {
      artifactMetadata.idempotency_key = idempotencyKey;
    }
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

  async getArtifact(artifactId, options = {}) {
    const artifact = await this.#op("artifact-get", { artifactId: required("artifactId", artifactId) }, options);
    return new ArtifactHandle(this, artifact);
  }

  async listArtifacts(input = {}, options = {}) {
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

  async uploadArtifactContent(artifactId, content, options = {}) {
    if (content === undefined || content === null) {
      throw new TypeError("artifact upload content is required");
    }
    const headers = {};
    const contentType = trim(options.mimeType || options.contentType);
    if (contentType) {
      headers["Content-Type"] = contentType;
    }
    const path = `${this.#workspacePath("/task-run/artifacts")}/${escapePath(required("artifactId", artifactId))}/content`;
    return this.#raw("PUT", path, content, { ...options, headers });
  }

  async finalizeArtifact(artifactId, input = {}, options = {}) {
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

  async getRuntimeCredential(input = {}, options = {}) {
    return this.#op("runtime-credential", {
      provider: required("credential provider", input.provider),
    }, options);
  }

  async completeRun(input = {}, options = {}) {
    const artifactIds = normalizeStringList(input.required_artifact_ids || input.requiredArtifactIDs || input.artifact_ids || input.artifactIds);
    const policy = input.taskStatusPolicy || input.task_status_policy || {};
    const closeTask = input.close_task ?? input.closeTask ?? policy.action === "close";
    const reason = input.close_reason || input.closeReason || policy.reason;
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

  #workspacePath(suffix) {
    return `/api/workspaces/${escapePath(this.workspace)}${suffix}`;
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
    headers.Authorization = `Bearer ${this.leaseToken}`;
    headers["X-Loom-Task-Run-Id"] = this.taskRunId;
    headers["X-Loom-Task-Run-Node-Id"] = this.nodeId;
    headers["X-Loom-Task-Run-Lease-Id"] = this.leaseId;
    headers["X-Loom-Task-Run-Fencing-Token"] = String(this.fencingToken);

    const response = await this.fetch(this.apiUrl + path, {
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
      throw new LoomAPIError(`invalid JSON response from Loom task-run API: ${error.message}`, {
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

function normalizeApiUrl(value) {
  const out = trim(value);
  if (!out) {
    throw new TypeError("apiUrl is required (set LOOM_TASK_RUN_API_URL)");
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

function logAppendRequestId(input) {
  const requestId = trim(input.requestId);
  const snakeRequestId = trim(input.request_id);
  if (requestId && snakeRequestId && requestId !== snakeRequestId) {
    throw new TypeError("logs.append requestId and request_id must match");
  }
  return required("logs.append requestId", requestId || snakeRequestId);
}

function logAppendTimestamp(value) {
  if (value instanceof Date) {
    if (Number.isNaN(value.getTime())) {
      throw new TypeError("logs.append timestamp must be a valid date-time");
    }
    return value.toISOString();
  }
  const timestamp = trim(value);
  const rfc3339 = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/;
  if (!rfc3339.test(timestamp) || Number.isNaN(Date.parse(timestamp))) {
    throw new TypeError("logs.append timestamp must be a valid date-time");
  }
  return timestamp;
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
  let message = `Loom task-run API request failed with HTTP ${response.status}`;
  let code = "";
  let details;
  if (trim(text)) {
    try {
      const body = JSON.parse(text);
      if (body?.error) {
        message = body.error.message || message;
        code = body.error.code || "";
        details = body.error.details;
      } else if (body?.message) {
        message = body.message;
        code = body.code || "";
        details = body.details;
      }
    } catch {
      message = text;
    }
  }
  return new LoomAPIError(message, {
    status: response.status,
    code,
    details,
    responseBody: text,
  });
}
