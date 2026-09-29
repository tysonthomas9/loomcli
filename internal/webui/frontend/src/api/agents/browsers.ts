/**
 * Interactive-agent browser inventory client (durable FleetDB identities).
 *
 * These routes use their own fetch wrapper instead of the shared client in
 * `@/api/common/client`: that client treats every 401 as a global sign-out
 * (clears the app token), but a browser 401 only means the browser principal
 * is missing/expired and must stay local to the browser surface.
 *
 * Auth:
 * - remote (`/api/config` mode "oidc"): the app's `Authorization: Bearer`
 *   user token; no operator header.
 * - local desktop (mode "open"): `X-Loom-Operator-Session`, a short-lived
 *   bearer issued ONLY through the Loom Desktop native bridge. It lives in
 *   the module-level `operatorTokens` map (memory only) and is never written
 *   to a URL, cookie, web storage, IndexedDB, or the console.
 */

import { AUTH_MODE_OIDC, fetchAppConfig } from "@/api/common/appConfig";
import { API_BASE_URL, getAuthToken, wsUrl } from "@/api/common/client";
import { browserOperatorSession, isDesktopRuntime } from "@/api/common/desktop";
import type { AgentBrowser, AgentBrowserList } from "@/types";

const OPERATOR_HEADER = "X-Loom-Operator-Session";
const REQUEST_TIMEOUT_MS = 15000;
/** Refresh cadence while the window is visible (server idle TTL is 20 min). */
export const OPERATOR_REFRESH_INTERVAL_MS = 5 * 60 * 1000;

/** Client-side code: local mode without the native bridge. */
export const BROWSER_DESKTOP_REQUIRED = "browser_desktop_required";
export const BROWSER_DESKTOP_REQUIRED_MESSAGE =
  "Open this workspace in Loom Desktop to view agent browsers";

const SESSION_CODES = new Set([
  "browser_operator_session_required",
  "browser_operator_session_expired",
]);

export class BrowserApiError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = "BrowserApiError";
    this.status = status;
    this.code = code;
  }

  /** True for failures that mean "no valid browser principal". */
  get isAuthFailure(): boolean {
    return (
      this.status === 401 ||
      SESSION_CODES.has(this.code) ||
      this.code === BROWSER_DESKTOP_REQUIRED
    );
  }
}

// ============= Operator session manager (memory only) =============

const operatorTokens = new Map<string, string>();
const pendingIssues = new Map<string, Promise<string>>();
type SessionLostListener = (workspace: string) => void;
const sessionLostListeners = new Set<SessionLostListener>();
let lifecycleInstalled = false;
let refreshTimer: ReturnType<typeof setInterval> | null = null;
let lastRefreshAt = 0;

/**
 * Subscribe to "operator session dropped" notifications (401, expiry,
 * revocation). Browser surfaces clear their inventory immediately on this.
 */
export function onBrowserOperatorSessionLost(
  listener: SessionLostListener,
): () => void {
  sessionLostListeners.add(listener);
  return () => {
    sessionLostListeners.delete(listener);
  };
}

function notifySessionLost(workspace: string): void {
  for (const listener of [...sessionLostListeners]) {
    try {
      listener(workspace);
    } catch {
      // A listener failure must not block the others.
    }
  }
}

function dropOperatorToken(workspace: string, token?: string): void {
  const held = operatorTokens.get(workspace);
  if (held === undefined) return;
  if (token !== undefined && held !== token) return;
  operatorTokens.delete(workspace);
  notifySessionLost(workspace);
}

function issueOperatorToken(workspace: string): Promise<string> {
  const pending = pendingIssues.get(workspace);
  if (pending) return pending;
  const promise = (async () => {
    const result = await browserOperatorSession("issue", workspace);
    if (result === null) {
      throw new BrowserApiError(
        401,
        BROWSER_DESKTOP_REQUIRED,
        BROWSER_DESKTOP_REQUIRED_MESSAGE,
      );
    }
    if (!result.ok || typeof result.token !== "string" || !result.token) {
      throw new BrowserApiError(
        503,
        result.code || "browser_operator_bridge_unavailable",
        result.error || "Loom Desktop could not issue a browser session",
      );
    }
    operatorTokens.set(workspace, result.token);
    installSessionLifecycle();
    return result.token;
  })().finally(() => {
    pendingIssues.delete(workspace);
  });
  pendingIssues.set(workspace, promise);
  return promise;
}

