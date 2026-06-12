export interface FlueDriverResult {
  status: "completed" | "failed" | "needs_review" | "cancelled" | string;
  summary?: string;
  errorClass?: string;
  taskRunId?: string;
  logsRef?: string;
  artifactsRef?: string;
  [key: string]: unknown;
}

export interface FlueTaskSelector {
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

export interface FlueTaskRunRequest {
  taskId?: string;
  taskRunId?: string;
  driverStepId?: string;
  providerProfile?: string;
  workerProfileId?: string;
  parentSessionId?: string;
  nodeId?: string;
  runnerId?: string;
  supportedProviders?: string[];
  capabilities?: string[];
  sandboxPlacement?: {
    provider?: string;
    sandboxId?: string;
    cwd?: string;
    repoRef?: string;
  };
  leaseToken?: string;
}

export interface FlueEpicInput {
  epicId?: string;
}

export interface FlueEpicWatchInput extends FlueEpicInput {
  /** Exclusive journal cursor: only events with Seq greater than this are yielded. */
  afterSeq?: number | string;
  /** Aborting the signal ends iteration without throwing. */
  signal?: AbortSignal;
  /** Reconnect backoff in milliseconds (default 2000); server `retry:` hints override it. */
  reconnectMs?: number;
}

export interface FlueEpicWatchEvent {
  type: "snapshot" | "taskRun" | "closed" | string;
  /** Last-Event-ID cursor for the frame (journal Seq as a string). */
  id: string;
  data: unknown;
}

export interface FlueAgentInput {
  agent?: string;
  agentName?: string;
  name?: string;
}

export interface FlueAgentParentUpdateInput extends FlueAgentInput {
  parent?: string;
  parentEpicId?: string;
  expectParent?: string;
}

export interface FlueAgentMessageInput extends FlueAgentInput {
  message?: string;
  text?: string;
  body?: string;
}

export interface FlueTaskRunActiveInput extends FlueEpicInput {
  limit?: number;
}

export interface FlueTaskRunRecoverStaleInput {
  staleBefore?: string;
  maxAgeSeconds?: number;
  errorClass?: string;
  errorMessage?: string;
}

export interface FlueTaskRunGetInput {
  taskRunId?: string;
  id?: string;
}

export interface FlueTaskRunAwaitInput extends FlueTaskRunGetInput {
  pollMs?: number;
  timeoutMs?: number;
}

/** camelCase freshness assertions for connector egress (CV9 wire shape). */
export interface FlueConnectorPreconditions {
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
export interface FlueConnectorCallInput extends FlueConnectorPreconditions {
  connectorId?: string;
  resource?: string;
  /** Explicit sequence override; omitted = auto-increment per action. */
  callSeq?: number;
  args?: Record<string, unknown>;
  preconditions?: FlueConnectorPreconditions;
  [key: string]: unknown;
}

/** Irreversible merge: expectedHeadSha is a first-class REQUIRED parameter. */
export interface FlueConnectorGitHubMergeInput extends FlueConnectorCallInput {
  expectedHeadSha: string;
}

/** Review post is gated by a pre-egress liveness read at expectedHeadSha. */
export interface FlueConnectorGitHubReviewInput extends FlueConnectorCallInput {
  expectedHeadSha: string;
}

export interface FlueConnectorDispatchInput extends FlueConnectorCallInput {
  /** Dotted connector action, e.g. "github.merge". */
  action: string;
}

export interface FlueConnectorCallResult {
  callId: string;
  decision: "granted" | string;
  status?: number;
  body?: Record<string, unknown>;
  [key: string]: unknown;
}

export interface FlueConnectorsNamespace {
  github: {
    merge(input: FlueConnectorGitHubMergeInput): Promise<FlueConnectorCallResult>;
    postReview(input: FlueConnectorGitHubReviewInput): Promise<FlueConnectorCallResult>;
    readPullRequest(input?: FlueConnectorCallInput): Promise<FlueConnectorCallResult>;
    listPulls(input?: FlueConnectorCallInput): Promise<FlueConnectorCallResult>;
    compare(input?: FlueConnectorCallInput): Promise<FlueConnectorCallResult>;
    postIssueComment(input?: FlueConnectorCallInput): Promise<FlueConnectorCallResult>;
  };
  slack: {
    post(input?: FlueConnectorCallInput): Promise<FlueConnectorCallResult>;
    readConversations(input?: FlueConnectorCallInput): Promise<FlueConnectorCallResult>;
  };
  datadog: {
    readMonitors(input?: FlueConnectorCallInput): Promise<FlueConnectorCallResult>;
    readAlert(input?: FlueConnectorCallInput): Promise<FlueConnectorCallResult>;
    declareIncident(input?: FlueConnectorCallInput): Promise<FlueConnectorCallResult>;
  };
  dispatch(input: FlueConnectorDispatchInput): Promise<FlueConnectorCallResult>;
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
export interface FlueAwaitEventInput {
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
export interface FlueAwaitWireEvent {
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
export interface FlueAwaitEventResult {
  status: "satisfied" | "timed_out";
  instanceKey: string;
  pattern: string;
  deadline: string;
  event: FlueAwaitWireEvent;
}

export interface FlueAwaitListResult {
  runId: string;
  awaits: Record<string, unknown>[];
}

export interface FlueWorkflowStartInput {
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

export interface FlueWorkflowStartResult {
  childRunId: string;
  workflowName: string;
  status: string;
  parentRunId: string;
}

export interface FlueWorkflowAwaitInput {
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

export interface FlueWorkflowAwaitResult extends FlueAwaitEventResult {
  /** The child's outcome at response time (fresher than event.payload). */
  child?: { runId: string; status: string; summary?: string; errorClass?: string };
}

/**
 * Suspend sentinel thrown by events.await / workflows.await when the server
 * parked the run. NOT a failure: let it propagate (the runner exits cleanly
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

export interface FlueDriverClientOptions {
  env?: NodeJS.ProcessEnv | Record<string, string | undefined>;
  input?: Record<string, unknown>;
  apiUrl?: string;
  apiToken?: string;
}

export declare class DriverApiError extends Error {
  readonly code: string;
  readonly retryable: boolean;
  readonly status: number;
  readonly details?: unknown;
  constructor(message: string, options?: { code?: string; retryable?: boolean; status?: number; details?: unknown });
}

export declare class FlueDriverClient {
  static fromEnv(options?: FlueDriverClientOptions): FlueDriverClient;
  constructor(options?: FlueDriverClientOptions);

  readonly input: Record<string, unknown>;
  readonly workspace: string;
  readonly driverRunId: string;
  readonly epics: {
    get(input?: FlueEpicInput): Promise<Record<string, unknown> | null>;
    snapshot(input?: FlueEpicInput): Promise<Record<string, unknown> | null>;
    watch(input?: FlueEpicWatchInput): AsyncGenerator<FlueEpicWatchEvent, void, undefined>;
  };
  readonly agents: {
    list(input?: Record<string, unknown>): Promise<Record<string, unknown>[] | null>;
    orchestrationSession(input?: FlueAgentInput): Promise<Record<string, unknown> | null>;
    updateParent(input?: FlueAgentParentUpdateInput): Promise<Record<string, unknown> | null>;
    deliverAssignment(input?: FlueAgentInput): Promise<Record<string, unknown> | null>;
    message(input?: FlueAgentMessageInput): Promise<Record<string, unknown> | null>;
  };
  readonly tasks: {
    claimReady(input?: FlueEpicInput): Promise<Record<string, unknown> | null>;
    complete(input?: FlueTaskSelector | string): Promise<Record<string, unknown> | null>;
    release(input?: FlueTaskSelector | string): Promise<Record<string, unknown> | null>;
  };
  readonly taskRuns: {
    request(input?: FlueTaskRunRequest): Promise<Record<string, unknown>>;
    get(input?: FlueTaskRunGetInput): Promise<Record<string, unknown> | null>;
    await(input?: FlueTaskRunAwaitInput): Promise<Record<string, unknown> | null>;
    active(input?: FlueTaskRunActiveInput): Promise<Record<string, unknown> | null>;
    recoverStale(input?: FlueTaskRunRecoverStaleInput): Promise<Record<string, unknown> | null>;
  };
  readonly connectors: FlueConnectorsNamespace;
  readonly events: {
    /** Throws WorkflowSuspended when the run parks; see FlueAwaitEventInput for the determinism and freshness rules. */
    await(input: FlueAwaitEventInput): Promise<FlueAwaitEventResult>;
    /** Re-entry context: the run's awaits in index order; consumes no await slot. */
    list(input?: Record<string, unknown>): Promise<FlueAwaitListResult>;
  };
  readonly workflows: {
    start(input: FlueWorkflowStartInput): Promise<FlueWorkflowStartResult>;
    /** Throws WorkflowSuspended when the run parks; shares the awaitIndex counter with events.await. */
    await(input: FlueWorkflowAwaitInput): Promise<FlueWorkflowAwaitResult>;
  };

