export declare const RunnerEnv: Readonly<{
  apiUrl: "LOOM_TASK_RUN_API_URL";
  baseUrl: "LOOM_FLEET_DB_URL";
  apiKey: "LOOM_FLEET_DB_API_KEY";
  actor: "LOOM_FLEET_DB_ACTOR";
  agentName: "LOOM_AGENT_NAME";
  workspace: "LOOM_WORKSPACE";
  taskRunId: "LOOM_TASK_RUN_ID";
  taskId: "LOOM_TASK_ID";
  nodeId: "LOOM_TASK_RUN_NODE_ID";
  leaseId: "LOOM_TASK_RUN_LEASE_ID";
  leaseToken: "LOOM_TASK_RUN_LEASE_TOKEN";
  runnerLeaseToken: "LOOM_RUNNER_LEASE_TOKEN";
  fencingToken: "LOOM_TASK_RUN_FENCING_TOKEN";
  requestJson: "LOOM_TASK_RUN_REQUEST_JSON";
}>;

/**
 * Request/upload body accepted by fetch on Node >= 18. Local alias because
 * DOM's BodyInit is NOT declared under node-only TypeScript configs (the
 * typecheck gate compiles with `types: ["node"]`, matching how workflow
 * consumers build).
 */
export type RunnerBodyInit =
  | string
  | Uint8Array
  | ArrayBuffer
  | Blob
  | FormData
  | URLSearchParams
  | ReadableStream;

export type FetchLike = (input: string, init?: {
  method?: string;
  headers?: Record<string, string>;
  body?: RunnerBodyInit;
  signal?: AbortSignal;
}) => Promise<Response>;

export interface TaskRunClientOptions {
  /**
   * loom serve task-run API base URL (LOOM_TASK_RUN_API_URL). When set, all
   * operations use the serve transport authenticated by the per-task-run
   * lease token alone; baseUrl/apiKey (direct fleet-db) are not required.
   */
  apiUrl?: string;
  baseUrl?: string;
  apiKey?: string;
  actor?: string;
  authToken?: string;
  fetch?: FetchLike;
  workspace?: string;
  taskRunId?: string;
  taskId?: string;
  nodeId?: string;
  leaseId?: string;
  leaseToken?: string;
  fencingToken?: string | number;
  requestJson?: string;
}

export interface TaskRun {
  workspace_key?: string;
  task_run_id: string;
  driver_run_id?: string;
  driver_step_id?: string;
  task_id: string;
  worker_profile_id?: string;
  runner?: string;
  runner_ref?: string;
  runner_kind?: string;
  runner_entrypoint?: string;
  runner_driver_version_id?: string;
  provider_profile?: string;
  status: string;
  node_id?: string;
  lease_id?: string;
  fencing_token?: number;
  runner_placement?: TaskRunPlacement;
  sandbox_placement?: TaskRunPlacement;
  [key: string]: unknown;
}

export interface TaskRunPlacement {
  provider?: string;
  node_id?: string;
  runner_id?: string;
  process_ref?: string;
  sandbox_id?: string;
  image_or_snapshot?: string;
  cwd?: string;
  repo_ref?: string;
  cleanup_policy?: string;
  started_at?: string;
  heartbeat_at?: string;
  retained_until?: string | null;
}

export interface Issue {
  id: string;
  title?: string;
  status?: string;
  type?: string;
  task_run?: TaskRun;
  taskRun?: TaskRun;
  [key: string]: unknown;
}

export interface LogAppendInput {
  stream?: "stdout" | "stderr" | string;
  text: string;
  timestamp?: string | Date;
}

export interface ArtifactDeclareInput {
  id?: string;
  artifactId?: string;
  artifact_id?: string;
  type: string;
  taskId?: string;
  task_id?: string;
  uri?: string;
  summary?: string;
  mimeType?: string;
  mime_type?: string;
  sizeBytes?: number;
  size_bytes?: number;
  checksum?: string;
  contentHash?: string;
  content_hash?: string;
  visibility?: string;
  redactionStatus?: string;
  redaction_status?: string;
  durableStatus?: string;
  durable_status?: string;
  idempotencyKey?: string;
  idempotency_key?: string;
  metadata?: Record<string, string | number | boolean>;
}

export interface ArtifactFinalizeInput {
  uri?: string;
  summary?: string;
  mimeType?: string;
  mime_type?: string;
  sizeBytes?: number;
  size_bytes?: number;
  checksum?: string;
  contentHash?: string;
  content_hash?: string;
  visibility?: string;
  redactionStatus?: string;
  redaction_status?: string;
  metadata?: Record<string, string | number | boolean>;
}

export interface ArtifactUploadOptions {
  mimeType?: string;
  contentType?: string;
  signal?: AbortSignal;
}

