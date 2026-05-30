package defs

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var definitionNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
var toolNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

var reservedModelToolNames = map[string]bool{
	"bash":  true,
	"edit":  true,
	"glob":  true,
	"grep":  true,
	"read":  true,
	"task":  true,
	"write": true,
}

func InitTypeScriptProject(root string) error {
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	dirs := []string{
		filepath.Join(root, ".loom", "agents"),
		filepath.Join(root, ".loom", "workflows"),
		filepath.Join(root, ".loom", "runtimes"),
		filepath.Join(root, ".loom", "tools"),
		filepath.Join(root, ".loom", "skills"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	if err := writeIfMissing(filepath.Join(root, "loom.config.ts"), []byte(defaultConfig)); err != nil {
		return err
	}
	if err := writeIfMissing(filepath.Join(root, ".loom", "start.md"), []byte(startContract)); err != nil {
		return err
	}
	if err := writeIfMissing(filepath.Join(root, ".loom", "runtime.d.ts"), []byte(runtimeTypes)); err != nil {
		return err
	}
	return nil
}

func ScaffoldAgent(root, name string) (string, error) {
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	name = strings.TrimSpace(name)
	if !definitionNamePattern.MatchString(name) {
		return "", fmt.Errorf("agent name %q must be lower-kebab-case", name)
	}
	if err := InitTypeScriptProject(root); err != nil {
		return "", err
	}
	path := filepath.Join(root, ".loom", "agents", name+".ts")
	if err := writeIfMissing(path, []byte(agentTemplate(name))); err != nil {
		return "", err
	}
	return path, nil
}

func ScaffoldWorkflow(root, name string) (string, error) {
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	name = strings.TrimSpace(name)
	if !definitionNamePattern.MatchString(name) {
		return "", fmt.Errorf("workflow name %q must be lower-kebab-case", name)
	}
	if err := InitTypeScriptProject(root); err != nil {
		return "", err
	}
	path := filepath.Join(root, ".loom", "workflows", name+".ts")
	if err := writeIfMissing(path, []byte(workflowTemplate(name))); err != nil {
		return "", err
	}
	return path, nil
}

func ScaffoldSkill(root, name string) (string, error) {
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	name = strings.TrimSpace(name)
	if !definitionNamePattern.MatchString(name) {
		return "", fmt.Errorf("skill name %q must be lower-kebab-case", name)
	}
	if err := InitTypeScriptProject(root); err != nil {
		return "", err
	}
	path := filepath.Join(root, ".loom", "skills", name, "SKILL.md")
	if err := writeIfMissing(path, []byte(skillTemplate(name))); err != nil {
		return "", err
	}
	return path, nil
}

func writeIfMissing(path string, data []byte) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func skillTemplate(name string) string {
	return fmt.Sprintf(`---
name: %s
description: Reusable guidance for %s work.
---

Use this skill when the agent needs to perform %s work consistently.

Before finishing, summarize what changed, what evidence was checked, and any follow-up work.
`, name, name, name)
}

const defaultConfig = `import { defineConfig } from '@loom/runtime';

export default defineConfig({
  sourceRoot: '.loom',
});
`

const startContract = `# Create a TypeScript-First Loom Agent

You are helping the user create or update a TypeScript-first Loom agent or
workflow in this repository.

## Contract

Before changing files, inspect the current repository and treat the existing
layout as authoritative. Use the .loom source layout:

.loom/
  agents/
  workflows/
  runtimes/
  tools/
  skills/

Ask only for choices that cannot be inferred safely:

1. Agent or workflow purpose.
2. Whether this is a continuing agent session or a finite workflow run.
3. Runtime target and model/provider.
4. Required environment variable names.

## First Agent Path

For a continuing agent, create .loom/agents/<name>.ts with createAgent(...).
Keep the first version small and locally provable. The first success moment is
a private local connect session:

loom add agent <name>
loom check
loom connect <name> local --message "hello"
loom defs plan
loom defs apply --start

Source files alone do not make an agent visible in the Loom UI. UI/runtime
visibility starts after durable apply and explicit start creates or updates the
workspace agent instance.

## First Workflow Path

For a finite orchestration, create .loom/workflows/<name>.ts with
defineWorkflow(...). The first success moment is a bounded workflow run:

loom add workflow <name>
loom check
loom run <name> --payload '{}'
loom defs plan
loom defs apply

Do not turn an agent request into a workflow-only artifact unless the user asks
for a bounded job.

## Runtime And Capabilities

Keep runtime authority explicit:

- Runtime profiles live in .loom/runtimes/*.ts.
- Typed tools live in .loom/tools/*.ts and need trusted handlers.
- Skills live in .loom/skills/<name>/SKILL.md when source-owned.
- Environment bindings list names only; never invent or write secret values.
- Route and trigger exposure must be reviewed in loom defs plan.

## Control Plane Fit

TypeScript is the reviewed authoring layer. Loom/FleetDB durable records remain
the runtime source of truth. Direct control-plane changes are valid for
operators and should converge with TypeScript through plan/apply or
from-workspace export.

## Finish Criteria

Before finishing:

- Run loom check or explain why it cannot run.
- Show the files changed.
- Show the plan/apply command the user should run next.
- State whether the agent/workflow is only local source or has been durably
  applied and started.
`

func agentTemplate(name string) string {
	return fmt.Sprintf(`import { createAgent, runtime } from '@loom/runtime';

export default createAgent({
  name: '%s',
  description: 'A TypeScript-defined Loom agent.',
  backend: 'echo',
  model: 'local/echo',
  runtime: runtime.local({
    repos: ['.'],
    env: [],
  }),
  instructions: `+"`"+`
    You are a helpful engineering agent.
    Inspect the workspace before making changes and keep edits scoped.
  `+"`"+`,
  tools: [],
  policy: {
    allowedCommands: ['git status', 'go test ./...'],
    deniedCommands: ['git reset --hard'],
    maxConcurrency: 1,
  },
});
`, name)
}

func workflowTemplate(name string) string {
	return fmt.Sprintf(`import { defineWorkflow, trigger } from '@loom/runtime';

type Input = {
  parentId: string;
  role?: string;
  maxConcurrency?: number;
};

export default defineWorkflow({
  name: '%s',
  description: 'Ensure one live task run per ready child under a parent work item.',
  singleton: (input: Input) => `+"`"+`parent:${input.parentId}`+"`"+`,
  expose: {
    http: {
      path: '/workflows/%s/run',
      auth: 'workspace',
    },
  },
  triggers: [
    trigger.issueLabelAdded({ label: '%s', type: 'epic' }),
  ],
  tools: ['workItems.readyChildren', 'taskRuns.ensure'],
  repos: ['.'],
  env: [],

  async run(ctx) {
    const parentId = String(ctx.input.parentId ?? ctx.input.parent_id ?? '');
    if (!parentId) throw new Error('parentId is required');

    const role = String(ctx.input.role ?? 'task');
    const maxConcurrency = Number(ctx.input.maxConcurrency ?? ctx.input.max_concurrency ?? 4);
    ctx.log.info('checking ready child work', { parentId, role, maxConcurrency });

    const ready = await ctx.workItems.readyChildren(parentId, { limit: maxConcurrency });
    for (const child of ready.slice(0, maxConcurrency)) {
      await ctx.taskRuns.ensure({
        workItemId: String(child.id),
        role,
        reason: String(child.title ?? ''),
      });
    }

    return { parentId, ensured: ready.length };
  },
});
`, name, name, name)
}

const runtimeTypes = `declare module '@loom/runtime' {
  export type ModelValidationPolicy = {
    allowed?: string[];
    allow?: string[];
    allowedModels?: string[];
    allowed_models?: string[];
    providers?: string[];
    allowedProviders?: string[];
    allowed_providers?: string[];
    allowUnknown?: boolean;
    allow_unknown?: boolean;
  };

  export type LoomConfig = {
    sourceRoot?: string;
    models?: ModelValidationPolicy;
    modelPolicy?: ModelValidationPolicy;
    allowedModels?: string[];
    allowed_models?: string[];
    allowedModelProviders?: string[];
    allowed_model_providers?: string[];
    allowUnknownModels?: boolean;
    allow_unknown_models?: boolean;
  };

  export type RuntimeCleanupPolicy = {
    mode?: 'never' | 'on_completion' | 'after_ttl' | 'provider_default' | string;
    ttl?: string;
    retention?: string;
  };

  export type RuntimeFilesystemPolicy = {
    persistence?: 'ephemeral' | 'session' | 'durable' | 'provider_default' | string;
    durability?: 'ephemeral' | 'session' | 'durable' | 'provider_default' | string;
    retention?: string;
  };

  export type RuntimeCapabilities = {
    filesystem?: {
      read?: boolean;
      write?: boolean;
      artifactURI?: boolean;
      artifact_uri?: boolean;
      policy?: 'local' | 'provider_default' | string;
      persistence?: string;
      durability?: string;
      retention?: string;
    };
    shell?: {
      enabled?: boolean;
      commands?: string[];
      policy?: 'local' | 'provider_default' | string;
    };
    network?: {
      enabled?: boolean;
      policy?: 'local' | 'provider_default' | string;
    };
    env?: {
      forwarded?: string[];
      policy?: 'allowlist' | string;
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
      policy?: 'local' | 'provider_default' | string;
    };
  };

  export type RuntimeWorkspacePolicy = {
    id?: string;
    providerWorkspaceId?: string;
    provider_workspace_id?: string;
    workspaceId?: string;
    workspace_id?: string;
    owner?: 'loom' | 'external' | 'user' | 'provider' | string;
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

  export type AgentDefinition = {
    name: string;
    description?: string;
    backend?: string;
    model?: string;
    profileName?: string;
    profile_name?: string;
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
    policy?: {
      allowedCommands?: string[];
      deniedCommands?: string[];
      maxConcurrency?: number;
      maxBudgetUSD?: number;
      readOnly?: boolean;
    };
  };

  export type AgentProfileDefinition = Omit<AgentDefinition, 'profileName' | 'profile_name'> & {
    name: string;
  };

  export type AgentFactoryContext = {
    id: string;
    input: Record<string, unknown>;
    payload: Record<string, unknown>;
    env: Record<string, string | undefined>;
    req: WorkflowRequestContext;
    request: WorkflowRequestContext;
  };

  export type AgentFactory = (ctx: AgentFactoryContext) => Partial<AgentDefinition> & {
    name?: string;
  };

  export type CreatedAgent = Partial<AgentDefinition> & {
    name?: string;
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

  export type ToolSchema = Record<string, unknown>;

  export type ToolDefinition = {
    name: string;
    description: string;
    parameters: ToolSchema;
    handler?: string;
    runtime?: string;
    repos?: string[];
    env?: string[];
    readOnly?: boolean;
    execute?: (args: Record<string, unknown>, signal?: AbortSignal) => string | Promise<string>;
  };

  export type WorkflowDefinition = {
    name: string;
    description?: string;
    builtin?: string;
    runner?: string;
    singleton?: string | ((input: Record<string, unknown>) => string);
    path?: string;
    auth?: string;
    routePath?: string;
    routeAuth?: string;
    expose?: {
      http?: {
        path?: string;
        auth?: string;
      };
    };
    triggerEvent?: string;
    triggerFilter?: Record<string, string>;
    issueLabelAdded?: Record<string, string>;
    triggers?: Array<{ event: string; filter?: Record<string, string> }>;
    runtime?: string | RuntimeProfile;
    runtimeProfile?: string;
    runtime_profile?: string;
    tools?: Array<string | unknown>;
    repos?: string[];
    env?: string[];
    run?: (ctx: WorkflowContext) => unknown | Promise<unknown>;
  };

  export type WorkflowRequestContext = {
    workspaceKey: string;
    workflowName: string;
    workflowVersion: string;
    actor?: string;
  };

  export type WorkflowRunState = {
    status: 'queued' | 'running' | 'waiting' | 'completed' | 'failed' | 'cancelled' | string;
    waitCondition?: string;
    cancelRequested: boolean;
  };

  export type WorkflowRuntimeProfile = {
    name: string;
    version?: string;
    provider: string;
    image?: string;
    repos?: string[];
    env?: string[];
    cpu?: string;
    memory?: string;
    status?: string;
    capabilities?: RuntimeCapabilities;
    workspace?: {
      providerWorkspaceId?: string;
      owner?: string;
      cleanup?: RuntimeCleanupPolicy;
      filesystem?: RuntimeFilesystemPolicy;
    };
  };

  export type WorkflowWorkspaceSkill = {
    name: string;
    description?: string;
    source: 'runtime_workspace' | string;
    compatibility?: string;
    metadata?: Record<string, string>;
  };

  export type WorkflowSkillQuery = {
    name?: string;
    names?: string[];
    source?: string;
    compatibility?: string;
    limit?: number;
  };

  export type WorkflowRuntimeWorkspaceLifecycleInput = Record<string, unknown> & {
    providerWorkspaceId?: string;
    provider_workspace_id?: string;
    owner?: string;
    cleanup?: RuntimeCleanupPolicy;
    filesystem?: RuntimeFilesystemPolicy;
    reason?: string;
    idempotencyKey?: string;
    idempotency_key?: string;
    metadata?: Record<string, string>;
  };

  export type WorkflowRuntimeWorkspaceLifecycleReceipt = Record<string, unknown> & {
    accepted: boolean;
    status: 'admitted' | string;
    action: 'materialize' | 'cleanup' | string;
    runtimeProfileName?: string;
    provider?: string;
    providerWorkspaceId?: string;
    owner?: string;
    cleanup?: RuntimeCleanupPolicy;
    filesystem?: RuntimeFilesystemPolicy;
    reason?: string;
    idempotencyKey?: string;
    cleanupEnforced?: boolean;
    cleanupScope?: string;
    cleanedFiles?: number;
  };

  export type WorkflowWorkspaceRepo = {
    name: string;
    sourceRepoId?: string;
    defaultBranch?: string;
    groups?: string[];
    found: boolean;
  };

  export type WorkflowRuntimeWorkspace = {
    key: string;
    name?: string;
    state?: string;
    defaultBranch?: string;
    workflow: {
      name: string;
      version?: string;
    };
    runtime?: {
      profileName?: string;
      provider?: string;
      version?: string;
      repos?: string[];
      env?: string[];
      providerWorkspaceId?: string;
      owner?: string;
      cleanup?: RuntimeCleanupPolicy;
      filesystem?: RuntimeFilesystemPolicy;
      capabilities?: RuntimeCapabilities;
    };
    repos?: WorkflowWorkspaceRepo[];
    selectedRepos?: string[];
    skills?: WorkflowWorkspaceSkill[];
    env?: string[];
  };

  export type WorkflowTaskRun = Record<string, unknown> & {
    task_run_id?: string;
    workflow_run_id?: string;
    work_item_id?: string;
    role_name?: string;
    status?: string;
    agent_id?: string;
    session_id?: string;
  };

  export type WorkflowTaskRunQuery = {
    status?: string;
    live?: boolean;
    limit?: number;
    workItemId?: string;
    work_item_id?: string;
    role?: string;
    roleName?: string;
    role_name?: string;
  };

  export type WorkflowTaskClaim = Record<string, unknown> & {
    task_run_id?: string;
    work_item_id?: string;
    claim_actor?: string;
    claim_event_id?: string;
    status?: string;
    agent_id?: string;
    session_id?: string;
    active?: boolean;
  };

  export type WorkflowTaskClaimQuery = {
    active?: boolean;
    limit?: number;
    workItemId?: string;
    work_item_id?: string;
    taskRunId?: string;
    task_run_id?: string;
    actor?: string;
    claimActor?: string;
    claim_actor?: string;
  };

  export type WorkflowAgentSessionInput = {
    sessionId?: string;
    session_id?: string;
    id?: string;
    agentId?: string;
    agent_id?: string;
    agent?: string;
    name?: string;
    nodeId?: string;
    node_id?: string;
    harness?: string;
    harnessName?: string;
    harness_name?: string;
    sessionName?: string;
    session_name?: string;
    kind?: 'task' | 'orchestration' | 'terminal' | 'maintenance' | 'ad_hoc';
    taskId?: string;
    task_id?: string;
    workItemId?: string;
    work_item_id?: string;
    terminalId?: string;
    terminal_id?: string;
    parentSessionId?: string;
    parent_session_id?: string;
    status?: 'queued' | 'leased' | 'starting' | 'running' | 'idle' | 'yielded' | 'completed' | 'failed' | 'cancelled' | 'expired';
    phase?: string;
    attempt?: number;
    model?: string;
    backend?: string;
    metadata?: Record<string, string>;
  };

  export type WorkflowAgentSession = Record<string, unknown> & {
    accepted: boolean;
    agentId?: string;
    agent_id?: string;
    sessionId?: string;
    session_id?: string;
    sessionName?: string;
    session_name?: string;
    profileName?: string;
    profile_name?: string;
    harness?: string;
    prompt<T = unknown>(input: WorkflowAgentSessionOperationInput<T>): Promise<WorkflowAgentSessionOperationResult<T>>;
    skill<T = unknown>(input: WorkflowAgentSessionOperationInput<T>): Promise<WorkflowAgentSessionOperationResult<T>>;
    task<T = unknown>(input: WorkflowAgentSessionOperationInput<T>): Promise<WorkflowAgentSessionOperationResult<T>>;
    shell<T = unknown>(input: WorkflowAgentSessionOperationInput<T>): Promise<WorkflowAgentSessionOperationResult<T>>;
    compact<T = unknown>(input?: WorkflowAgentSessionOperationInput<T>): Promise<WorkflowAgentSessionOperationResult<T>>;
  };

  export type WorkflowModelUsage = Record<string, unknown> & {
    inputTokens?: number;
    input_tokens?: number;
    outputTokens?: number;
    output_tokens?: number;
    totalTokens?: number;
    total_tokens?: number;
    costUSD?: number;
    cost_usd?: number;
  };

  export type WorkflowAgentSessionOperationStatus =
    | 'completed'
    | 'failed'
    | 'cancelled'
    | 'result_unavailable';

  export type WorkflowAgentSessionOperationInput<T = unknown> = Record<string, unknown> & {
    instruction?: string;
    prompt?: string;
    name?: string;
    command?: string;
    args?: Record<string, unknown>;
    model?: string;
    provider?: string;
    providerModel?: string;
    provider_model?: string;
    usage?: WorkflowModelUsage;
    status?: WorkflowAgentSessionOperationStatus;
    result?: unknown;
    response?: T;
    mockResult?: T;
    resultUnavailable?: string | boolean | Record<string, unknown>;
    result_unavailable?: string | boolean | Record<string, unknown>;
    cancellation?: string | boolean | Record<string, unknown>;
    cancelled?: string | boolean | Record<string, unknown>;
    cancelReason?: string;
    cancel_reason?: string;
    failure?: string | boolean | Record<string, unknown>;
    error?: string | boolean | Record<string, unknown>;
    metadata?: Record<string, string>;
  };

  export type WorkflowAgentSessionOperationResult<T = unknown> =
    WorkflowAgentSessionOperationReceipt<T> & (T extends object ? T : Record<string, unknown>);

  export type WorkflowAgentSessionOperationReceipt<T = unknown> = Record<string, unknown> & {
    accepted: boolean;
    status: WorkflowAgentSessionOperationStatus;
    operation: 'prompt' | 'skill' | 'task' | 'shell' | 'compact';
    operationId: string;
    agentId?: string;
    sessionId?: string;
    sessionName?: string;
    profileName?: string;
    model?: string;
    provider?: string;
    providerModel?: string;
    usage?: WorkflowModelUsage;
    startedAt?: string;
    completedAt?: string;
    durationMs?: number;
    text?: string;
    data?: T;
    result?: T;
    validation?: {
      requested: boolean;
      status: 'validated' | 'failed' | 'not_validated';
      reason?: string;
    };
    resultUnavailable?: Record<string, unknown>;
    result_unavailable?: Record<string, unknown>;
    cancellation?: Record<string, unknown>;
    failure?: Record<string, unknown>;
    eventType?: 'agent_session_operation';
  };

  export type WorkflowAgentDispatchInput = Record<string, unknown> & {
    agentId?: string;
    agent_id?: string;
    dispatchId?: string;
    dispatch_id?: string;
    operationId?: string;
    operation_id?: string;
    sessionId?: string;
    session_id?: string;
    sessionName?: string;
    session_name?: string;
    taskRunId?: string;
    task_run_id?: string;
    taskId?: string;
    task_id?: string;
    workItemId?: string;
    work_item_id?: string;
    idempotencyKey?: string;
    idempotency_key?: string;
    model?: string;
    provider?: string;
    providerModel?: string;
    provider_model?: string;
    metadata?: Record<string, string>;
  };

  export type WorkflowAgentDispatchReceipt = Record<string, unknown> & {
    accepted: boolean;
    status: 'admitted' | 'rejected';
    dispatchId: string;
    operationId: string;
    agentId: string;
    sessionId?: string;
    sessionName?: string;
    taskRunId?: string;
    taskId?: string;
    workItemId?: string;
    model?: string;
    provider?: string;
    providerModel?: string;
    idempotencyKey?: string;
    admittedAt: string;
    input: Record<string, unknown>;
    metadata?: Record<string, string>;
    correlation?: Record<string, unknown>;
  };

  export type WorkflowStagedFile = Record<string, unknown> & {
    accepted: boolean;
    path: string;
    uri: string;
    type?: string;
    summary?: string;
    mimeType?: string;
    mime_type?: string;
    sizeBytes?: number;
    size_bytes?: number;
    checksum?: string;
    metadata?: Record<string, string>;
    visibility: 'controller' | 'runtime_workspace';
  };

  export type WorkflowFileWriteOptions = {
    type?: string;
    artifactType?: string;
    artifact_type?: string;
    summary?: string;
    mimeType?: string;
    mime_type?: string;
    metadata?: Record<string, string>;
  };

  export type WorkflowFileController = {
    writeText(path: string, content: string, options?: WorkflowFileWriteOptions): Promise<WorkflowStagedFile>;
    readText(path: string): Promise<string>;
    writeJSON(path: string, value: unknown, options?: WorkflowFileWriteOptions): Promise<WorkflowStagedFile>;
    readJSON<T = unknown>(path: string): Promise<T>;
  };

  export type WorkflowControllerShellRunInput<T = WorkflowControllerShellResult> = Record<string, unknown> & {
    command?: string;
    args?: Array<string>;
    cwd?: string;
    env?: Record<string, string>;
    timeoutMs?: number;
    timeout_ms?: number;
    exitCode?: number;
    exit_code?: number;
    response?: T;
    mockResult?: T;
    status?: 'completed' | 'failed' | 'cancelled';
    metadata?: Record<string, string>;
  };

  export type WorkflowControllerShellResult = Record<string, unknown> & {
    accepted?: boolean;
    command?: string;
    cwd?: string;
    exitCode?: number;
    exit_code?: number;
  };

  export type WorkflowControllerShell = {
    run<T = WorkflowControllerShellResult>(command: string, options?: WorkflowControllerShellRunInput<T>): Promise<T>;
    run<T = WorkflowControllerShellResult>(input: WorkflowControllerShellRunInput<T> & { command: string }): Promise<T>;
  };

  export type WorkflowAgentHarness = {
    agentId: string;
    harness: string;
    profileName?: string;
    session(name?: string, options?: WorkflowAgentSessionInput): Promise<WorkflowAgentSession>;
    session(options?: WorkflowAgentSessionInput): Promise<WorkflowAgentSession>;
    sessions: {
      create(name?: string, options?: WorkflowAgentSessionInput): Promise<WorkflowAgentSession>;
      create(options?: WorkflowAgentSessionInput): Promise<WorkflowAgentSession>;
      get(name?: string, options?: WorkflowAgentSessionInput): Promise<WorkflowAgentSession>;
      get(options?: WorkflowAgentSessionInput): Promise<WorkflowAgentSession>;
    };
  };

  export type WorkflowContext = {
    id: string;
    input: Record<string, unknown>;
    payload: Record<string, unknown>;
    env: Record<string, string | undefined>;
    req: WorkflowRequestContext;
    request: WorkflowRequestContext;
    workspace: WorkflowRuntimeWorkspace;
    workflow: {
      status(): Promise<WorkflowRunState>;
      cancelRequested(): Promise<boolean>;
      waitUntil(condition: string, metadata?: Record<string, unknown>): Promise<Record<string, unknown>>;
      cancel(reasonOrOptions?: string | {
        reason?: string;
        metadata?: Record<string, unknown>;
      }, metadata?: Record<string, unknown>): Promise<Record<string, unknown>>;
    };
    runtime: {
      workspace(): Promise<WorkflowRuntimeWorkspace>;
      profile(): Promise<WorkflowRuntimeProfile | null>;
      skills(options?: WorkflowSkillQuery): Promise<Array<WorkflowWorkspaceSkill>>;
      materializeWorkspace(options?: WorkflowRuntimeWorkspaceLifecycleInput): Promise<WorkflowRuntimeWorkspaceLifecycleReceipt>;
      cleanupWorkspace(reason?: string, metadata?: Record<string, string>): Promise<WorkflowRuntimeWorkspaceLifecycleReceipt>;
      cleanupWorkspace(options?: WorkflowRuntimeWorkspaceLifecycleInput): Promise<WorkflowRuntimeWorkspaceLifecycleReceipt>;
      releaseWorkspace(reason?: string, metadata?: Record<string, string>): Promise<WorkflowRuntimeWorkspaceLifecycleReceipt>;
      releaseWorkspace(options?: WorkflowRuntimeWorkspaceLifecycleInput): Promise<WorkflowRuntimeWorkspaceLifecycleReceipt>;
      workspaceLifecycle: {
        materialize(options?: WorkflowRuntimeWorkspaceLifecycleInput): Promise<WorkflowRuntimeWorkspaceLifecycleReceipt>;
        cleanup(reason?: string, metadata?: Record<string, string>): Promise<WorkflowRuntimeWorkspaceLifecycleReceipt>;
        cleanup(options?: WorkflowRuntimeWorkspaceLifecycleInput): Promise<WorkflowRuntimeWorkspaceLifecycleReceipt>;
        release(reason?: string, metadata?: Record<string, string>): Promise<WorkflowRuntimeWorkspaceLifecycleReceipt>;
        release(options?: WorkflowRuntimeWorkspaceLifecycleInput): Promise<WorkflowRuntimeWorkspaceLifecycleReceipt>;
      };
      files: WorkflowFileController;
      filesystem: WorkflowFileController;
    };
    skills: {
      list(options?: WorkflowSkillQuery): Promise<Array<WorkflowWorkspaceSkill>>;
      get(nameOrOptions: string | WorkflowSkillQuery): Promise<WorkflowWorkspaceSkill | null>;
    };
    init(agent: AgentDefinition | CreatedAgent | AgentFactory, options?: {
      name?: string;
      harness?: string;
      agentId?: string;
      agent_id?: string;
      metadata?: Record<string, string>;
    }): Promise<WorkflowAgentHarness>;
    log: {
      info(message: string, attributes?: Record<string, unknown>): void;
      warn(message: string, attributes?: Record<string, unknown>): void;
      error(message: string, attributes?: Record<string, unknown>): void;
    };
    workItems: {
      get(id: string): Promise<Record<string, unknown> | null>;
      comment(id: string, body: string, metadata?: Record<string, unknown>): Promise<Record<string, unknown>>;
      comment(input: {
        id?: string;
        workItemId?: string;
        work_item_id?: string;
        body?: string;
        text?: string;
        comment?: string;
        metadata?: Record<string, unknown>;
      }): Promise<Record<string, unknown>>;
      readyChildren(parentId: string, options?: Record<string, unknown>): Promise<Array<Record<string, unknown>>>;
      blockedChildren(parentId: string, options?: Record<string, unknown>): Promise<Array<Record<string, unknown>>>;
      listChildren(parentId: string, options?: Record<string, unknown>): Promise<Array<Record<string, unknown>>>;
    };
    taskRuns: {
      list(options?: WorkflowTaskRunQuery): Promise<Array<WorkflowTaskRun>>;
      wait(options?: WorkflowTaskRunQuery): Promise<Array<WorkflowTaskRun>>;
      ensure(input: {
        workItemId?: string;
        work_item_id?: string;
        role?: string;
        roleName?: string;
        reason?: string;
        metadata?: Record<string, string>;
      }): Promise<Record<string, unknown>>;
    };
    taskClaims: {
      list(options?: WorkflowTaskClaimQuery): Promise<Array<WorkflowTaskClaim>>;
      get(idOrOptions: string | WorkflowTaskClaimQuery): Promise<WorkflowTaskClaim | null>;
      wait(options?: WorkflowTaskClaimQuery): Promise<Array<WorkflowTaskClaim>>;
    };
    agents: {
      session(input: WorkflowAgentSessionInput): Promise<WorkflowAgentSession>;
      dispatch(agent: string | AgentDefinition | CreatedAgent | AgentFactory, input?: WorkflowAgentDispatchInput): Promise<WorkflowAgentDispatchReceipt>;
    };
    artifacts: {
      record(input: {
        artifactId?: string;
        artifact_id?: string;
        type?: string;
        uri: string;
        summary?: string;
        mimeType?: string;
        mime_type?: string;
        sizeBytes?: number;
        size_bytes?: number;
        checksum?: string;
        taskId?: string;
        task_id?: string;
        workItemId?: string;
        work_item_id?: string;
        metadata?: Record<string, string>;
      }): Promise<Record<string, unknown>>;
      create(input: {
        artifactId?: string;
        artifact_id?: string;
        type?: string;
        uri: string;
        summary?: string;
        mimeType?: string;
        mime_type?: string;
        sizeBytes?: number;
        size_bytes?: number;
        checksum?: string;
        taskId?: string;
        task_id?: string;
        workItemId?: string;
        work_item_id?: string;
        metadata?: Record<string, string>;
      }): Promise<Record<string, unknown>>;
    };
    shell: WorkflowControllerShell;
    setup: {
      shell: WorkflowControllerShell;
    };
    files: WorkflowFileController;
    staging: WorkflowFileController;
    tools: Record<string, (args?: Record<string, unknown>) => Promise<unknown>>;
    tool(name: string, args?: Record<string, unknown>): Promise<unknown>;
  };

  export function defineConfig<T extends LoomConfig>(config: T): T;
  export function defineAgent<T extends AgentDefinition>(agent: T): T;
  export function createAgent<T extends AgentDefinition | AgentFactory>(agent: T): CreatedAgent;
  export function createAgent<T extends AgentProfileDefinition>(profile: T, overrides?: Partial<AgentDefinition>): CreatedAgent;
  export function defineAgentProfile<T extends AgentProfileDefinition>(profile: T): T;
  export function defineSkill<T extends SkillDefinition>(skill: T): T;
  export function defineTool<T extends ToolDefinition>(tool: T): T;
  export function defineWorkflow<T extends WorkflowDefinition>(workflow: T): T;

  export const schema: {
    [kind: string]: (...args: unknown[]) => ToolSchema;
  };

  export const Type: {
    [kind: string]: (...args: unknown[]) => ToolSchema;
  };

  export const runtime: {
    local(config: Omit<RuntimeProfile, 'provider'>): RuntimeProfile;
    podman(config: Omit<RuntimeProfile, 'provider'>): RuntimeProfile;
    remote(config: Omit<RuntimeProfile, 'provider'> & { provider?: string }): RuntimeProfile;
  };

  export const trigger: {
    issueLabelAdded(config?: Record<string, string>): { event: 'issue.label_added'; filter: Record<string, string> };
  };
}

declare module '*.md' {
  const skill: import('@loom/runtime').SkillDefinition;
  export default skill;
}
`
