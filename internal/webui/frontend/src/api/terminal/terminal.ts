/** Terminal API functions. openapi-fetch for typed endpoints, raw fetch for untyped.
 *
 * The tmux-era endpoints (spawn, restart, kill, session-status, seed,
 * lead-session, close-all, scrollback, scrollback-info, export,
 * list-sessions) were removed with the wterm migration. What remains is
 * WebSocket delivery, Interaction-owned tab intents and projections,
 * presentation-only active-tab preference, and the cross-workspace
 * "sessions by issue" lookup.
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
import type { components } from "@/types/generated/openapi";

type TabPutRequest = components["schemas"]["TabPutRequest"];
type TerminalSetupRequest = components["schemas"]["TerminalSetupRequest"];
export type TerminalBackend = TabPutRequest["backend"];
export type TerminalSetupBackend = TerminalSetupRequest["backend"];
export type TerminalSetupAction = TerminalSetupRequest["action"];

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

export type TabMetadata = components["schemas"]["TabMetadata"];

export async function ensureAgentTerminalSession(
  workspaceId: string,
  agentName: string,
): Promise<TabMetadata> {
  const { data, error, response } = await api.POST(
    "/api/workspaces/{ws}/agents/{name}/terminal/session",
    {
      params: { path: { ws: workspaceId, name: agentName } },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return unwrapResponse(data, response);
}

export type TerminalSetupResult = components["schemas"]["TerminalSetupResult"];

/**
 * Start a typed backend-owned setup command in a workspace terminal.
 */
export async function startTerminalSetup(
  workspaceId: string,
  backend: TerminalSetupBackend,
  action: TerminalSetupAction,
): Promise<TerminalSetupResult> {
  const { data, error, response } = await api.POST(
    "/api/workspaces/{ws}/terminal/setup",
    {
      params: { path: { ws: workspaceId } },
      body: { backend, action },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return unwrapResponse(data, response);
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
    return (unwrapResponse(data, response) ?? []) as unknown as TabMetadata[];
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
  meta: TabPutRequest,
): Promise<void> {
  const { error, response } = await api.PUT(
    "/api/workspaces/{ws}/terminal/tabs/{session}",
    {
      params: { path: { ws: workspaceId, session } },
      body: meta,
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
 */
export async function listSessionsByIssue(
  workspaceId: string,
): Promise<Record<string, string[]>> {
  try {
    const { data, error, response } = await api.GET(
      "/api/workspaces/{ws}/terminal/sessions/by-issue",
      {
        params: { path: { ws: workspaceId }, query: {} },
      },
    );
    if (error) throw apiErrorFromResponse(error, response);
    return unwrapResponse(data, response);
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