export interface Artifact {
  workspace_key?: string;
  artifact_id: string;
  owner_type?: string;
  owner_id?: string;
  task_id?: string;
  type: string;
  durable_status?: string;
  uri?: string;
  checksum?: string;
  content_hash?: string;
  [key: string]: unknown;
}

export interface CompleteRunInput {
  completionId?: string;
  completion_id?: string;
  status?: "completed" | "failed" | "cancelled" | string;
  exitCode?: number;
  exit_code?: number;
  logsRef?: string;
  logs_ref?: string;
  artifactsRef?: string;
  artifacts_ref?: string;
  artifactIds?: string[];
  artifact_ids?: string[];
  requiredArtifactIDs?: string[];
  required_artifact_ids?: string[];
  requireArtifacts?: boolean;
  require_artifacts?: boolean;
  inputTokens?: number;
  input_tokens?: number;
  outputTokens?: number;
  output_tokens?: number;
  cacheReadTokens?: number;
  cache_read_tokens?: number;
  cacheWriteTokens?: number;
  cache_write_tokens?: number;
  estimatedCostUsd?: number;
  estimated_cost_usd?: number;
  runtimeMetadata?: Record<string, string | number | boolean>;
  runtime_metadata?: Record<string, string | number | boolean>;
  errorClass?: string;
  error_class?: string;
  errorMessage?: string;
  error_message?: string;
  closeTask?: boolean;
  close_task?: boolean;
  closeReason?: string;
  close_reason?: string;
  taskStatusPolicy?: {
    action?: "close" | "leave_open" | string;
    reason?: string;
  };
  task_status_policy?: {
    action?: "close" | "leave_open" | string;
    reason?: string;
  };
}

export interface CompleteRunResponse {
  completion?: Record<string, unknown>;
  task_run?: TaskRun;
  taskRun?: TaskRun;
  [key: string]: unknown;
}

export interface RuntimeCredentialResponse {
  provider: "daytona" | "github" | string;
  value: string;
  [key: string]: unknown;
}

export type AgentSessionKind = "task" | "judge" | "orchestration" | "terminal" | "maintenance" | "ad_hoc" | string;

export interface AgentSessionOpenInput {
  invocationKey: string;
  backend: string;
  /** Omit only when the backend truthfully has no model label; SDK sends "unknown". */
  model?: string;
  parentSessionId?: string;
  kind?: AgentSessionKind;
  tags?: string[];
  metadata?: Record<string, string | number | boolean>;
}

export interface AgentSessionOpenResult {
  sessionId: string;
  attempt: number;
}

export interface AgentSessionUsage {
  tokens?: number | null;
  cost?: number | null;
}

export interface AgentSessionCloseInput {
  sessionId: string;
  status: "completed" | "failed" | "cancelled" | string;
  exitCode?: number | null;
  summary?: string;
  usage?: AgentSessionUsage | null;
  transcriptRef?: string | null;
  /** Set driver_runner_session_id here when the backend exposes its native session id. */
  metadata?: Record<string, string | number | boolean>;
}

export interface AgentSessionCloseResult {
  sessionId: string;
  status: string;
}

export interface AgentExecSpec {
  invocationKey: string;
  backend: string;
  model?: string;
  parentSessionId?: string;
  kind?: AgentSessionKind;
  tags?: string[];
  metadata?: Record<string, string | number | boolean>;
  /** The process form is deliberately required and disjoint from future invoke. */
  argv: string[];
  cwd?: string;
  env?: Record<string, string | undefined>;
  stdin?: string | Uint8Array;
  timeoutMs?: number;
  live?: boolean;
  transcript?: "stream-json" | "minimal" | "none";
  /** Explicit values redacted from transcript entries/upload only; returned stdout/stderr stay raw. The SDK never guesses secrets from env. */
  redactSecrets?: string[] | Record<string, string>;
  /** Retries after the first open attempt; default 2. */
  openRetries?: number;
  close?: "auto" | "deferred";
}

export interface AgentExecSession {
  id: string | null;
  attempt: number | null;
  transcriptRef: string | null;
  opened: boolean;
  closed: boolean;
  degraded: boolean;
  /** Machine-readable first observability failure code, or null when healthy. */
  degradedReason: string | null;
}

export interface AgentExecFinalizeInput {
  status?: "completed" | "failed" | "cancelled" | string;
  summary?: string;
  metadata?: Record<string, string | number | boolean>;
}

export interface AgentExecResult {
  exitCode: number | null;
  timedOut: boolean;
  spawnError: string | null;
  /** Raw process stdout; redactSecrets does not alter this leaf-facing value. */
  stdout: string;
  /** Raw process stderr; redactSecrets does not alter this leaf-facing value. */
  stderr: string;
  durationMs: number;
  entries: Record<string, unknown>[];
  usage: AgentSessionUsage | null;
  session: AgentExecSession;
  /** Merge this into the leaf's TaskRun runtimeMetadata on completion. */
  runtimeMetadata: Record<string, string>;
  finalize?: (input?: AgentExecFinalizeInput) => Promise<{ ok: boolean }>;
}

