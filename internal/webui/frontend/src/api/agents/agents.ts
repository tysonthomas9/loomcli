/**
 * Loom Agent API client.
 * Uses openapi-fetch generated client (monitor endpoints are in the spec).
 */

import type {
  LoomAgentStatus,
  LoomAgentsResponse,
  LoomTaskSummary,
  LoomTaskInfo,
  LoomTaskLists,
  LoomTasksResponse,
  LoomSyncInfo,
  LoomStats,
  LoomStatusResponse,
} from "@/types";
import {
  api,
  apiErrorFromResponse,
  del,
  get,
  patch,
  post,
  wsUrl,
} from "@/api/common";

function monitorPath(path: string, workspaceId?: string): string {
  if (!workspaceId) return path;
  return `${path}?workspace=${encodeURIComponent(workspaceId)}`;
}

export type AgentLifecycleAction = "start" | "stop" | "restart" | "yield";

export type AgentLifecycleCommandStatus =
  | "queued"
  | "acked"
  | "running"
  | "succeeded"
  | "failed"
  | "cancelled";

export interface AgentLifecycleRequestResult {
  message: string;
  pending: boolean;
  command_id?: string;
  status?: AgentLifecycleCommandStatus;
}

export interface AgentLifecycleCommandResult {
  command_id: string;
  action: AgentLifecycleAction;
  status: AgentLifecycleCommandStatus;
  result?: string;
  error_class?: string;
  created_at?: string;
  updated_at?: string;
}

const agentLifecycleCommandStatuses = new Set<AgentLifecycleCommandStatus>([
  "queued",
  "acked",
  "running",
  "succeeded",
  "failed",
  "cancelled",
]);

function parseAgentLifecycleCommandStatus(
  value: unknown,
): AgentLifecycleCommandStatus | undefined {
  return typeof value === "string" &&
    agentLifecycleCommandStatuses.has(value as AgentLifecycleCommandStatus)
    ? (value as AgentLifecycleCommandStatus)
    : undefined;
}

function parseAgentLifecycleRequestResult(
  value: unknown,
): AgentLifecycleRequestResult {
  if (value == null || typeof value !== "object" || Array.isArray(value)) {
    throw new Error("Invalid agent lifecycle response");
  }
  const response = value as Record<string, unknown>;
  if (
    typeof response.message !== "string" ||
    typeof response.pending !== "boolean"
  ) {
    throw new Error("Invalid agent lifecycle response");
  }
  const commandID =
    typeof response.command_id === "string" && response.command_id.trim() !== ""
      ? response.command_id
      : undefined;
  if (response.pending && commandID == null) {
    throw new Error(
      "Invalid agent lifecycle response: pending command_id is missing",
    );
  }
  const status = parseAgentLifecycleCommandStatus(response.status);
  if (response.status != null && status == null) {
    throw new Error("Invalid agent lifecycle response: unknown status");
  }
  return {
    message: response.message,
    pending: response.pending,
    ...(commandID != null && { command_id: commandID }),
    ...(status != null && { status }),
  };
}

function parseAgentLifecycleCommandResult(
  value: unknown,
): AgentLifecycleCommandResult {
  if (value == null || typeof value !== "object" || Array.isArray(value)) {
    throw new Error("Invalid agent lifecycle command response");
  }
  const response = value as Record<string, unknown>;
  const commandID =
    typeof response.command_id === "string" ? response.command_id.trim() : "";
  const action = response.action;
  const status = parseAgentLifecycleCommandStatus(response.status);
  if (
    commandID === "" ||
    (action !== "start" &&
      action !== "stop" &&
      action !== "restart" &&
      action !== "yield") ||
    status == null
  ) {
    throw new Error("Invalid agent lifecycle command response");
  }
  const optionalString = (key: string): string | undefined =>
    typeof response[key] === "string" ? response[key] : undefined;
  const result = optionalString("result");
  const errorClass = optionalString("error_class");
  const createdAt = optionalString("created_at");
  const updatedAt = optionalString("updated_at");
  return {
    command_id: commandID,
    action,
    status,
    ...(result != null && { result }),
    ...(errorClass != null && { error_class: errorClass }),
    ...(createdAt != null && { created_at: createdAt }),
    ...(updatedAt != null && { updated_at: updatedAt }),
  };
}

/**
 * Fetch agents from the loom server.
 * Throws on network errors or non-OK responses so callers can handle connection state.
 */
export async function fetchAgents(
  workspaceId?: string,
): Promise<LoomAgentStatus[]> {
  if (workspaceId) {
    const data = await get<LoomAgentsResponse>(
      monitorPath("/api/monitor/agents", workspaceId),
      { signal: AbortSignal.timeout(15000) },
    );
    return (data.agents ?? []) as unknown as LoomAgentStatus[];
  }

  const { data, error, response } = await api.GET("/api/monitor/agents", {
    signal: AbortSignal.timeout(15000),
  });
  if (error) throw apiErrorFromResponse(error, response);
  return (data!.agents ?? []) as unknown as LoomAgentStatus[];
}

/**
 * Check if the loom server is available.
 */
