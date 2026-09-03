/**
 * FROZEN (SDK v1 contract, sdk/api-surface.v1.json): terminal driver-result
 * statuses. The suspend path is NOT a result status — it is the
 * WorkflowSuspended sentinel (whose `result.status` is the distinct literal
 * "suspended_awaiting_event"). Extending this union is a breaking change.
 */
export type LoomDriverResultStatus = "completed" | "failed" | "needs_review" | "cancelled";

/**
 * FROZEN (SDK v1 contract): epic watch SSE frame types.
 * "snapshot" (handshake) -> "taskRun"* (journal) -> "closed" (terminal).
 */
export type LoomEpicWatchEventType = "snapshot" | "taskRun" | "closed";

/**
 * FROZEN (SDK v1 contract): resolved-await statuses returned to the caller.
 * The third wire status, "suspended", never reaches workflow code — the
 * client converts it into the thrown WorkflowSuspended sentinel.
 */
export type LoomAwaitStatus = "satisfied" | "timed_out";

/**
 * FROZEN (SDK v1 contract): every code the {code, message, retryable,
 * details?} error envelope can carry — server-emitted plus the client-side
 * transport codes (timeout/unavailable/internal) and synchronous refusals
 * (precondition_required, await_timeout_required). Removing or renaming a
 * code is a breaking change; additions require a manifest + minor bump.
 */
export type DriverApiErrorCode =
  // Client transport + generic server mapping.
  | "internal"
  | "invalid"
  | "timeout"
  | "canceled"
  | "unavailable"
  | "unknown_op"
  | "not_found"
  | "not_owner"
  | "conflict"
  | "unschedulable"
  | "invalid_transition"
  // Auth (token_expired is ALWAYS non-retryable: the token TTL is the
  // max-run-duration cap, so the run must end rather than retry).
  | "unauthenticated"
  | "identity_mismatch"
  | "token_expired"
  // Connector egress.
  | "grant_denied"
  | "precondition_required"
  | "stale_subject"
  | "rate_limited"
  | "upstream_error"
  // Awaits + composition.
  | "await_pattern_unscoped"
  | "await_timeout_required"
  | "await_instance_key_malformed"
  | "await_actor_forbidden"
  | "driver_run_already_resumed"
  | "composition_depth_exceeded";

export interface LoomDriverResult {
  status: LoomDriverResultStatus;
  summary?: string;
  errorClass?: string;
  taskRunId?: string;
  logsRef?: string;
  artifactsRef?: string;
  [key: string]: unknown;
}

export interface LoomTaskSelector {
  taskId?: string;
  id?: string;
  taskRunId?: string;
  reason?: string;
  completionId?: string;
  leaseToken?: string;
  logsRef?: string;
  artifactsRef?: string;
  artifactIds?: string[];
}

export interface LoomTaskRunRequest {
  taskId?: string;
  taskRunId?: string;
  driverStepId?: string;
  /**
   * User-authored runner name declared by the pinned driver version manifest.
   * This is the runtime strategy selector. It is distinct from runnerId, which
   * identifies the worker process that claims the TaskRun.
   */
  runner?: string;
  workerProfileId?: string;
  parentSessionId?: string;
  nodeId?: string;
  runnerId?: string;
  capabilities?: string[];
  leaseToken?: string;
  repoRef?: string;
  repo_ref?: string;
  sandboxPlacement?: {
    repoRef?: string;
    repo_ref?: string;
    [key: string]: unknown;
  };
  /**
   * Optional task-run payload (e.g. a review diff+rubric), persisted on the
   * run and delivered verbatim to the runner via LOOM_TASK_RUN_REQUEST_JSON.
   * Passed verbatim (not compacted).
   */
  input?: unknown;
}

export interface LoomEpicInput {
  epicId?: string;
}

export interface LoomEpicWatchInput extends LoomEpicInput {
  /** Exclusive journal cursor: only events with Seq greater than this are yielded. */
  afterSeq?: number | string;
  /** Aborting the signal ends iteration without throwing. */
  signal?: AbortSignal;
  /** Reconnect backoff in milliseconds (default 2000); server `retry:` hints override it. */
  reconnectMs?: number;
}

export interface LoomEpicWatchEvent {
  type: LoomEpicWatchEventType;
  /** Last-Event-ID cursor for the frame (journal Seq as a string). */
  id: string;
  data: unknown;
}

