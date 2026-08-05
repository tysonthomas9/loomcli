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
import type { WorkflowRun } from "@/api/workflows";
import type { components } from "@/types/generated/openapi";

function monitorPath(path: string, workspaceId?: string): string {
  if (!workspaceId) return path;
  return `${path}?workspace=${encodeURIComponent(workspaceId)}`;
}

export type AgentLifecycleAction = "start" | "stop" | "restart";

export type AgentLifecycleStatus = "succeeded" | "failed" | "cancelled";

export interface AgentLifecycleRequestResult {
  message: string;
  status: AgentLifecycleStatus;
}

const agentLifecycleStatuses = new Set<AgentLifecycleStatus>([
  "succeeded",
  "failed",
  "cancelled",
]);

function parseAgentLifecycleRequestResult(
  value: unknown,
): AgentLifecycleRequestResult {
  if (value == null || typeof value !== "object" || Array.isArray(value)) {
    throw new Error("Invalid agent lifecycle response");
  }
  const response = value as Record<string, unknown>;
  const status = response.status;
  if (
    typeof response.message !== "string" ||
    typeof status !== "string" ||
    !agentLifecycleStatuses.has(status as AgentLifecycleStatus)
  ) {
    throw new Error("Invalid agent lifecycle response");
  }
  return { message: response.message, status: status as AgentLifecycleStatus };
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
): Promise<AgentLifecycleRequestResult> {
  const { data, error, response } = await api.POST(
    "/api/workspaces/{ws}/agents/{name}/start",
    {
      params: { path: { ws: workspaceId, name: agentName } },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return parseAgentLifecycleRequestResult(data);
}

/**
 * Request that a running workspace agent stops through canonical desired
 * state. Interactive runtime interruption is coordinated by the server.
 */
export async function stopAgent(
  workspaceId: string,
  agentName: string,
): Promise<AgentLifecycleRequestResult> {
  const { data, error, response } = await api.POST(
    "/api/workspaces/{ws}/agents/{name}/stop",
    {
      params: { path: { ws: workspaceId, name: agentName } },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return parseAgentLifecycleRequestResult(data);
}

/**
 * Request that a workspace agent restarts through canonical desired state.
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

export type AgentRecordBinding = components["schemas"]["AgentRecordBinding"];

export interface AgentRecord {
  id: string;
  name: string;
  kind: string;
  enabled: boolean;
  behavior: AgentRecordBehavior;
  workspace_key: string;
  bindings?: AgentRecordBinding[];
  budget_policy?: string;
  last_run_status?: string;
  consecutive_failures?: number;
  next_fire_at?: string;
  metadata?: Record<string, string>;
  created_at?: string;
  updated_at?: string;
}

/**
 * Prompt/scripted records in the unified list already carry their complete
 * record state. Keep that state instead of reducing the response to identity
 * fields: record routes and orphan recovery cannot infer desired state,
 * behavior, or aggregate health from a trigger binding that may not exist.
 */
export type AgentRecordSummary = AgentRecord;

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
  budget_policy?: string;
  /** Exact attached managed binding whose cron configuration is changing. */
  binding_id?: string;
  schedule?: string;
  schedule_timezone?: string;
}

/** Result of archiving an AgentService and deleting all attached bindings. */
export interface DeleteAgentRecordResult {
  agent: AgentRecord;
  archived: boolean;
  bindings_deleted: number;
  grants_revoked: number;
}

export type AgentHistorySession = components["schemas"]["AgentHistorySession"];
export type AgentHistorySessionStatus = AgentHistorySession["status"];
export type AgentRunsResponse = Omit<
  components["schemas"]["AgentRunsResponse"],
  "runs"
> & {
  runs: WorkflowRun[];
};
export type AgentActivity = components["schemas"]["AgentActivity"];
export type AgentActivityResponse =
  components["schemas"]["AgentActivityResponse"];

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
 * Interactive identities are deliberately excluded because this query feeds
 * Automation binding ownership and workflow-run history.
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

/** List one agent's durable workflow runs or interactive session history. */
export async function listAgentRuns(
  workspaceId: string,
  agentId: string,
  opts?: { limit?: number },
): Promise<AgentRunsResponse> {
  const params = new URLSearchParams();
  if (opts?.limit !== undefined) params.set("limit", String(opts.limit));
  const query = params.toString();
  return get<AgentRunsResponse>(
    wsUrl(
      workspaceId,
      `/agents/${encodeURIComponent(agentId)}/runs${query ? `?${query}` : ""}`,
    ),
  );
}

/** List Interaction-owned session and Execution batch activity for one agent. */
export async function listAgentActivity(
  workspaceId: string,
  agentId: string,
  opts?: { limit?: number },
): Promise<AgentActivityResponse> {
  const params = new URLSearchParams();
  if (opts?.limit !== undefined) params.set("limit", String(opts.limit));
  const query = params.toString();
  return get<AgentActivityResponse>(
    wsUrl(
      workspaceId,
      `/agents/${encodeURIComponent(agentId)}/activity${query ? `?${query}` : ""}`,
    ),
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
