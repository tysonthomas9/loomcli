/** Terminal API functions. openapi-fetch for typed endpoints, raw fetch for untyped.
 *
 * The tmux-era endpoints (spawn, restart, kill, session-status, seed,
 * lead-session, close-all, scrollback, scrollback-info, export,
 * list-sessions) were removed during terminal simplification. What remains is
 * WebSocket auth, tab metadata CRUD (Redis), terminal UI state (Redis),
 * and the cross-workspace "sessions by issue" lookup.
 */
import {
  api,
  ApiError,
  apiErrorFromResponse,
  unwrapResponse,
  get,
  post,
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
  kind?: string;
  agent_id?: string;
  role?: string;
  backend?: string;
  writable?: boolean;
  launch?: {
    argv?: string[];
    env?: Record<string, string>;
  };
  created_at: string;
  updated_at: string;
  /**
   * Whether connecting to this tab will yield a working PTY — true for a
   * live PTY, and for a tab created during this server process (its PTY is
   * spawned by the first WebSocket). Not a process-liveness check. false
   * means the tab metadata outlived its server or its PTY exited, so the
   * UI should render the tab as "session ended" and prompt before
   * reconnecting, since reconnecting spawns a fresh session.
   */
  attachable: boolean;
  /**
   * Number of concurrent WebSocket clients currently viewing this session.
   * 0 = no one attached; ≥2 = multi-viewer state the UI can surface before
   * destructive tab-close actions.
   */
  attached_clients: number;
  /**
   * RFC3339 timestamp of the last time this tab's shell was replaced because
   * the server restarted. Persisted server-side, so it survives a page
   * reload; absent when the tab has never been replaced (or the notice was
   * dismissed).
   */
  replaced_at?: string;
  /** Server-written reason for the replacement, e.g. "server_restart". */
  replaced_reason?: string;
}

export async function ensureAgentTerminalSession(
  workspaceId: string,
  agentName: string,
): Promise<TabMetadata> {
  const response = await post<{
    success: boolean;
    data?: TabMetadata;
    error?: string;
  }>(
    wsUrl(
      workspaceId,
      `/agents/${encodeURIComponent(agentName)}/terminal/session`,
    ),
    {},
  );
  if (!response.success || !response.data) {
    throw new Error(response.error || "Failed to resolve agent terminal");
  }
  return response.data;
}

export interface TerminalSetupResult {
  session_name: string;
  label: string;
  backend: string;
  action: string;
  command: string;
  title: string;
  message: string;
  manual: boolean;
  created: boolean;
}

type TerminalSetupApiResponse =
  | { success: true; data: TerminalSetupResult }
  | { success: false; error: string };

function unwrapTerminalSetupResponse(
  response: TerminalSetupApiResponse,
): TerminalSetupResult {
  if (!response.success) {
    throw new ApiError(0, response.error);
  }
  return response.data;
}

/**
 * Start a typed backend-owned setup command in a workspace terminal.
 */
export async function startTerminalSetup(
  workspaceId: string,
  backend: string,
  action: string,
): Promise<TerminalSetupResult> {
  const response = await post<TerminalSetupApiResponse>(
    wsUrl(workspaceId, "/terminal/setup"),
    { backend, action },
  );
  return unwrapTerminalSetupResponse(response);
}

/**
 * List all tab metadata from GET /api/workspaces/{workspace}/terminal/tabs.
 *
 * Throws ApiError on any failure — including 404 (no Redis) and 503 (Redis
 * down). Those two used to be swallowed into an empty array, which made an
 * unavailable metadata store indistinguishable from a workspace that genuinely
 * has no tabs. Callers classify: useTerminalMetadata treats 404/503 as a
 * settled-empty degraded mode and every other status as a real error.
 */
export async function listTabMetadata(
  workspaceId: string,
): Promise<TabMetadata[]> {
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/terminal/tabs",
    {
      params: { path: { ws: workspaceId } },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return (unwrapResponse(data, response) ?? []) as unknown as TabMetadata[];
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
  return unwrapResponse(data, response) as unknown as TabMetadata;
}

/**
 * Patch tab metadata fields via PATCH /api/workspaces/{ws}/terminal/tabs/{session}.
 */
export async function patchTabMetadata(
  workspaceId: string,
  session: string,
  fields: Partial<
    Pick<TabMetadata, "label" | "notes" | "pinned" | "sort_order" | "issue_id">
  >,
): Promise<TabMetadata> {
  const { data, error, response } = await api.PATCH(
    "/api/workspaces/{ws}/terminal/tabs/{session}",
    {
      params: { path: { ws: workspaceId, session } },
      body: fields as unknown as Record<string, unknown>,
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return unwrapResponse(data, response) as unknown as TabMetadata;
}

/**
 * Put (create/replace) tab metadata via PUT /api/workspaces/{ws}/terminal/tabs/{session}.
 */
export async function putTabMetadata(
  workspaceId: string,
  session: string,
  meta: Omit<
    TabMetadata,
    "created_at" | "updated_at" | "attachable" | "attached_clients"
  >,
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
 * Uses raw fetch because the spec response is untyped.
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