export interface LoomAgentInput {
  agent?: string;
  agentName?: string;
  name?: string;
}

export interface LoomAgentParentUpdateInput extends LoomAgentInput {
  parent?: string;
  parentEpicId?: string;
  expectParent?: string;
}

export interface LoomAgentMessageInput extends LoomAgentInput {
  message?: string;
  text?: string;
  body?: string;
}

export interface LoomTaskRunActiveInput extends LoomEpicInput {
  limit?: number;
}

export interface LoomTaskRunRecoverStaleInput {
  staleBefore?: string;
  maxAgeSeconds?: number;
  errorClass?: string;
  errorMessage?: string;
}

export interface LoomTaskRunGetInput {
  taskRunId?: string;
  id?: string;
}

export interface LoomTaskRunAwaitInput extends LoomTaskRunGetInput {
  pollMs?: number;
  timeoutMs?: number;
}

export interface LoomEvalPromptInput {
  promptVersion?: string;
  prompt_version?: string;
}

export interface LoomEvalSessionInput extends LoomEvalPromptInput {
  sessionId?: string;
  session_id?: string;
}

export interface LoomEvalMetricInput extends LoomEvalSessionInput {
  judgeSessionId?: string;
  judge_session_id?: string;
  status?: "done" | "failed" | string;
  errorClass?: string;
  error_class?: string;
  eval?: unknown;
}

export interface LoomEvalRejudgeInput {
  sessionId?: string;
  session_id?: string;
}

/** camelCase freshness assertions for connector egress (CV9 wire shape). */
export interface LoomConnectorPreconditions {
  expectedHeadSha?: string;
  expectedIssueRevision?: string;
  expectedMessageTs?: string;
  expectedMonitorRevision?: string;
}

/**
 * Flat input for loom.connectors.{source}.{method}: connectorId/resource/
 * callSeq address the dispatch envelope, expected* fields become
 * preconditions, every other key is a camelCase provider arg.
 */
export interface LoomConnectorCallInput extends LoomConnectorPreconditions {
  connectorId?: string;
  resource?: string;
  /** Explicit sequence override; omitted = auto-increment per action. */
  callSeq?: number;
  args?: Record<string, unknown>;
  preconditions?: LoomConnectorPreconditions;
  [key: string]: unknown;
}

/** Irreversible merge: expectedHeadSha is a first-class REQUIRED parameter. */
export interface LoomConnectorGitHubMergeInput extends LoomConnectorCallInput {
  expectedHeadSha: string;
}

/** Review post is gated by a pre-egress liveness read at expectedHeadSha. */
export interface LoomConnectorGitHubReviewInput extends LoomConnectorCallInput {
  expectedHeadSha: string;
}

export interface LoomConnectorDispatchInput extends LoomConnectorCallInput {
  /** Dotted connector action, e.g. "github.merge". */
  action: string;
}

export interface LoomConnectorCallResult {
  callId: string;
  /** FROZEN: a dispatch that returns (rather than throws) was granted. */
  decision: "granted";
  status?: number;
  body?: Record<string, unknown>;
  [key: string]: unknown;
}

export interface LoomConnectorsNamespace {
  github: {
    merge(input: LoomConnectorGitHubMergeInput): Promise<LoomConnectorCallResult>;
    postReview(input: LoomConnectorGitHubReviewInput): Promise<LoomConnectorCallResult>;
    readPullRequest(input?: LoomConnectorCallInput): Promise<LoomConnectorCallResult>;
    listPulls(input?: LoomConnectorCallInput): Promise<LoomConnectorCallResult>;
    compare(input?: LoomConnectorCallInput): Promise<LoomConnectorCallResult>;
    postIssueComment(input?: LoomConnectorCallInput): Promise<LoomConnectorCallResult>;
  };
  slack: {
    post(input?: LoomConnectorCallInput): Promise<LoomConnectorCallResult>;
    readConversations(input?: LoomConnectorCallInput): Promise<LoomConnectorCallResult>;
  };
  datadog: {
    readMonitors(input?: LoomConnectorCallInput): Promise<LoomConnectorCallResult>;
    readAlert(input?: LoomConnectorCallInput): Promise<LoomConnectorCallResult>;
    declareIncident(input?: LoomConnectorCallInput): Promise<LoomConnectorCallResult>;
  };
  dispatch(input: LoomConnectorDispatchInput): Promise<LoomConnectorCallResult>;
}

