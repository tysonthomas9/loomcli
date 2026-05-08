import createClient, { type Middleware } from "openapi-fetch";
import type { paths } from "@/types/generated/openapi";
import { reportError } from "./errorReporter";

const DEFAULT_TIMEOUT = 30000;

/**
 * API server base URL.
 *
 * IMPORTANT: this is intentionally an empty string in production builds so
 * every fetch is same-origin and resolved by the browser against
 * `window.location.origin` at runtime. That lets one committed `dist/` serve
 * behind any reverse proxy (Caddy sidecars in the parity harness, the Go
 * embedded handler, a CDN, etc.) without being tied to a build-time host.
 *
 * For cross-origin deployments, set the API origin on the server side (CORS
 * + path routing) and keep the SPA same-origin. If you truly need a
 * cross-origin SPA, wire it at runtime via `window.__LOOM_API_ORIGIN__` or
 * similar — NOT via `VITE_API_BASE_URL`, which inlines a literal at build
 * time and turns the bundle into an environment-specific artifact.
 */
export const API_BASE_URL: string = "";

/**
 * Return the API origin for use in absolute URLs (SSE EventSource,
 * WebSocket, etc.). Resolved at runtime from `window.location.origin` so the
 * same bundle works on any host/port the SPA is served from.
 *
 * In non-browser environments (Node-based unit tests) where `window` is
 * unavailable, falls back to "http://localhost".
 */
export function getApiOrigin(): string {
  if (typeof window !== "undefined" && window.location) {
    return window.location.origin;
  }
  return "http://localhost";
}

/**
 * Derive the WebSocket base URL from the API origin.
 * Converts http: → ws:, https: → wss:.
 */
export function getWsBaseUrl(): string {
  const origin = getApiOrigin();
  return origin.replace(/^http/, "ws");
}

// Auth token stored in memory (not localStorage) for XSS safety
let authToken: string | null = null;

/**
 * Generate a W3C traceparent header value. Format:
 *   <version>-<trace-id (16 bytes hex)>-<span-id (8 bytes hex)>-<flags>
 * Per the trace contract (§5) we set sampled=1 so server-side honors
 * the trace; sampling is enforced server-side via OTEL_TRACES_SAMPLER.
 */
function makeTraceparent(): string {
  const traceId = randomHex(16);
  const spanId = randomHex(8);
  return `00-${traceId}-${spanId}-01`;
}

function randomHex(byteCount: number): string {
  const bytes = new Uint8Array(byteCount);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
}

/**
 * Build a workspace-scoped API URL path.
 * All workspace-scoped API functions use this to construct paths like
 * /api/workspaces/{wsId}/issues/... instead of flat /api/issues/...
 */
export function wsUrl(workspaceId: string, path: string): string {
  return `/api/workspaces/${encodeURIComponent(workspaceId)}${path}`;
}

// Auth state tracking
export type AuthState = "none" | "authenticated" | "failed";
let authState: AuthState = "none";
type AuthStateListener = {
  callback: (state: AuthState) => void;
  active: boolean;
};
const authStateListeners: AuthStateListener[] = [];

// ApiError lives in src/types/errors.ts so components can render error UIs
// without crossing the frontend layer DAG back into the api layer. Re-export
// here so existing @/api/client call sites continue to work.
export { ApiError } from "@/types/common";
import { ApiError } from "@/types/common";

export type RequestOptions = {
  headers?: Record<string, string>;
  timeout?: number;
  signal?: AbortSignal;
  responseType?: "json" | "text";
};

export function setAuthState(state: AuthState): void {
  if (authState !== state) {
    authState = state;
    for (const listener of authStateListeners) {
      if (listener.active) {
        listener.callback(state);
      }
    }
  }
}

/**
 * Get the current auth state.
 */
export function getAuthState(): AuthState {
  return authState;
}

/**
 * Register a callback for auth state changes.
 * Returns an unsubscribe function.
 */
export function onAuthStateChange(
  callback: (state: AuthState) => void,
): () => void {
  const listener: AuthStateListener = { callback, active: true };
  authStateListeners.push(listener);
  return () => {
    listener.active = false;
  };
}

// Workspace service-unavailable listeners
type WorkspaceUnavailableListener = { callback: () => void; active: boolean };
const workspaceUnavailableListeners: WorkspaceUnavailableListener[] = [];

export function onWorkspaceUnavailable(callback: () => void): () => void {
  const listener: WorkspaceUnavailableListener = { callback, active: true };
  workspaceUnavailableListeners.push(listener);
  return () => {
    listener.active = false;
  };
}

