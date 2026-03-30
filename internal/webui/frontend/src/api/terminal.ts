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

// TODO(workspace-routing): migrate to wsUrl when workspace-scoped route lands
/**
 * List active terminal sessions from GET /api/terminal/sessions.
 */
export async function listTerminalSessions(
  _workspaceId: string,
): Promise<TerminalSessionInfo[]> {
  const response = await get<ApiResult<{ sessions: TerminalSessionInfo[] }>>(
    "/api/terminal/sessions",
  );
  return unwrap(response).sessions;
}

/**
 * Fetch a one-time terminal auth token for the given session.
 * Returns null on failure (WebSocket will be rejected by server).
 */
// TODO(workspace-routing): migrate to wsUrl when workspace-scoped route lands
export async function fetchTerminalToken(
  _workspaceId: string,
  sessionName: string,
): Promise<string | null> {
  try {
    const resp = await get<{ token: string }>(
      `/api/terminal/token?session=${encodeURIComponent(sessionName)}`, // allow-url
    );
    return resp.token;
  } catch {
    return null;
  }
}

/**
 * Build the WebSocket URL for the terminal relay endpoint.
 */
// TODO(workspace-routing): migrate to wsUrl when workspace-scoped route lands
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
  let url = `${proto}//${host}/api/terminal/ws?session=${encodeURIComponent(sessionName)}`; // allow-url
  if (token) {
    url += `&token=${encodeURIComponent(token)}`;
  }
  if (workspaceId) {
    url += `&workspace=${encodeURIComponent(workspaceId)}`;
  }
  return url;
}

// ============= Spawn Session =============

/**
 * Pre-create a tmux session for the given backend via POST /api/terminal/spawn.
 * Used for shell tabs to ensure the correct command before WebSocket connects.
 */
// TODO(workspace-routing): migrate to wsUrl when workspace-scoped route lands
export async function spawnTerminalSession(
  _workspaceId: string,
  sessionName: string,
  backend: string,
): Promise<void> {
  await post<{ success: boolean; data?: unknown; error?: string }>(
    "/api/terminal/spawn",
    { session_name: sessionName, backend },
  );
}

// ============= Restart Session =============

/**
 * Restart a terminal session with the current backend.
 * POST /api/terminal/restart?session=<name>&token=<token>
 */
// TODO(workspace-routing): migrate to wsUrl when workspace-scoped route lands
export async function restartTerminalSession(
  _workspaceId: string,
  sessionName: string,
  token: string | null,
): Promise<{ success: boolean; backend: string }> {
  let url = `/api/terminal/restart?session=${encodeURIComponent(sessionName)}`; // allow-url
  if (token) {
    url += `&token=${encodeURIComponent(token)}`;
  }
  return post<{ success: boolean; backend: string }>(url, {});
}

// ============= Kill Session =============

/**
 * Forcibly kill a terminal session (for hung backends).
 * POST /api/terminal/kill?session=<name>&token=<token>
 */
// TODO(workspace-routing): migrate to wsUrl when workspace-scoped route lands
export async function killTerminalSession(
  _workspaceId: string,
  sessionName: string,
  token: string | null,
): Promise<void> {
  let url = `/api/terminal/kill?session=${encodeURIComponent(sessionName)}`; // allow-url
  if (token) {
    url += `&token=${encodeURIComponent(token)}`;
  }
  await post<{ success: boolean }>(url, {});
}

// ============= Session Status =============

/**
 * Check whether a terminal session's backend is alive.
 * GET /api/terminal/session-status?session=<name>
 */
// TODO(workspace-routing): migrate to wsUrl when workspace-scoped route lands
export async function getSessionStatus(
  _workspaceId: string,
  sessionName: string,
): Promise<{ alive: boolean; exit_reason?: string }> {
  return get<{ alive: boolean; exit_reason?: string }>(
    `/api/terminal/session-status?session=${encodeURIComponent(sessionName)}`, // allow-url
  );
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
 * Seed a terminal session with issue context via POST /api/terminal/sessions/{name}/seed.
 */
// TODO(workspace-routing): migrate to wsUrl when workspace-scoped route lands
export async function seedTerminalSession(
  _workspaceId: string,
  sessionName: string,
  context: IssueContext,
): Promise<void> {
  await post<{ success: boolean }>(
    `/api/terminal/sessions/${encodeURIComponent(sessionName)}/seed`,
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
 * POST /api/terminal/sessions/{session}/kill
 */
// TODO(workspace-routing): migrate to wsUrl when workspace-scoped route lands
export async function scheduleSessionKill(
  _workspaceId: string,
  sessionName: string,
): Promise<void> {
  await post<{ success: boolean }>(
    `/api/terminal/sessions/${encodeURIComponent(sessionName)}/kill`,
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
 * Close all terminal sessions via POST /api/terminal/sessions/close-all.
 * Kills all tmux sessions and clears all tab metadata.
 */
// TODO(workspace-routing): migrate to wsUrl when workspace-scoped route lands
export async function closeAllSessions(_workspaceId: string): Promise<void> {
  await post<{ success: boolean }>("/api/terminal/sessions/close-all", {});
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