export declare class LoomAPIError extends Error {
  status: number;
  code: string;
  retryable: boolean;
  details?: unknown;
  responseBody: string;
}

/** Thrown only for invalid agent.exec caller input, never process failures. */
export declare class AgentExecSpecError extends Error {}

export declare class TaskRunClient {
  static fromEnv(env?: NodeJS.ProcessEnv | Record<string, string | undefined>, options?: TaskRunClientOptions): TaskRunClient;

  readonly apiUrl: string;
  readonly serveMode: boolean;
  readonly baseUrl: string;
  readonly workspace: string;
  readonly taskRunId: string;
  readonly taskId: string;
  readonly nodeId: string;
  readonly leaseId: string;
  readonly leaseToken: string;
  readonly fencingToken: number | string;
  readonly requestJson: string;
  readonly logs: {
    append(input: LogAppendInput, options?: { signal?: AbortSignal }): Promise<Record<string, unknown>>;
  };
  readonly artifacts: {
    declare(input: ArtifactDeclareInput, options?: { signal?: AbortSignal }): Promise<ArtifactHandle>;
    get(artifactId: string, options?: { signal?: AbortSignal }): Promise<ArtifactHandle>;
    list(input?: { type?: string; durableStatus?: string; durable_status?: string; status?: string; limit?: number }, options?: { signal?: AbortSignal }): Promise<{ artifacts: ArtifactHandle[]; count?: number; [key: string]: unknown }>;
  };
  readonly runtimeCredentials: {
    get(input: { provider: "daytona" | "github" | string }, options?: { signal?: AbortSignal }): Promise<RuntimeCredentialResponse>;
  };
  /**
   * Agent Invocation helpers. agent.exec opens sessions only for AGENT
   * invocations; never use it to capture deterministic commands.
   */
  readonly agent: {
    exec(spec: AgentExecSpec): Promise<AgentExecResult>;
  };

  constructor(options: TaskRunClientOptions);
  request<T = Record<string, unknown>>(): T;
  input<T = unknown>(): T | undefined;
  getTaskRun(options?: { signal?: AbortSignal }): Promise<TaskRun>;
  getTask(options?: { signal?: AbortSignal }): Promise<Issue | { taskRun: TaskRun }>;
  heartbeat(input?: { runtimeMetadata?: Record<string, string | number | boolean>; runtime_metadata?: Record<string, string | number | boolean>; logsRef?: string; logs_ref?: string; artifactsRef?: string; artifacts_ref?: string }, options?: { signal?: AbortSignal }): Promise<TaskRun>;
  appendLog(input: LogAppendInput, options?: { signal?: AbortSignal }): Promise<Record<string, unknown>>;
  declareArtifact(input: ArtifactDeclareInput, options?: { signal?: AbortSignal }): Promise<ArtifactHandle>;
  getArtifact(artifactId: string, options?: { signal?: AbortSignal }): Promise<ArtifactHandle>;
  listArtifacts(input?: { type?: string; durableStatus?: string; durable_status?: string; status?: string; limit?: number }, options?: { signal?: AbortSignal }): Promise<{ artifacts: ArtifactHandle[]; count?: number; [key: string]: unknown }>;
  uploadArtifactContent(artifactId: string, content: RunnerBodyInit, options?: ArtifactUploadOptions): Promise<Artifact>;
  finalizeArtifact(artifactId: string, input?: ArtifactFinalizeInput, options?: { signal?: AbortSignal }): Promise<Artifact>;
  getRuntimeCredential(input: { provider: "daytona" | "github" | string }, options?: { signal?: AbortSignal }): Promise<RuntimeCredentialResponse>;
  sessionOpen(input: AgentSessionOpenInput, options?: { signal?: AbortSignal }): Promise<AgentSessionOpenResult>;
  sessionClose(input: AgentSessionCloseInput, options?: { signal?: AbortSignal }): Promise<AgentSessionCloseResult>;
  /** Non-bridge topologies only; bridge task-plane leaves return an IPC result instead. */
  completeRun(input?: CompleteRunInput, options?: { signal?: AbortSignal }): Promise<CompleteRunResponse>;
}

export declare class ArtifactHandle {
  readonly client: TaskRunClient;
  artifact: Artifact;
  id: string;

  constructor(client: TaskRunClient, artifact: Artifact);
  upload(content: RunnerBodyInit, options?: ArtifactUploadOptions): Promise<this>;
  finalize(input?: ArtifactFinalizeInput, options?: { signal?: AbortSignal }): Promise<this>;
  toJSON(): Artifact;
}
