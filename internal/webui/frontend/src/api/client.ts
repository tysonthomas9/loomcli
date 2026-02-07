const API_BASE_URL = '';
const DEFAULT_TIMEOUT = 30000;

// Auth token stored in memory (not localStorage) for XSS safety
let authToken: string | null = null;

export class ApiError extends Error {
  constructor(
    public status: number,
    public statusText: string,
    public body?: unknown
  ) {
    super(`API Error: ${status} ${statusText}`);
    this.name = 'ApiError';
  }
}

export type RequestOptions = {
  headers?: Record<string, string>;
  timeout?: number;
  signal?: AbortSignal;
};

/**
 * Initialize authentication by fetching the API token from the bootstrap endpoint.
 * This endpoint is same-origin only and returns the pre-shared API key.
 * Must be called before any authenticated API requests.
 */
export async function initAuth(): Promise<void> {
  try {
    const response = await fetch(`${API_BASE_URL}/api/auth/token`, {
      headers: { Accept: 'application/json' },
    });
    if (response.ok) {
      const data = (await response.json()) as { token: string };
      authToken = data.token;
    }
    // If 404 or other error, auth is likely disabled — proceed without token
  } catch {
    // Auth endpoint not available — server may have auth disabled
  }
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
  options: RequestOptions = {}
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
      Accept: 'application/json',
      ...options.headers,
    };

    // Inject auth token if available
    if (authToken && !headers['Authorization']) {
      headers['Authorization'] = `Bearer ${authToken}`;
    }

    if (body !== undefined) {
      headers['Content-Type'] = 'application/json';
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
    if (error instanceof ApiError) throw error;
    if (error instanceof DOMException && error.name === 'AbortError') {
      if (timedOut) {
        throw new ApiError(0, 'Request timeout');
      }
      // User-provided signal was aborted - re-throw as-is
      throw error;
    }
    throw new ApiError(0, 'Network error', error);
  }
}

export const get = <T>(path: string, options?: RequestOptions): Promise<T> =>
  fetchApi<T>('GET', path, undefined, options);

export const post = <T>(path: string, body: unknown, options?: RequestOptions): Promise<T> =>
  fetchApi<T>('POST', path, body, options);

export const patch = <T>(path: string, body: unknown, options?: RequestOptions): Promise<T> =>
  fetchApi<T>('PATCH', path, body, options);

export const del = <T>(path: string, options?: RequestOptions): Promise<T> =>
  fetchApi<T>('DELETE', path, undefined, options);
