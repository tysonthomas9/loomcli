import { get, post, put, patch, del, ApiError, wsUrl } from "./client";

// ============= Types =============

export interface TerminalSessionInfo {
  name: string;
  label: string;
  created: number;
}

// ============= Response Types =============

interface ApiSuccess<T> {
  success: true;
  data: T;
}

interface ApiFailure {
  success: false;
  error: string;
}

type ApiResult<T> = ApiSuccess<T> | ApiFailure;

function unwrap<T>(response: ApiResult<T>): T {
  if (!response.success) {
    throw new ApiError(0, response.error);
  }
  return response.data;
}

// ============= API Functions =============

/**
 * List active terminal sessions for the workspace.
 * GET /api/workspaces/{ws}/terminal/sessions
 */
export async function listTerminalSessions(
  workspaceId: string,
): Promise<TerminalSessionInfo[]> {
  const response = await get<ApiResult<{ sessions: TerminalSessionInfo[] }>>(
    wsUrl(workspaceId, "/terminal/sessions"),
  );
  return unwrap(response).sessions;
}

/**
 * Fetch a one-time terminal auth token for the given session.
 * Returns null on failure (WebSocket will be rejected by server).
 * GET /api/workspaces/{ws}/terminal/token?session=X
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
 * The workspace is now carried in the URL path rather than a query param,
 * so WorkspaceMiddleware can gate access on the server side.
 */
export function buildTerminalWsUrl(
  workspaceId: string,
  sessionName: string,
  token: string | null,
): string {
  const location =
    typeof window !== "undefined"
      ? window.location
      : (globalThis as { location?: Location }).location;
  const proto = location?.protocol === "https:" ? "wss:" : "ws:";
  const host = location?.host ?? "localhost";
  let url = `${proto}//${host}/api/workspaces/${encodeURIComponent(workspaceId)}/terminal/ws?session=${encodeURIComponent(sessionName)}`; // allow-url
  if (token) {
    url += `&token=${encodeURIComponent(token)}`;
  }
  return url;
}

// ============= Spawn Session =============

/**
 * Pre-create a tmux session for the given backend.
 * POST /api/workspaces/{ws}/terminal/spawn
 * Used for shell tabs to ensure the correct command before WebSocket connects.
 */
export async function spawnTerminalSession(
  workspaceId: string,
  sessionName: string,
  backend: string,
): Promise<void> {
  await post<{ success: boolean; data?: unknown; error?: string }>(
    wsUrl(workspaceId, "/terminal/spawn"),
    { session_name: sessionName, backend },
  );
}

// ============= Restart Session =============

/**
 * Restart a terminal session with the current backend.
 * POST /api/workspaces/{ws}/terminal/restart?session=X&token=Y
 */
export async function restartTerminalSession(
  workspaceId: string,
  sessionName: string,
  token: string | null,
): Promise<{ success: boolean; backend: string }> {
  let path = `/terminal/restart?session=${encodeURIComponent(sessionName)}`;
  if (token) {
    path += `&token=${encodeURIComponent(token)}`;
  }
  return post<{ success: boolean; backend: string }>(
    wsUrl(workspaceId, path),
    {},
  );
}

// ============= Kill Session =============

/**
 * Forcibly kill a terminal session (for hung backends).
 * POST /api/workspaces/{ws}/terminal/kill?session=X&token=Y
 */
export async function killTerminalSession(
  workspaceId: string,
  sessionName: string,
  token: string | null,
): Promise<void> {
  let path = `/terminal/kill?session=${encodeURIComponent(sessionName)}`;
  if (token) {
    path += `&token=${encodeURIComponent(token)}`;
  }
  await post<{ success: boolean }>(wsUrl(workspaceId, path), {});
}

// ============= Session Status =============

/**
 * Check whether a terminal session's backend is alive.
 * GET /api/workspaces/{ws}/terminal/session-status?session=X
 */
export async function getSessionStatus(
  workspaceId: string,
  sessionName: string,
): Promise<{ alive: boolean; exit_reason?: string }> {
  return get<{ alive: boolean; exit_reason?: string }>(
    wsUrl(
      workspaceId,
      `/terminal/session-status?session=${encodeURIComponent(sessionName)}`,
    ),
  );
}

// ============= Lead Session (--message) =============

export interface LeadSessionResult {
  session_name: string;
  backend: string;
}

/**
 * Create a new tmux session running `loom lead --backend <backend> --message <text>`.
 * The user's message is baked into the loom-lead invocation so the agent receives it
 * as part of its initial prompt — no post-hoc send-keys or readiness polling needed.
 *
 * POST /api/workspaces/{ws}/terminal/lead-session. The workspace ID now comes
 * from the URL path (via WorkspaceMiddleware) rather than a body field, and
 * the backend resolves it to an on-disk path so the tmux session starts with
 * its cwd at that workspace instead of inheriting the loom service's cwd.
 */
export async function createLeadSession(
  workspaceId: string,
  message: string,
  backend: string,
): Promise<LeadSessionResult> {
  const response = await post<ApiResult<LeadSessionResult>>(
    wsUrl(workspaceId, "/terminal/lead-session"),
    { message, backend },
  );
  return unwrap(response);
}

// ============= Issue Context Seeding =============

export interface IssueContext {
  issue_id: string;
  title: string;
  description?: string;
  design?: string;
  blockers?: Array<{ id: string; title: string }>;
}

