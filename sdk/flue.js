// NOTE: this module must stay self-contained (no local imports): callers such as
// scripts/run-slack-codex-epic-runner-stack.sh vendor flue.js as a single file.
//
// Workflow driver operations use the loom serve driver-op HTTP API with
// camelCase JSON and structured errors.

const DEFAULT_HTTP_TIMEOUT_MS = 30_000;

// DriverApiError carries the structured v2 error envelope:
// {code, message, retryable} plus the HTTP status.
export class DriverApiError extends Error {
  constructor(message, { code, retryable, status, details } = {}) {
    super(message);
    this.name = "DriverApiError";
    this.code = code || "internal";
    this.retryable = Boolean(retryable);
    this.status = status || 0;
    if (details !== undefined) {
      this.details = details;
    }
  }
}

export class FlueDriverClient {
  static fromEnv(options = {}) {
    return new FlueDriverClient({
      env: options.env || process.env,
      input: options.input,
      apiUrl: options.apiUrl,
      apiToken: options.apiToken,
    });
  }

  constructor(options = {}) {
    this.env = options.env || process.env;
    this.input = options.input || {};
    this.apiUrl = stripTrailingSlash(String(options.apiUrl || pickEnv(this.env, "LOOM_DRIVER_API_URL")));
    this.apiToken = String(options.apiToken || pickEnv(this.env, "LOOM_DRIVER_API_TOKEN"));
    this.workspace = pickEnv(this.env, "LOOM_DRIVER_WORKSPACE");
    this.driverRunId = pickEnv(this.env, "LOOM_DRIVER_RUN_ID");
    this.taskRunResultsByTaskId = new Map();
    this.taskRunResultsByRunId = new Map();
    this.epics = Object.freeze({
      get: (input = {}) => this.getEpic(input),
      snapshot: (input = {}) => this.epicSnapshot(input),
      watch: (input = {}) => this.watchEpic(input),
    });
    this.agents = Object.freeze({
      list: (input = {}) => this.listAgents(input),
      orchestrationSession: (input = {}) => this.agentOrchestrationSession(input),
      updateParent: (input = {}) => this.updateAgentParent(input),
      deliverAssignment: (input = {}) => this.deliverLeadAssignment(input),
      message: (input = {}) => this.messageAgent(input),
    });
    this.tasks = Object.freeze({
      claimReady: (input = {}) => this.claimReady(input),
      complete: (input = {}) => this.completeTask(input),
      release: (input = {}) => this.releaseTask(input),
    });
    this.taskRuns = Object.freeze({
      request: (input = {}) => this.requestTaskRun(input),
      get: (input = {}) => this.getTaskRun(input),
      await: (input = {}) => this.awaitTaskRun(input),
      active: (input = {}) => this.activeTaskRuns(input),
      recoverStale: (input = {}) => this.recoverStaleTaskRuns(input),
    });
  }

  completed(input = {}) {
    return { status: "completed", summary: input.summary || "completed" };
  }

  failed(input = {}) {
    return {
      status: "failed",
      summary: input.summary || "failed",
      errorClass: input.errorClass || "driver_failed",
    };
  }

  needsReview(input = {}) {
    return {
      status: "needs_review",
      summary: input.summary || "needs review",
      errorClass: input.errorClass || "needs_review",
      taskRunId: input.taskRunId,
      logsRef: input.logsRef,
      artifactsRef: input.artifactsRef,
    };
  }