async function getOperatorToken(workspace: string): Promise<string> {
  return operatorTokens.get(workspace) ?? issueOperatorToken(workspace);
}

/** Refresh every held bearer; a failed refresh drops (and reports) it. */
export async function refreshBrowserOperatorSessions(): Promise<void> {
  lastRefreshAt = Date.now();
  await Promise.all(
    [...operatorTokens.entries()].map(async ([workspace, token]) => {
      const result = await browserOperatorSession("refresh", workspace, token);
      if (operatorTokens.get(workspace) !== token) return;
      if (result?.ok) {
        if (typeof result.token === "string" && result.token) {
          operatorTokens.set(workspace, result.token);
        }
        return;
      }
      dropOperatorToken(workspace, token);
    }),
  );
}

/**
 * Best-effort revoke of every held bearer (page hide / window close). The
 * page is going away, so listeners are not asked to refetch (that would just
 * issue a fresh bearer); the next request after a bfcache restore re-issues.
 */
export function revokeBrowserOperatorSessions(): void {
  const held = [...operatorTokens.entries()];
  operatorTokens.clear();
  for (const [workspace, token] of held) {
    void browserOperatorSession("revoke", workspace, token);
  }
}

function installSessionLifecycle(): void {
  if (lifecycleInstalled || typeof window === "undefined") return;
  lifecycleInstalled = true;
  lastRefreshAt = Date.now();
  const isVisible = () =>
    typeof document === "undefined" || document.visibilityState === "visible";
  refreshTimer = setInterval(() => {
    if (operatorTokens.size > 0 && isVisible()) {
      void refreshBrowserOperatorSessions();
    }
  }, OPERATOR_REFRESH_INTERVAL_MS);
  document.addEventListener("visibilitychange", onVisibilityChange);
  window.addEventListener("pagehide", revokeBrowserOperatorSessions);
}

function onVisibilityChange(): void {
  if (document.visibilityState !== "visible") return;
  if (operatorTokens.size === 0) return;
  if (Date.now() - lastRefreshAt >= OPERATOR_REFRESH_INTERVAL_MS) {
    void refreshBrowserOperatorSessions();
  }
}

/** Test hook: forget every bearer and listener without calling the bridge. */
export function __resetBrowserOperatorSessionsForTest(): void {
  operatorTokens.clear();
  pendingIssues.clear();
  sessionLostListeners.clear();
  if (refreshTimer !== null) clearInterval(refreshTimer);
  refreshTimer = null;
  if (lifecycleInstalled && typeof window !== "undefined") {
    document.removeEventListener("visibilitychange", onVisibilityChange);
    window.removeEventListener("pagehide", revokeBrowserOperatorSessions);
  }
  lifecycleInstalled = false;
  lastRefreshAt = 0;
}

// ============= Dedicated fetch wrapper =============

interface BrowserRequestOptions {
  signal?: AbortSignal | undefined;
}

async function sendBrowserRequest<T>(
  method: "GET" | "POST",
  path: string,
  headers: Record<string, string>,
  body: unknown,
  options: BrowserRequestOptions,
): Promise<T> {
  const timeout = AbortSignal.timeout(REQUEST_TIMEOUT_MS);
  const signal = options.signal
    ? AbortSignal.any([options.signal, timeout])
    : timeout;
  const init: RequestInit = {
    method,
    headers: {
      Accept: "application/json",
      ...(body !== undefined && { "Content-Type": "application/json" }),
      ...headers,
    },
    signal,
    credentials: "same-origin",
  };
  if (body !== undefined) init.body = JSON.stringify(body);

  let response: Response;
  try {
    response = await fetch(`${API_BASE_URL}${path}`, init);
  } catch (err) {
    if (options.signal?.aborted) throw err;
    throw new BrowserApiError(
      0,
      "browser_unavailable",
      timeout.aborted
        ? "Browser request timed out"
        : "Browser service unreachable",
    );
  }

  if (!response.ok) {
    let code = "";
    let message = "";
    try {
      const parsed = (await response.json()) as {
        error?: unknown;
        code?: unknown;
      };
      if (typeof parsed?.code === "string") code = parsed.code;
      if (typeof parsed?.error === "string") message = parsed.error;
    } catch {
      // Non-JSON error body; fall back to the status.
    }
    throw new BrowserApiError(
      response.status,
      code || fallbackCode(response.status),
      message || `Browser request failed (${response.status})`,
    );
  }
  return (await response.json()) as T;
}