/**
 * Seed a terminal session with issue context.
 * POST /api/workspaces/{ws}/terminal/sessions/{name}/seed
 */
export async function seedTerminalSession(
  workspaceId: string,
  sessionName: string,
  context: IssueContext,
): Promise<void> {
  await post<{ success: boolean }>(
    wsUrl(
      workspaceId,
      `/terminal/sessions/${encodeURIComponent(sessionName)}/seed`,
    ),
    context,
  );
}

// ============= Tab Metadata Types =============

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

// ============= Tab Metadata API Functions =============

/**
 * List all tab metadata from GET /api/workspaces/{workspace}/terminal/tabs.
 * Returns an empty array when tab metadata is unavailable (404 = no Redis, 503 = Redis down).
 */
export async function listTabMetadata(
  workspaceId: string,
): Promise<TabMetadata[]> {
  try {
    const response = await get<ApiResult<TabMetadata[]>>(
      wsUrl(workspaceId, "/terminal/tabs"),
    );
    return unwrap(response);
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
  const response = await get<ApiResult<TabMetadata>>(
    wsUrl(workspaceId, `/terminal/tabs/${encodeURIComponent(session)}`),
  );
  return unwrap(response);
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
  const response = await patch<ApiResult<TabMetadata>>(
    wsUrl(workspaceId, `/terminal/tabs/${encodeURIComponent(session)}`),
    fields,
  );
  return unwrap(response);
}

/**
 * Create or replace tab metadata via PUT /api/workspaces/{workspace}/terminal/tabs/{session}.
 */
export async function putTabMetadata(
  workspaceId: string,
  session: string,
  fields: { label: string; sort_order: number; notes?: string },
): Promise<TabMetadata> {
  const response = await put<ApiResult<TabMetadata>>(
    wsUrl(workspaceId, `/terminal/tabs/${encodeURIComponent(session)}`),
    fields,
  );
  return unwrap(response);
}

/**
 * Delete tab metadata via DELETE /api/workspaces/{workspace}/terminal/tabs/{session}.
 */
export async function deleteTabMetadata(
  workspaceId: string,
  session: string,
): Promise<void> {
  await del<ApiResult<undefined>>(
    wsUrl(workspaceId, `/terminal/tabs/${encodeURIComponent(session)}`),
  );
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
  const qs = force ? "?force=true" : "";
  await post<{ success: boolean }>(
    wsUrl(
      workspaceId,
      `/terminal/sessions/${encodeURIComponent(sessionName)}/kill${qs}`,
    ),
    {},
  );
}

// ============= Issue Session Management =============

/**
 * List sessions grouped by issue ID from GET /api/workspaces/{workspace}/terminal/sessions/by-issue.
 * Returns a map of issue_id → session_name[].
 * Returns an empty map when tab metadata is unavailable (404 = no Redis, 503 = Redis down).
 */
export async function listSessionsByIssue(
  workspaceId: string,
): Promise<Record<string, string[]>> {
  try {
    const response = await get<ApiResult<Record<string, string[]>>>(
      wsUrl(workspaceId, "/terminal/sessions/by-issue"),
    );
    return unwrap(response);
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
 * Close all terminal sessions belonging to the workspace.
 * POST /api/workspaces/{ws}/terminal/sessions/close-all
 * Kills the workspace's tmux sessions and clears its tab metadata.
 * Sessions in other workspaces are untouched — important for multi-VM
 * deployments where each loom instance only sees its own workspaces.
 */
export async function closeAllSessions(workspaceId: string): Promise<void> {
  await post<{ success: boolean }>(
    wsUrl(workspaceId, "/terminal/sessions/close-all"),
    {},
  );
}

// ============= Scrollback API =============

/**
 * Fetch scrollback content for a terminal session.
 * GET /api/workspaces/{ws}/terminal/sessions/{session}/scrollback
 */
export async function fetchScrollback(
  workspaceId: string,
  sessionName: string,
): Promise<{ content: string; lines: number }> {
  const response = await get<ApiResult<{ content: string; lines: number }>>(
    wsUrl(
      workspaceId,
      `/terminal/sessions/${encodeURIComponent(sessionName)}/scrollback`,
    ),
  );
  return unwrap(response);
}

// ============= Export API =============

export interface ScrollbackInfo {
  line_count: number;
  max_lines: number;
  truncated_count: number;
}

/**
 * Get scrollback buffer info for a terminal session.
 * GET /api/workspaces/{ws}/terminal/sessions/{session}/scrollback-info
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
 * Returns the URL for direct browser download.
 */
export function getExportUrl(
  workspaceId: string,
  sessionName: string,
  format: "txt" | "md" = "txt",
): string {
  return wsUrl(
    workspaceId,
    `/terminal/sessions/${encodeURIComponent(sessionName)}/export?format=${format}`,
  );
}

// ============= Terminal UI State =============

/**
 * Get persisted terminal UI state (active tab).
 * GET /api/workspaces/{ws}/terminal/state
 */
export async function getTerminalState(
  workspaceId: string,
): Promise<{ active_tab: string }> {
  return get<{ active_tab: string }>(wsUrl(workspaceId, "/terminal/state"));
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
  await patch<{ active_tab: string }>(
    wsUrl(workspaceId, "/terminal/state"),
    state,
  );
}