/**
 * Input for loom.events.await. timeoutMs (or its alias timeout) is REQUIRED
 * (RULE 5) — the call throws synchronously without it, before any network
 * I/O and before an awaitIndex is consumed.
 *
 * DETERMINISM (RULE 3): awaitIndex derives from CALL ORDER. Resume re-runs
 * the workflow from the top, so the nth events.await/workflows.await call
 * always maps to runId#await-{n} and replays its recorded event. Awaits
 * must NEVER be conditionally skipped or reordered across re-entries (same
 * rule as deterministic task run ids and connector callSeq).
 */
export interface LoomAwaitEventInput {
  /**
   * Fully rendered subject key, EXACT equality (no glob), e.g.
   * "approval:owner/repo#123@sha" or
   * "slack.thread_reply:C123/1718012345.0001".
   */
  pattern: string;
  /** Optional eligible-resolver allow-list (single actor or array). */
  actor?: string | string[];
  /**
   * REQUIRED await timeout in milliseconds (server-capped). On expiry the
   * run resumes with a synthetic timeout event and the await returns
   * status "timed_out" — it does not throw.
   */
  timeoutMs?: number;
  /** Alias for timeoutMs. */
  timeout?: number;
  /** Explicit 1-based ordinal; overrides without advancing the counter. */
  awaitIndex?: number;
}

/** The recorded resolving event, replayed inline on every re-entry. */
export interface LoomAwaitWireEvent {
  id: string;
  /** Size-capped resume payload persisted on the satisfied await row. */
  payload?: unknown;
  /** Verified resolving actor (absent on synthetic timeout events). */
  actor?: string;
  occurredAt: string;
}

/**
 * Resolved await. FRESHNESS (vet A2): state observed before the await may
 * be arbitrarily stale by the time it returns (a suspend can last days) —
 * re-run non-memoized freshness checks (e.g. the PR head sha) after every
 * await before acting on the event.
 */
export interface LoomAwaitEventResult {
  status: LoomAwaitStatus;
  instanceKey: string;
  pattern: string;
  deadline: string;
  event: LoomAwaitWireEvent;
}

export interface LoomAwaitListResult {
  runId: string;
  awaits: Record<string, unknown>[];
}

export interface LoomWorkflowStartInput {
  /** Registered workflow (driver) name; the active passed version is pinned. */
  workflow?: string;
  /** Alias for workflow. */
  workflowName?: string;
  /** Child input payload, passed verbatim (not compacted). */
  input?: unknown;
  /**
   * Explicit child identity key. Omitted = identity derives from the
   * per-process start counter (call order, RULE 3 determinism), so a
   * re-entered parent re-issuing the same start gets the same childRunId.
   */
  idempotencyKey?: string;
  /** Explicit 1-based start ordinal; overrides without advancing the counter. */
  startIndex?: number;
}

export interface LoomWorkflowStartResult {
  childRunId: string;
  workflowName: string;
  status: string;
  parentRunId: string;
}

export interface LoomWorkflowAwaitInput {
  childRunId?: string;
  /** Alias for childRunId. */
  runId?: string;
  /** REQUIRED (RULE 5), milliseconds. */
  timeoutMs?: number;
  /** Alias for timeoutMs. */
  timeout?: number;
  /** Explicit 1-based ordinal (shared counter with events.await). */
  awaitIndex?: number;
}

export interface LoomWorkflowAwaitResult extends LoomAwaitEventResult {
  /** The child's outcome at response time (fresher than event.payload). */
  child?: { runId: string; status: string; summary?: string; errorClass?: string };
}

/**
 * Suspend sentinel thrown by events.await / workflows.await when the server
 * suspended the run. NOT a failure: let it propagate (the runner exits cleanly
 * with a suspended completion shape and resume re-runs from the top), or
 * `return err.result`. Catch blocks around awaits MUST rethrow when
 * isWorkflowSuspended(err) is true.
 */
export declare class WorkflowSuspended extends Error {
  readonly type: "workflow_suspended";
  readonly awaitIndex: number;
  readonly result: { status: "suspended_awaiting_event"; summary: string };
  constructor(awaitIndex: number);
}