function fallbackCode(status: number): string {
  switch (status) {
    case 400:
      return "browser_invalid";
    case 401:
      return "browser_operator_session_required";
    case 403:
      return "browser_forbidden";
    case 404:
      return "browser_not_found";
    default:
      return "browser_unavailable";
  }
}

async function browserRequest<T>(
  workspaceId: string,
  method: "GET" | "POST",
  path: string,
  body: unknown,
  options: BrowserRequestOptions,
): Promise<T> {
  let mode: string;
  try {
    mode = (await fetchAppConfig()).mode;
  } catch {
    throw new BrowserApiError(
      0,
      "browser_unavailable",
      "Unable to determine the workspace auth mode",
    );
  }

  if (mode === AUTH_MODE_OIDC) {
    const userToken = getAuthToken();
    const headers: Record<string, string> = userToken
      ? { Authorization: `Bearer ${userToken}` }
      : {};
    return sendBrowserRequest<T>(method, path, headers, body, options);
  }

  // Local desktop mode: the operator bearer comes only from the native bridge.
  if (!isDesktopRuntime()) {
    throw new BrowserApiError(
      401,
      BROWSER_DESKTOP_REQUIRED,
      BROWSER_DESKTOP_REQUIRED_MESSAGE,
    );
  }

  const token = await getOperatorToken(workspaceId);
  try {
    return await sendBrowserRequest<T>(
      method,
      path,
      { [OPERATOR_HEADER]: token },
      body,
      options,
    );
  } catch (err) {
    if (!(err instanceof BrowserApiError) || err.status !== 401) throw err;
    // Drop the rejected bearer (clears UI state via the lost listeners),
    // re-issue exactly once through the bridge, and retry once.
    dropOperatorToken(workspaceId, token);
    options.signal?.throwIfAborted();
    const reissued = await getOperatorToken(workspaceId);
    try {
      return await sendBrowserRequest<T>(
        method,
        path,
        { [OPERATOR_HEADER]: reissued },
        body,
        options,
      );
    } catch (retryErr) {
      if (retryErr instanceof BrowserApiError && retryErr.status === 401) {
        dropOperatorToken(workspaceId, reissued);
      }
      throw retryErr;
    }
  }
}

function browsersPath(workspaceId: string, agentName: string): string {
  return wsUrl(
    workspaceId,
    `/agents/${encodeURIComponent(agentName)}/browsers`,
  );
}

/** List the durable browsers owned by an interactive agent. */
export async function listAgentBrowsers(
  workspaceId: string,
  agentName: string,
  options: BrowserRequestOptions = {},
): Promise<AgentBrowser[]> {
  const data = await browserRequest<AgentBrowserList>(
    workspaceId,
    "GET",
    browsersPath(workspaceId, agentName),
    undefined,
    options,
  );
  if (!data || !Array.isArray(data.browsers)) {
    throw new BrowserApiError(
      0,
      "browser_unavailable",
      "Invalid browser inventory response",
    );
  }
  return data.browsers;
}

/** Durably select one browser for its owner agent. */
export async function selectAgentBrowser(
  workspaceId: string,
  agentName: string,
  browserId: string,
  options: BrowserRequestOptions = {},
): Promise<AgentBrowser> {
  return browserRequest<AgentBrowser>(
    workspaceId,
    "POST",
    `${browsersPath(workspaceId, agentName)}/${encodeURIComponent(browserId)}/select`,
    {},
    options,
  );
}