export function notifyWorkspaceUnavailable(): void {
  for (const listener of workspaceUnavailableListeners) {
    if (listener.active) listener.callback();
  }
}

// Auth-token-expired listeners
type AuthTokenExpiredListener = { callback: () => void; active: boolean };
const authTokenExpiredListeners: AuthTokenExpiredListener[] = [];

export function onAuthTokenExpired(callback: () => void): () => void {
  const listener: AuthTokenExpiredListener = { callback, active: true };
  authTokenExpiredListeners.push(listener);
  return () => {
    listener.active = false;
  };
}

export function notifyAuthTokenExpired(): void {
  for (const listener of authTokenExpiredListeners) {
    if (listener.active) listener.callback();
  }
}

/**
 * Set the auth token externally (called by AuthContext).
 * When token is non-null, transitions to 'authenticated'.
 * When token is null, transitions to 'none'.
 */
export function setAuthToken(token: string | null): void {
  authToken = token;
  setAuthState(token !== null ? "authenticated" : "none");
}

/**
 * Get the current auth token for use in WebSocket/SSE connections.
 */
export function getAuthToken(): string | null {
  return authToken;
}

// ============= openapi-fetch client =============

/**
 * Middleware that injects auth token, handles timeouts, intercepts
 * 401/503 responses, and reports 5xx errors.
 */
const apiMiddleware: Middleware = {
  async onRequest({ request }) {
    // Inject auth token
    if (authToken && !request.headers.get("Authorization")) {
      request.headers.set("Authorization", `Bearer ${authToken}`);
    }

    // Inject W3C traceparent so server-side spans connect to a stable
    // browser-side trace ID. Propagation-only — no client-side spans.
    // See docs/observability/tracing-contract.md §5.
    if (!request.headers.get("traceparent")) {
      request.headers.set("traceparent", makeTraceparent());
    }

    // Apply default timeout for openapi-fetch calls. Request always has a
    // signal, so combine with it instead of treating presence as caller-owned.
    if (!request.signal.aborted) {
      const controller = new AbortController();
      const timeoutId = setTimeout(() => controller.abort(), DEFAULT_TIMEOUT);
      (
        controller as unknown as { _timeoutId: ReturnType<typeof setTimeout> }
      )._timeoutId = timeoutId;

      const signal = AbortSignal.any([
        request.signal,
        controller.signal,
      ]);
      const newReq = new Request(request, { signal });
      (
        newReq as unknown as { _timeoutController: AbortController }
      )._timeoutController = controller;
      return newReq;
    }
    return request;
  },
  async onError({ request }) {
    clearOpenAPITimeout(request);
  },
  async onResponse({ request, response }) {
    // Clean up timeout if we created one
    clearOpenAPITimeout(request);

    if (!response.ok) {
      // 401 interceptor
      if (response.status === 401 && authToken !== null) {
        setAuthToken(null);
        notifyAuthTokenExpired();
      }

      // 503 workspace service unavailable — skip for terminal endpoints which return 503
      // when Redis is absent (the workspace service itself is still healthy)
      const url503 = new URL(request.url, "http://localhost");
      if (response.status === 503 && !url503.pathname.includes("/terminal/")) {
        notifyWorkspaceUnavailable();
      }

      // 5xx error reporting (avoid recursion for the error endpoint itself)
      const url = new URL(request.url, "http://localhost");
      if (
        response.status >= 500 &&
        !url.pathname.endsWith("/api/client-errors")
      ) {
        reportError("api-error", `${response.status} ${response.statusText}`, {
          url: url.pathname,
        });
      }
    }
    return response;
  },
};

function clearOpenAPITimeout(request: Request): void {
  const controller = (
    request as unknown as { _timeoutController?: AbortController }
  )._timeoutController;
  if (controller) {
    clearTimeout(
      (controller as unknown as { _timeoutId: ReturnType<typeof setTimeout> })
        ._timeoutId,
    );
  }
}

/** Typed openapi-fetch client for all REST API calls. */
export const api = createClient<paths>({ baseUrl: API_BASE_URL });
api.use(apiMiddleware);

// ============= Helpers for facade layer =============

/**
 * Convert an openapi-fetch error response into an ApiError.
 * Called by facade functions when `error` is truthy.
 */
export function apiErrorFromResponse(
  error: unknown,
  response?: Response,
): ApiError {
  if (response) {
    return new ApiError(response.status, response.statusText, error);
  }
  return new ApiError(0, "Network error", error);
}

/**
 * Unwrap the standard {success, data} response envelope.
 * Throws ApiError if success is false.
 */
