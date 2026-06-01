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

export type SkillDefinition = {
  name: string;
  description?: string;
  version?: string;
  source_path?: string;
  source_hash?: string;
  instructions?: string;
  resources?: string[];
};

export type WorkflowDefinition = {
  name: string;
  description?: string;
  builtin?: string;
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

export declare function defineConfig<T extends Config>(config: T): T;
export declare function defineAgent<T extends AgentDefinition>(agent: T): T;
export declare function createAgent<T extends AgentDefinition>(agent: T): T;
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

declare module "*.md" {
  const skill: import("@loom/runtime").SkillDefinition;
  export default skill;
}
