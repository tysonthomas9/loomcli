export type RuntimeCleanupPolicy = {
  mode?: "never" | "on_completion" | "after_ttl" | "provider_default" | string;
  ttl?: string;
  retention?: string;
};

export type RuntimeFilesystemPolicy = {
  persistence?: "ephemeral" | "session" | "durable" | "provider_default" | string;
  durability?: "ephemeral" | "session" | "durable" | "provider_default" | string;
  retention?: string;
};

export type RuntimeCapabilities = {
  filesystem?: {
    read?: boolean;
    write?: boolean;
    artifactURI?: boolean;
    artifact_uri?: boolean;
    policy?: "local" | "provider_default" | string;
    persistence?: string;
    durability?: string;
    retention?: string;
  };
  shell?: {
    enabled?: boolean;
    commands?: string[];
    policy?: "local" | "provider_default" | string;
  };
  network?: {
    enabled?: boolean;
    policy?: "local" | "provider_default" | string;
  };
  env?: {
    forwarded?: string[];
    policy?: "allowlist" | string;
  };
  workspace?: {
    providerWorkspaceId?: string;
    provider_workspace_id?: string;
    owner?: string;
    cwd?: string;
    repos?: string[];
    skillDirs?: string[];
    skill_dirs?: string[];
  };
  lifecycle?: {
    materialize?: boolean;
    cleanup?: boolean;
    release?: boolean;
    cancellation?: boolean;
    defaultTimeout?: string;
    default_timeout?: string;
    policy?: "local" | "provider_default" | string;
  };
};

export type RuntimeWorkspacePolicy = {
  id?: string;
  providerWorkspaceId?: string;
  provider_workspace_id?: string;
  workspaceId?: string;
  workspace_id?: string;
  owner?: "loom" | "external" | "user" | "provider" | string;
  cleanup?: RuntimeCleanupPolicy;
  filesystem?: RuntimeFilesystemPolicy;
};

export type RuntimeProfile = {
  provider: string;
  name?: string;
  image?: string;
  repos?: string[];
  env?: string[];
  cpu?: string;
  memory?: string;
  cwd?: string;
  workspaceSkillDirs?: string[];
  workspace_skill_dirs?: string[];
  workspace?: RuntimeWorkspacePolicy;
  providerWorkspaceId?: string;
  provider_workspace_id?: string;
  workspaceId?: string;
  workspace_id?: string;
  workspaceOwner?: string;
  workspace_owner?: string;
  cleanup?: RuntimeCleanupPolicy;
  cleanupPolicy?: RuntimeCleanupPolicy;
  cleanupMode?: string;
  cleanupTTL?: string;
  cleanupRetention?: string;
  filesystem?: RuntimeFilesystemPolicy;
  filesystemPersistence?: string;
  filesystemDurability?: string;
  filesystemRetention?: string;
  capabilities?: RuntimeCapabilities;
};

export type AgentPolicy = {
  allowedCommands?: string[];
  deniedCommands?: string[];
  maxConcurrency?: number;
  maxBudgetUSD?: number;
  readOnly?: boolean;
};

export type SkillDefinition = {
  name: string;
  description?: string;
  version?: string;
  source_path?: string;
  source_hash?: string;
  instructions?: string;
  resources?: string[];
};

export type AgentDefinition = {
  name: string;
  description?: string;
  backend?: string;
  model?: string;
  runtime?: RuntimeProfile;
  instructions?: string;
  skills?: Array<string | SkillDefinition>;
  tools?: Array<string | unknown>;
  allowedCommands?: string[];
  deniedCommands?: string[];
  repos?: string[];
  env?: string[];
  maxConcurrency?: number;
  maxBudgetUSD?: number;
  readOnly?: boolean;
  policy?: AgentPolicy;
};

export type WorkflowDefinition = {
  name: string;
  description?: string;
  builtin?: string;
  runner?: string;
  singleton?: string | ((input: unknown) => string);
  routePath?: string;
  routeAuth?: string;
  triggerEvent?: string;
  triggerFilter?: Record<string, string>;
  issueLabelAdded?: Record<string, string>;
  triggers?: TriggerDefinition[];
  tools?: Array<string | unknown>;
  repos?: string[];
  env?: string[];
};

export type TriggerDefinition = {
  event: string;
  filter: Record<string, string>;
};

export type Config = {
  sourceRoot?: string;
};

export type LoomTransportResult = {
  stdout: string;
  stderr?: string;
  status?: number | null;
  signal?: string | null;
};

export type LoomTransportRunOptions = {
  cwd?: string;
  env?: Record<string, string | undefined>;
  signal?: AbortSignal;
  stdin?: string;
};

export type LoomHTTPRequestOptions = {
  query?: Record<string, string | number | boolean | undefined>;
  body?: unknown;
  signal?: AbortSignal;
  headers?: Record<string, string>;
};

