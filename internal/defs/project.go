package defs

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
    timeout?: string;
    cancellable?: boolean;
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
    triggers?: TriggerDefinition[];
    runtime?: string | RuntimeProfile;
    runtimeProfile?: string;
    runtime_profile?: string;
    tools?: Array<string | unknown>;
    repos?: string[];
    env?: string[];
    run?: (ctx: WorkflowContext) => unknown | Promise<unknown>;
  };

  export type TriggerDefinition = {
    event: string;
    filter: Record<string, string>;
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
    reconcile?: boolean;
    cleanupReconcile?: boolean;
    cleanup_reconcile?: boolean;
    scope?: 'current_run' | 'runtime_workspace' | 'provider_workspace' | 'workspace' | 'all' | string;
    cleanupScope?: string;
    cleanup_scope?: string;
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
    reconcileRequested?: boolean;
    reconciled?: boolean;
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

  export type WorkflowAgentSessionToolCall = Record<string, unknown> & {
    id?: string;
    callId?: string;
    call_id?: string;
    providerCallId?: string;
    provider_call_id?: string;
    name?: string;
    toolName?: string;
    tool_name?: string;
    status?: string;
    authorizationStatus?: string;
    authorization_status?: string;
    idempotencyKey?: string;
    idempotency_key?: string;
    args?: Record<string, unknown>;
    arguments?: Record<string, unknown>;
    input?: unknown;
    result?: unknown;
    output?: unknown;
    error?: string | Record<string, unknown>;
    toolVersion?: string;
    tool_version?: string;
    sourceHash?: string;
    source_hash?: string;
    handler?: string;
    runtime?: string;
    timeout?: string;
    cancellable?: boolean;
    readOnly?: boolean;
    read_only?: boolean;
    redacted?: boolean;
    startedAt?: string;
    started_at?: string;
    completedAt?: string;
    completed_at?: string;
    durationMs?: number;
    duration_ms?: number;
  };

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
    toolCalls?: Array<WorkflowAgentSessionToolCall>;
    tool_calls?: Array<WorkflowAgentSessionToolCall>;
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
    toolCalls?: Array<WorkflowAgentSessionToolCall>;
    tool_calls?: Array<WorkflowAgentSessionToolCall>;
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
  export function defineRuntimeProfile<T extends RuntimeProfile>(profile: T): T;

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
    issueLabelAdded(config?: Record<string, string>): TriggerDefinition;
    event(event: string, filter?: Record<string, unknown>): TriggerDefinition;
    cron(schedule: string, filter?: Record<string, unknown>): TriggerDefinition;
    webhook(provider: string, filter?: Record<string, unknown>): TriggerDefinition;
    github(event: string, filter?: Record<string, unknown>): TriggerDefinition;
    datadogAlert(filter?: Record<string, unknown>): TriggerDefinition;
    chat(provider: string, filter?: Record<string, unknown>): TriggerDefinition;
  };
}

declare module '@loom/sdk' {
  export {
    Type,
    createAgent,
    defineAgent,
    defineAgentProfile,
    defineConfig,
    defineRuntimeProfile,
    defineSkill,
    defineTool,
    defineWorkflow,
    runtime,
    schema,
    trigger,
  } from '@loom/runtime';

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