  async claimReady(input = {}) {
    return this.#httpCall("claim-ready", {
      epicId: this.#epicID(input),
      actor: input.actor || "",
      limit: input.limit || "",
    });
  }

  async getEpic(input = {}) {
    return this.#httpCall("epic-get", { epicId: this.#epicID(input) });
  }

  async epicSnapshot(input = {}) {
    return this.#httpCall("epic-snapshot", { epicId: this.#epicID(input) });
  }

  // watchEpic returns an async iterator over the epic watch SSE stream
  // (GET /api/workspaces/{ws}/driver/watch/epic), yielding {type, id, data}
  // where type is "snapshot" | "taskRun" | "closed". The iterator reconnects
  // automatically after stream end or network errors, resuming with the
  // Last-Event-ID cursor; it honors server "retry:" hints, ends after a
  // "closed" event, ends silently when input.signal aborts, and throws
  // DriverApiError for non-retryable HTTP failures (e.g. 401).
  watchEpic(input = {}) {
    this.#requireHttpConfig();
    return this.#watchEpicStream(input);
  }

  async *#watchEpicStream(input) {
    const epicId = this.#epicID(input);
    const signal = input.signal;
    let retryMs = Math.max(0, Number(input.reconnectMs ?? 2000));
    let lastEventId =
      input.afterSeq === undefined || input.afterSeq === null || input.afterSeq === ""
        ? ""
        : String(input.afterSeq);
    const query = new URLSearchParams();
    if (epicId) {
      query.set("epicId", String(epicId));
    }
    const queryString = query.toString();
    const url = `${this.apiUrl}/api/workspaces/${encodeURIComponent(this.workspace)}/driver/watch/epic`
      + (queryString ? `?${queryString}` : "");
    while (true) {
      if (signal?.aborted) {
        return;
      }
      const controller = new AbortController();
      const onAbort = () => controller.abort(signal?.reason);
      signal?.addEventListener("abort", onAbort, { once: true });
      try {
        const headers = this.#identityHeaders();
        headers.Accept = "text/event-stream";
        if (lastEventId !== "") {
          headers["Last-Event-ID"] = lastEventId;
        }
        const response = await fetch(url, { headers, signal: controller.signal });
        if (!response.ok) {
          throw await watchHttpError(response);
        }
        for await (const frame of sseFrames(response.body)) {
          if (frame.retryMs !== undefined) {
            retryMs = frame.retryMs;
          }
          if (frame.id !== undefined && frame.id !== "") {
            lastEventId = frame.id;
          }
          if (frame.data === undefined) {
            continue;
          }
          const event = { type: frame.event || "message", id: lastEventId, data: parseSSEData(frame.data) };
          yield event;
          if (event.type === "closed") {
            return;
          }
        }
        // Stream ended without a "closed" event: reconnect with the cursor.
      } catch (err) {
        if (signal?.aborted) {
          return;
        }
        if (err instanceof DriverApiError && !err.retryable) {
          throw err;
        }
        // Retryable API errors and network/stream failures fall through to
        // the reconnect path below.
      } finally {
        signal?.removeEventListener("abort", onAbort);
        controller.abort();
      }
      await watchDelay(retryMs, signal);
    }
  }

  async listAgents(_input = {}) {
    return this.#httpCall("list-agents", {});
  }

  async agentOrchestrationSession(input = {}) {
    const agent = agentNameOf(input);
    if (!agent) {
      throw new Error("agents.orchestrationSession requires agent");
    }
    return this.#httpCall("agent-orchestration-session", { agent: String(agent) });
  }

  async updateAgentParent(input = {}) {
    const agent = agentNameOf(input);
    const parent = input.parent || input.parentEpicId || "";
    if (!agent || !parent) {
      throw new Error("agents.updateParent requires agent and parent");
    }
    const params = {
      agent: String(agent),
      parent: String(parent),
      expectParent: input.expectParent || "",
    };
    return this.#httpCall("update-agent-parent", params);
  }

  async deliverLeadAssignment(input = {}) {
    const agent = agentNameOf(input);
    if (!agent) {
      throw new Error("agents.deliverAssignment requires agent");
    }
    return this.#httpCall("deliver-lead-assignment", { agent: String(agent) });
  }

  async messageAgent(input = {}) {
    const agent = agentNameOf(input);
    const message = input.message || input.text || input.body || "";
    if (!agent || !message) {
      throw new Error("agents.message requires agent and message");
    }
    return this.#httpCall("deliver-agent-message", { agent: String(agent), message: String(message) });
  }

  async requestTaskRun(input = {}) {
    const taskId = input.taskId;
    if (!taskId) {
      throw new Error("taskRuns.request requires taskId");
    }
    const sandboxPlacement = input.sandboxPlacement || {};
    const params = {
      taskId: String(taskId),
      providerProfile: input.providerProfile || "",
      taskRunId: input.taskRunId || "",
      workerProfileId: input.workerProfileId || "",
      parentSessionId: input.parentSessionId || "",
      nodeId: input.nodeId || "",
      runnerId: input.runnerId || "",
      driverStepId: input.driverStepId || "",
      supportedProviders: stringList(input.supportedProviders),
      capabilities: stringList(input.capabilities),
      sandboxPlacement: {
        provider: sandboxPlacement.provider || "",
        sandboxId: sandboxPlacement.sandboxId || "",
        cwd: sandboxPlacement.cwd || "",
        repoRef: sandboxPlacement.repoRef || "",
      },
      leaseToken: input.leaseToken || pickEnv(this.env, "LOOM_TASK_RUN_LEASE_TOKEN") || pickEnv(this.env, "LOOM_RUNNER_LEASE_TOKEN"),
      deferCompletion: true,
    };
    const result = await this.#httpCall("exec-task", { ...params, enqueueOnly: true });
    rememberTaskRunResult(this, result || {});
    return result;
  }

  async getTaskRun(input = {}) {
    const taskRunId = input.taskRunId || "";
    if (!taskRunId) {
      throw new Error("taskRuns.get requires taskRunId");
    }
    return this.#httpCall("task-run-get", { taskRunId: String(taskRunId) });
  }

  async awaitTaskRun(input = {}) {
    const taskRunId = input.taskRunId || "";
    if (!taskRunId) {
      throw new Error("taskRuns.await requires taskRunId");
    }
    const pollMs = Math.max(100, Number(input.pollMs || 2000));
    const timeoutMs = Math.max(0, Number(input.timeoutMs || 0));
    const started = Date.now();
    while (true) {
      const result = await this.getTaskRun({ taskRunId });
      if (isTerminalTaskRunStatus(result?.status)) {
        rememberTaskRunResult(this, result || {});
        return result;
      }
      if (timeoutMs > 0 && Date.now() - started >= timeoutMs) {
        throw new DriverApiError(`task run ${taskRunId} did not finish within ${timeoutMs}ms`, { code: "timeout", retryable: true });
      }
      await sleep(Math.min(pollMs, timeoutMs > 0 ? Math.max(1, timeoutMs - (Date.now() - started)) : pollMs));
    }
  }

  async activeTaskRuns(input = {}) {
    return this.#httpCall("active-task-runs", {
      epicId: this.#epicID(input),
      limit: input.limit || "",
    });
  }

  async recoverStaleTaskRuns(input = {}) {
    return this.#httpCall("recover-stale-tasks", {
      staleBefore: input.staleBefore || "",
      maxAgeSeconds: input.maxAgeSeconds || "",
      errorClass: input.errorClass || "",
      errorMessage: input.errorMessage || "",
    });
  }

  async completeTask(input = {}) {
    const taskId = taskPayloadID(input);
    const requestedTaskRunId = input.taskRunId || "";
    const remembered = requestedTaskRunId
      ? this.taskRunResultsByRunId.get(String(requestedTaskRunId))
      : this.taskRunResultsByTaskId.get(String(taskId));
    const taskRunId = requestedTaskRunId || remembered?.taskRunId || remembered?.id || "";
    if (!taskId && !taskRunId) {
      throw new Error("tasks.complete requires taskId or taskRunId");
    }
    const params = {
      taskId: taskId || "",
      taskRunId: taskRunId || "",
      reason: input.reason || "",
      completionId: input.completionId || "",
      leaseToken:
        input.leaseToken || remembered?.leaseToken ||
        pickEnv(this.env, "LOOM_TASK_RUN_LEASE_TOKEN") || pickEnv(this.env, "LOOM_RUNNER_LEASE_TOKEN"),
      logsRef: input.logsRef || remembered?.logsRef || "",
      artifactsRef: input.artifactsRef || remembered?.artifactsRef || "",
      artifactIds: stringList(input.artifactIds || remembered?.artifactIds),
    };
    return this.#httpCall("complete-task", params);
  }

  async releaseTask(input = {}) {
    const taskId = taskPayloadID(input);
    if (!taskId) {
      throw new Error("tasks.release requires taskId");
    }
    const params = { taskId: String(taskId), actor: input.actor || "" };
    return this.#httpCall("release-task", params);
  }

  #epicID(input) {
    return input.epicId || this.input.epicId || "";
  }

  #requireHttpConfig() {
    if (!this.driverRunId) {
      throw new Error("LOOM_DRIVER_RUN_ID is required");
    }
    if (!this.workspace) {
      throw new Error("LOOM_DRIVER_WORKSPACE is required for the driver HTTP API");
    }
    if (!this.apiUrl) {
      throw new Error("LOOM_DRIVER_API_URL is required for the driver HTTP API");
    }
  }

  #identityHeaders() {
    const headers = {
      "X-Loom-Driver-Run-Id": this.driverRunId,
    };
    setHeaderIfSet(headers, "X-Loom-Driver-Node-Id", pickEnv(this.env, "LOOM_DRIVER_NODE_ID"));
    setHeaderIfSet(headers, "X-Loom-Driver-Lease-Id", pickEnv(this.env, "LOOM_DRIVER_LEASE_ID"));
    setHeaderIfSet(headers, "X-Loom-Driver-Fencing-Token", pickEnv(this.env, "LOOM_DRIVER_FENCING_TOKEN"));
    if (this.apiToken) {
      headers.Authorization = "Bearer " + this.apiToken;
    }
    return headers;
  }

  async #httpCall(op, params, options = {}) {
    this.#requireHttpConfig();
    const url = `${this.apiUrl}/api/workspaces/${encodeURIComponent(this.workspace)}/driver/${op}`;
    const headers = this.#identityHeaders();
    headers["Content-Type"] = "application/json";
    const timeoutMs = options.timeoutMs === undefined ? DEFAULT_HTTP_TIMEOUT_MS : options.timeoutMs;
    const controller = timeoutMs > 0 ? new AbortController() : null;
    const timer = controller
      ? setTimeout(() => controller.abort(new DriverApiError(`driver op ${op} timed out after ${timeoutMs}ms`, { code: "timeout", retryable: true })), timeoutMs)
      : null;
    let response;
    let text;
    try {
      response = await fetch(url, {
        method: "POST",
        headers,
        body: JSON.stringify(compactParams(params)),
        signal: controller ? controller.signal : undefined,
      });
      text = await response.text();
    } catch (err) {
      if (err instanceof DriverApiError) {
        throw err;
      }
      if (controller && controller.signal.aborted && controller.signal.reason instanceof DriverApiError) {
        throw controller.signal.reason;
      }
      throw new DriverApiError(`driver op ${op} request failed: ${err?.message || err}`, { code: "unavailable", retryable: true });
    } finally {
      if (timer) {
        clearTimeout(timer);
      }
    }
    let parsed = null;
    if (text && text.trim() !== "") {
      try {
        parsed = JSON.parse(text);
      } catch (err) {
        if (response.ok) {
          throw new DriverApiError(`driver op ${op} returned invalid JSON: ${err.message}`, { code: "internal", status: response.status });
        }
      }
    }
    if (!response.ok) {
      const envelope = parsed && typeof parsed.error === "object" && parsed.error !== null ? parsed.error : null;
      const message = envelope?.message
        || (typeof parsed?.error === "string" ? parsed.error : "")
        || `driver op ${op} failed with HTTP ${response.status}`;
      throw new DriverApiError(message, {
        code: envelope?.code,
        retryable: envelope?.retryable,
        status: response.status,
        details: envelope?.details,
      });
    }
    return parsed;
  }
}