export interface LoomTransport {
  workspace?: string;
  workspaceKey?: string;
  workspace_key?: string;
  run?(args: string[], options?: LoomTransportRunOptions): Promise<LoomTransportResult>;
  request?<T = unknown>(method: string, path: string, options?: LoomHTTPRequestOptions): Promise<T>;
}

export type LoomClientOptions = {
  transport?: LoomTransport;
  binary?: string;
  cwd?: string;
  env?: Record<string, string | undefined>;
  baseURL?: string;
  url?: string;
  workspace?: string;
  workspaceKey?: string;
  workspace_key?: string;
  apiKey?: string;
  api_key?: string;
  authToken?: string;
  auth_token?: string;
  fetch?: (input: unknown, init?: unknown) => Promise<{ ok: boolean; status: number; text(): Promise<string> }>;
};

export type SourceOptions = {
  source?: string;
  dir?: string;
  cwd?: string;
  envVars?: Record<string, string | undefined>;
  signal?: AbortSignal;
  stdin?: string;
  workspace?: string;
  key?: string;
  workspaceKey?: string;
  workspace_key?: string;
  headers?: Record<string, string>;
};

export type ConnectOptions = SourceOptions & {
  id?: string;
  instance?: string;
  session?: string;
  message?: string;
  env?: string;
  envFile?: string;
};

export type RunOptions = SourceOptions & {
  input?: unknown;
  payload?: unknown;
  wait?: boolean;
  once?: boolean;
};

export type DefsPlanOptions = SourceOptions & {
  fromWorkspace?: boolean;
};

export type DefsApplyOptions = SourceOptions & {
  start?: boolean;
};

export type DefsExportSourceOptions = SourceOptions & {
  force?: boolean;
  includeState?: boolean;
};

export type WorkflowRouteBindOptions = SourceOptions & {
  auth?: string;
};

export type WorkflowTriggerBindOptions = SourceOptions & {
  filter?: Record<string, string> | string;
};

export type RunArtifactsOptions = SourceOptions & {
  type?: string;
};

export type AgentCreateOptions = SourceOptions & {
  role: string;
  roleName?: string;
  auto?: boolean;
  backend?: string;
  repos?: string[] | string;
  repoGroups?: string[] | string;
  repo_groups?: string[] | string;
  crossRepo?: boolean;
  cross_repo?: boolean;
  parent?: string;
  mode?: string;
  taskFilter?: string;
  task_filter?: string;
  maxConcurrency?: number;
  max_concurrency?: number;
  budgetPolicy?: string;
  budget_policy?: string;
  task?: string;
  orchestrator?: string;
};

export type AgentStopOptions = SourceOptions & {
  force?: boolean;
};

export type AdminOptions = SourceOptions & {
  workspace?: string;
  key?: string;
  workspaceKey?: string;
  workspace_key?: string;
  timeout?: number;
};

export declare function defineConfig<T extends Config>(config: T): T;
export declare function defineAgent<T extends AgentDefinition>(agent: T): T;
export declare const createAgent: typeof defineAgent;
export declare function defineAgentProfile<T extends AgentDefinition>(profile: T): T;
export declare function defineSkill<T extends SkillDefinition>(skill: T): T;
export declare function defineWorkflow<T extends WorkflowDefinition>(workflow: T): T;
export declare function defineTool<T extends object>(tool: T): T;
export declare function defineRuntimeProfile<T extends RuntimeProfile>(profile: T): T;

export declare const runtime: {
  local(config: Omit<RuntimeProfile, "provider">): RuntimeProfile;
  podman(config: Omit<RuntimeProfile, "provider">): RuntimeProfile;
  remote(config: Omit<RuntimeProfile, "provider"> & { provider?: string }): RuntimeProfile;
};

export declare const trigger: {
  issueLabelAdded(config?: Record<string, string>): TriggerDefinition;
  event(event: string, filter?: Record<string, unknown>): TriggerDefinition;
  cron(schedule: string, filter?: Record<string, unknown>): TriggerDefinition;
  webhook(provider: string, filter?: Record<string, unknown>): TriggerDefinition;
  github(event: string, filter?: Record<string, unknown>): TriggerDefinition;
  datadogAlert(filter?: Record<string, unknown>): TriggerDefinition;
  chat(provider: string, filter?: Record<string, unknown>): TriggerDefinition;
};

export declare const schema: Record<string, (...args: unknown[]) => unknown>;
export declare const Type: typeof schema;

export declare class CLILoomTransport implements LoomTransport {
  constructor(options?: LoomClientOptions);
  run(args: string[], options?: LoomTransportRunOptions): Promise<LoomTransportResult>;
}

export declare class FetchLoomTransport implements LoomTransport {
  workspace?: string;
  constructor(options: LoomClientOptions & { baseURL?: string; url?: string });
  request<T = unknown>(method: string, path: string, options?: LoomHTTPRequestOptions): Promise<T>;
}

