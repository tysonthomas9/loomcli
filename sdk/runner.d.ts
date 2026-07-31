export declare const RunnerEnv: Readonly<{
  apiUrl: "LOOM_TASK_RUN_API_URL";
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

export declare const DaytonaProviderSchemaV1: "daytona-task-run-execution.v1";

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
   * Required loom serve task-run API base URL (LOOM_TASK_RUN_API_URL). All
   * operations use this facade with the per-task-run lease token; direct
   * FleetDB transport is intentionally unsupported.
   */
  apiUrl?: string;
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

export type LogAppendInput = {
  stream?: "stdout" | "stderr" | string;
  text: string;
  /**
   * Immutable timestamp for this logical append. Reuse it with the same
   * request identity after a timeout or lost response.
   */
  timestamp: string | Date;
} & (
  | { requestId: string; request_id?: never }
  | { request_id: string; requestId?: never }
);

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

export interface DaytonaProviderDelivery {
  openPullRequest: boolean;
  baseBranch?: string;
  outputBranch?: string;
  draft?: boolean;
}

export interface DaytonaProviderIntent {
  schemaVersion?: typeof DaytonaProviderSchemaV1;
  repositoryUrl: string;
  baseRef?: string;
  taskPrompt: string;
  backend: "codex";
  model?: string;
  mode?: string;
  delivery: DaytonaProviderDelivery;
}

export interface DaytonaTranscriptEntry {
  sequence: number;
  timestamp: string;
  role: "user" | "assistant" | "tool" | "system" | string;
  type: "text" | "tool_use" | "tool_result" | "session_meta" | string;
  text?: string;
  toolName?: string;
  toolUseId?: string;
  output?: string;
  uuid?: string;
}

export interface DaytonaProviderUsage {
  inputTokens?: number;
  outputTokens?: number;
  cacheReadTokens?: number;
  cacheWriteTokens?: number;
  estimatedCostUsd?: number;
}

export interface DaytonaSandboxReceipt {
  provider: "daytona";
  id?: string;
  workDir?: string;
  cwd?: string;
  repoRef?: string;
}

export interface DaytonaPatchReceipt {
  content: string;
  diffStat?: string;
  baseRef?: string;
  headSha?: string;
}

export interface DaytonaPullRequestReceipt {
  url: string;
  number: number;
  baseBranch: string;
  headBranch: string;
  commitSha: string;
}

export interface DaytonaProviderResult {
  schemaVersion: typeof DaytonaProviderSchemaV1;
  status: "completed" | "failed" | "cancelled";
  exitCode: number;
  errorClass?: string;
  errorMessage?: string;
  logs?: string;
  transcript?: string;
  transcriptEntries?: DaytonaTranscriptEntry[];
  usage: DaytonaProviderUsage;
  sandbox: DaytonaSandboxReceipt;
  patch?: DaytonaPatchReceipt;
  pullRequest?: DaytonaPullRequestReceipt;
}

export declare class LoomAPIError extends Error {
  status: number;
  code: string;
  details?: unknown;
  responseBody: string;
}

export declare class TaskRunClient {
  static fromEnv(env?: NodeJS.ProcessEnv | Record<string, string | undefined>, options?: TaskRunClientOptions): TaskRunClient;

  readonly apiUrl: string;
  /** @deprecated Always true; the direct FleetDB transport was removed. */
  readonly serveMode: true;
  /** @deprecated Alias of apiUrl retained for source compatibility. */
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
  readonly daytona: {
    execute(input: DaytonaProviderIntent, options?: { signal?: AbortSignal }): Promise<DaytonaProviderResult>;
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
  executeDaytona(input: DaytonaProviderIntent, options?: { signal?: AbortSignal }): Promise<DaytonaProviderResult>;
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