export declare function isWorkflowSuspended(err: unknown): boolean;

export interface LoomDriverClientOptions {
  env?: NodeJS.ProcessEnv | Record<string, string | undefined>;
  input?: Record<string, unknown>;
  apiUrl?: string;
  /**
   * Legacy shared static API token (LOOM_DRIVER_API_TOKEN). Used only on the
   * legacy header-quad transport; IGNORED whenever a run token is present.
   */
  apiToken?: string;
  /**
   * Run-scoped bearer token minted at claim. Precedence: this option, then
   * the LOOM_RUN_TOKEN env (injected by the executor). When set, every
   * request — JSON ops and the watch SSE stream — authenticates token-only:
   * `Authorization: Bearer <run token>` with NO X-Loom-Driver-* identity
   * headers and no apiToken; the server derives {run, node, lease, fence}
   * from the verified claims. When absent, the legacy transport (header quad
   * + optional static token) is unchanged. An expired token surfaces as
   * DriverApiError {code: "token_expired", retryable: false} — the token TTL
   * is the max-run-duration cap, so the run must end rather than retry.
   */
  runToken?: string;
}

export declare class DriverApiError extends Error {
  readonly code: DriverApiErrorCode;
  readonly retryable: boolean;
  readonly status: number;
  /** Optional machine-readable context from the error envelope (additive). */
  readonly details?: unknown;
  constructor(message: string, options?: { code?: DriverApiErrorCode; retryable?: boolean; status?: number; details?: unknown });
}

export declare class LoomDriverClient {
  static fromEnv(options?: LoomDriverClientOptions): LoomDriverClient;
  constructor(options?: LoomDriverClientOptions);

  readonly input: Record<string, unknown>;
  readonly workspace: string;
  readonly driverRunId: string;
  /** Run-scoped bearer token in effect ("" = legacy header-quad transport). */
  readonly runToken: string;
  readonly epics: {
    get(input?: LoomEpicInput): Promise<Record<string, unknown> | null>;
    snapshot(input?: LoomEpicInput): Promise<Record<string, unknown> | null>;
    watch(input?: LoomEpicWatchInput): AsyncGenerator<LoomEpicWatchEvent, void, undefined>;
  };
  readonly agents: {
    list(input?: Record<string, unknown>): Promise<Record<string, unknown>[] | null>;
    orchestrationSession(input?: LoomAgentInput): Promise<Record<string, unknown> | null>;
    updateParent(input?: LoomAgentParentUpdateInput): Promise<Record<string, unknown> | null>;
    deliverAssignment(input?: LoomAgentInput): Promise<Record<string, unknown> | null>;
    message(input?: LoomAgentMessageInput): Promise<Record<string, unknown> | null>;
  };
  readonly tasks: {
    claimReady(input?: LoomEpicInput): Promise<Record<string, unknown> | null>;
    complete(input?: LoomTaskSelector | string): Promise<Record<string, unknown> | null>;
    release(input?: LoomTaskSelector | string): Promise<Record<string, unknown> | null>;
  };
  readonly taskRuns: {
    request(input?: LoomTaskRunRequest): Promise<Record<string, unknown>>;
    get(input?: LoomTaskRunGetInput): Promise<Record<string, unknown> | null>;
    /**
     * CLIENT-SIDE POLLING (kept for compat): repeatedly calls taskRuns.get
     * until the run reaches a terminal status. Prefer epics.watch — the
     * server-push SSE stream — for reacting to task-run progress.
     */
    await(input?: LoomTaskRunAwaitInput): Promise<Record<string, unknown> | null>;
    active(input?: LoomTaskRunActiveInput): Promise<Record<string, unknown> | null>;
    recoverStale(input?: LoomTaskRunRecoverStaleInput): Promise<Record<string, unknown> | null>;
  };
  readonly evals: {
    listUnevaluated(input?: LoomEvalPromptInput): Promise<Record<string, unknown> | null>;
    getTranscript(input?: LoomEvalSessionInput): Promise<Record<string, unknown> | null>;
    putMetric(input?: LoomEvalMetricInput): Promise<Record<string, unknown> | null>;
    rejudge(input?: LoomEvalRejudgeInput): Promise<Record<string, unknown> | null>;
  };
  readonly connectors: LoomConnectorsNamespace;
  readonly events: {
    /** Throws WorkflowSuspended when the run suspends; see LoomAwaitEventInput for the determinism and freshness rules. */
    await(input: LoomAwaitEventInput): Promise<LoomAwaitEventResult>;
    /** Re-entry context: the run's awaits in index order; consumes no await slot. */
    list(input?: Record<string, unknown>): Promise<LoomAwaitListResult>;
  };
  readonly workflows: {
    start(input: LoomWorkflowStartInput): Promise<LoomWorkflowStartResult>;
    /** Throws WorkflowSuspended when the run suspends; shares the awaitIndex counter with events.await. */
    await(input: LoomWorkflowAwaitInput): Promise<LoomWorkflowAwaitResult>;
  };

