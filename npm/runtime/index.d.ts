export type RuntimeProfile = {
  provider: string;
  name?: string;
  image?: string;
  repos?: string[];
  env?: string[];
  cpu?: string;
  memory?: string;
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
  skills?: string[];
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
  singleton?: string | ((input: unknown) => string);
  routePath?: string;
  routeAuth?: string;
  triggerEvent?: string;
  triggerFilter?: Record<string, string>;
  tools?: Array<string | unknown>;
  repos?: string[];
  env?: string[];
};

export type Config = {
  sourceRoot?: string;
};

export declare function defineConfig<T extends Config>(config: T): T;
export declare function defineAgent<T extends AgentDefinition>(agent: T): T;
export declare function createAgent<T extends AgentDefinition>(agent: T): T;
export declare function defineAgentProfile<T extends AgentDefinition>(profile: T): T;
export declare function defineWorkflow<T extends WorkflowDefinition>(workflow: T): T;
export declare function defineTool<T extends object>(tool: T): T;

export declare const runtime: {
  local(config: Omit<RuntimeProfile, "provider">): RuntimeProfile;
  podman(config: Omit<RuntimeProfile, "provider">): RuntimeProfile;
  remote(config: Omit<RuntimeProfile, "provider"> & { provider?: string }): RuntimeProfile;
};

export declare const trigger: {
  issueLabelAdded(config?: Record<string, string>): { event: "issue.label_added"; filter: Record<string, string> };
};

export declare const schema: Record<string, (...args: unknown[]) => unknown>;
