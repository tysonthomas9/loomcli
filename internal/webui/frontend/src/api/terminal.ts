import { get, post, put, patch, del, ApiError } from "./client";

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
 * List active terminal sessions from GET /api/terminal/sessions.
 */
export async function listTerminalSessions(): Promise<TerminalSessionInfo[]> {
  const response = await get<ApiResult<{ sessions: TerminalSessionInfo[] }>>(
    "/api/terminal/sessions",
  );
  return unwrap(response).sessions;
}

/**
 * Fetch a one-time terminal auth token for the given session.
 * Returns null on failure (WebSocket will be rejected by server).
 */
export async function fetchTerminalToken(
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
export function buildTerminalWsUrl(
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
  return url;
}

// ============= Restart Session =============

/**
 * Restart a terminal session with the current backend.
 * POST /api/terminal/restart?session=<name>&token=<token>
 */
export async function restartTerminalSession(
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
export async function killTerminalSession(
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
export async function getSessionStatus(
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
export async function seedTerminalSession(
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
  issue_id?: string;
  created_at: string;
  updated_at: string;
}

// ============= Tab Metadata API Functions =============

/** Build workspace query string suffix for tab metadata requests. */
function wsQuery(workspace?: string): string {
  return workspace ? `?workspace=${encodeURIComponent(workspace)}` : "";
}

/**
 * List all tab metadata from GET /api/terminal/tabs.
 */
export async function listTabMetadata(
  workspace?: string,
): Promise<TabMetadata[]> {
  const response = await get<ApiResult<TabMetadata[]>>(
    `/api/terminal/tabs${wsQuery(workspace)}`,
  );
  return unwrap(response);
}

/**
 * Get metadata for a single tab from GET /api/terminal/tabs/{session}.
 */
export async function getTabMetadata(
  session: string,
  workspace?: string,
): Promise<TabMetadata> {
  const response = await get<ApiResult<TabMetadata>>(
    `/api/terminal/tabs/${encodeURIComponent(session)}${wsQuery(workspace)}`,
  );
  return unwrap(response);
}

/**
 * Partially update tab metadata via PATCH /api/terminal/tabs/{session}.
 */
export async function patchTabMetadata(
  session: string,
  fields: Partial<{
    label: string;
    notes: string;
    sort_order: number;
    issue_id: string;
  }>,
  workspace?: string,
): Promise<TabMetadata> {
  const response = await patch<ApiResult<TabMetadata>>(
    `/api/terminal/tabs/${encodeURIComponent(session)}${wsQuery(workspace)}`,
    fields,
  );
  return unwrap(response);
}

/**
 * Create or replace tab metadata via PUT /api/terminal/tabs/{session}.
 */
export async function putTabMetadata(
  session: string,
  fields: { label: string; sort_order: number; notes?: string },
  workspace?: string,
): Promise<TabMetadata> {
  const response = await put<ApiResult<TabMetadata>>(
    `/api/terminal/tabs/${encodeURIComponent(session)}${wsQuery(workspace)}`,
    fields,
  );
  return unwrap(response);
}

/**
 * Delete tab metadata via DELETE /api/terminal/tabs/{session}.
 */
export async function deleteTabMetadata(
  session: string,
  workspace?: string,
): Promise<void> {
  await del<ApiResult<undefined>>(
    `/api/terminal/tabs/${encodeURIComponent(session)}${wsQuery(workspace)}`,
  );
}

/**
 * Schedule a deferred tmux session kill with grace period.
 * POST /api/terminal/sessions/{session}/kill
 */
export async function scheduleSessionKill(sessionName: string): Promise<void> {
  await post<{ success: boolean }>(
    `/api/terminal/sessions/${encodeURIComponent(sessionName)}/kill`,
    {},
  );
}

// ============= Issue Session Management =============

/**
 * List sessions grouped by issue ID from GET /api/terminal/sessions/by-issue.
 * Returns a map of issue_id → session_name[].
 */
export async function listSessionsByIssue(): Promise<Record<string, string[]>> {
  const response = await get<ApiResult<Record<string, string[]>>>(
    "/api/terminal/sessions/by-issue",
  );
  return unwrap(response);
}

/**
 * Close all terminal sessions via POST /api/terminal/sessions/close-all.
 * Kills all tmux sessions and clears all tab metadata.
 */
export async function closeAllSessions(): Promise<void> {
  await post<{ success: boolean }>("/api/terminal/sessions/close-all", {});
}

// ============= Scrollback API =============

/**
 * Fetch scrollback content for a terminal session.
 * GET /api/terminal/sessions/{session}/scrollback
 */
export async function fetchScrollback(
  sessionName: string,
): Promise<{ content: string; lines: number }> {
  const response = await get<ApiResult<{ content: string; lines: number }>>(
    `/api/terminal/sessions/${encodeURIComponent(sessionName)}/scrollback`,
  );
  return unwrap(response);
}

// ============= Terminal UI State =============

/**
 * Get persisted terminal UI state (active tab).
 * GET /api/terminal/state
 */
export async function getTerminalState(): Promise<{ active_tab: string }> {
  return get<{ active_tab: string }>("/api/terminal/state");
}

/**
 * Persist terminal UI state (active tab).
 * PATCH /api/terminal/state
 */
export async function patchTerminalState(state: {
  active_tab: string;
}): Promise<void> {
  await patch<{ active_tab: string }>("/api/terminal/state", state);
}