  completed(input?: { summary?: string }): LoomDriverResult;
  failed(input?: { summary?: string; errorClass?: string }): LoomDriverResult;
  needsReview(input?: {
    summary?: string;
    errorClass?: string;
    taskRunId?: string;
    logsRef?: string;
    artifactsRef?: string;
  }): LoomDriverResult;
  claimReady(input?: LoomEpicInput): Promise<Record<string, unknown> | null>;
  getEpic(input?: LoomEpicInput): Promise<Record<string, unknown> | null>;
  epicSnapshot(input?: LoomEpicInput): Promise<Record<string, unknown> | null>;
  watchEpic(input?: LoomEpicWatchInput): AsyncGenerator<LoomEpicWatchEvent, void, undefined>;
  listAgents(input?: Record<string, unknown>): Promise<Record<string, unknown>[] | null>;
  agentOrchestrationSession(input?: LoomAgentInput): Promise<Record<string, unknown> | null>;
  updateAgentParent(input?: LoomAgentParentUpdateInput): Promise<Record<string, unknown> | null>;
  deliverLeadAssignment(input?: LoomAgentInput): Promise<Record<string, unknown> | null>;
  messageAgent(input?: LoomAgentMessageInput): Promise<Record<string, unknown> | null>;
  requestTaskRun(input?: LoomTaskRunRequest): Promise<Record<string, unknown>>;
  getTaskRun(input?: LoomTaskRunGetInput): Promise<Record<string, unknown> | null>;
  /** Client-side polling loop (see taskRuns.await); epics.watch is the push alternative. */
  awaitTaskRun(input?: LoomTaskRunAwaitInput): Promise<Record<string, unknown> | null>;
  activeTaskRuns(input?: LoomTaskRunActiveInput): Promise<Record<string, unknown> | null>;
  recoverStaleTaskRuns(input?: LoomTaskRunRecoverStaleInput): Promise<Record<string, unknown> | null>;
  listUnevaluatedSessions(input?: LoomEvalPromptInput): Promise<Record<string, unknown> | null>;
  getSessionTranscript(input?: LoomEvalSessionInput): Promise<Record<string, unknown> | null>;
  putEvalMetric(input?: LoomEvalMetricInput): Promise<Record<string, unknown> | null>;
  rejudgeSession(input?: LoomEvalRejudgeInput): Promise<Record<string, unknown> | null>;
  completeTask(input?: LoomTaskSelector | string): Promise<Record<string, unknown> | null>;
  releaseTask(input?: LoomTaskSelector | string): Promise<Record<string, unknown> | null>;
  /**
   * Generic connector egress; throws SYNCHRONOUSLY (DriverApiError
   * precondition_required, before any network call) when a registered
   * irreversible/precondition-gated action lacks its required precondition.
   */
  dispatchConnector(input?: LoomConnectorDispatchInput): Promise<LoomConnectorCallResult>;
  awaitEvent(input: LoomAwaitEventInput): Promise<LoomAwaitEventResult>;
  listAwaits(input?: Record<string, unknown>): Promise<LoomAwaitListResult>;
  startWorkflow(input: LoomWorkflowStartInput): Promise<LoomWorkflowStartResult>;
  awaitChildWorkflow(input: LoomWorkflowAwaitInput): Promise<LoomWorkflowAwaitResult>;
}

export declare function createLoomDriverClient(options?: LoomDriverClientOptions | Record<string, unknown>): LoomDriverClient;
export declare const createLoomClient: typeof createLoomDriverClient;
