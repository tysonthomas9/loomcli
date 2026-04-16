/** Terminal API functions. openapi-fetch for typed endpoints, legacy for untyped.
 *
 * The tmux-era endpoints (spawn, restart, kill, session-status, seed,
 * lead-session, close-all, scrollback, scrollback-info, export,
 * list-sessions) were removed with the wterm migration. What remains is
 * WebSocket auth, tab metadata CRUD (Redis), terminal UI state (Redis),
 * and the cross-workspace "sessions by issue" lookup.
 */
import {
  api,
  ApiError,
  apiErrorFromResponse,
  unwrapResponse,
  get,
  wsUrl,
  getWsBaseUrl,
} from "@/api/common";

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

export interface IssueContext {
  issue_id: string;
  title: string;
  description?: string;
  design?: string;
  blockers?: Array<{ id: string; title: string }>;
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
 * Patch tab metadata fields via PATCH /api/workspaces/{ws}/terminal/tabs/{session}.
 */
export async function patchTabMetadata(
  workspaceId: string,
  session: string,
  fields: Partial<Pick<TabMetadata, "label" | "notes" | "pinned" | "sort_order" | "issue_id">>,
): Promise<TabMetadata> {
  const { data, error, response } = await api.PATCH(
    "/api/workspaces/{ws}/terminal/tabs/{session}",
    {
      params: { path: { ws: workspaceId, session } },
      body: fields as unknown as Record<string, unknown>,
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return unwrapResponse(data!) as unknown as TabMetadata;
}

/**
 * Put (create/replace) tab metadata via PUT /api/workspaces/{ws}/terminal/tabs/{session}.
 */
export async function putTabMetadata(
  workspaceId: string,
  session: string,
  meta: Omit<TabMetadata, "created_at" | "updated_at">,
): Promise<void> {
  const { error, response } = await api.PUT(
    "/api/workspaces/{ws}/terminal/tabs/{session}",
    {
      params: { path: { ws: workspaceId, session } },
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      body: meta as any,
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
}

/**
 * Delete tab metadata via DELETE /api/workspaces/{ws}/terminal/tabs/{session}.
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