export function createLoomDriverClient(options = {}) {
  if (options && !("input" in options) && !("env" in options) && !("apiUrl" in options) && !("apiToken" in options)) {
    return FlueDriverClient.fromEnv({ input: options });
  }
  return FlueDriverClient.fromEnv(options);
}

export const createLoomClient = createLoomDriverClient;

// compactParams strips empty values so the wire payload only carries what the
// caller actually set; nested objects are compacted recursively and dropped
// when empty.
function compactParams(params) {
  const out = {};
  for (const [key, value] of Object.entries(params || {})) {
    if (value === undefined || value === null || value === "") {
      continue;
    }
    if (Array.isArray(value)) {
      if (value.length > 0) {
        out[key] = value;
      }
      continue;
    }
    if (typeof value === "object") {
      const nested = compactParams(value);
      if (Object.keys(nested).length > 0) {
        out[key] = nested;
      }
      continue;
    }
    out[key] = value;
  }
  return out;
}

function stringList(values) {
  const list = Array.isArray(values) ? values : values ? [values] : [];
  return list.map(String).filter((value) => value.trim() !== "");
}

function setHeaderIfSet(headers, name, value) {
  if (value) {
    headers[name] = value;
  }
}

function stripTrailingSlash(value) {
  return value.replace(/\/+$/, "");
}

