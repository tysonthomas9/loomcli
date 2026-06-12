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
}

export declare function createLoomDriverClient(options?: FlueDriverClientOptions | Record<string, unknown>): FlueDriverClient;
export declare const createLoomClient: typeof createLoomDriverClient;