export function unwrapResponse<T>(
  envelope: {
    success: boolean;
    data?: T;
    error?: string;
  } | null | undefined,
  response?: Response,
): T {
  if (envelope == null) {
    throw new ApiError(
      response?.status ?? 0,
      response?.statusText || "Invalid API response",
      "missing response envelope",
    );
  }
  if (!envelope.success) {
    throw new ApiError(
      response?.status ?? 0,
      response?.statusText || envelope.error || "Unknown error",
      envelope.error,
    );
  }
  return envelope.data as T;
}

/**
 * Strip undefined values from an object so exactOptionalPropertyTypes is satisfied.
 * Use with explicit type parameter: cleanQuery<TargetType>({...})
 */
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function cleanQuery<Q = any>(obj: Record<string, unknown>): Q {
  const result: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(obj)) {
    if (value !== undefined) {
      result[key] = value;
    }
  }
  return result as Q;
}

// ============= Fetch Helpers For Non-OpenAPI Endpoints =============

async function fetchApi<T>(
  method: string,
  path: string,
  body?: unknown,
  options: RequestOptions = {},
): Promise<T> {
  const controller = new AbortController();
  const timeout = options.timeout ?? DEFAULT_TIMEOUT;
  let timedOut = false;

  const timeoutId = setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, timeout);

  const clearTimeoutCleanup = () => clearTimeout(timeoutId);

  const signal =
    options.signal instanceof AbortSignal
      ? AbortSignal.any([controller.signal, options.signal])
      : controller.signal;

  try {
    const headers: Record<string, string> = {
      Accept: "application/json",
      ...options.headers,
    };

    // Inject auth token if available
    if (authToken && !headers["Authorization"]) {
      headers["Authorization"] = `Bearer ${authToken}`;
    }

    if (body !== undefined) {
      headers["Content-Type"] = "application/json";
    }

    // Inject W3C traceparent (mirrors the openapi-fetch middleware).
    if (!headers["traceparent"]) {
      headers["traceparent"] = makeTraceparent();
    }

    const requestBody = body !== undefined ? JSON.stringify(body) : null;

    const response = await fetch(`${API_BASE_URL}${path}`, {
      method,
      headers,
      body: requestBody,
      signal,
    });

    clearTimeoutCleanup();

    if (!response.ok) {
      // 401 interceptor: clear token and notify AuthContext
      if (response.status === 401 && authToken !== null) {
        setAuthToken(null);
        notifyAuthTokenExpired();
      }

      // Report 5xx errors (but not errors about the error endpoint itself)
      if (response.status >= 500 && path !== "/api/client-errors") {
        reportError("api-error", `${response.status} ${response.statusText}`, {
          url: path,
        });
      }

      let errorBody: unknown;
      const responseText = await response.text();
      try {
        errorBody = JSON.parse(responseText);
      } catch {
        errorBody = responseText;
      }
      throw new ApiError(response.status, response.statusText, errorBody);
    }

    if (response.status === 204) {
      return undefined as T;
    }

    if (options.responseType === "text") {
      return (await response.text()) as T;
    }

    return (await response.json()) as T;
  } catch (error) {
    clearTimeoutCleanup();
    if (error instanceof ApiError) {
      // Notify workspace service-unavailable for 503, but not for terminal endpoints
      // which return 503 when Redis is absent (workspace service itself is still healthy)
      if (error.status === 503 && !path.includes("/terminal/")) {
        notifyWorkspaceUnavailable();
      }
      throw error;
    }
    if (error instanceof DOMException && error.name === "AbortError") {
      if (timedOut) {
        throw new ApiError(0, "Request timeout");
      }
      // User-provided signal was aborted - re-throw as-is
      throw error;
    }
    // Network error (status 0) — workspace service likely unreachable.
    notifyWorkspaceUnavailable();
    throw new ApiError(0, "Network error", error);
  }
}

export const get = <T>(path: string, options?: RequestOptions): Promise<T> =>
  fetchApi<T>("GET", path, undefined, options);

export const getText = (
  path: string,
  options?: RequestOptions,
): Promise<string> =>
  fetchApi<string>("GET", path, undefined, {
    ...options,
    responseType: "text",
  });

export const post = <T>(
  path: string,
  body: unknown,
  options?: RequestOptions,
): Promise<T> => fetchApi<T>("POST", path, body, options);

export const patch = <T>(
  path: string,
  body: unknown,
  options?: RequestOptions,
): Promise<T> => fetchApi<T>("PATCH", path, body, options);

export const put = <T>(
  path: string,
  body: unknown,
  options?: RequestOptions,
): Promise<T> => fetchApi<T>("PUT", path, body, options);

export const del = <T>(path: string, options?: RequestOptions): Promise<T> =>
  fetchApi<T>("DELETE", path, undefined, options);
