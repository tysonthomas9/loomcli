export interface FlueDriverResult {
  status: "completed" | "failed" | "needs_human" | "cancelled" | string;
  summary?: string;
  errorClass?: string;
  taskRunId?: string;
  logsRef?: string;
  artifactsRef?: string;
  [key: string]: unknown;
}

export interface FlueTaskSelector {
  taskId?: string;
  task_id?: string;
  id?: string;
  taskRunId?: string;
  task_run_id?: string;
  reason?: string;
  completionId?: string;
  completion_id?: string;
  leaseToken?: string;
  lease_token?: string;
  logsRef?: string;
  logs_ref?: string;
  artifactsRef?: string;
  artifacts_ref?: string;
  artifactIds?: string[];
  artifact_ids?: string[];
  session?: string;
  force?: boolean;
}

export interface FlueTaskRunRequest {
  taskId?: string;
  task_id?: string;
  taskRunId?: string;
  task_run_id?: string;
  providerProfile?: string;
  provider_profile?: string;
  workerProfileId?: string;
  worker_profile_id?: string;
  nodeId?: string;
  node_id?: string;
  runnerId?: string;
  runner_id?: string;
  supportedProviders?: string[];
  supported_providers?: string[];
  capabilities?: string[];
  sandboxPlacement?: {
    provider?: string;
    sandboxId?: string;
    sandbox_id?: string;
    cwd?: string;
    repoRef?: string;
    repo_ref?: string;
  };
  sandbox_placement?: Record<string, string | undefined>;
  sandboxProvider?: string;
  sandbox_provider?: string;
  sandboxId?: string;
  sandbox_id?: string;
  sandboxCwd?: string;
  sandbox_cwd?: string;
  sandboxRepoRef?: string;
  sandbox_repo_ref?: string;
}

export interface FlueEpicInput {
  epicId?: string;
  epic_id?: string;
}

export interface FlueAgentInput {
  agent?: string;
  agentName?: string;
  agent_name?: string;
  name?: string;
}

export interface FlueAgentParentUpdateInput extends FlueAgentInput {
  parent?: string;
  parentEpicId?: string;
  parent_epic_id?: string;
  expectParent?: string;
  expect_parent?: string;
}

export interface FlueTaskRunActiveInput extends FlueEpicInput {
  limit?: number;
}

export interface FlueTaskRunRecoverStaleInput {
  staleBefore?: string;
  stale_before?: string;
  maxAgeSeconds?: number;
  max_age_seconds?: number;
  errorClass?: string;
  error_class?: string;
  errorMessage?: string;
  error_message?: string;
}

export interface FlueDriverClientOptions {
  env?: NodeJS.ProcessEnv | Record<string, string | undefined>;
  input?: Record<string, unknown>;
  command?: string[];
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
  };
  readonly agents: {
    list(input?: Record<string, unknown>): Promise<Record<string, unknown>[] | null>;
    orchestrationSession(input?: FlueAgentInput): Promise<Record<string, unknown> | null>;
    updateParent(input?: FlueAgentParentUpdateInput): Promise<Record<string, unknown> | null>;
    deliverAssignment(input?: FlueAgentInput): Promise<Record<string, unknown> | null>;
  };
  readonly tasks: {
    claimReady(input?: FlueEpicInput): Promise<Record<string, unknown> | null>;
    complete(input?: FlueTaskSelector | string): Promise<Record<string, unknown> | null>;
    release(input?: FlueTaskSelector | string): Promise<Record<string, unknown> | null>;
  };
  readonly taskRuns: {
    request(input?: FlueTaskRunRequest): Promise<Record<string, unknown>>;
    active(input?: FlueTaskRunActiveInput): Promise<Record<string, unknown> | null>;
    recoverStale(input?: FlueTaskRunRecoverStaleInput): Promise<Record<string, unknown> | null>;
  };

  completed(input?: { summary?: string }): FlueDriverResult;
  failed(input?: { summary?: string; errorClass?: string; error_class?: string }): FlueDriverResult;
  needsHuman(input?: {
    summary?: string;
    errorClass?: string;
    error_class?: string;
    taskRunId?: string;
    task_run_id?: string;
    logsRef?: string;
    logs_ref?: string;
    artifactsRef?: string;
    artifacts_ref?: string;
  }): FlueDriverResult;
  claimReady(input?: FlueEpicInput): Promise<Record<string, unknown> | null>;
  getEpic(input?: FlueEpicInput): Promise<Record<string, unknown> | null>;
  epicSnapshot(input?: FlueEpicInput): Promise<Record<string, unknown> | null>;
  listAgents(input?: Record<string, unknown>): Promise<Record<string, unknown>[] | null>;
  agentOrchestrationSession(input?: FlueAgentInput): Promise<Record<string, unknown> | null>;
  updateAgentParent(input?: FlueAgentParentUpdateInput): Promise<Record<string, unknown> | null>;
  deliverLeadAssignment(input?: FlueAgentInput): Promise<Record<string, unknown> | null>;
  requestTaskRun(input?: FlueTaskRunRequest): Promise<Record<string, unknown>>;
  activeTaskRuns(input?: FlueTaskRunActiveInput): Promise<Record<string, unknown> | null>;
  recoverStaleTaskRuns(input?: FlueTaskRunRecoverStaleInput): Promise<Record<string, unknown> | null>;
  completeTask(input?: FlueTaskSelector | string): Promise<Record<string, unknown> | null>;
  releaseTask(input?: FlueTaskSelector | string): Promise<Record<string, unknown> | null>;
}

export declare function createLoomDriverClient(options?: FlueDriverClientOptions | Record<string, unknown>): FlueDriverClient;
export declare const createLoomClient: typeof createLoomDriverClient;
