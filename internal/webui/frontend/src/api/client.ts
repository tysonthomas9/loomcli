const API_BASE_URL = "";
const DEFAULT_TIMEOUT = 30000;

// Auth token stored in memory (not localStorage) for XSS safety
let authToken: string | null = null;

// Auth state tracking
export type AuthState =
  | "initializing"
  | "authenticated"
  | "disabled"
  | "failed";
let authState: AuthState = "initializing";
type AuthStateListener = {
  callback: (state: AuthState) => void;
  active: boolean;
};
const authStateListeners: AuthStateListener[] = [];

// Promise deduplication for concurrent re-auth attempts
let pendingAuthPromise: Promise<void> | null = null;

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
};

function setAuthState(state: AuthState): void {
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

/** Whether a fetch error or HTTP status is transient (worth retrying). */
function isTransientFailure(error: unknown, status?: number): boolean {
  // Network errors are transient
  if (error instanceof TypeError) return true;
  // 5xx server errors are transient
  if (status !== undefined && status >= 500) return true;
  return false;
}

/**
 * Initialize authentication by fetching the API token from the bootstrap endpoint.
 * This endpoint is same-origin only and returns the pre-shared API key.
 * Must be called before any authenticated API requests.
 *
 * Retries up to maxRetries times with exponential backoff for transient failures.
 */
export async function initAuth(
  options: { maxRetries?: number } = {},
): Promise<void> {
  const maxRetries = options.maxRetries ?? 3;

  // Deduplicate concurrent initAuth calls
  if (pendingAuthPromise) {
    return pendingAuthPromise;
  }

  pendingAuthPromise = (async () => {
    try {
      for (let attempt = 0; attempt <= maxRetries; attempt++) {
        try {
          const response = await fetch(`${API_BASE_URL}/api/auth/token`, {
            headers: { Accept: "application/json" },
          });

          if (response.ok) {
            const contentType = response.headers.get("Content-Type") || "";
            if (!contentType.includes("application/json")) {
              // Non-JSON 200 response (e.g., SPA HTML fallback) means endpoint not registered
              setAuthState("disabled");
              return;
            }
            const data = (await response.json()) as { token: string };
            authToken = data.token;
            setAuthState("authenticated");
            return;
          }

          // 404 means auth is disabled on the server
          if (response.status === 404) {
            setAuthState("disabled");
            return;
          }

          // Non-retryable client errors (403, etc.) - stop immediately
          if (response.status >= 400 && response.status < 500) {
            setAuthState("disabled");
            return;
          }

          // Server error (5xx) - retry if we have attempts left
          if (
            isTransientFailure(null, response.status) &&
            attempt < maxRetries
          ) {
            const delay = 500 * Math.pow(2, attempt); // 500ms, 1s, 2s
            await new Promise((resolve) => setTimeout(resolve, delay));
            continue;
          }

          // Exhausted retries on server error
          console.error(
            `[Auth] Token acquisition failed after ${attempt + 1} attempts`,
          );
          setAuthState("failed");
          return;
        } catch (error) {
          // Network error - retry if we have attempts left
          if (isTransientFailure(error) && attempt < maxRetries) {
            const delay = 500 * Math.pow(2, attempt);
            await new Promise((resolve) => setTimeout(resolve, delay));
            continue;
          }

          // Exhausted retries on network error
          console.error(
            `[Auth] Token acquisition failed after ${attempt + 1} attempts`,
          );
          setAuthState("failed");
          return;
        }
      }
    } finally {
      pendingAuthPromise = null;
    }
  })();

  return pendingAuthPromise;
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
  _retried = false,
): Promise<T> {
  const controller = new AbortController();
  const timeout = options.timeout ?? DEFAULT_TIMEOUT;
  let timedOut = false;

  const timeoutId = setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, timeout);

  const clearTimeoutCleanup = () => clearTimeout(timeoutId);

  const signal = options.signal
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
      // 401 interceptor: try re-acquiring token and retrying once
      if (response.status === 401 && authToken !== null && !_retried) {
        authToken = null;
        await initAuth({ maxRetries: 0 });
        if (authToken !== null) {
          return fetchApi<T>(method, path, body, options, true);
        }
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
    // Network error (status 0) — daemon likely unreachable
    if (typeof window !== "undefined") {
      window.dispatchEvent(new CustomEvent("daemon-unavailable"));
    }
    throw new ApiError(0, "Network error", error);
  }
}

export const get = <T>(path: string, options?: RequestOptions): Promise<T> =>
  fetchApi<T>("GET", path, undefined, options);

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
