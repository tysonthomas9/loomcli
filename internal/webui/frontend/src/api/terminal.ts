import { get, ApiError } from "./client";

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
