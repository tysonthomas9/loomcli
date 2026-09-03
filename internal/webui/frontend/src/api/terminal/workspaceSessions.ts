/**
 * API functions for workspace-scoped session audit trail endpoints.
 */

import type {
  SubagentListResponse,
  TranscriptEntry,
  TranscriptResponse,
  WorkspaceSessionFilters,
  WorkspaceSessionListData,
  WorkspaceSessionListResponse,
  WorkspaceSessionListItem,
  WorkspaceTraceRunData,
  WorkspaceTraceRunResponse,
} from "@/types/agent";

import { ApiError, get, getText, unwrapResponse, wsUrl } from "@/api/common";

type Envelope<T> = {
  success: boolean;
  data?: T;
  error?: string;
};

function appendQuery(
  path: string,
  params: Record<string, string | number | string[] | undefined>,
): string {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === "") continue;
    if (Array.isArray(value)) {
      for (const item of value) query.append(key, item);
      continue;
    }
    query.set(key, String(value));
  }
  const qs = query.toString();
  return qs ? `${path}?${qs}` : path;
}

export function buildWorkspaceSessionsUrl(
  workspaceId: string,
  filters: WorkspaceSessionFilters = {},
): string {
  return appendQuery(wsUrl(workspaceId, "/sessions"), {
    since: filters.since,
    until: filters.until,
    status: filters.status,
    agent_id: filters.agent_id,
    task_run_id: filters.task_run_id,
    tag: filters.tags,
    kind: filters.kind,
    limit: filters.limit,
  });
}

export async function listWorkspaceSessions(
  workspaceId: string,
  filters: WorkspaceSessionFilters = {},
): Promise<WorkspaceSessionListData> {
  const envelope = await get<WorkspaceSessionListResponse>(
    buildWorkspaceSessionsUrl(workspaceId, filters),
  );
  const data = unwrapResponse(envelope);
  return {
    sessions: data.sessions ?? [],
    total: data.total ?? 0,
    limit: data.limit ?? filters.limit ?? 0,
    score_dimensions: data.score_dimensions ?? [],
  };
}

export async function getWorkspaceTraceRun(
  workspaceId: string,
  taskRunId: string,
): Promise<WorkspaceTraceRunData> {
  const envelope = await get<WorkspaceTraceRunResponse>(
    wsUrl(workspaceId, `/traces/runs/${encodeURIComponent(taskRunId)}`),
  );
  return unwrapResponse(envelope);
}

export async function getWorkspaceSession(
  workspaceId: string,
  sessionId: string,
): Promise<WorkspaceSessionListItem | null> {
  try {
    const envelope = await get<Envelope<WorkspaceSessionListItem>>(
      wsUrl(workspaceId, `/sessions/${encodeURIComponent(sessionId)}`),
    );
    return unwrapResponse(envelope) ?? null;
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) return null;
    throw err;
  }
}

export async function getWorkspaceSessionTranscript(
  workspaceId: string,
  sessionId: string,
): Promise<TranscriptEntry[]> {
  try {
    const envelope = await get<TranscriptResponse>(
      wsUrl(
        workspaceId,
        `/sessions/${encodeURIComponent(sessionId)}/transcript`,
      ),
    );
    return unwrapResponse(envelope)?.entries ?? [];
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) return [];
    throw err;
  }
}

export async function getWorkspaceSessionDiff(
  workspaceId: string,
  sessionId: string,
): Promise<string | null> {
  try {
    return await getText(
      wsUrl(workspaceId, `/sessions/${encodeURIComponent(sessionId)}/diff`),
    );
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) return null;
    throw err;
  }
}

export async function listWorkspaceSessionSubagents(
  workspaceId: string,
  sessionId: string,
): Promise<string[]> {
  try {
    const envelope = await get<SubagentListResponse>(
      wsUrl(
        workspaceId,
        `/sessions/${encodeURIComponent(sessionId)}/subagents`,
      ),
    );
    return unwrapResponse(envelope)?.subagent_ids ?? [];
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) return [];
    throw err;
  }
}

export async function getWorkspaceSessionSubagentTranscript(
  workspaceId: string,
  sessionId: string,
  subagentId: string,
): Promise<TranscriptEntry[]> {
  try {
    const envelope = await get<TranscriptResponse>(
      wsUrl(
        workspaceId,
        `/sessions/${encodeURIComponent(sessionId)}/subagents/${encodeURIComponent(
          subagentId,
        )}/transcript`,
      ),
    );
    return unwrapResponse(envelope)?.entries ?? [];
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) return [];
    throw err;
  }
}
