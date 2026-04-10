/** Terminal API functions. openapi-fetch for typed endpoints, legacy for untyped. */
import {
  api,
  ApiError,
  apiErrorFromResponse,
  unwrapResponse,
  get,
  wsUrl,
  getApiOrigin,
  getWsBaseUrl,
} from "@/api/common";

export interface TerminalSessionInfo {
  name: string;
  label: string;
  created: number;
}

/**
 * List active terminal sessions from GET /api/workspaces/{ws}/terminal/sessions.
 */
export async function listTerminalSessions(
  workspaceId: string,
): Promise<TerminalSessionInfo[]> {
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/terminal/sessions",
    {
      params: { path: { ws: workspaceId } },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return (data.data?.sessions ?? []) as unknown as TerminalSessionInfo[];
}

/**
 * Fetch a one-time terminal auth token for the given session.
 * Returns null on failure (WebSocket will be rejected by server).
 */
export async function fetchTerminalToken(
  workspaceId: string,
  sessionName: string,
): Promise<string | null> {
  try {
    const resp = await get<{ token: string }>(
      wsUrl(
        workspaceId,
        `/terminal/token?session=${encodeURIComponent(sessionName)}`,
      ),
    );
    return resp.token;
  } catch {
    return null;
  }
}

/**
 * Build the WebSocket URL for the terminal relay endpoint.
 */
export function buildTerminalWsUrl(
  workspaceId: string,
  sessionName: string,
  token: string | null,
): string {
  let url = `${getWsBaseUrl()}${wsUrl(workspaceId, "/terminal/ws")}?session=${encodeURIComponent(sessionName)}`;
  if (token) {
    url += `&token=${encodeURIComponent(token)}`;
  }
  return url;
}

/**
 * Pre-create a tmux session for the given backend via POST /api/workspaces/{ws}/terminal/spawn.
 */
export async function spawnTerminalSession(
  workspaceId: string,
  sessionName: string,
  backend: string,
): Promise<void> {
  const { error, response } = await api.POST(
    "/api/workspaces/{ws}/terminal/spawn",
    {
      params: { path: { ws: workspaceId } },
      body: { session_name: sessionName, backend },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
}

/**
 * Restart a terminal session with the current backend.
 * POST /api/workspaces/{ws}/terminal/restart?session=<name>&token=<token>
 * Uses legacy client (spec doesn't support query params on this endpoint).
 */
export async function restartTerminalSession(
  workspaceId: string,
  sessionName: string,
  token: string | null,
): Promise<{ success: boolean; backend: string }> {
  const { post } = await import("@/api/common");
  let url = wsUrl(
    workspaceId,
    `/terminal/restart?session=${encodeURIComponent(sessionName)}`,
  );
  if (token) {
    url += `&token=${encodeURIComponent(token)}`;
  }
  return post<{ success: boolean; backend: string }>(url, {});
}

/**
 * Forcibly kill a terminal session (for hung backends).
 * POST /api/workspaces/{ws}/terminal/kill?session=<name>&token=<token>
 * Uses legacy client (spec doesn't support query params on this endpoint).
 */
export async function killTerminalSession(
  workspaceId: string,
  sessionName: string,
  token: string | null,
): Promise<void> {
  const { post } = await import("@/api/common");
  let url = wsUrl(
    workspaceId,
    `/terminal/kill?session=${encodeURIComponent(sessionName)}`,
  );
  if (token) {
    url += `&token=${encodeURIComponent(token)}`;
  }
  await post<{ success: boolean }>(url, {});
}

/**
 * Check whether a terminal session's backend is alive.
 * GET /api/workspaces/{ws}/terminal/session-status?session=<name>
 */
export async function getSessionStatus(
  workspaceId: string,
  sessionName: string,
): Promise<{ alive: boolean; exit_reason?: string }> {
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/terminal/session-status",
    {
      params: {
        path: { ws: workspaceId },
        query: { session: sessionName },
      },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  const result: { alive: boolean; exit_reason?: string } = {
    alive: data.alive ?? false,
  };
  if (data.exit_reason !== undefined) result.exit_reason = data.exit_reason;
  return result;
}

export interface LeadSessionResult {
  session_name: string;
  backend: string;
}

/**
 * Create a new tmux session running `loom lead --backend <backend> --message <text>`.
 * The user's message is baked into the loom-lead invocation so the agent receives it
 * as part of its initial prompt — no post-hoc send-keys or readiness polling needed.
 */
export async function createLeadSession(
  workspaceId: string,
  message: string,
  backend: string,
): Promise<LeadSessionResult> {
  const { post } = await import("@/api/common");
  const response = await post<{
    success: boolean;
    data?: LeadSessionResult;
    error?: string;
  }>(wsUrl(workspaceId, "/terminal/lead-session"), { message, backend });
  if (!response.success || !response.data) {
    throw new ApiError(500, response.error ?? "failed to create lead session");
  }
  return response.data;
}

export interface IssueContext {
  issue_id: string;
  title: string;
  description?: string;
  design?: string;
  blockers?: Array<{ id: string; title: string }>;
}

/**
 * Seed a terminal session with issue context via POST /api/workspaces/{ws}/terminal/sessions/{name}/seed.
 */
export async function seedTerminalSession(
  workspaceId: string,
  sessionName: string,
  context: IssueContext,
): Promise<void> {
  const { error, response } = await api.POST(
    "/api/workspaces/{ws}/terminal/sessions/{name}/seed",
    {
      params: { path: { ws: workspaceId, name: sessionName } },
      body: (() => {
        const b: {
          issue_id: string;
          title: string;
          description?: string;
          design?: string;
          blockers?: { id: string; title: string }[];
        } = {
          issue_id: context.issue_id,
          title: context.title,
        };
        if (context.description !== undefined)
          b.description = context.description;
        if (context.design !== undefined) b.design = context.design;
        if (context.blockers !== undefined) b.blockers = context.blockers;
        return b;
      })(),
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
}

export interface TabMetadata {
  session_name: string;
  label: string;
  notes: string;
  sort_order: number;
  pinned: boolean;
  issue_id?: string;
  created_at: string;
  updated_at: string;
}

/**
 * List all tab metadata from GET /api/workspaces/{workspace}/terminal/tabs.
 * Returns an empty array when tab metadata is unavailable (404 = no Redis, 503 = Redis down).
 */
export async function listTabMetadata(
  workspaceId: string,
): Promise<TabMetadata[]> {
  try {
    const { data, error, response } = await api.GET(
      "/api/workspaces/{ws}/terminal/tabs",
      {
        params: { path: { ws: workspaceId } },
      },
    );
    if (error) throw apiErrorFromResponse(error, response);
    return (unwrapResponse(data) ?? []) as unknown as TabMetadata[];
  } catch (error) {
    if (
      error instanceof ApiError &&
      (error.status === 404 || error.status === 503)
    ) {
      return [];
    }
    throw error;
  }
}

/**
 * Get metadata for a single tab from GET /api/workspaces/{workspace}/terminal/tabs/{session}.
 */
export async function getTabMetadata(
  workspaceId: string,
  session: string,
): Promise<TabMetadata> {
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/terminal/tabs/{session}",
    {
      params: { path: { ws: workspaceId, session } },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return unwrapResponse(data!) as unknown as TabMetadata;
}

/**
 * Partially update tab metadata via PATCH /api/workspaces/{workspace}/terminal/tabs/{session}.
 */
export async function patchTabMetadata(
  workspaceId: string,
  session: string,
  fields: Partial<{
    label: string;
    notes: string;
    sort_order: number;
    pinned: boolean;
    issue_id: string;
  }>,
): Promise<TabMetadata> {
  const { error, response } = await api.PATCH(
    "/api/workspaces/{ws}/terminal/tabs/{session}",
    {
      params: { path: { ws: workspaceId, session } },
      body: fields,
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  // Refetch after patch since the response is just {success: boolean}
  return getTabMetadata(workspaceId, session);
}

/**
 * Create or replace tab metadata via PUT /api/workspaces/{workspace}/terminal/tabs/{session}.
 */
export async function putTabMetadata(
  workspaceId: string,
  session: string,
  fields: { label: string; sort_order: number; notes?: string },
): Promise<TabMetadata> {
  const { error, response } = await api.PUT(
    "/api/workspaces/{ws}/terminal/tabs/{session}",
    {
      params: { path: { ws: workspaceId, session } },
      body: {
        label: fields.label,
        sort_order: fields.sort_order,
        notes: fields.notes ?? "",
        pinned: false,
      },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  // Refetch after put since the response is just {success: boolean}
  return getTabMetadata(workspaceId, session);
}

/**
 * Delete tab metadata via DELETE /api/workspaces/{workspace}/terminal/tabs/{session}.
 */
export async function deleteTabMetadata(
  workspaceId: string,
  session: string,
): Promise<void> {
  const { error, response } = await api.DELETE(
    "/api/workspaces/{ws}/terminal/tabs/{session}",
    {
      params: { path: { ws: workspaceId, session } },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
}

/**
 * Schedule a deferred tmux session kill with grace period.
 * POST /api/workspaces/{ws}/terminal/sessions/{session}/kill
 * Pass force=true to kill immediately (for explicit user close).
 */
export async function scheduleSessionKill(
  workspaceId: string,
  sessionName: string,
  force = false,
): Promise<void> {
  const params: {
    path: { ws: string; session: string };
    query?: { force?: string };
  } = {
    path: { ws: workspaceId, session: sessionName },
  };
  if (force) {
    params.query = { force: "true" };
  }
  const { error, response } = await api.POST(
    "/api/workspaces/{ws}/terminal/sessions/{session}/kill",
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    { params: params as any },
  );
  if (error) throw apiErrorFromResponse(error, response);
}

/**
 * List sessions grouped by issue ID from GET /api/workspaces/{workspace}/terminal/sessions/by-issue.
 * Returns a map of issue_id → session_name[].
 * Uses legacy client (spec response is untyped).
 */
export async function listSessionsByIssue(
  workspaceId: string,
): Promise<Record<string, string[]>> {
  try {
    const response = await get<{
      success: boolean;
      data: Record<string, string[]>;
    }>(wsUrl(workspaceId, "/terminal/sessions/by-issue"));
    if (!response.success) return {};
    return response.data ?? {};
  } catch (error) {
    if (
      error instanceof ApiError &&
      (error.status === 404 || error.status === 503)
    ) {
      return {};
    }
    throw error;
  }
}

/**
 * Close all terminal sessions via POST /api/workspaces/{ws}/terminal/sessions/close-all.
 * Kills all tmux sessions and clears all tab metadata.
 */
export async function closeAllSessions(workspaceId: string): Promise<void> {
  const { error, response } = await api.POST(
    "/api/workspaces/{ws}/terminal/sessions/close-all",
    {
      params: { path: { ws: workspaceId } },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
}

/**
 * Fetch scrollback content for a terminal session.
 * GET /api/workspaces/{ws}/terminal/sessions/{session}/scrollback
 */
export async function fetchScrollback(
  workspaceId: string,
  sessionName: string,
): Promise<{ content: string; lines: number }> {
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/terminal/sessions/{session}/scrollback",
    {
      params: { path: { ws: workspaceId, session: sessionName } },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  const unwrapped = unwrapResponse(data);
  return {
    content: unwrapped?.content ?? "",
    lines: unwrapped?.lines ?? 0,
  };
}

export interface ScrollbackInfo {
  line_count: number;
  max_lines: number;
  truncated_count: number;
}

/**
 * Get scrollback buffer info for a terminal session.
 * Uses legacy client (spec response is untyped).
 */
export async function getScrollbackInfo(
  workspaceId: string,
  sessionName: string,
): Promise<ScrollbackInfo> {
  return get<ScrollbackInfo>(
    wsUrl(
      workspaceId,
      `/terminal/sessions/${encodeURIComponent(sessionName)}/scrollback-info`,
    ),
  );
}

/**
 * Export session scrollback as a downloadable file.
 * Returns the URL for direct browser download. Absolute when VITE_API_BASE_URL
 * is set so window.open() reaches the API server on a cross-origin deployment.
 */
export function getExportUrl(
  workspaceId: string,
  sessionName: string,
  format: "txt" | "md" = "txt",
): string {
  const path = wsUrl(
    workspaceId,
    `/terminal/sessions/${encodeURIComponent(sessionName)}/export?format=${format}`,
  );
  return `${getApiOrigin()}${path}`;
}

/**
 * Get persisted terminal UI state (active tab).
 * GET /api/workspaces/{ws}/terminal/state
 */
export async function getTerminalState(
  workspaceId: string,
): Promise<{ active_tab: string }> {
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/terminal/state",
    {
      params: { path: { ws: workspaceId } },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return { active_tab: data.active_tab ?? "" };
}

/**
 * Persist terminal UI state (active tab).
 * PATCH /api/workspaces/{ws}/terminal/state
 */
export async function patchTerminalState(
  workspaceId: string,
  state: {
    active_tab: string;
  },
): Promise<void> {
  const { error, response } = await api.PATCH(
    "/api/workspaces/{ws}/terminal/state",
    {
      params: { path: { ws: workspaceId } },
      body: { active_tab: state.active_tab },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
}