export async function checkLoomHealth(): Promise<boolean> {
  try {
    await get<unknown>("/health", { timeout: 15000 });
    return true;
  } catch {
    return false;
  }
}

/**
 * Request that a workspace agent starts or resumes work.
 */
export async function startAgent(
  workspaceId: string,
  agentName: string,
  options?: { taskId?: string },
): Promise<AgentLifecycleRequestResult> {
  const { data, error, response } = await api.POST(
    "/api/workspaces/{ws}/agents/{name}/start",
    {
      params: { path: { ws: workspaceId, name: agentName } },
      ...(options?.taskId
        ? { body: { payload: { task_id: options.taskId } } }
        : {}),
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return parseAgentLifecycleRequestResult(data);
}

/**
 * Request that a running workspace agent stops. Without `force` the backend
 * drains active work cooperatively before keeping the agent stopped; with
 * `force: true` it skips the cooperative yield and terminates directly.
 */
export async function stopAgent(
  workspaceId: string,
  agentName: string,
  options?: { force?: boolean },
): Promise<AgentLifecycleRequestResult> {
  const { data, error, response } = await api.POST(
    "/api/workspaces/{ws}/agents/{name}/stop",
    {
      params: { path: { ws: workspaceId, name: agentName } },
      ...(options?.force ? { body: { force: true } } : {}),
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return parseAgentLifecycleRequestResult(data);
}

/**
 * Request that a workspace agent restarts. Supervised runtimes complete the
 * stop + start asynchronously after accepting the request.
 */
export async function restartAgent(
  workspaceId: string,
  agentName: string,
): Promise<AgentLifecycleRequestResult> {
  const { data, error, response } = await api.POST(
    "/api/workspaces/{ws}/agents/{name}/restart",
    {
      params: { path: { ws: workspaceId, name: agentName } },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return parseAgentLifecycleRequestResult(data);
}

/**
 * Read the authoritative lifecycle command state for one supervised agent.
 * This route is intentionally called through the handwritten helper until the
 * next generated OpenAPI refresh lands.
 */
export async function getAgentLifecycleCommand(
  workspaceId: string,
  agentName: string,
  commandID: string,
  options?: { signal?: AbortSignal },
): Promise<AgentLifecycleCommandResult> {
  const result = await get<unknown>(
    wsUrl(
      workspaceId,
      `/agents/${encodeURIComponent(
        agentName,
      )}/lifecycle-commands/${encodeURIComponent(commandID)}`,
    ),
    options?.signal == null ? undefined : { signal: options.signal },
  );
  return parseAgentLifecycleCommandResult(result);
}

/**
 * Fetched status result type.
 */
export interface FetchStatusResult {
  agents: LoomAgentStatus[];
  tasks: LoomTaskSummary;
  taskLists: LoomTaskLists;
  agentTasks: Record<string, LoomTaskInfo>;
  sync: LoomSyncInfo;
  stats: LoomStats;
  timestamp: string;
}

export interface AgentRecordBehavior {
  role_name?: string;
  driver_id?: string;
  driver_version_id?: string;
}

export interface AgentRecordBinding {
  binding_id: string;
  name?: string;
  source_kind?: string;
  event_type_patterns?: string[];
  schedule?: string;
  schedule_timezone?: string;
  enabled?: boolean;
}

export interface AgentRecord {
  id: string;
  name: string;
  kind: string;
  enabled: boolean;
  behavior: AgentRecordBehavior;
  workspace_key: string;
  bindings?: AgentRecordBinding[];
  created_at?: string;
  updated_at?: string;
}

/** Identity fields shared by prompt/scripted records in the unified list. */
export type AgentRecordSummary = Pick<AgentRecord, "id" | "name" | "kind">;

export interface PromptRoleCreateInput {
  prompt?: string;
  prompt_filename?: string;
  description?: string;
  task_filter?: string;
  model?: string;
  backend?: string;
  effort?: string;
  read_only?: boolean;
  allowed_tools?: string[];
  denied_tools?: string[];
  skills?: string[];
}

export interface CreatePromptAgentRecordRequest {
  kind: "prompt";
  name: string;
  backend?: string;
  behavior: {
    role_name: string;
    role_create?: PromptRoleCreateInput;
  };
  trigger?: {
    source_kind?: "internal" | "cron" | string;
    binding_id?: string;
    event_type_patterns?: string[];
    schedule?: string;
    schedule_timezone?: string;
  };
  enabled?: boolean;
}

/** Mutable fields on a durable prompt/scripted AgentService record. */
export interface UpdateAgentRecordRequest {
  name?: string;
  behavior?: {
    role_name?: string;
  };
  budget_policy?: string;
}

/** Result of archiving an AgentService and deleting all attached bindings. */
export interface DeleteAgentRecordResult {
  agent: AgentRecord;
  archived: boolean;
  bindings_deleted: number;
  grants_revoked: number;
}

interface UnifiedAgentListResponse {
  success: boolean;
  data: AgentRecordSummary[];
  total: number;
}

/**
 * Fetch full status from the loom server.
 * Throws on network errors or invalid responses so callers can handle connection state.
 */
export async function fetchStatus(
  workspaceId?: string,
): Promise<FetchStatusResult> {
  if (workspaceId) {
    const d = await get<LoomStatusResponse>(
      wsUrl(workspaceId, "/monitor/status"),
      {
        signal: AbortSignal.timeout(15000),
      },
    );
    return statusResponseToResult(d);
  }

  const { data, error, response } = await api.GET("/api/monitor/status", {
    signal: AbortSignal.timeout(15000),
  });
  if (error) throw apiErrorFromResponse(error, response);
  return statusResponseToResult(data! as unknown as LoomStatusResponse);
}

function statusResponseToResult(d: LoomStatusResponse): FetchStatusResult {
  return {
    agents: (d.agents ?? []) as unknown as LoomAgentStatus[],
    tasks: d.tasks as unknown as LoomTaskSummary,
    taskLists: tasksResponseToLists(d),
    agentTasks: (d.agent_tasks ?? {}) as unknown as Record<
      string,
      LoomTaskInfo
    >,
    sync: d.sync as unknown as LoomSyncInfo,
    stats: d.stats as unknown as LoomStats,
    timestamp: d.timestamp,
  };
}

/**
 * Fetch task lists from the loom server.
 * Throws on network errors or invalid responses so callers can handle connection state.
 */
export async function fetchTasks(workspaceId?: string): Promise<LoomTaskLists> {
  if (workspaceId) {
    const d = await get<LoomTasksResponse>(
      monitorPath("/api/monitor/tasks", workspaceId),
      { signal: AbortSignal.timeout(15000) },
    );
    return tasksResponseToLists(d);
  }

  const { data, error, response } = await api.GET("/api/monitor/tasks", {
    signal: AbortSignal.timeout(15000),
  });
  if (error) throw apiErrorFromResponse(error, response);
  return tasksResponseToLists(data! as unknown as LoomTasksResponse);
}

type MonitorTaskListFields = Partial<
  Pick<
    LoomTasksResponse,
    | "needs_planning"
    | "ready_to_implement"
    | "needs_review"
    | "in_progress"
    | "closed"
    | "backlog"
  > & {
    in_progress_list: LoomTaskInfo[];
  }
>;

function tasksResponseToLists(d: MonitorTaskListFields): LoomTaskLists {
  return {
    needsPlanning: d.needs_planning ?? [],
    readyToImplement: d.ready_to_implement ?? [],
    needsReview: d.needs_review ?? [],
    inProgress: d.in_progress ?? d.in_progress_list ?? [],
    backlog: d.backlog ?? [],
    done: d.closed ?? [],
  } as unknown as LoomTaskLists;
}

/**
 * Enable/disable an AGENT identity record (the durable agent — not a raw
 * trigger binding). Sets the record's desired_state and fans out enabled to
 * every attached binding. This is the ONLY enable path for attached bindings:
 * the binding-scoped enable/disable routes reject them with 409
 * ("managed by agent") so the record stays the single source of truth.
 */
export async function setAgentRecordEnabled(
  workspaceId: string,
  agentId: string,
  enabled: boolean,
): Promise<void> {
  await post<unknown>(
    wsUrl(
      workspaceId,
      `/agents/${encodeURIComponent(agentId)}/${enabled ? "enable" : "disable"}`,
    ),
    undefined,
  );
}

/**
 * List durable prompt/scripted identities from the unified agent collection.
 * The collection also contains supervised and legacy binding entries, which
 * are deliberately excluded: only AgentService records can own the display
 * name of an attached binding.
 */
export async function listAgentRecords(
  workspaceId: string,
): Promise<AgentRecordSummary[]> {
  const response = await get<UnifiedAgentListResponse>(
    wsUrl(workspaceId, "/agents"),
  );
  return (response.data ?? []).filter(
    (item) => item.kind === "prompt" || item.kind === "scripted",
  );
}

/**
 * Update the durable AgentService identity behind an attached prompt/scripted
 * binding. Identity fields such as name belong to the agent record, not to one
 * of its trigger bindings.
 */
export async function updateAgentRecord(
  workspaceId: string,
  agentId: string,
  req: UpdateAgentRecordRequest,
): Promise<AgentRecord> {
  return patch<AgentRecord>(
    wsUrl(workspaceId, `/agents/${encodeURIComponent(agentId)}`),
    req,
  );
}

/**
 * Archive a durable AgentService. The unified agent endpoint owns cleanup of
 * every attached binding and connector grant, so callers must not delete only
 * the currently displayed binding.
 */
export async function deleteAgentRecord(
  workspaceId: string,
  agentId: string,
): Promise<DeleteAgentRecordResult> {
  return del<DeleteAgentRecordResult>(
    wsUrl(workspaceId, `/agents/${encodeURIComponent(agentId)}`),
  );
}

/**
 * Transactionally create a prompt-backed agent record plus its trigger binding.
 * The server ensures a new role when `behavior.role_create` is present.
 */
export async function createPromptAgentRecord(
  workspaceId: string,
  req: CreatePromptAgentRecordRequest,
): Promise<AgentRecord> {
  return post<AgentRecord>(wsUrl(workspaceId, "/agents"), req, {
    timeout: 120_000,
  });
}