  export type LoomSourceOptions = {
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

  export type LoomConnectOptions = LoomSourceOptions & {
    id?: string;
    instance?: string;
    session?: string;
    message?: string;
    env?: string;
    envFile?: string;
  };

  export type LoomRunOptions = LoomSourceOptions & {
    input?: unknown;
    payload?: unknown;
    wait?: boolean;
    once?: boolean;
  };

  export type LoomDefsPlanOptions = LoomSourceOptions & {
    fromWorkspace?: boolean;
  };

  export type LoomDefsApplyOptions = LoomSourceOptions & {
    start?: boolean;
  };

  export type LoomDefsExportSourceOptions = LoomSourceOptions & {
    force?: boolean;
    includeState?: boolean;
  };

  export type LoomWorkflowRouteBindOptions = LoomSourceOptions & {
    auth?: string;
  };

  export type LoomWorkflowTriggerBindOptions = LoomSourceOptions & {
    filter?: Record<string, string> | string;
  };

  export type LoomRunArtifactsOptions = LoomSourceOptions & {
    type?: string;
  };

  export type LoomAgentCreateOptions = LoomSourceOptions & {
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

  export type LoomAgentStopOptions = LoomSourceOptions & {
    force?: boolean;
  };

  export type LoomAdminOptions = LoomSourceOptions & {
    workspace?: string;
    key?: string;
    workspaceKey?: string;
    workspace_key?: string;
    timeout?: number;
  };

  export class CLILoomTransport implements LoomTransport {
    constructor(options?: LoomClientOptions);
    run(args: string[], options?: LoomTransportRunOptions): Promise<LoomTransportResult>;
  }

  export class FetchLoomTransport implements LoomTransport {
    workspace?: string;
    constructor(options: LoomClientOptions & { baseURL?: string; url?: string });
    request<T = unknown>(method: string, path: string, options?: LoomHTTPRequestOptions): Promise<T>;
  }

  export class LoomClient {
    constructor(options?: LoomClientOptions);
    check<T = unknown>(request?: LoomSourceOptions): Promise<T>;
    connect<T = unknown>(agent: string | { name: string }, request?: LoomConnectOptions): Promise<T>;
    run<T = unknown>(workflow: string | { name: string }, request?: LoomRunOptions): Promise<T>;
    agents: {
      list<T = unknown>(request?: LoomSourceOptions): Promise<T>;
      get<T = unknown>(agent: string | { name: string }, request?: LoomSourceOptions): Promise<T>;
      create<T = unknown>(agent: string | { name: string }, request: LoomAgentCreateOptions): Promise<T>;
      remove<T = unknown>(agent: string | { name: string }, request?: LoomSourceOptions): Promise<T>;
      start<T = unknown>(agent: string | { name: string }, request?: LoomSourceOptions): Promise<T>;
      stop<T = unknown>(agent: string | { name: string }, request?: LoomAgentStopOptions): Promise<T>;
    };
    defs: {
      plan<T = unknown>(request?: LoomDefsPlanOptions): Promise<T>;
      apply<T = unknown>(request?: LoomDefsApplyOptions): Promise<T>;
      exportSource<T = unknown>(request?: LoomDefsExportSourceOptions): Promise<T>;
    };
    workflows: {
      list<T = unknown>(request?: LoomSourceOptions): Promise<T>;
      listRoutes<T = unknown>(workflow?: string | { name: string }, request?: LoomSourceOptions): Promise<T>;
      bindRoute<T = unknown>(
        workflow: string | { name: string },
        path: string,
        request?: LoomWorkflowRouteBindOptions,
      ): Promise<T>;
      unbindRoute<T = unknown>(workflow: string | { name: string }, path: string, request?: LoomSourceOptions): Promise<T>;
      listTriggers<T = unknown>(workflow?: string | { name: string }, request?: LoomSourceOptions): Promise<T>;
      bindTrigger<T = unknown>(
        workflow: string | { name: string },
        event: string,
        request?: LoomWorkflowTriggerBindOptions,
      ): Promise<T>;
      unbindTrigger<T = unknown>(workflow: string | { name: string }, event: string, request?: LoomSourceOptions): Promise<T>;
    };
    runs: {
      get<T = unknown>(runId: string, request?: LoomSourceOptions): Promise<T>;
      events<T = unknown>(runId: string, request?: LoomSourceOptions): Promise<T>;
      tasks<T = unknown>(runId: string, request?: LoomSourceOptions): Promise<T>;
      sessions<T = unknown>(runId: string, request?: LoomSourceOptions): Promise<T>;
      artifacts<T = unknown>(runId: string, request?: LoomRunArtifactsOptions): Promise<T>;
      cancel<T = unknown>(runId: string, request?: LoomSourceOptions): Promise<T>;
    };
    sessions: {
      list<T = unknown>(request?: LoomSourceOptions): Promise<T[]>;
      get<T = unknown>(sessionId: string, request?: LoomSourceOptions): Promise<T | undefined>;
      forRun<T = unknown>(runId: string, request?: LoomSourceOptions): Promise<T>;
    };
    tasks: {
      list<T = unknown>(request?: LoomSourceOptions): Promise<T[]>;
      get<T = unknown>(taskRunId: string, request?: LoomSourceOptions): Promise<T | undefined>;
      forRun<T = unknown>(runId: string, request?: LoomSourceOptions): Promise<T>;
    };
    events: {
      list<T = unknown>(request?: LoomSourceOptions): Promise<T[]>;
      get<T = unknown>(eventId: string, request?: LoomSourceOptions): Promise<T | undefined>;
      forRun<T = unknown>(runId: string, request?: LoomSourceOptions): Promise<T>;
    };
    tools: {
      list<T = unknown>(request?: LoomSourceOptions): Promise<T[]>;
      get<T = unknown>(tool: string | { name: string }, request?: LoomSourceOptions): Promise<T | undefined>;
    };
    admin: {
      status<T = unknown>(request?: LoomAdminOptions): Promise<T>;
      diagnose<T = unknown>(request?: LoomAdminOptions): Promise<T>;
      ensureRuntime<T = unknown>(request?: LoomAdminOptions): Promise<T>;
      repair<T = unknown>(request?: LoomAdminOptions): Promise<T>;
    };
  }

  export function createLoomClient(options?: LoomClientOptions): LoomClient;
  export const loom: LoomClient;
  export function sourceToProjectDir(source: string): string;
}

declare module '*.md' {
  const skill: import('@loom/runtime').SkillDefinition;
  export default skill;
}
`