export declare class LoomClient {
  constructor(options?: LoomClientOptions);
  check<T = unknown>(request?: SourceOptions): Promise<T>;
  connect<T = unknown>(agent: string | { name: string }, request?: ConnectOptions): Promise<T>;
  run<T = unknown>(workflow: string | { name: string }, request?: RunOptions): Promise<T>;
  agents: {
    list<T = unknown>(request?: SourceOptions): Promise<T>;
    get<T = unknown>(agent: string | { name: string }, request?: SourceOptions): Promise<T>;
    create<T = unknown>(agent: string | { name: string }, request: AgentCreateOptions): Promise<T>;
    remove<T = unknown>(agent: string | { name: string }, request?: SourceOptions): Promise<T>;
    start<T = unknown>(agent: string | { name: string }, request?: SourceOptions): Promise<T>;
    stop<T = unknown>(agent: string | { name: string }, request?: AgentStopOptions): Promise<T>;
  };
  defs: {
    plan<T = unknown>(request?: DefsPlanOptions): Promise<T>;
    apply<T = unknown>(request?: DefsApplyOptions): Promise<T>;
    exportSource<T = unknown>(request?: DefsExportSourceOptions): Promise<T>;
  };
  workflows: {
    list<T = unknown>(request?: SourceOptions): Promise<T>;
    listRoutes<T = unknown>(workflow?: string | { name: string }, request?: SourceOptions): Promise<T>;
    bindRoute<T = unknown>(
      workflow: string | { name: string },
      path: string,
      request?: WorkflowRouteBindOptions,
    ): Promise<T>;
    unbindRoute<T = unknown>(workflow: string | { name: string }, path: string, request?: SourceOptions): Promise<T>;
    listTriggers<T = unknown>(workflow?: string | { name: string }, request?: SourceOptions): Promise<T>;
    bindTrigger<T = unknown>(
      workflow: string | { name: string },
      event: string,
      request?: WorkflowTriggerBindOptions,
    ): Promise<T>;
    unbindTrigger<T = unknown>(workflow: string | { name: string }, event: string, request?: SourceOptions): Promise<T>;
  };
  runs: {
    get<T = unknown>(runId: string, request?: SourceOptions): Promise<T>;
    events<T = unknown>(runId: string, request?: SourceOptions): Promise<T>;
    tasks<T = unknown>(runId: string, request?: SourceOptions): Promise<T>;
    sessions<T = unknown>(runId: string, request?: SourceOptions): Promise<T>;
    operations<T = unknown>(runId: string, request?: SourceOptions): Promise<T>;
    toolCalls<T = unknown>(runId: string, request?: SourceOptions): Promise<T>;
    artifacts<T = unknown>(runId: string, request?: RunArtifactsOptions): Promise<T>;
    cancel<T = unknown>(runId: string, request?: SourceOptions): Promise<T>;
  };
  sessions: {
    list<T = unknown>(request?: SourceOptions): Promise<T[]>;
    get<T = unknown>(sessionId: string, request?: SourceOptions): Promise<T | undefined>;
    forRun<T = unknown>(runId: string, request?: SourceOptions): Promise<T>;
  };
  operations: {
    list<T = unknown>(request?: SourceOptions): Promise<T[]>;
    get<T = unknown>(operationId: string, request?: SourceOptions): Promise<T | undefined>;
    forRun<T = unknown>(runId: string, request?: SourceOptions): Promise<T>;
    cancel<T = unknown>(operationId: string, request?: SourceOptions & { reason?: string }): Promise<T>;
  };
  toolCalls: {
    list<T = unknown>(request?: SourceOptions): Promise<T[]>;
    get<T = unknown>(callId: string, request?: SourceOptions): Promise<T | undefined>;
    forRun<T = unknown>(runId: string, request?: SourceOptions): Promise<T>;
  };
  tasks: {
    list<T = unknown>(request?: SourceOptions): Promise<T[]>;
    get<T = unknown>(taskRunId: string, request?: SourceOptions): Promise<T | undefined>;
    forRun<T = unknown>(runId: string, request?: SourceOptions): Promise<T>;
  };
  events: {
    list<T = unknown>(request?: SourceOptions): Promise<T[]>;
    get<T = unknown>(eventId: string, request?: SourceOptions): Promise<T | undefined>;
    forRun<T = unknown>(runId: string, request?: SourceOptions): Promise<T>;
  };
  tools: {
    list<T = unknown>(request?: SourceOptions): Promise<T[]>;
    get<T = unknown>(tool: string | { name: string }, request?: SourceOptions): Promise<T | undefined>;
  };
  admin: {
    status<T = unknown>(request?: AdminOptions): Promise<T>;
    diagnose<T = unknown>(request?: AdminOptions): Promise<T>;
    ensureRuntime<T = unknown>(request?: AdminOptions): Promise<T>;
    repair<T = unknown>(request?: AdminOptions): Promise<T>;
  };
}

export declare function createLoomClient(options?: LoomClientOptions): LoomClient;
export declare const loom: LoomClient;
export declare function sourceToProjectDir(source: string): string;

declare module "*.md" {
  const skill: SkillDefinition;
  export default skill;
}
