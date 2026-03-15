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
  created_at: string;
  updated_at: string;
}

// ============= Tab Metadata API Functions =============

/**
 * List all tab metadata from GET /api/terminal/tabs.
 */
export async function listTabMetadata(): Promise<TabMetadata[]> {
  const response = await get<ApiResult<TabMetadata[]>>("/api/terminal/tabs");
  return unwrap(response);
}

/**
 * Get metadata for a single tab from GET /api/terminal/tabs/{session}.
 */
export async function getTabMetadata(session: string): Promise<TabMetadata> {
  const response = await get<ApiResult<TabMetadata>>(
    `/api/terminal/tabs/${encodeURIComponent(session)}`,
  );
  return unwrap(response);
}

/**
 * Partially update tab metadata via PATCH /api/terminal/tabs/{session}.
 */
export async function patchTabMetadata(
  session: string,
  fields: Partial<{ label: string; notes: string; sort_order: number }>,
): Promise<TabMetadata> {
  const response = await patch<ApiResult<TabMetadata>>(
    `/api/terminal/tabs/${encodeURIComponent(session)}`,
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
): Promise<TabMetadata> {
  const response = await put<ApiResult<TabMetadata>>(
    `/api/terminal/tabs/${encodeURIComponent(session)}`,
    fields,
  );
  return unwrap(response);
}

/**
 * Delete tab metadata via DELETE /api/terminal/tabs/{session}.
 */
export async function deleteTabMetadata(session: string): Promise<void> {
  await del<ApiResult<undefined>>(
    `/api/terminal/tabs/${encodeURIComponent(session)}`,
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