function isTerminalTaskRunStatus(status) {
  switch (String(status || "")) {
    case "completed":
    case "failed":
    case "cancelled":
    case "needs_review":
      return true;
    default:
      return false;
  }
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

// watchHttpError maps a non-OK watch response onto DriverApiError using the
// structured {code, message, retryable} envelope when present. Without an
// envelope, 5xx/429 default to retryable so transient proxy errors reconnect.
async function watchHttpError(response) {
  let envelope = null;
  try {
    const parsed = JSON.parse(await response.text());
    if (parsed && typeof parsed.error === "object" && parsed.error !== null) {
      envelope = parsed.error;
    }
  } catch {
    // Non-JSON error bodies fall back to the status-based defaults.
  }
  const retryable = envelope
    ? Boolean(envelope.retryable)
    : response.status >= 500 || response.status === 429;
  return new DriverApiError(envelope?.message || `epic watch failed with HTTP ${response.status}`, {
    code: envelope?.code,
    retryable,
    status: response.status,
    details: envelope?.details,
  });
}

// sseFrames is a minimal SSE parser over a fetch body stream: frames are
// separated by blank lines; "event:", "id:", "data:" and "retry:" fields are
// recognized; comment lines (leading ":") are ignored. A trailing partial
// frame at stream end is dropped, per the SSE spec.
async function* sseFrames(body) {
  const decoder = new TextDecoder();
  let buffer = "";
  for await (const chunk of body) {
    buffer += decoder.decode(chunk, { stream: true });
    let boundary;
    while ((boundary = buffer.indexOf("\n\n")) !== -1) {
      const raw = buffer.slice(0, boundary);
      buffer = buffer.slice(boundary + 2);
      const frame = parseSSEFrame(raw);
      if (frame) {
        yield frame;
      }
    }
  }
}

function parseSSEFrame(raw) {
  const frame = { event: "", id: undefined, data: undefined, retryMs: undefined };
  let sawField = false;
  for (const rawLine of raw.split("\n")) {
    const line = rawLine.endsWith("\r") ? rawLine.slice(0, -1) : rawLine;
    if (line === "" || line.startsWith(":")) {
      continue;
    }
    const colon = line.indexOf(":");
    const field = colon === -1 ? line : line.slice(0, colon);
    let value = colon === -1 ? "" : line.slice(colon + 1);
    if (value.startsWith(" ")) {
      value = value.slice(1);
    }
    switch (field) {
      case "event":
        frame.event = value;
        sawField = true;
        break;
      case "id":
        frame.id = value;
        sawField = true;
        break;
      case "data":
        frame.data = frame.data === undefined ? value : frame.data + "\n" + value;
        sawField = true;
        break;
      case "retry": {
        const ms = Number(value);
        if (Number.isFinite(ms) && ms >= 0) {
          frame.retryMs = ms;
          sawField = true;
        }
        break;
      }
      default:
        break;
    }
  }
  return sawField ? frame : null;
}

function parseSSEData(text) {
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

// watchDelay sleeps for the reconnect backoff but resolves early when the
// caller's abort signal fires so iteration can end promptly.
function watchDelay(ms, signal) {
  if (signal?.aborted) {
    return Promise.resolve();
  }
  return new Promise((resolve) => {
    const timer = setTimeout(done, ms);
    function done() {
      clearTimeout(timer);
      signal?.removeEventListener("abort", done);
      resolve();
    }
    signal?.addEventListener("abort", done, { once: true });
  });
}

function rememberTaskRunResult(client, result = {}) {
  const runId = result.taskRunId || result.id || "";
  const taskId = result.taskId || "";
  if (runId) {
    client.taskRunResultsByRunId.set(String(runId), result);
  }
  if (taskId) {
    client.taskRunResultsByTaskId.set(String(taskId), result);
  }
}

function pickEnv(env, key) {
  return String(env?.[key] || "").trim();
}

function agentNameOf(input) {
  return input.agent || input.agentName || input.name || "";
}

function taskPayloadID(input) {
  if (typeof input === "string") {
    return input;
  }
  return input.taskId || input.id || "";
}
