import createClient, { type Client } from "openapi-fetch";
import type { components, paths } from "./generated/openapi.js";
import { bootstrapFromEnv, type TaskRunBootstrap } from "./bootstrap.js";

/** A task as returned by the control plane (title, description, design, AC, …). */
export type Task = components["schemas"]["IssueResponse"];

/** Thrown when a control-plane call returns a non-2xx response. */
export class TaskRunError extends Error {
  constructor(
    readonly op: string,
    readonly status: number | undefined,
    readonly body: unknown,
  ) {
    super(`loom ${op}: request failed${status ? ` (HTTP ${status})` : ""}`);
    this.name = "TaskRunError";
  }
}

/** Thrown when the runner's lease/fencing token is stale (server returns 409). */
export class FencedError extends TaskRunError {
  constructor(op: string, body: unknown) {
    super(op, 409, body);
    this.name = "FencedError";
  }
}

/** Thrown by methods whose server endpoint is not built yet (Phase C/D). */
export class NotImplementedError extends Error {
  constructor(method: string, detail: string) {
    super(`loom ${method}: not available yet — ${detail}`);
    this.name = "NotImplementedError";
  }
}

/**
 * TaskRunClient is the typed facade a flue runner uses to talk to the loom
 * control plane (`loom serve`). It is constructed from the scoped bootstrap loom
 * injects; the runner pulls its task and reports results without the `loom` CLI.
 * See docs/product/loom-typescript-sdk-spec.md.
 *
 * Phase B (read path) — getTask().
 * Phase B/C control — comment(), updateStatus(), complete(), block().
 * Phase C/D (server endpoints pending) — postArtifact(), recordUsage(),
 *   appendLog(), heartbeat() throw NotImplementedError until the loom-serve
 *   write surface + scoped-token/fencing auth land.
 */
export class TaskRunClient {
  private constructor(
    readonly bootstrap: TaskRunBootstrap,
    private readonly http: Client<paths>,
  ) {}

  /** Construct from the scoped capability loom injects. */
  static fromBootstrap(bootstrap: TaskRunBootstrap): TaskRunClient {
    const headers: Record<string, string> = {};
    if (bootstrap.token) headers["Authorization"] = `Bearer ${bootstrap.token}`;
    // Fencing token rides every request so the server can reject stale writers.
    if (bootstrap.fencingToken) headers["X-Loom-Fencing-Token"] = bootstrap.fencingToken;
    // fleetdb dev-mode auth compatibility (local only).
    if (bootstrap.actor) headers["X-Actor"] = bootstrap.actor;
    const http = createClient<paths>({ baseUrl: bootstrap.serverUrl, headers });
    return new TaskRunClient(bootstrap, http);
  }

  /** Construct from environment variables (the common runner entry point). */
  static fromEnv(env?: Record<string, string | undefined>): TaskRunClient {
    return TaskRunClient.fromBootstrap(bootstrapFromEnv(env));
  }

  private get path() {
    return { ws: this.bootstrap.workspace, id: this.bootstrap.taskId };
  }

  /** Phase B: fetch the task's title/description/design/acceptance criteria. */
  async getTask(): Promise<Task> {
    const { data, error, response } = await this.http.GET(
      "/api/workspaces/{ws}/issues/{id}",
      { params: { path: this.path } },
    );
    if (error || !data) throw this.toError("getTask", response, error);
    return data.data;
  }

  /** Post a comment (progress note / human-visible status) on the task. */
  async comment(text: string): Promise<void> {
    const { error, response } = await this.http.POST(
      "/api/workspaces/{ws}/issues/{id}/comments",
      { params: { path: this.path }, body: { text } },
    );
    if (error) throw this.toError("comment", response, error);
  }

  /** Set the task status (e.g. "review", "in_progress", "blocked"). */
  async updateStatus(
    status: NonNullable<components["schemas"]["PatchIssueRequest"]["status"]>,
  ): Promise<void> {
    const { error, response } = await this.http.PATCH(
      "/api/workspaces/{ws}/issues/{id}",
      { params: { path: this.path }, body: { status } },
    );
    if (error) throw this.toError("updateStatus", response, error);
  }

  /** Mark the task blocked, recording why (used when the run cannot proceed). */
  async block(reason: string): Promise<void> {
    await this.comment(`blocked: ${reason}`);
    await this.updateStatus("blocked");
  }

  /** Complete (close) the task. Mirrors what a local agent does at end of run. */
  async complete(opts: { reason?: string } = {}): Promise<void> {
    const { error, response } = await this.http.POST(
      "/api/workspaces/{ws}/issues/{id}/close",
      {
        params: { path: this.path },
        body: { reason: opts.reason, suggest_next: false },
      },
    );
    if (error) throw this.toError("complete", response, error);
  }

  /** Record a non-fatal failure on the task (no dedicated endpoint yet). */
  async fail(opts: { errorClass?: string; reason?: string } = {}): Promise<void> {
    const tag = opts.errorClass ? ` [${opts.errorClass}]` : "";
    await this.comment(`run failed${tag}: ${opts.reason ?? "see logs"}`);
  }

  // ── Phase C/D: pending loom-serve write surface + scoped-token/fencing auth ──

  /** Register a result artifact (commit/patch/log) on the TaskRun. */
  async postArtifact(_artifact: ArtifactInput): Promise<never> {
    throw new NotImplementedError(
      "postArtifact",
      "needs a loom-serve artifact endpoint on the AgentSession/TaskRun (PRD Phase D)",
    );
  }

  /** Stream token usage to the control plane. */
  async recordUsage(_usage: UsageInput): Promise<never> {
    throw new NotImplementedError(
      "recordUsage",
      "needs a loom-serve usage endpoint (PRD Phase C)",
    );
  }

  /** Append a log line to the TaskRun's server-visible log. */
  async appendLog(_log: LogInput): Promise<never> {
    throw new NotImplementedError(
      "appendLog",
      "needs a loom-serve log-append endpoint (PRD Phase C)",
    );
  }

  /** Keep the lease alive (refreshes the scoped token TTL). */
  async heartbeat(): Promise<never> {
    throw new NotImplementedError(
      "heartbeat",
      "needs a loom-serve session-heartbeat endpoint + lease/fencing (PRD Phase C)",
    );
  }

  private toError(
    op: string,
    response: { status?: number } | undefined,
    body: unknown,
  ): TaskRunError {
    if (response?.status === 409) return new FencedError(op, body);
    return new TaskRunError(op, response?.status, body);
  }
}

export interface ArtifactInput {
  type: "patch" | "commit" | "log" | "test" | string;
  uri: string;
  summary?: string;
  filesChanged?: number;
}

export interface UsageInput {
  inputTokens: number;
  outputTokens: number;
  cacheReadTokens?: number;
  cacheWriteTokens?: number;
}

export interface LogInput {
  stream: "stdout" | "stderr";
  text: string;
}