  completed(input?: { summary?: string }): FlueDriverResult;
  failed(input?: { summary?: string; errorClass?: string }): FlueDriverResult;
  needsReview(input?: {
    summary?: string;
    errorClass?: string;
    taskRunId?: string;
    logsRef?: string;
    artifactsRef?: string;
  }): FlueDriverResult;
  claimReady(input?: FlueEpicInput): Promise<Record<string, unknown> | null>;
  getEpic(input?: FlueEpicInput): Promise<Record<string, unknown> | null>;
  epicSnapshot(input?: FlueEpicInput): Promise<Record<string, unknown> | null>;
  watchEpic(input?: FlueEpicWatchInput): AsyncGenerator<FlueEpicWatchEvent, void, undefined>;
  listAgents(input?: Record<string, unknown>): Promise<Record<string, unknown>[] | null>;
  agentOrchestrationSession(input?: FlueAgentInput): Promise<Record<string, unknown> | null>;
  updateAgentParent(input?: FlueAgentParentUpdateInput): Promise<Record<string, unknown> | null>;
  deliverLeadAssignment(input?: FlueAgentInput): Promise<Record<string, unknown> | null>;
  messageAgent(input?: FlueAgentMessageInput): Promise<Record<string, unknown> | null>;
  requestTaskRun(input?: FlueTaskRunRequest): Promise<Record<string, unknown>>;
  getTaskRun(input?: FlueTaskRunGetInput): Promise<Record<string, unknown> | null>;
  awaitTaskRun(input?: FlueTaskRunAwaitInput): Promise<Record<string, unknown> | null>;
  activeTaskRuns(input?: FlueTaskRunActiveInput): Promise<Record<string, unknown> | null>;
  recoverStaleTaskRuns(input?: FlueTaskRunRecoverStaleInput): Promise<Record<string, unknown> | null>;
  completeTask(input?: FlueTaskSelector | string): Promise<Record<string, unknown> | null>;
  releaseTask(input?: FlueTaskSelector | string): Promise<Record<string, unknown> | null>;
  /**
   * Generic connector egress; throws SYNCHRONOUSLY (DriverApiError
   * precondition_required, before any network call) when a registered
   * irreversible/precondition-gated action lacks its required precondition.
   */
  dispatchConnector(input?: FlueConnectorDispatchInput): Promise<FlueConnectorCallResult>;
  awaitEvent(input: FlueAwaitEventInput): Promise<FlueAwaitEventResult>;
  listAwaits(input?: Record<string, unknown>): Promise<FlueAwaitListResult>;
  startWorkflow(input: FlueWorkflowStartInput): Promise<FlueWorkflowStartResult>;
  awaitChildWorkflow(input: FlueWorkflowAwaitInput): Promise<FlueWorkflowAwaitResult>;
}

export declare function createLoomDriverClient(options?: FlueDriverClientOptions | Record<string, unknown>): FlueDriverClient;
export declare const createLoomClient: typeof createLoomDriverClient;
