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
  readonly tasks: {
    claimReady(input?: { epicId?: string; epic_id?: string }): Promise<Record<string, unknown> | null>;
    complete(input?: FlueTaskSelector | string): Promise<Record<string, unknown> | null>;
    release(input?: FlueTaskSelector | string): Promise<Record<string, unknown> | null>;
  };
  readonly taskRuns: {
    request(input?: FlueTaskRunRequest): Promise<Record<string, unknown>>;
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
  claimReady(input?: { epicId?: string; epic_id?: string }): Promise<Record<string, unknown> | null>;
  requestTaskRun(input?: FlueTaskRunRequest): Promise<Record<string, unknown>>;
  completeTask(input?: FlueTaskSelector | string): Promise<Record<string, unknown> | null>;
  releaseTask(input?: FlueTaskSelector | string): Promise<Record<string, unknown> | null>;
}

export declare function createLoomDriverClient(options?: FlueDriverClientOptions | Record<string, unknown>): FlueDriverClient;
export declare const createLoomClient: typeof createLoomDriverClient;
