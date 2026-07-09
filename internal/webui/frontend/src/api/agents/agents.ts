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
import { api, apiErrorFromResponse, get, post, wsUrl } from "@/api/common";

function monitorPath(path: string, workspaceId?: string): string {
  if (!workspaceId) return path;
  return `${path}?workspace=${encodeURIComponent(workspaceId)}`;
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
): Promise<void> {
  await post<unknown>(
    wsUrl(workspaceId, `/agents/${encodeURIComponent(agentName)}/start`),
    options?.taskId ? { payload: { task_id: options.taskId } } : undefined,
  );
}

/**
 * Request that a running workspace agent stops. Without `force` the backend
 * sends a graceful yield (202, poll GET /agents to see it wind down); with
 * `force: true` it hard-stops the agent (200). Mirrors the agentcontrol HTTP
 * surface (internal/webui/handlers/agentcontrol).
 */
export async function stopAgent(
  workspaceId: string,
  agentName: string,
  options?: { force?: boolean },
): Promise<void> {
  await post<unknown>(
    wsUrl(workspaceId, `/agents/${encodeURIComponent(agentName)}/stop`),
    options?.force ? { force: true } : undefined,
  );
}

/**
 * Request that a workspace agent restarts (stop + start in one daemon call).
 */
export async function restartAgent(
  workspaceId: string,
  agentName: string,
): Promise<void> {
  await post<unknown>(
    wsUrl(workspaceId, `/agents/${encodeURIComponent(agentName)}/restart`),
    undefined,
  );
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
