const API_BASE_URL = "";
const DEFAULT_TIMEOUT = 30000;

// Auth token stored in memory (not localStorage) for XSS safety
let authToken: string | null = null;

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

export class ApiError extends Error {
  constructor(
    public status: number,
    public statusText: string,
    public body?: unknown,
  ) {
    super(`API Error: ${status} ${statusText}`);
    this.name = "ApiError";
  }
}

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

    const requestBody = body !== undefined ? JSON.stringify(body) : null;

    const response = await fetch(`${API_BASE_URL}${path}`, {
      method,
      headers,
      body: requestBody,
      signal,
    });

    clearTimeoutCleanup();

    if (!response.ok) {
      // 401 interceptor: clear token and notify AuthContext via event
      if (response.status === 401 && authToken !== null) {
        setAuthToken(null);
        if (typeof window !== "undefined") {
          window.dispatchEvent(new CustomEvent("auth-token-expired"));
        }
      }

      // Report 5xx errors (but not errors about the error endpoint itself)
      if (response.status >= 500 && path !== "/api/client-errors") {
        import("@/api/errorReporter")
          .then(({ reportError }) => {
            reportError(
              "api-error",
              `${response.status} ${response.statusText}`,
              {
                url: path,
              },
            );
          })
          .catch(() => {}); // silent - error reporting must never throw
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
      // Dispatch daemon-unavailable for 503 (Service Unavailable)
      if (error.status === 503 && typeof window !== "undefined") {
        window.dispatchEvent(new CustomEvent("daemon-unavailable"));
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
    // Network error (status 0) — daemon likely unreachable.
    if (typeof window !== "undefined") {
      window.dispatchEvent(new CustomEvent("daemon-unavailable"));
    }
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
