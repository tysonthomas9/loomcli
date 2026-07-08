/**
 * Loom Agent API client.
 * Uses openapi-fetch generated client (monitor endpoints are in the spec).
 */

import type {
  LoomAgentStatus,
  LoomTaskSummary,
  LoomTaskInfo,
  LoomTaskLists,
  LoomTasksResponse,
  LoomSyncInfo,
  LoomStats,
  LoomStatusResponse,
} from "@/types";
import { api, apiErrorFromResponse, get, post, wsUrl } from "@/api/common";

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
